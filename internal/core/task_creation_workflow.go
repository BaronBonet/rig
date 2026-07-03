package core

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	slugpkg "github.com/BaronBonet/rig/internal/pkg/slug"
)

type taskCreationStepAction struct {
	step TaskCreateProgressStep
	run  func(context.Context) error
}

type taskCreationStepPersistence int

const (
	taskCreationStepPersistenceReadyOnly taskCreationStepPersistence = iota
	taskCreationStepPersistenceEachStep
)

func createTaskWithProgress(
	ctx context.Context,
	service *taskService,
	input CreateTaskInput,
	reporter TaskCreateProgressReporter,
) (*Task, error) {
	repoCtx, err := service.gitWorktree.DetectRepo(ctx, input.Cwd)
	if err != nil {
		return nil, err
	}

	if input.Source.PullRequest != nil {
		return createTaskFromPullRequest(ctx, service, repoCtx, input, reporter)
	}

	return createTaskFromPrompt(ctx, service, repoCtx, input, reporter)
}

func retryTaskCreationWithProgress(
	ctx context.Context,
	service *taskService,
	taskID string,
	reporter TaskCreateProgressReporter,
) (*Task, error) {
	task, err := service.taskByID(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if task.CreationStatus != TaskCreationStatusFailed {
		return nil, fmt.Errorf("task creation is not failed")
	}

	steps, ok := taskCreationStepsFrom(taskCreationSteps(
		service,
		task,
		task.RepoRoot,
		func(ctx context.Context) error {
			return service.gitWorktree.CreateTaskWorkspace(ctx, task)
		},
	), task.CreationStep)
	if !ok {
		return nil, fmt.Errorf("task creation failed step %q is not retryable", task.CreationStep)
	}

	if err := runTaskCreationSteps(
		ctx,
		service,
		task,
		reporter,
		steps,
		taskCreationStepPersistenceEachStep,
	); err != nil {
		return task, err
	}
	return task, nil
}

func createTaskFromPrompt(
	ctx context.Context,
	service *taskService,
	repoCtx RepoContext,
	input CreateTaskInput,
	reporter TaskCreateProgressReporter,
) (*Task, error) {
	provider, providerClient, err := service.resolveCreateProvider(ctx, input.Provider)
	if err != nil {
		return nil, err
	}

	reportTaskCreateProgress(reporter, TaskCreateProgressSuggestingName)
	suggestion, err := suggestTaskName(ctx, providerClient, input.Prompt)
	if err != nil {
		return nil, err
	}

	existingTasks, err := service.tasks.ListTasks(ctx)
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

	if err := service.tasks.CreateTask(ctx, task); err != nil {
		return nil, err
	}

	steps := taskCreationSteps(
		service,
		task,
		repoCtx.Root,
		func(ctx context.Context) error {
			return service.gitWorktree.CreateTaskWorkspace(ctx, task)
		},
	)
	if err := runTaskCreationSteps(
		ctx,
		service,
		task,
		reporter,
		steps,
		taskCreationStepPersistenceReadyOnly,
	); err != nil {
		return task, err
	}
	return task, nil
}

func createTaskFromPullRequest(
	ctx context.Context,
	service *taskService,
	repoCtx RepoContext,
	input CreateTaskInput,
	reporter TaskCreateProgressReporter,
) (*Task, error) {
	pr := input.Source.PullRequest
	if pr == nil {
		return nil, fmt.Errorf("pull request source is required")
	}

	provider, _, err := service.resolveCreateProvider(ctx, input.Provider)
	if err != nil {
		return nil, err
	}

	existingTasks, err := service.tasks.ListTasks(ctx)
	if err != nil {
		return nil, err
	}
	if existingTaskForBranch(existingTasks, repoCtx.Root, pr.BranchName) != nil {
		return nil, fmt.Errorf("PR already has workspace")
	}

	inUseByWorktree, err := service.gitWorktree.IsBranchUsedByWorktree(ctx, repoCtx.Root, pr.BranchName)
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
	if err := service.tasks.CreateTask(ctx, task); err != nil {
		return nil, err
	}

	steps := taskCreationSteps(
		service,
		task,
		repoCtx.Root,
		func(ctx context.Context) error {
			return service.gitWorktree.CreateTaskWorkspaceFromPullRequest(ctx, task, pr.Number)
		},
	)
	if err := runTaskCreationSteps(
		ctx,
		service,
		task,
		reporter,
		steps,
		taskCreationStepPersistenceReadyOnly,
	); err != nil {
		return task, err
	}
	return task, nil
}

// resolveCreateProvider resolves the provider a new task will use, defaulting
// to the user's default provider, and returns its configured adapter client.
func (s *taskService) resolveCreateProvider(
	ctx context.Context,
	provider Provider,
) (Provider, ProviderClient, error) {
	setup, err := s.GetProviderSetup(ctx)
	if err != nil {
		return "", nil, err
	}
	if setup == nil {
		return "", nil, ErrProviderSetupRequired
	}

	if provider == "" {
		provider = setup.Default
	}
	providerClient, err := s.configuredClientFor(ctx, provider)
	if err != nil {
		return "", nil, err
	}

	return provider, providerClient, nil
}

func taskCreationSteps(
	service *taskService,
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
				return service.prepareTaskWorkspace(ctx, task, repoRoot)
			},
		},
		{
			step: TaskCreateProgressStartingSession,
			run: func(ctx context.Context) error {
				_, err := service.startTaskRuntime(ctx, task)
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

func runTaskCreationSteps(
	ctx context.Context,
	service *taskService,
	task *Task,
	reporter TaskCreateProgressReporter,
	steps []taskCreationStepAction,
	stepPersistence taskCreationStepPersistence,
) error {
	for _, action := range steps {
		reportTaskCreateProgress(reporter, action.step)
		if stepPersistence == taskCreationStepPersistenceEachStep {
			if err := markTaskCreationStep(ctx, service, task, action.step); err != nil {
				return err
			}
		}
		if err := action.run(ctx); err != nil {
			return markTaskCreationFailed(ctx, service, task, action.step, err)
		}
	}

	return markTaskCreationReady(ctx, service, task)
}

func reportTaskCreateProgress(reporter TaskCreateProgressReporter, step TaskCreateProgressStep) {
	if reporter == nil {
		return
	}

	reporter.ReportTaskCreateProgress(step)
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

func markTaskCreationStep(
	ctx context.Context,
	service *taskService,
	task *Task,
	step TaskCreateProgressStep,
) error {
	task.CreationStatus = TaskCreationStatusCreating
	task.CreationStep = step
	task.CreationError = ""
	task.UpdatedAt = time.Now().UTC()
	return service.tasks.UpdateTask(ctx, task)
}

func markTaskCreationFailed(
	ctx context.Context,
	service *taskService,
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
	if err := service.tasks.UpdateTask(ctx, task); err != nil {
		return fmt.Errorf("%w; persist task creation failure: %w", cause, err)
	}
	return cause
}

func markTaskCreationReady(ctx context.Context, service *taskService, task *Task) error {
	task.CreationStatus = TaskCreationStatusReady
	task.CreationStep = ""
	task.CreationError = ""
	task.UpdatedAt = time.Now().UTC()
	return service.tasks.UpdateTask(ctx, task)
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
