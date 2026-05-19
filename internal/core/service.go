package core

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

type TaskServiceDependencies struct {
	Tasks                TaskRepository
	GitWorktree          GitWorktreeClient
	TmuxSession          TmuxSessionClient
	PullRequests         PullRequestClient
	Providers            map[Provider]ProviderClient
	Workspace            TaskWorkspaceManager
	DefaultProvider      Provider
	EnableWorkspaceSetup bool
}

type taskService struct {
	tasks                TaskRepository
	gitWorktree          GitWorktreeClient
	tmuxSession          TmuxSessionClient
	pullRequests         PullRequestClient
	providers            map[Provider]ProviderClient
	workspace            TaskWorkspaceManager
	defaultProvider      Provider
	enableWorkspaceSetup bool
}

type HealthCheck struct {
	Err      error
	Name     string
	Required bool
}

func healthCheckError(checks []HealthCheck) error {
	var failed []string
	var errs []error
	for _, check := range checks {
		if !check.Required || check.Err == nil {
			continue
		}
		failed = append(failed, check.Name)
		errs = append(errs, fmt.Errorf("%s: %w", check.Name, check.Err))
	}
	if len(errs) == 0 {
		return nil
	}

	return fmt.Errorf("required health check(s) failed: %s: %w", strings.Join(failed, ", "), errors.Join(errs...))
}

func NewTaskService(deps TaskServiceDependencies) TaskService {
	return &taskService{
		tasks:                deps.Tasks,
		gitWorktree:          deps.GitWorktree,
		tmuxSession:          deps.TmuxSession,
		pullRequests:         deps.PullRequests,
		providers:            deps.Providers,
		workspace:            deps.Workspace,
		enableWorkspaceSetup: deps.EnableWorkspaceSetup,
		defaultProvider:      deps.DefaultProvider,
	}
}

func (s *taskService) HealthCheck(ctx context.Context) ([]HealthCheck, error) {
	var checks []HealthCheck
	checks = append(checks, runRequiredHealthCheck(ctx, "git", s.gitWorktree))
	checks = append(checks, runRequiredHealthCheck(ctx, "tmux", s.tmuxSession))

	if s.defaultProvider != "" {
		checks = append(checks, runRequiredDoctorCheck(ctx, string(s.defaultProvider), s.providers[s.defaultProvider]))
	}
	for _, provider := range additionalProviderNames(s.providers, s.defaultProvider) {
		checks = append(checks, runRequiredDoctorCheck(ctx, string(provider), s.providers[provider]))
	}

	checks = append(checks, runOptionalHealthCheck(ctx, "gh", s.pullRequests))
	checks = append(checks, runRequiredHealthCheck(ctx, "sqlite", s.tasks))

	return checks, healthCheckError(checks)
}

type healthChecker interface {
	HealthCheck(ctx context.Context) error
}

type doctorChecker interface {
	Doctor(ctx context.Context) error
}

func runRequiredHealthCheck(ctx context.Context, name string, checker healthChecker) HealthCheck {
	return runHealthCheck(ctx, name, true, checker)
}

func runOptionalHealthCheck(ctx context.Context, name string, checker healthChecker) HealthCheck {
	return runHealthCheck(ctx, name, false, checker)
}

func runRequiredDoctorCheck(ctx context.Context, name string, checker doctorChecker) HealthCheck {
	if checker == nil {
		return HealthCheck{
			Name:     name,
			Required: true,
			Err:      fmt.Errorf("not configured"),
		}
	}

	return HealthCheck{
		Name:     name,
		Required: true,
		Err:      checker.Doctor(ctx),
	}
}

func runHealthCheck(ctx context.Context, name string, required bool, checker healthChecker) HealthCheck {
	if checker == nil {
		return HealthCheck{
			Name:     name,
			Required: required,
			Err:      fmt.Errorf("not configured"),
		}
	}

	return HealthCheck{
		Name:     name,
		Required: required,
		Err:      checker.HealthCheck(ctx),
	}
}

func additionalProviderNames(providers map[Provider]ProviderClient, defaultProvider Provider) []Provider {
	names := make([]Provider, 0, len(providers))
	for provider := range providers {
		if provider == defaultProvider {
			continue
		}
		names = append(names, provider)
	}
	sort.Slice(names, func(i, j int) bool {
		return names[i] < names[j]
	})
	return names
}

func (s *taskService) CreateTaskWithProgress(
	ctx context.Context,
	input CreateTaskInput,
	reporter TaskCreateProgressReporter,
) (*Task, error) {
	return createTaskWithProgress(ctx, s, input, reporter)
}

