package core

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	slugpkg "github.com/BaronBonet/rig/internal/pkg/slug"
)

// taskCreation is the Task creation module: it turns a prompt or pull request
// source into a persisted task record, an isolated worktree, a prepared
// workspace, and a started provider session, reporting coarse-grained
// progress along the way. Failed creations persist the failed step and are
// retryable from that step. A new task's Launch choice becomes its Launch
// provider's Launch default.
type taskCreation struct {
	tasks       TaskRepository
	gitWorktree GitWorktreeClient
	userConfig  UserConfigStore
	// repos resolves a target repository given by name.
	repos      *knownRepos
	launcher   *sessionLauncher
	operations *taskOperationCoordinator
	// saveMu serializes saving new tasks; see saveNewTask.
	saveMu sync.Mutex
}

func newTaskCreation(
	tasks TaskRepository,
	gitWorktree GitWorktreeClient,
	userConfig UserConfigStore,
	repos *knownRepos,
	launcher *sessionLauncher,
	operations *taskOperationCoordinator,
) *taskCreation {
	return &taskCreation{
		tasks:       tasks,
		gitWorktree: gitWorktree,
		userConfig:  userConfig,
		repos:       repos,
		launcher:    launcher,
		operations:  operations,
	}
}

type taskCreationStepAction struct {
	step TaskCreateProgressStep
	run  func(context.Context) error
}

type taskCreationStepPersistence int

const (
	taskCreationStepPersistenceReadyOnly taskCreationStepPersistence = iota
	taskCreationStepPersistenceEachStep
)

// CreateTaskStream creates a new task and streams progress events followed by
// exactly one terminal result event. The stream lifetime is owned by ctx.
func (c *taskCreation) CreateTaskStream(
	ctx context.Context,
	input CreateTaskInput,
) (<-chan TaskCreateEvent, error) {
	return c.taskCreateEventStream(
		ctx,
		func(ctx context.Context, reporter TaskCreateProgressReporter) (*Task, error) {
			return c.CreateTaskWithProgress(ctx, input, reporter)
		},
	)
}

// RetryTaskCreationStream resumes a failed task creation and streams the same
// progress milestones as initial creation, followed by exactly one terminal
// result event.
func (c *taskCreation) RetryTaskCreationStream(
	ctx context.Context,
	taskID string,
) (<-chan TaskCreateEvent, error) {
	return c.taskCreateEventStream(
		ctx,
		func(ctx context.Context, reporter TaskCreateProgressReporter) (*Task, error) {
			return c.RetryTaskCreationWithProgress(ctx, taskID, reporter)
		},
	)
}

// CreateTaskWithProgress creates a new task while reporting coarse-grained
// creation milestones to the provided reporter when non-nil.
func (c *taskCreation) CreateTaskWithProgress(
	ctx context.Context,
	input CreateTaskInput,
	reporter TaskCreateProgressReporter,
) (*Task, error) {
	input, err := normalizeTaskLaunchChoice(input)
	if err != nil {
		return nil, err
	}

	repoCtx, err := c.targetRepo(ctx, input)
	if err != nil {
		return nil, err
	}

	provider, providerClient, err := c.launcher.resolveProvider(ctx, input.Provider)
	if err != nil {
		return nil, err
	}
	c.saveLaunchDefault(ctx, input)

	if input.Source.PullRequest != nil {
		return c.createTaskFromPullRequest(ctx, repoCtx, provider, input, reporter)
	}

	return c.createTaskFromPrompt(ctx, repoCtx, provider, providerClient, input, reporter)
}

// saveLaunchDefault makes a new task's Launch choice, even an empty one, its
// Launch provider's Launch default, as the provider CLIs keep the last model
// picked. Only a creation that asks to remember its choice, as the TUI's Task
// draft does, and whose input names its provider saves one: a task from the
// CLI makes no choice of model, and a task that falls to the Default provider
// made no choice for it, which is also why normalizeTaskLaunchChoice refuses
// a choice without an explicit provider.
//
// It runs once the provider is resolved and before the long creation steps,
// so the next task starts from it whatever becomes of this one, and retries,
// which make no new choice, never run it. Saving is best-effort and its error
// is dropped: the default is a convenience, and must never fail or block
// creating a task.
func (c *taskCreation) saveLaunchDefault(ctx context.Context, input CreateTaskInput) {
	if c.userConfig == nil || !input.RememberLaunchChoice || input.Provider == "" {
		return
	}
	choice := LaunchChoice{Model: input.Model, ReasoningLevel: input.ReasoningLevel}
	_ = c.userConfig.SaveLaunchDefault(ctx, input.Provider, choice)
}

