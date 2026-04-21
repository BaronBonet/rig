package core

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	slugpkg "rig/internal/pkg/slug"
)

type TaskServiceDependencies struct {
	Tasks                TaskRepository
	GitWorktree          GitWorktreeClient
	TmuxSession          TmuxSessionClient
	Providers            map[Provider]ProviderClient
	Workspace            TaskWorkspaceManager
	DefaultProvider      Provider
	EnableWorkspaceSetup bool
}

type taskService struct {
	tasks                TaskRepository
	gitWorktree          GitWorktreeClient
	tmuxSession          TmuxSessionClient
	providers            map[Provider]ProviderClient
	workspace            TaskWorkspaceManager
	defaultProvider      Provider
	enableWorkspaceSetup bool
}

func NewTaskService(deps TaskServiceDependencies) TaskService {
	return &taskService{
		tasks:                deps.Tasks,
		gitWorktree:          deps.GitWorktree,
		tmuxSession:          deps.TmuxSession,
		providers:            deps.Providers,
		workspace:            deps.Workspace,
		enableWorkspaceSetup: deps.EnableWorkspaceSetup,
		defaultProvider:      deps.DefaultProvider,
	}
}

func reportTaskCreateProgress(reporter TaskCreateProgressReporter, step TaskCreateProgressStep) {
	if reporter == nil {
		return
	}

	reporter.ReportTaskCreateProgress(step)
}

func (s *taskService) CreateTaskWithProgress(
	ctx context.Context,
	input CreateTaskInput,
	reporter TaskCreateProgressReporter,
) (*Task, error) {
	repoCtx, err := s.gitWorktree.DetectRepo(ctx, input.Cwd)
	if err != nil {
		return nil, err
	}

	if input.Source.PullRequest != nil {
		return s.createTaskFromPullRequest(ctx, repoCtx, input)
	}

	return s.createTaskFromPrompt(ctx, repoCtx, input, reporter)
}

func (s *taskService) ListTasks(ctx context.Context) ([]*Task, error) {
	return s.tasks.ListTasks(ctx)
}

func (s *taskService) DeleteTask(ctx context.Context, taskID string) error {
	task, err := s.taskByID(ctx, taskID)
	if err != nil {
		return err
	}

	if err := s.tmuxSession.DeleteTaskSession(ctx, task); err != nil {
		return fmt.Errorf("delete task session: %w", err)
	}
	if err := s.gitWorktree.RemoveTaskWorkspace(ctx, task); err != nil {
		return fmt.Errorf("remove task workspace: %w", err)
	}
	if err := s.tasks.DeleteTask(ctx, task.ID); err != nil {
		return fmt.Errorf("delete task record: %w", err)
	}

	return nil
}

func (s *taskService) LatestTaskStatus(ctx context.Context, taskID string) (*TaskStatusUpdate, error) {
	return s.tasks.LatestTaskStatus(ctx, strings.TrimSpace(taskID))
}

func (s *taskService) SubscribeTaskStatus(
	ctx context.Context,
	taskID string,
) (<-chan TaskStatusUpdate, error) {
	return s.tasks.SubscribeTaskStatus(ctx, strings.TrimSpace(taskID))
}

func (s *taskService) HandleHookEvent(ctx context.Context, input HookEventInput) error {
	if input.Provider == "" {
		return ErrUnmanagedHookEvent
	}

	providerClient, err := s.providerClientFor(input.Provider)
	if err != nil {
		return err
	}

	input.TaskID = strings.TrimSpace(input.TaskID)
	if input.TaskID == "" {
		resolvedTaskID, err := s.resolveTaskIDFromCwd(ctx, input.Cwd)
		if err != nil {
			return err
		}
		input.TaskID = resolvedTaskID
	}

	update, err := providerClient.HookEventToTaskStatus(input)
	if err != nil {
		return err
	}
	if update == nil {
		return nil
	}
	if strings.TrimSpace(update.TaskID) == "" {
		update.TaskID = input.TaskID
	}
	if update.Provider == "" {
		update.Provider = input.Provider
	}
	if update.ObservedAt.IsZero() {
		update.ObservedAt = input.OccurredAt
	}

	update.TaskID = strings.TrimSpace(update.TaskID)
	if update.TaskID == "" {
		return fmt.Errorf("task status update task ID is required")
	}

	return s.tasks.UpsertTaskStatus(ctx, *update)
}

