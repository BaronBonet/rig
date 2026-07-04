package core

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	slugpkg "github.com/BaronBonet/rig/internal/pkg/slug"
)

// taskCreation is the Task creation module: it turns a prompt or pull request
// source into a persisted task record, an isolated worktree, a prepared
// workspace, and a started provider session, reporting coarse-grained
// progress along the way. Failed creations persist the failed step and are
// retryable from that step.
type taskCreation struct {
	tasks       TaskRepository
	gitWorktree GitWorktreeClient
	launcher    *sessionLauncher
}

func newTaskCreation(
	tasks TaskRepository,
	gitWorktree GitWorktreeClient,
	launcher *sessionLauncher,
) *taskCreation {
	return &taskCreation{
		tasks:       tasks,
		gitWorktree: gitWorktree,
		launcher:    launcher,
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
	repoCtx, err := c.gitWorktree.DetectRepo(ctx, input.Cwd)
	if err != nil {
		return nil, err
	}

	if input.Source.PullRequest != nil {
		return c.createTaskFromPullRequest(ctx, repoCtx, input, reporter)
	}

	return c.createTaskFromPrompt(ctx, repoCtx, input, reporter)
}

// RetryTaskCreationWithProgress resumes a failed task creation from its
// recorded failed step while reporting the same progress milestones as
// initial creation.
func (c *taskCreation) RetryTaskCreationWithProgress(
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

	steps, ok := taskCreationStepsFrom(c.creationSteps(
		task,
		task.RepoRoot,
		func(ctx context.Context) error {
			return c.gitWorktree.CreateTaskWorkspace(ctx, task)
		},
	), task.CreationStep)
	if !ok {
		return nil, fmt.Errorf("task creation failed step %q is not retryable", task.CreationStep)
	}

	if err := c.runSteps(ctx, task, reporter, steps, taskCreationStepPersistenceEachStep); err != nil {
		return task, err
	}
	return task, nil
}

func (c *taskCreation) createTaskFromPrompt(
	ctx context.Context,
	repoCtx RepoContext,
	input CreateTaskInput,
	reporter TaskCreateProgressReporter,
) (*Task, error) {
	provider, providerClient, err := c.launcher.resolveProvider(ctx, input.Provider)
	if err != nil {
		return nil, err
	}

	reportTaskCreateProgress(reporter, TaskCreateProgressSuggestingName)
	suggestion, err := suggestTaskName(ctx, providerClient, input.Prompt)
	if err != nil {
		return nil, err
	}

	existingTasks, err := c.tasks.ListTasks(ctx)
	if err != nil {
		return nil, err
	}

	taskSlug := uniqueTaskSlug(repoCtx.Root, suggestion.Name, existingTasks)
	task := newPromptTaskRecord(
		repoCtx,
		provider,
		suggestion.Name,
		taskSlug,
		suggestion.BranchType,
	)
	task.Prompt = input.Prompt

	if err := c.tasks.CreateTask(ctx, task); err != nil {
		return nil, err
	}

	steps := c.creationSteps(
		task,
		repoCtx.Root,
		func(ctx context.Context) error {
			return c.gitWorktree.CreateTaskWorkspace(ctx, task)
		},
	)
	if err := c.runSteps(ctx, task, reporter, steps, taskCreationStepPersistenceReadyOnly); err != nil {
		return task, err
	}
	return task, nil
}

func (c *taskCreation) createTaskFromPullRequest(
	ctx context.Context,
	repoCtx RepoContext,
	input CreateTaskInput,
	reporter TaskCreateProgressReporter,
) (*Task, error) {
	pr := input.Source.PullRequest
	if pr == nil {
		return nil, fmt.Errorf("pull request source is required")
	}

	provider, _, err := c.launcher.resolveProvider(ctx, input.Provider)
	if err != nil {
		return nil, err
	}

	existingTasks, err := c.tasks.ListTasks(ctx)
	if err != nil {
		return nil, err
	}
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
	if err := c.tasks.CreateTask(ctx, task); err != nil {
		return nil, err
	}

	steps := c.creationSteps(
		task,
		repoCtx.Root,
		func(ctx context.Context) error {
			return c.gitWorktree.CreateTaskWorkspaceFromPullRequest(ctx, task, pr.Number)
		},
	)
	if err := c.runSteps(ctx, task, reporter, steps, taskCreationStepPersistenceReadyOnly); err != nil {
		return task, err
	}
	return task, nil
}

func (c *taskCreation) creationSteps(
	task *Task,
	repoRoot string,
	createWorktree func(context.Context) error,
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
				_, err := c.launcher.startSession(ctx, task)
				return err
			},
		},
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
		reportTaskCreateProgress(reporter, action.step)
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

func reportTaskCreateProgress(reporter TaskCreateProgressReporter, step TaskCreateProgressStep) {
	if reporter == nil {
		return
	}

	reporter.ReportTaskCreateProgress(step)
}

// taskCreateChanReporter bridges the workflow's progress reporter onto a
// TaskCreateEvent channel without blocking past ctx cancellation.
type taskCreateChanReporter struct {
	ctx    context.Context
	events chan<- TaskCreateEvent
}

func (r taskCreateChanReporter) ReportTaskCreateProgress(step TaskCreateProgressStep) {
	select {
	case <-r.ctx.Done():
	case r.events <- TaskCreateEvent{Progress: &TaskCreateProgressEvent{Step: step}}:
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

func suggestTaskName(ctx context.Context, providerClient ProviderClient, prompt string) (TaskSuggestion, error) {
	suggestion, err := providerClient.SuggestTaskName(ctx, prompt)
	if err != nil {
		return TaskSuggestion{}, fmt.Errorf("suggest task name: %w", err)
	}

	suggestion.Name = strings.TrimSpace(suggestion.Name)
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
