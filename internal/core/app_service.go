package core

import "context"

type LegacyTaskService interface {
	Doctor(ctx context.Context, cwd string) (DoctorResult, error)
	SuggestTaskName(ctx context.Context, prompt string, provider string) (TaskSuggestion, error)
	CreateTaskWithProgress(
		ctx context.Context,
		input NewTaskInput,
		options CreateTaskOptions,
		progress func(TaskProgress),
	) (*Task, error)
	CreateTaskFromPRWithProgress(
		ctx context.Context,
		input CreateTaskFromPRInput,
		options CreateTaskOptions,
		progress func(TaskProgress),
	) (*Task, error)
	ListTasks(ctx context.Context) ([]*Task, error)
	ListTaskViews(ctx context.Context) ([]*TaskView, error)
	ListTaskViewsByRepo(ctx context.Context, repoRoot string) ([]*TaskView, error)
	ListRepoPullRequests(ctx context.Context, repoRoot string) ([]RepoPullRequest, error)
	SubscribeTaskHookUpdates(ctx context.Context) (<-chan HookSessionSummary, func(), error)
	OpenTask(ctx context.Context, idOrSlug string) error
	DeleteTaskResources(ctx context.Context, idOrSlug string) (*Task, error)
	GetTaskHookEvents(ctx context.Context, taskID string, limit int) ([]HookEvent, error)
	GetPRStatus(ctx context.Context, repoRoot string, branchName string) (*PRStatus, error)
	InvalidatePRCache()
}

type AppService struct {
	create TaskService
	legacy LegacyTaskService
}

func NewAppService(create TaskService, legacy LegacyTaskService) *AppService {
	return &AppService{create: create, legacy: legacy}
}

func (s *AppService) CreateTask(ctx context.Context, input CreateTaskInput) (*Task, error) {
	return s.create.CreateTask(ctx, input)
}

func (s *AppService) Doctor(ctx context.Context, cwd string) (DoctorResult, error) {
	return s.legacy.Doctor(ctx, cwd)
}

func (s *AppService) SuggestTaskName(ctx context.Context, prompt string, provider string) (TaskSuggestion, error) {
	return s.legacy.SuggestTaskName(ctx, prompt, provider)
}

func (s *AppService) CreateTaskWithProgress(
	ctx context.Context,
	input NewTaskInput,
	options CreateTaskOptions,
	progress func(TaskProgress),
) (*Task, error) {
	return s.legacy.CreateTaskWithProgress(ctx, input, options, progress)
}

func (s *AppService) CreateTaskFromPRWithProgress(
	ctx context.Context,
	input CreateTaskFromPRInput,
	options CreateTaskOptions,
	progress func(TaskProgress),
) (*Task, error) {
	return s.legacy.CreateTaskFromPRWithProgress(ctx, input, options, progress)
}

func (s *AppService) ListTasks(ctx context.Context) ([]*Task, error) {
	return s.legacy.ListTasks(ctx)
}

func (s *AppService) ListTaskViews(ctx context.Context) ([]*TaskView, error) {
	return s.legacy.ListTaskViews(ctx)
}

func (s *AppService) ListTaskViewsByRepo(ctx context.Context, repoRoot string) ([]*TaskView, error) {
	return s.legacy.ListTaskViewsByRepo(ctx, repoRoot)
}

func (s *AppService) ListRepoPullRequests(ctx context.Context, repoRoot string) ([]RepoPullRequest, error) {
	return s.legacy.ListRepoPullRequests(ctx, repoRoot)
}

func (s *AppService) SubscribeTaskHookUpdates(ctx context.Context) (<-chan HookSessionSummary, func(), error) {
	return s.legacy.SubscribeTaskHookUpdates(ctx)
}

func (s *AppService) OpenTask(ctx context.Context, idOrSlug string) error {
	return s.legacy.OpenTask(ctx, idOrSlug)
}

func (s *AppService) DeleteTaskResources(ctx context.Context, idOrSlug string) (*Task, error) {
	return s.legacy.DeleteTaskResources(ctx, idOrSlug)
}

func (s *AppService) GetTaskHookEvents(ctx context.Context, taskID string, limit int) ([]HookEvent, error) {
	return s.legacy.GetTaskHookEvents(ctx, taskID, limit)
}

func (s *AppService) GetPRStatus(ctx context.Context, repoRoot string, branchName string) (*PRStatus, error) {
	return s.legacy.GetPRStatus(ctx, repoRoot, branchName)
}

func (s *AppService) InvalidatePRCache() {
	s.legacy.InvalidatePRCache()
}
