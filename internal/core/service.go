package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type TaskServiceDependencies struct {
	Tasks                TaskRepository
	GitWorktree          GitWorktreeClient
	TmuxSession          TmuxSessionClient
	PullRequests         PullRequestClient
	Providers            map[Provider]ProviderClient
	Workspace            TaskWorkspaceManager
	ProviderConfig       ProviderConfigStore
	EnableWorkspaceSetup bool
}

// service is the concrete task application service. Composition code holds
// this type directly; consumers depend on the narrower ports it satisfies:
// TaskService (the daemon socket surface), HookEventHandler (the daemon hook
// server), and HealthChecker (doctor).
//
// Task creation lives in the taskCreation module and the workspace/session
// launching behaviour shared with reconnect and provider switching lives in
// the sessionLauncher module; the service delegates to both.
type service struct {
	tasks          TaskRepository
	gitWorktree    GitWorktreeClient
	tmuxSession    TmuxSessionClient
	pullRequests   PullRequestClient
	providers      map[Provider]ProviderClient
	providerConfig ProviderConfigStore
	launcher       *sessionLauncher
	creation       *taskCreation
	observation    *taskObservation
}

// The concrete service must satisfy every port it is wired into. These
// compile-time assertions catch signature drift between the service and a
// port that no other code path in this package would surface.
var (
	_ TaskService      = (*service)(nil)
	_ HookEventHandler = (*service)(nil)
	_ HealthChecker    = (*service)(nil)
)

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

func NewTaskService(deps TaskServiceDependencies) *service {
	launcher := newSessionLauncher(
		deps.Providers,
		deps.ProviderConfig,
		deps.Workspace,
		deps.TmuxSession,
		deps.EnableWorkspaceSetup,
	)

	return &service{
		tasks:          deps.Tasks,
		gitWorktree:    deps.GitWorktree,
		tmuxSession:    deps.TmuxSession,
		pullRequests:   deps.PullRequests,
		providers:      deps.Providers,
		providerConfig: deps.ProviderConfig,
		launcher:       launcher,
		creation:       newTaskCreation(deps.Tasks, deps.GitWorktree, launcher),
		observation: newTaskObservation(
			deps.Tasks,
			deps.TmuxSession,
			deps.Providers,
			deps.ProviderConfig,
		),
	}
}