// RetryTaskCreationWithProgress resumes a failed task creation from its
// recorded failed step while reporting the same progress milestones as
// initial creation.
func (c *taskCreation) RetryTaskCreationWithProgress(
	ctx context.Context,
	taskID string,
	reporter TaskCreateProgressReporter,
) (*Task, error) {
	var task *Task
	err := c.operations.Run(ctx, taskID, taskOperationRetryCreation, false, func(ctx context.Context) error {
		var runErr error
		task, runErr = c.retryTaskCreationWithProgress(ctx, taskID, reporter)
		return runErr
	})
	return task, err
}

func (c *taskCreation) retryTaskCreationWithProgress(
	ctx context.Context,
	taskID string,
	reporter TaskCreateProgressReporter,
) (*Task, error) {
	task, err := taskByID(ctx, c.tasks, taskID)
	if err != nil {
		return nil, err
	}
	if task.CreationStatus != TaskCreationStatusFailed {
		return nil, fmt.Errorf("task creation is not failed")
	}

	// The choice to submit the prompt belongs to the initial creation alone:
	// a retry never submits it.
	steps, ok := taskCreationStepsFrom(c.creationSteps(
		task,
		task.RepoRoot,
		func(ctx context.Context) error {
			return c.gitWorktree.CreateTaskWorkspace(ctx, task)
		},
		false,
	), task.CreationStep)
	if !ok {
		return nil, fmt.Errorf("task creation failed step %q is not retryable", task.CreationStep)
	}
	keepExistingSessionOnRetry(steps)

	if err := c.runSteps(ctx, task, reporter, steps, taskCreationStepPersistenceEachStep); err != nil {
		return task, err
	}
	return task, nil
}

func (c *taskCreation) createTaskFromPrompt(
	ctx context.Context,
	repoCtx RepoContext,
	provider Provider,
	providerClient ProviderClient,
	input CreateTaskInput,
	reporter TaskCreateProgressReporter,
) (*Task, error) {
	reportTaskCreateProgress(reporter, TaskCreateProgressSuggestingName, nil)
	suggestion, err := suggestTaskName(ctx, providerClient, repoCtx.Root, input.Prompt)
	if err != nil {
		return nil, err
	}

	task, err := c.saveNewTask(ctx, func(existingTasks []*Task) (*Task, error) {
		taskSlug := uniqueTaskSlug(repoCtx.Root, suggestion.Name, existingTasks)
		task := newPromptTaskRecord(
			repoCtx,
			provider,
			suggestion.Name,
			taskSlug,
			suggestion.BranchType,
		)
		task.Prompt = input.Prompt
		task.Model = input.Model
		task.ReasoningLevel = input.ReasoningLevel
		task.ParentTaskID = parentTaskID(input, existingTasks)
		return task, nil
	})
	if err != nil {
		return nil, err
	}

	steps := c.creationSteps(
		task,
		repoCtx.Root,
		func(ctx context.Context) error {
			return c.gitWorktree.CreateTaskWorkspace(ctx, task)
		},
		input.SubmitPrompt,
	)
	if err := c.runSteps(ctx, task, reporter, steps, taskCreationStepPersistenceReadyOnly); err != nil {
		return task, err
	}
	return task, nil
}

