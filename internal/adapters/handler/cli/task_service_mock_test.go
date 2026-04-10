package cli

import (
	"context"

	"agent/internal/core"

	"github.com/stretchr/testify/mock"
)

type MockTaskService struct {
	mock.Mock
}

type MockTaskService_Expecter struct {
	mock *mock.Mock
}

func NewMockTaskService(t interface {
	mock.TestingT
	Cleanup(func())
}) *MockTaskService {
	service := &MockTaskService{}
	service.Mock.Test(t)
	t.Cleanup(func() {
		service.AssertExpectations(t)
	})
	return service
}

func (m *MockTaskService) EXPECT() *MockTaskService_Expecter {
	return &MockTaskService_Expecter{mock: &m.Mock}
}

func (m *MockTaskService) Doctor(ctx context.Context, cwd string) (core.DoctorResult, error) {
	args := m.Called(ctx, cwd)

	var result core.DoctorResult
	if value := args.Get(0); value != nil {
		result = value.(core.DoctorResult)
	}

	return result, args.Error(1)
}

func (e *MockTaskService_Expecter) Doctor(ctx any, cwd any) *mock.Call {
	return e.mock.On("Doctor", ctx, cwd)
}

func (m *MockTaskService) SuggestTaskName(ctx context.Context, prompt string, provider string) (string, error) {
	args := m.Called(ctx, prompt, provider)
	return args.String(0), args.Error(1)
}

func (e *MockTaskService_Expecter) SuggestTaskName(ctx any, prompt any, provider any) *mock.Call {
	return e.mock.On("SuggestTaskName", ctx, prompt, provider)
}

func (m *MockTaskService) CreateTaskWithProgress(
	ctx context.Context,
	input core.NewTaskInput,
	options core.CreateTaskOptions,
	progress func(core.TaskProgress),
) (*core.Task, error) {
	args := m.Called(ctx, input, options, progress)

	var task *core.Task
	if value := args.Get(0); value != nil {
		task = value.(*core.Task)
	}

	return task, args.Error(1)
}

func (e *MockTaskService_Expecter) CreateTaskWithProgress(
	ctx any,
	input any,
	options any,
	progress any,
) *mock.Call {
	return e.mock.On("CreateTaskWithProgress", ctx, input, options, progress)
}

func (m *MockTaskService) ListTasks(ctx context.Context) ([]*core.Task, error) {
	args := m.Called(ctx)

	var tasks []*core.Task
	if value := args.Get(0); value != nil {
		tasks = value.([]*core.Task)
	}

	return tasks, args.Error(1)
}

func (e *MockTaskService_Expecter) ListTasks(ctx any) *mock.Call {
	return e.mock.On("ListTasks", ctx)
}

func (m *MockTaskService) ListTaskViews(ctx context.Context) ([]*core.TaskView, error) {
	args := m.Called(ctx)

	var views []*core.TaskView
	if value := args.Get(0); value != nil {
		views = value.([]*core.TaskView)
	}

	return views, args.Error(1)
}

func (e *MockTaskService_Expecter) ListTaskViews(ctx any) *mock.Call {
	return e.mock.On("ListTaskViews", ctx)
}

func (m *MockTaskService) SubscribeTaskHookUpdates(
	ctx context.Context,
) (<-chan core.HookSessionSummary, func(), error) {
	args := m.Called(ctx)

	var updates <-chan core.HookSessionSummary
	if value := args.Get(0); value != nil {
		updates = value.(<-chan core.HookSessionSummary)
	}

	cleanup := func() {}
	if value := args.Get(1); value != nil {
		cleanup = value.(func())
	}

	return updates, cleanup, args.Error(2)
}

func (e *MockTaskService_Expecter) SubscribeTaskHookUpdates(ctx any) *mock.Call {
	return e.mock.On("SubscribeTaskHookUpdates", ctx)
}

func (m *MockTaskService) OpenTask(ctx context.Context, idOrSlug string) error {
	args := m.Called(ctx, idOrSlug)
	return args.Error(0)
}

func (e *MockTaskService_Expecter) OpenTask(ctx any, idOrSlug any) *mock.Call {
	return e.mock.On("OpenTask", ctx, idOrSlug)
}

func (m *MockTaskService) DeleteTaskResources(ctx context.Context, idOrSlug string) (*core.Task, error) {
	args := m.Called(ctx, idOrSlug)

	var task *core.Task
	if value := args.Get(0); value != nil {
		task = value.(*core.Task)
	}

	return task, args.Error(1)
}

func (e *MockTaskService_Expecter) DeleteTaskResources(ctx any, idOrSlug any) *mock.Call {
	return e.mock.On("DeleteTaskResources", ctx, idOrSlug)
}

func (m *MockTaskService) GetPRStatus(
	ctx context.Context,
	repoRoot string,
	branchName string,
) (*core.PRStatus, error) {
	args := m.Called(ctx, repoRoot, branchName)

	var status *core.PRStatus
	if value := args.Get(0); value != nil {
		status = value.(*core.PRStatus)
	}

	return status, args.Error(1)
}

func (e *MockTaskService_Expecter) GetPRStatus(ctx any, repoRoot any, branchName any) *mock.Call {
	return e.mock.On("GetPRStatus", ctx, repoRoot, branchName)
}

func (m *MockTaskService) InvalidatePRCache() {
	m.Called()
}

func (e *MockTaskService_Expecter) InvalidatePRCache() *mock.Call {
	return e.mock.On("InvalidatePRCache")
}