func (s *taskService) createTaskFromPrompt(
	ctx context.Context,
	repoCtx RepoContext,
	input CreateTaskInput,
	reporter TaskCreateProgressReporter,
) (*Task, error) {
	providerClient, err := s.providerClientFor(input.Provider)
	if err != nil {
		return nil, err
	}

	reportTaskCreateProgress(reporter, TaskCreateProgressSuggestingName)
	suggestion, err := s.suggestTaskName(ctx, providerClient, input.Prompt)
	if err != nil {
		return nil, err
	}

	existingTasks, err := s.tasks.ListTasks(ctx)
	if err != nil {
		return nil, err
	}

	taskSlug := uniqueTaskSlug(repoCtx.Root, suggestion.Name, existingTasks)
	task := newPromptTaskRecord(
		repoCtx,
		input.Provider,
		s.defaultProvider,
		suggestion.Name,
		taskSlug,
		suggestion.BranchType,
	)
	task.Prompt = input.Prompt

	if err := s.tasks.CreateTask(ctx, task); err != nil {
		return nil, err
	}
	reportTaskCreateProgress(reporter, TaskCreateProgressCreatingWorktree)
	if err := s.gitWorktree.CreateTaskWorkspace(ctx, task); err != nil {
		return task, fmt.Errorf("create worktree: %w", err)
	}

	reportTaskCreateProgress(reporter, TaskCreateProgressPreparingWorkspace)
	if err := s.prepareTaskWorkspace(ctx, task, repoCtx.Root); err != nil {
		return task, err
	}

	reportTaskCreateProgress(reporter, TaskCreateProgressStartingSession)
	return s.startTaskRuntime(ctx, task)
}

func (s *taskService) createTaskFromPullRequest(
	ctx context.Context,
	repoCtx RepoContext,
	input CreateTaskInput,
) (*Task, error) {
	pr := input.Source.PullRequest
	if pr == nil {
		return nil, fmt.Errorf("pull request source is required")
	}

	existingTasks, err := s.tasks.ListTasks(ctx)
	if err != nil {
		return nil, err
	}
	if existingTaskForBranch(existingTasks, repoCtx.Root, pr.BranchName) != nil {
		return nil, fmt.Errorf("PR already has workspace")
	}

	inUseByWorktree, err := s.gitWorktree.IsBranchUsedByWorktree(ctx, repoCtx.Root, pr.BranchName)
	if err != nil {
		return nil, err
	}
	if inUseByWorktree {
		return nil, fmt.Errorf("PR already has workspace")
	}

	taskSlug := uniqueTaskSlug(repoCtx.Root, pr.BranchName, existingTasks)
	task := newPullRequestTaskRecord(
		repoCtx,
		input.Provider,
		s.defaultProvider,
		prDisplayName(*pr),
		taskSlug,
		pr.BranchName,
	)
	if err := s.tasks.CreateTask(ctx, task); err != nil {
		return nil, err
	}
	if err := s.gitWorktree.CreateTaskWorkspaceFromBranch(ctx, task); err != nil {
		return task, fmt.Errorf("create worktree: %w", err)
	}
	if err := s.prepareTaskWorkspace(ctx, task, repoCtx.Root); err != nil {
		return task, err
	}

	return s.startTaskRuntime(ctx, task)
}