func (c *taskCreation) createTaskFromPullRequest(
	ctx context.Context,
	repoCtx RepoContext,
	provider Provider,
	input CreateTaskInput,
	reporter TaskCreateProgressReporter,
) (*Task, error) {
	pr := input.Source.PullRequest
	if pr == nil {
		return nil, fmt.Errorf("pull request source is required")
	}

	task, err := c.saveNewTask(ctx, func(existingTasks []*Task) (*Task, error) {
		if existingTaskForBranch(existingTasks, repoCtx.Root, pr.BranchName) != nil {
			return nil, fmt.Errorf("PR already has workspace")
		}

		inUseByWorktree, err := c.gitWorktree.IsBranchUsedByWorktree(ctx, repoCtx.Root, pr.BranchName)
		if err != nil {
			return nil, err
		}
		if inUseByWorktree {
			return nil, fmt.Errorf("PR already has workspace")
		}

		taskSlug := uniqueTaskSlug(repoCtx.Root, pr.BranchName, existingTasks)
		task := newPullRequestTaskRecord(
			repoCtx,
			provider,
			prDisplayName(*pr),
			taskSlug,
			pr.BranchName,
		)
		task.Model = input.Model
		task.ReasoningLevel = input.ReasoningLevel
		task.ParentTaskID = parentTaskID(input, existingTasks)
		return task, nil
	})
	if err != nil {
		return nil, err
	}

	steps := c.creationSteps(
		task,
		repoCtx.Root,
		func(ctx context.Context) error {
			return c.gitWorktree.CreateTaskWorkspaceFromPullRequest(ctx, task, pr.Number)
		},
		input.SubmitPrompt,
	)
	if err := c.runSteps(ctx, task, reporter, steps, taskCreationStepPersistenceReadyOnly); err != nil {
		return task, err
	}
	return task, nil
}

// saveNewTask saves the task newTask makes from the tasks saved so far. It
// lets one creation at a time list, pick and save, as creations run at once
// and two picking from the same list could take the same slug, and so the
// same branch, worktree and session.
func (c *taskCreation) saveNewTask(
	ctx context.Context,
	newTask func(existingTasks []*Task) (*Task, error),
) (*Task, error) {
	c.saveMu.Lock()
	defer c.saveMu.Unlock()

	existingTasks, err := c.tasks.ListTasks(ctx)
	if err != nil {
		return nil, err
	}
	task, err := newTask(existingTasks)
	if err != nil {
		return nil, err
	}
	if err := c.tasks.CreateTask(ctx, task); err != nil {
		return nil, err
	}
	return task, nil
}

// creationSteps returns the retryable steps that turn a task record into a
// ready task. submitPrompt submits the task's prompt once the session starts
// with it pasted in; only an initial creation that asked for it sets it.
func (c *taskCreation) creationSteps(
	task *Task,
	repoRoot string,
	createWorktree func(context.Context) error,
	submitPrompt bool,
) []taskCreationStepAction {
	return []taskCreationStepAction{
		{
			step: TaskCreateProgressCreatingWorktree,
			run: func(ctx context.Context) error {
				if err := createWorktree(ctx); err != nil {
					return fmt.Errorf("create worktree: %w", err)
				}
				return nil
			},
		},
		{
			step: TaskCreateProgressPreparingWorkspace,
			run: func(ctx context.Context) error {
				return c.launcher.prepareWorkspace(ctx, task, repoRoot)
			},
		},
		{
			step: TaskCreateProgressStartingSession,
			run: func(ctx context.Context) error {
				_, err := c.launcher.startSession(ctx, task, submitPrompt)
				return err
			},
		},
	}
}

// keepExistingSessionOnRetry lets a retried task finish when its Session
// exists by the time retry starts it: the failed attempt left it, or Enter
// reconnected it since, so the task's agent already runs there. Retry then
// neither launches into it nor prefills the prompt.
func keepExistingSessionOnRetry(steps []taskCreationStepAction) {
	for i := range steps {
		if steps[i].step != TaskCreateProgressStartingSession {
			continue
		}
		start := steps[i].run
		steps[i].run = func(ctx context.Context) error {
			if err := start(ctx); !errors.Is(err, ErrTaskSessionExists) {
				return err
			}
			return nil
		}
	}
}

func taskCreationStepsFrom(
	steps []taskCreationStepAction,
	step TaskCreateProgressStep,
) ([]taskCreationStepAction, bool) {
	for index, action := range steps {
		if action.step == step {
			return steps[index:], true
		}
	}
	return nil, false
}

func (c *taskCreation) runSteps(
	ctx context.Context,
	task *Task,
	reporter TaskCreateProgressReporter,
	steps []taskCreationStepAction,
	stepPersistence taskCreationStepPersistence,
) error {
	for _, action := range steps {
		reportTaskCreateProgress(reporter, action.step, task)
		if stepPersistence == taskCreationStepPersistenceEachStep {
			if err := c.markStep(ctx, task, action.step); err != nil {
				return err
			}
		}
		if err := action.run(ctx); err != nil {
			return c.markFailed(ctx, task, action.step, err)
		}
	}

	return c.markReady(ctx, task)
}

