package cli

import (
	"context"
	"testing"

	"agent/internal/core"

	"github.com/stretchr/testify/mock"
)

type MockTaskService struct {
	mock.Mock
}

type MockTaskServiceExpecter struct {
	mock *MockTaskService
}

type MockTaskServiceListTaskViewsCall struct {
	*mock.Call
}

func NewMockTaskService(t *testing.T) *MockTaskService {
	t.Helper()

	m := &MockTaskService{}
	m.Test(t)
	t.Cleanup(func() {
		m.AssertExpectations(t)
	})

	return m
}

func (m *MockTaskService) EXPECT() *MockTaskServiceExpecter {
	return &MockTaskServiceExpecter{mock: m}
}

func (e *MockTaskServiceExpecter) Doctor(ctx any, cwd any) *mock.Call {
	return e.mock.On("Doctor", ctx, cwd)
}

func (e *MockTaskServiceExpecter) SuggestTaskName(ctx any, prompt any, provider any) *mock.Call {
	return e.mock.On("SuggestTaskName", ctx, prompt, provider)
}

func (e *MockTaskServiceExpecter) CreateTaskWithProgress(ctx any, input any, options any, progress any) *mock.Call {
	return e.mock.On("CreateTaskWithProgress", ctx, input, options, progress)
}

func (e *MockTaskServiceExpecter) ListTasks(ctx any) *mock.Call {
	return e.mock.On("ListTasks", ctx)
}

func (e *MockTaskServiceExpecter) ListTaskViews(ctx any) *MockTaskServiceListTaskViewsCall {
	return &MockTaskServiceListTaskViewsCall{Call: e.mock.On("ListTaskViews", ctx)}
}

func (e *MockTaskServiceExpecter) SubscribeTaskHookUpdates(ctx any) *mock.Call {
	return e.mock.On("SubscribeTaskHookUpdates", ctx)
}

func (e *MockTaskServiceExpecter) OpenTask(ctx any, idOrSlug any) *mock.Call {
	return e.mock.On("OpenTask", ctx, idOrSlug)
}

func (e *MockTaskServiceExpecter) DeleteTaskResources(ctx any, idOrSlug any) *mock.Call {
	return e.mock.On("DeleteTaskResources", ctx, idOrSlug)
}

func (e *MockTaskServiceExpecter) GetPRStatus(ctx any, repoRoot any, branchName any) *mock.Call {
	return e.mock.On("GetPRStatus", ctx, repoRoot, branchName)
}

func (e *MockTaskServiceExpecter) InvalidatePRCache() *mock.Call {
	return e.mock.On("InvalidatePRCache")
}

func (c *MockTaskServiceListTaskViewsCall) Run(fn func(context.Context)) *MockTaskServiceListTaskViewsCall {
	c.Call.Run(func(args mock.Arguments) {
		fn(args.Get(0).(context.Context))
	})
	return c
}

func (m *MockTaskService) Doctor(ctx context.Context, cwd string) (core.DoctorResult, error) {
	args := m.Called(ctx, cwd)
	var result core.DoctorResult
	if v, ok := args.Get(0).(core.DoctorResult); ok {
		result = v
	}
	return result, args.Error(1)
}

func (m *MockTaskService) SuggestTaskName(ctx context.Context, prompt string, provider string) (core.TaskSuggestion, error) {
	args := m.Called(ctx, prompt, provider)
	var result core.TaskSuggestion
	if v, ok := args.Get(0).(core.TaskSuggestion); ok {
		result = v
	}
	return result, args.Error(1)
}

func (m *MockTaskService) CreateTaskWithProgress(
	ctx context.Context,
	input core.NewTaskInput,
	options core.CreateTaskOptions,
	progress func(core.TaskProgress),
) (*core.Task, error) {
	args := m.Called(ctx, input, options, progress)
	var result *core.Task
	if v, ok := args.Get(0).(*core.Task); ok {
		result = v
	}
	return result, args.Error(1)
}

func (m *MockTaskService) ListTasks(ctx context.Context) ([]*core.Task, error) {
	args := m.Called(ctx)
	var result []*core.Task
	if v, ok := args.Get(0).([]*core.Task); ok {
		result = v
	}
	return result, args.Error(1)
}

func (m *MockTaskService) ListTaskViews(ctx context.Context) ([]*core.TaskView, error) {
	args := m.Called(ctx)
	var result []*core.TaskView
	if v, ok := args.Get(0).([]*core.TaskView); ok {
		result = v
	}
	return result, args.Error(1)
}

func (m *MockTaskService) SubscribeTaskHookUpdates(ctx context.Context) (<-chan core.HookSessionSummary, func(), error) {
	args := m.Called(ctx)
	var updates <-chan core.HookSessionSummary
	if v, ok := args.Get(0).(<-chan core.HookSessionSummary); ok {
		updates = v
	}
	var cleanup func()
	if v, ok := args.Get(1).(func()); ok {
		cleanup = v
	}
	return updates, cleanup, args.Error(2)
}

func (m *MockTaskService) OpenTask(ctx context.Context, idOrSlug string) error {
	args := m.Called(ctx, idOrSlug)
	return args.Error(0)
}

func (m *MockTaskService) DeleteTaskResources(ctx context.Context, idOrSlug string) (*core.Task, error) {
	args := m.Called(ctx, idOrSlug)
	var result *core.Task
	if v, ok := args.Get(0).(*core.Task); ok {
		result = v
	}
	return result, args.Error(1)
}

func (m *MockTaskService) GetPRStatus(ctx context.Context, repoRoot string, branchName string) (*core.PRStatus, error) {
	args := m.Called(ctx, repoRoot, branchName)
	var result *core.PRStatus
	if v, ok := args.Get(0).(*core.PRStatus); ok {
		result = v
	}
	return result, args.Error(1)
}

func (m *MockTaskService) InvalidatePRCache() {
	m.Called()
}
