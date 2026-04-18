package core

import (
	"context"
	"fmt"
)

type DoctorResult struct {
	Notes    []string
	Failures []string
}

type NewTaskInput struct {
	Cwd                  string
	Prompt               string
	ConfirmedDisplayName string
	ConfirmedBranchType  string
	Provider             string
}

type CreateTaskFromPRInput struct {
	RepoRoot string
	PR       RepoPullRequest
	Provider string
}

type CreateTaskOptions struct {
	OpenSession bool
}

// Service is the temporary legacy surface retained only so the rest of the
// application can compile while features move to the new task service.
//
// Only the new task-service path should be treated as authoritative.
type Service struct{}

func NewService(
	_ TaskRepository,
	_ HookObservabilityRepository,
	_ ObserverRuntimeRepository,
	_ RepoClient,
	_ TmuxSessionClient,
	_ map[string]ProviderClient,
	_ RepoConfigLoader,
	_ WorkspaceSeeder,
	_ TaskWorkspaceBootstrapper,
	_ SetupScriptRunner,
	_ Config,
) *Service {
	return &Service{}
}

func (s *Service) SetSessionUsageReader(_ SessionUsageReader) {}

func (s *Service) SetPRStatusChecker(_ PRStatusChecker) {}

func (s *Service) Doctor(context.Context, string) (DoctorResult, error) {
	return DoctorResult{}, fmt.Errorf("legacy doctor is not implemented")
}

func (s *Service) SuggestTaskName(context.Context, string, string) (TaskSuggestion, error) {
	return TaskSuggestion{}, fmt.Errorf("legacy suggest task name is not implemented")
}

func (s *Service) CreateTaskWithProgress(
	context.Context,
	NewTaskInput,
	CreateTaskOptions,
	func(TaskProgress),
) (*Task, error) {
	return nil, fmt.Errorf("legacy create task with progress is not implemented")
}

func (s *Service) CreateTaskFromPRWithProgress(
	context.Context,
	CreateTaskFromPRInput,
	CreateTaskOptions,
	func(TaskProgress),
) (*Task, error) {
	return nil, fmt.Errorf("legacy create task from PR with progress is not implemented")
}

func (s *Service) ListTasks(context.Context) ([]*Task, error) {
	return nil, fmt.Errorf("legacy list tasks is not implemented")
}

func (s *Service) ListTaskViews(context.Context) ([]*TaskView, error) {
	return nil, fmt.Errorf("legacy list task views is not implemented")
}

func (s *Service) ListTaskViewsByRepo(context.Context, string) ([]*TaskView, error) {
	return nil, fmt.Errorf("legacy list task views by repo is not implemented")
}

func (s *Service) ListRepoPullRequests(context.Context, string) ([]RepoPullRequest, error) {
	return nil, fmt.Errorf("legacy list repo pull requests is not implemented")
}

func (s *Service) SubscribeTaskHookUpdates(context.Context) (<-chan HookSessionSummary, func(), error) {
	ch := make(chan HookSessionSummary)
	close(ch)
	return ch, func() {}, nil
}

func (s *Service) OpenTask(context.Context, string) error {
	return fmt.Errorf("legacy open task is not implemented")
}

func (s *Service) DeleteTaskResources(context.Context, string) (*Task, error) {
	return nil, fmt.Errorf("legacy delete task resources is not implemented")
}

func (s *Service) GetTaskHookEvents(context.Context, string, int) ([]HookEvent, error) {
	return nil, fmt.Errorf("legacy get task hook events is not implemented")
}

func (s *Service) GetPRStatus(context.Context, string, string) (*PRStatus, error) {
	return nil, fmt.Errorf("legacy get PR status is not implemented")
}

func (s *Service) InvalidatePRCache() {}

func cloneTask(task *Task) *Task {
	if task == nil {
		return nil
	}

	copy := *task
	return &copy
}