func reportTaskCreateProgress(reporter TaskCreateProgressReporter, step TaskCreateProgressStep, task *Task) {
	if reporter == nil {
		return
	}

	reporter.ReportTaskCreateProgress(step, task)
}

// taskCreateChanReporter bridges the workflow's progress reporter onto a
// TaskCreateEvent channel without blocking past ctx cancellation.
type taskCreateChanReporter struct {
	ctx    context.Context
	events chan<- TaskCreateEvent
}

func (r taskCreateChanReporter) ReportTaskCreateProgress(step TaskCreateProgressStep, task *Task) {
	progress := &TaskCreateProgressEvent{Step: step}
	if task != nil {
		// A copy, as the creation goes on changing its task while the event is
		// sent.
		snapshot := *task
		progress.Task = &snapshot
	}
	select {
	case <-r.ctx.Done():
	case r.events <- TaskCreateEvent{Progress: progress}:
	}
}

func (c *taskCreation) taskCreateEventStream(
	ctx context.Context,
	run func(context.Context, TaskCreateProgressReporter) (*Task, error),
) (<-chan TaskCreateEvent, error) {
	events := make(chan TaskCreateEvent, 8)

	go func() {
		defer close(events)

		task, err := run(ctx, taskCreateChanReporter{ctx: ctx, events: events})
		terminal := TaskCreateEvent{Task: task}
		if err != nil {
			terminal = TaskCreateEvent{Err: err, Task: task}
		}
		select {
		case <-ctx.Done():
		case events <- terminal:
		}
	}()

	return events, nil
}

// normalizeTaskLaunchChoice trims and checks a new task's Launch choice, as
// normalizeLaunchChoice does. A choice also needs an explicit provider: it was
// chosen for that provider's CLI, and must not fall to the Default provider.
func normalizeTaskLaunchChoice(input CreateTaskInput) (CreateTaskInput, error) {
	choice := LaunchChoice{
		Model:          strings.TrimSpace(input.Model),
		ReasoningLevel: strings.TrimSpace(input.ReasoningLevel),
	}
	if !choice.IsZero() && strings.TrimSpace(string(input.Provider)) == "" {
		return input, fmt.Errorf("a model or reasoning level needs an explicit provider")
	}
	choice, err := normalizeLaunchChoice(choice)
	if err != nil {
		return input, err
	}
	input.Model, input.ReasoningLevel = choice.Model, choice.ReasoningLevel
	return input, nil
}

// normalizeLaunchChoice trims a Launch model and Launch reasoning level and
// checks them, for a new task and for a Launch default alike. Each is passed to the
// provider CLI as one argument, so neither may hold whitespace or control
// characters, nor start with "-", which the CLI would read as a flag. They are
// not checked against the provider's models: the CLI does that at launch, and
// a cached model list may be stale.
func normalizeLaunchChoice(choice LaunchChoice) (LaunchChoice, error) {
	choice.Model = strings.TrimSpace(choice.Model)
	choice.ReasoningLevel = strings.TrimSpace(choice.ReasoningLevel)
	if err := checkLaunchChoiceValue("model", choice.Model); err != nil {
		return choice, err
	}
	if err := checkLaunchChoiceValue("reasoning level", choice.ReasoningLevel); err != nil {
		return choice, err
	}
	return choice, nil
}

// checkLaunchChoiceValue checks that value reaches the provider CLI as one
// argument, and as the value of its flag rather than as another flag.
func checkLaunchChoiceValue(name string, value string) error {
	if strings.ContainsFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return fmt.Errorf("%s %q must not contain whitespace or control characters", name, value)
	}
	if strings.HasPrefix(value, "-") {
		return fmt.Errorf("%s %q must not start with a dash, which the provider CLI reads as a flag", name, value)
	}
	return nil
}

func suggestTaskName(
	ctx context.Context,
	providerClient ProviderClient,
	repoRoot string,
	prompt string,
) (TaskSuggestion, error) {
	suggestion, err := providerClient.SuggestTaskName(ctx, repoRoot, prompt)
	if err != nil {
		return TaskSuggestion{}, fmt.Errorf("suggest task name: %w", err)
	}

	suggestion.Name = suggestion.DisplayName()
	if suggestion.Name == "" {
		return TaskSuggestion{}, fmt.Errorf("suggest task name: empty task name")
	}

	return suggestion, nil
}