func (s *service) HealthCheck(ctx context.Context) ([]HealthCheck, error) {
	var checks []HealthCheck
	checks = append(checks, runRequiredHealthCheck(ctx, "git", s.gitWorktree))
	checks = append(checks, runRequiredHealthCheck(ctx, "tmux", s.tmuxSession))

	setup, err := s.GetProviderSetup(ctx)
	switch {
	case err != nil:
		checks = append(checks, HealthCheck{Name: "provider setup", Required: true, Err: err})
	case setup == nil:
		checks = append(checks, HealthCheck{Name: "provider setup", Required: true, Err: ErrProviderSetupRequired})
	default:
		// Doctor validates every configured provider. Supported providers the
		// user has not configured are intentionally not checked.
		for _, provider := range configuredProvidersInOrder(*setup) {
			checks = append(checks, runRequiredDoctorCheck(ctx, string(provider), s.providers[provider]))
		}
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

// configuredProvidersInOrder returns the configured providers with the default
// provider first and the rest in supported-provider order.
func configuredProvidersInOrder(setup ProviderSetup) []Provider {
	ordered := make([]Provider, 0, len(setup.Configured))
	if setup.IsConfigured(setup.Default) {
		ordered = append(ordered, setup.Default)
	}
	for _, provider := range SupportedProviders() {
		if provider == setup.Default || !setup.IsConfigured(provider) {
			continue
		}
		ordered = append(ordered, provider)
	}
	for _, provider := range setup.Configured {
		if !IsSupportedProvider(provider) && provider != setup.Default {
			ordered = append(ordered, provider)
		}
	}
	return ordered
}

// CreateTaskWithProgress creates a new task while reporting coarse-grained
// creation milestones to the provided reporter when non-nil. It is not part
// of the TaskService port; port consumers use CreateTaskStream.
func (s *service) CreateTaskWithProgress(
	ctx context.Context,
	input CreateTaskInput,
	reporter TaskCreateProgressReporter,
) (*Task, error) {
	return s.creation.CreateTaskWithProgress(ctx, input, reporter)
}

// RetryTaskCreationWithProgress resumes a failed task creation from its
// recorded failed step while reporting the same progress milestones as
// initial creation. It is not part of the TaskService port; port consumers
// use RetryTaskCreationStream.
func (s *service) RetryTaskCreationWithProgress(
	ctx context.Context,
	taskID string,
	reporter TaskCreateProgressReporter,
) (*Task, error) {
	return s.creation.RetryTaskCreationWithProgress(ctx, taskID, reporter)
}

func (s *service) CreateTaskStream(
	ctx context.Context,
	input CreateTaskInput,
) (<-chan TaskCreateEvent, error) {
	return s.creation.CreateTaskStream(ctx, input)
}

func (s *service) RetryTaskCreationStream(
	ctx context.Context,
	taskID string,
) (<-chan TaskCreateEvent, error) {
	return s.creation.RetryTaskCreationStream(ctx, taskID)
}

func (s *service) ListTasks(ctx context.Context) ([]*Task, error) {
	return s.tasks.ListTasks(ctx)
}

// RefreshTaskWorkspaceHooks rewrites every configured provider's hook
// registration files into each ready task's workspace, so that manually
// launched configured providers stay observable (and adoptable) in workspaces
// prepared before a provider was configured or by an older Rig. It is not
// part of the TaskService port; the daemon runs it at startup. Failures
// degrade observability but must not stop the daemon.
func (s *service) RefreshTaskWorkspaceHooks(ctx context.Context) []error {
	tasks, err := s.tasks.ListTasks(ctx)
	if err != nil {
		return []error{fmt.Errorf("list tasks for workspace hook refresh: %w", err)}
	}

	var errs []error
	for _, task := range tasks {
		if task == nil || task.CreationStatus != TaskCreationStatusReady {
			continue
		}
		for _, refreshErr := range s.launcher.bootstrapConfiguredProviders(ctx, task, "") {
			errs = append(errs, fmt.Errorf("refresh task %s workspace hooks: %w", task.ID, refreshErr))
		}
	}
	return errs
}

func (s *service) GetTaskActivity(ctx context.Context, taskID string, limit int) ([]TaskActivityEvent, error) {
	return s.observation.GetTaskActivity(ctx, taskID, limit)
}

func (s *service) GetTaskTokenUsage(ctx context.Context, taskID string) (*TaskTokenUsage, error) {
	return s.observation.GetTaskTokenUsage(ctx, taskID)
}

func (s *service) ListRepoPullRequests(ctx context.Context, cwd string) ([]RepoPullRequest, error) {
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

func (s *service) PullRequestStatus(
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

func (s *service) DeleteTask(ctx context.Context, taskID string) error {
	task, err := taskByID(ctx, s.tasks, taskID)
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

func (s *service) ReconnectTaskSession(ctx context.Context, taskID string) error {
	task, err := taskByID(ctx, s.tasks, taskID)
	if err != nil {
		return err
	}

	_, providerClient, err := s.launcher.resolveProvider(ctx, task.Provider)
	if err != nil {
		return err
	}

	resumeMetadata, err := s.tasks.LatestTaskResumeMetadata(ctx, task.ID)
	if err != nil {
		return fmt.Errorf("load task resume metadata: %w", err)
	}
	// Resume metadata recorded for a previous active provider cannot resume the
	// current one; reconnect launches the active provider fresh instead.
	if resumeMetadata != nil && resumeMetadata.Provider != task.Provider {
		resumeMetadata = nil
	}

	if err := s.launcher.prepareWorkspace(ctx, task, task.RepoRoot); err != nil {
		return err
	}
	if err := providerClient.EnsureTaskSessionEnvironment(ctx); err != nil {
		return fmt.Errorf("ensure task session environment: %w", err)
	}

	var launch TaskSessionLaunchSpec
	if resumeMetadata != nil && strings.TrimSpace(resumeMetadata.SessionID) != "" {
		launch, err = providerClient.BuildReconnectTaskSessionLaunchSpec(task, resumeMetadata.SessionID)
		if err != nil {
			return fmt.Errorf("build reconnect task session launch spec: %w", err)
		}
	} else {
		launch, err = promptlessTaskSessionLaunchSpec(providerClient, task)
		if err != nil {
			return fmt.Errorf("build task session launch spec: %w", err)
		}
	}

	if err := s.tmuxSession.StartTaskSession(ctx, task, launch); err != nil {
		return fmt.Errorf("reconnect task session: %w", err)
	}

	return nil
}

// promptlessTaskSessionLaunchSpec builds a fresh provider launch spec without
// prefilling the original task prompt, for reconnects and provider switches.
func promptlessTaskSessionLaunchSpec(providerClient ProviderClient, task *Task) (TaskSessionLaunchSpec, error) {
	launchTask := *task
	launchTask.Prompt = ""
	return providerClient.BuildTaskSessionLaunchSpec(&launchTask)
}

func (s *service) LatestTaskStatus(ctx context.Context, taskID string) (*TaskStatusUpdate, error) {
	return s.observation.LatestTaskStatus(ctx, taskID)
}

func (s *service) SubscribeTaskStatus(
	ctx context.Context,
	taskID string,
) (<-chan TaskStatusUpdate, error) {
	return s.observation.SubscribeTaskStatus(ctx, taskID)
}

func (s *service) HandleHookEvent(ctx context.Context, input HookEventInput) error {
	return s.observation.HandleHookEvent(ctx, input)
}

// taskByID resolves a task record by ID from the repository. Shared by the
// service operations and the task creation module.
func taskByID(ctx context.Context, tasks TaskRepository, taskID string) (*Task, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, ErrTaskNotFound
	}

	taskList, err := tasks.ListTasks(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}

	for _, task := range taskList {
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