func (s *taskService) RetryTaskCreationWithProgress(
	ctx context.Context,
	taskID string,
	reporter TaskCreateProgressReporter,
) (*Task, error) {
	return retryTaskCreationWithProgress(ctx, s, taskID, reporter)
}

func (s *taskService) ListTasks(ctx context.Context) ([]*Task, error) {
	return s.tasks.ListTasks(ctx)
}

func (s *taskService) GetTaskActivity(ctx context.Context, taskID string, limit int) ([]TaskActivityEvent, error) {
	return getTaskActivity(ctx, s, taskID, limit)
}

func (s *taskService) GetTaskTokenUsage(ctx context.Context, taskID string) (*TaskTokenUsage, error) {
	return getTaskTokenUsage(ctx, s, taskID)
}

func (s *taskService) ListRepoPullRequests(ctx context.Context, cwd string) ([]RepoPullRequest, error) {
	if s.pullRequests == nil {
		return nil, fmt.Errorf("pull request client not configured")
	}

	repoCtx, err := s.gitWorktree.DetectRepo(ctx, cwd)
	if err != nil {
		return nil, err
	}

	prs, err := s.pullRequests.ListRepoPullRequests(ctx, repoCtx.Root)
	if err != nil {
		return nil, err
	}

	existingTasks, err := s.tasks.ListTasks(ctx)
	if err != nil {
		return nil, err
	}

	annotated := make([]RepoPullRequest, 0, len(prs))
	for _, pr := range prs {
		pr.HasExistingTask = existingTaskForBranch(existingTasks, repoCtx.Root, pr.BranchName) != nil
		if !pr.HasExistingTask {
			inUse, err := s.gitWorktree.IsBranchUsedByWorktree(ctx, repoCtx.Root, pr.BranchName)
			if err != nil {
				return nil, err
			}
			pr.HasExistingTask = inUse
		}
		annotated = append(annotated, pr)
	}

	return annotated, nil
}

func (s *taskService) PullRequestStatus(
	ctx context.Context,
	repoRoot string,
	branchName string,
) (*PRStatus, error) {
	repoRoot = strings.TrimSpace(repoRoot)
	branchName = strings.TrimSpace(branchName)
	if repoRoot == "" || branchName == "" || s.pullRequests == nil {
		return &PRStatus{State: PRStateNone}, nil
	}

	status := &PRStatus{State: PRStateNone}
	checkedStatus, checkErr := s.pullRequests.CheckPullRequestStatus(ctx, repoRoot, branchName)
	if checkErr == nil && checkedStatus != nil {
		status = checkedStatus
	}

	return clonePRStatus(status), nil
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

func (s *taskService) ReconnectTaskSession(ctx context.Context, taskID string) error {
	task, err := s.taskByID(ctx, taskID)
	if err != nil {
		return err
	}

	resumeMetadata, err := s.tasks.LatestTaskResumeMetadata(ctx, task.ID)
	if err != nil {
		return fmt.Errorf("load task resume metadata: %w", err)
	}
	if resumeMetadata == nil || strings.TrimSpace(resumeMetadata.SessionID) == "" {
		return fmt.Errorf("reconnect task session: no resume metadata for task %q", task.ID)
	}

	if err := s.prepareTaskWorkspace(ctx, task, task.RepoRoot); err != nil {
		return err
	}

	providerClient, err := s.providerClientFor(task.Provider)
	if err != nil {
		return err
	}
	if err := providerClient.EnsureTaskSessionEnvironment(ctx); err != nil {
		return fmt.Errorf("ensure task session environment: %w", err)
	}

	launch, err := providerClient.BuildReconnectTaskSessionLaunchSpec(task, resumeMetadata.SessionID)
	if err != nil {
		return fmt.Errorf("build reconnect task session launch spec: %w", err)
	}
	if err := s.tmuxSession.StartTaskSession(ctx, task, launch); err != nil {
		return fmt.Errorf("reconnect task session: %w", err)
	}

	return nil
}

func (s *taskService) LatestTaskStatus(ctx context.Context, taskID string) (*TaskStatusUpdate, error) {
	return latestTaskStatus(ctx, s, taskID)
}

func (s *taskService) SubscribeTaskStatus(
	ctx context.Context,
	taskID string,
) (<-chan TaskStatusUpdate, error) {
	return subscribeTaskStatus(ctx, s, taskID)
}

func (s *taskService) HandleHookEvent(ctx context.Context, input HookEventInput) error {
	return handleProviderHookEvent(ctx, s, input)
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
	if err := providerClient.EnsureTaskSessionEnvironment(ctx); err != nil {
		return task, fmt.Errorf("ensure task session environment: %w", err)
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

func clonePRStatus(status *PRStatus) *PRStatus {
	if status == nil {
		return nil
	}

	cloned := *status
	return &cloned
}