func (c *taskCreation) markStep(
	ctx context.Context,
	task *Task,
	step TaskCreateProgressStep,
) error {
	task.CreationStatus = TaskCreationStatusCreating
	task.CreationStep = step
	task.CreationError = ""
	task.UpdatedAt = time.Now().UTC()
	return c.tasks.UpdateTask(ctx, task)
}

func (c *taskCreation) markFailed(
	ctx context.Context,
	task *Task,
	step TaskCreateProgressStep,
	cause error,
) error {
	task.CreationStatus = TaskCreationStatusFailed
	task.CreationStep = step
	if cause != nil {
		task.CreationError = cause.Error()
	}
	task.UpdatedAt = time.Now().UTC()
	if err := c.tasks.UpdateTask(ctx, task); err != nil {
		return fmt.Errorf("%w; persist task creation failure: %w", cause, err)
	}
	return cause
}

func (c *taskCreation) markReady(ctx context.Context, task *Task) error {
	task.CreationStatus = TaskCreationStatusReady
	task.CreationStep = ""
	task.CreationError = ""
	task.UpdatedAt = time.Now().UTC()
	return c.tasks.UpdateTask(ctx, task)
}

func newPromptTaskRecord(
	repoCtx RepoContext,
	provider Provider,
	displayName string,
	taskSlug string,
	branchType string,
) *Task {
	now := time.Now().UTC()
	taskID := fmt.Sprintf("%d", now.UnixNano())

	return &Task{
		ID:             taskID,
		Slug:           taskSlug,
		DisplayName:    displayName,
		RepoRoot:       repoCtx.Root,
		RepoName:       repoCtx.Name,
		BranchName:     branchNameForTask(taskSlug, branchType),
		WorktreePath:   taskWorktreePath(repoCtx, taskSlug),
		TmuxSession:    taskSessionName(repoCtx, taskSlug),
		Provider:       provider,
		CreationStatus: TaskCreationStatusCreating,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}

func newPullRequestTaskRecord(
	repoCtx RepoContext,
	provider Provider,
	displayName string,
	taskSlug string,
	branchName string,
) *Task {
	now := time.Now().UTC()
	taskID := fmt.Sprintf("%d", now.UnixNano())

	return &Task{
		ID:             taskID,
		Slug:           taskSlug,
		DisplayName:    displayName,
		RepoRoot:       repoCtx.Root,
		RepoName:       repoCtx.Name,
		BranchName:     strings.TrimSpace(branchName),
		WorktreePath:   taskWorktreePath(repoCtx, taskSlug),
		TmuxSession:    taskSessionName(repoCtx, taskSlug),
		Provider:       provider,
		CreationStatus: TaskCreationStatusCreating,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}

func branchNameForTask(taskSlug string, branchType string) string {
	return TaskSuggestion{BranchType: branchType}.BranchTypeOrDefault() + "/" + taskSlug
}

func taskWorktreePath(repoCtx RepoContext, taskSlug string) string {
	return filepath.Join(filepath.Dir(repoCtx.Root), taskRuntimeStem(repoCtx, taskSlug))
}

func taskSessionName(repoCtx RepoContext, taskSlug string) string {
	return taskRuntimeStem(repoCtx, taskSlug)
}

func taskRuntimeStem(repoCtx RepoContext, taskSlug string) string {
	return repoCtx.Name + "_" + taskSlug
}

func uniqueTaskSlug(repoRoot string, raw string, tasks []*Task) string {
	base := slugpkg.FromDisplayName(raw)
	existing := make(map[string]struct{})
	for _, task := range tasks {
		if task == nil || task.RepoRoot != repoRoot {
			continue
		}
		slug := strings.TrimSpace(task.Slug)
		if slug == "" {
			slug = slugpkg.FromDisplayName(task.DisplayName)
		}
		existing[slug] = struct{}{}
	}
	return slugpkg.EnsureUnique(base, existing)
}

func existingTaskForBranch(tasks []*Task, repoRoot string, branchName string) *Task {
	for _, task := range tasks {
		if task == nil {
			continue
		}
		if task.RepoRoot == repoRoot && task.BranchName == branchName {
			return task
		}
	}
	return nil
}

func prDisplayName(pr RepoPullRequest) string {
	title := strings.TrimSpace(pr.Title)
	if title != "" {
		return title
	}
	return strings.TrimSpace(pr.BranchName)
}