func (s *taskService) suggestTaskName(
	ctx context.Context,
	providerClient ProviderClient,
	prompt string,
) (TaskSuggestion, error) {
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

func (s *taskService) providerClientFor(provider Provider) (ProviderClient, error) {
	if provider == "" {
		provider = s.defaultProvider
	}

	providerClient, ok := s.providers[provider]
	if !ok {
		return nil, fmt.Errorf("provider %q unavailable", provider)
	}

	return providerClient, nil
}

func (s *taskService) startTaskRuntime(ctx context.Context, task *Task) (*Task, error) {
	providerClient, err := s.providerClientFor(task.Provider)
	if err != nil {
		return task, err
	}

	launch, err := providerClient.BuildTaskSessionLaunchSpec(task)
	if err != nil {
		return task, fmt.Errorf("build task session launch spec: %w", err)
	}
	if err := s.tmuxSession.StartTaskSession(ctx, task, launch); err != nil {
		return task, fmt.Errorf("start task session: %w", err)
	}

	return task, nil
}

func (s *taskService) prepareTaskWorkspace(ctx context.Context, task *Task, repoRoot string) error {
	bootstrapSpec, err := s.buildWorkspaceBootstrapSpec(ctx, task)
	if err != nil {
		return fmt.Errorf("build workspace bootstrap spec: %w", err)
	}

	if s.workspace == nil {
		return nil
	}

	if s.enableWorkspaceSetup {
		if err := s.workspace.SetupTaskWorkspace(ctx, task, repoRoot); err != nil {
			return fmt.Errorf("setup workspace: %w", err)
		}
	}

	if err := s.workspace.BootstrapTaskWorkspace(ctx, task, bootstrapSpec); err != nil {
		return fmt.Errorf("bootstrap workspace: %w", err)
	}

	return nil
}

func (s *taskService) buildWorkspaceBootstrapSpec(ctx context.Context, task *Task) (WorkspaceBootstrapSpec, error) {
	providerClient, err := s.providerClientFor(task.Provider)
	if err != nil {
		return WorkspaceBootstrapSpec{}, err
	}

	return providerClient.BuildWorkspaceBootstrapSpec(task)
}

func (s *taskService) resolveTaskIDFromCwd(ctx context.Context, cwd string) (string, error) {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return "", ErrUnmanagedHookEvent
	}

	tasks, err := s.tasks.ListTasks(ctx)
	if err != nil {
		return "", fmt.Errorf("list tasks for hook resolution: %w", err)
	}

	for _, task := range tasks {
		if task != nil && strings.TrimSpace(task.WorktreePath) == cwd {
			return strings.TrimSpace(task.ID), nil
		}
	}

	return "", ErrUnmanagedHookEvent
}

func (s *taskService) taskByID(ctx context.Context, taskID string) (*Task, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, ErrTaskNotFound
	}

	tasks, err := s.tasks.ListTasks(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tasks for delete: %w", err)
	}

	for _, task := range tasks {
		if task != nil && strings.TrimSpace(task.ID) == taskID {
			return task, nil
		}
	}

	return nil, ErrTaskNotFound
}

func newPromptTaskRecord(
	repoCtx RepoContext,
	provider Provider,
	defaultProvider Provider,
	displayName string,
	taskSlug string,
	branchType string,
) *Task {
	if provider == "" {
		provider = defaultProvider
	}

	now := time.Now().UTC()
	taskID := fmt.Sprintf("%d", now.UnixNano())

	return &Task{
		ID:           taskID,
		Slug:         taskSlug,
		DisplayName:  displayName,
		RepoRoot:     repoCtx.Root,
		RepoName:     repoCtx.Name,
		BranchName:   branchNameForTask(taskSlug, branchType),
		WorktreePath: taskWorktreePath(repoCtx, taskSlug),
		TmuxSession:  taskSessionName(repoCtx, taskSlug),
		Provider:     provider,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

func newPullRequestTaskRecord(
	repoCtx RepoContext,
	provider Provider,
	defaultProvider Provider,
	displayName string,
	taskSlug string,
	branchName string,
) *Task {
	if provider == "" {
		provider = defaultProvider
	}

	now := time.Now().UTC()
	taskID := fmt.Sprintf("%d", now.UnixNano())

	return &Task{
		ID:           taskID,
		Slug:         taskSlug,
		DisplayName:  displayName,
		RepoRoot:     repoCtx.Root,
		RepoName:     repoCtx.Name,
		BranchName:   strings.TrimSpace(branchName),
		WorktreePath: taskWorktreePath(repoCtx, taskSlug),
		TmuxSession:  taskSessionName(repoCtx, taskSlug),
		Provider:     provider,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

func branchNameForTask(taskSlug string, branchType string) string {
	return TaskSuggestion{BranchType: branchType}.BranchTypeOrDefault() + "/" + taskSlug
}

func taskWorktreePath(repoCtx RepoContext, taskSlug string) string {
	return filepath.Join(filepath.Dir(repoCtx.Root), repoCtx.Name+"-"+taskSlug)
}

func taskSessionName(repoCtx RepoContext, taskSlug string) string {
	return repoCtx.Name + "_" + strings.ReplaceAll(taskSlug, "-", "_")
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
