package core

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTaskServiceContract_ExposesStatusMethods(t *testing.T) {
	var _ interface {
		LatestTaskStatus(context.Context, string) (*TaskStatusUpdate, error)
		SubscribeTaskStatus(context.Context, string) (<-chan TaskStatusUpdate, error)
		PublishTaskStatus(context.Context, TaskStatusUpdate) error
	} = (TaskService)(nil)
}

func TestTaskFrontendContract_ExposesCreateAndStatusReadMethods(t *testing.T) {
	var _ interface {
		CreateTask(context.Context, CreateTaskInput) (*Task, error)
		LatestTaskStatus(context.Context, string) (*TaskStatusUpdate, error)
		SubscribeTaskStatus(context.Context, string) (<-chan TaskStatusUpdate, error)
	} = (TaskFrontend)(nil)
}

func TestTaskStatusService_LatestReturnsNilWhenTaskHasNoStatus(t *testing.T) {
	svc := newTestTaskService(t)

	update, err := svc.service.LatestTaskStatus(t.Context(), "task-123")
	require.NoError(t, err)
	require.Nil(t, update)
}

func TestTaskStatusService_SubscribePublishesMatchingTaskUpdates(t *testing.T) {
	svc := newTestTaskService(t)

	updates, err := svc.service.SubscribeTaskStatus(t.Context(), "task-123")
	require.NoError(t, err)

	require.NoError(t, svc.service.PublishTaskStatus(t.Context(), TaskStatusUpdate{
		TaskID:       "task-999",
		Provider:     AgentProviderCodex,
		Phase:        TaskStatusPhaseWorking,
		RawEventName: "PreToolUse",
		ObservedAt:   time.Date(2026, time.April, 19, 11, 0, 0, 0, time.UTC),
	}))

	require.NoError(t, svc.service.PublishTaskStatus(t.Context(), TaskStatusUpdate{
		TaskID:       "task-123",
		Provider:     AgentProviderCodex,
		Phase:        TaskStatusPhaseWorking,
		RawEventName: "PostToolUse",
		ObservedAt:   time.Date(2026, time.April, 19, 11, 1, 0, 0, time.UTC),
	}))

	select {
	case update := <-updates:
		require.Equal(t, "task-123", update.TaskID)
		require.Equal(t, TaskStatusPhaseWorking, update.Phase)
		require.Equal(t, "PostToolUse", update.RawEventName)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for matching task update")
	}
}

func TestTaskStatusService_SubscribeClosesChannelWhenContextIsCancelled(t *testing.T) {
	svc := newTestTaskService(t)
	ctx, cancel := context.WithCancel(t.Context())

	updates, err := svc.service.SubscribeTaskStatus(ctx, "task-123")
	require.NoError(t, err)

	cancel()

	select {
	case _, ok := <-updates:
		require.False(t, ok)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for subscription channel to close")
	}
}

func TestTaskStatusService_LatestReturnsMostRecentTaskUpdate(t *testing.T) {
	svc := newTestTaskService(t)

	first := TaskStatusUpdate{
		TaskID:       "task-123",
		Provider:     AgentProviderCodex,
		Phase:        TaskStatusPhaseStarting,
		RawEventName: "SessionStart",
		ObservedAt:   time.Date(2026, time.April, 19, 11, 2, 0, 0, time.UTC),
	}
	second := TaskStatusUpdate{
		TaskID:       "task-123",
		Provider:     AgentProviderCodex,
		Phase:        TaskStatusPhaseWaitingForInput,
		RawEventName: "Stop",
		ObservedAt:   time.Date(2026, time.April, 19, 11, 3, 0, 0, time.UTC),
	}

	require.NoError(t, svc.service.PublishTaskStatus(t.Context(), first))
	require.NoError(t, svc.service.PublishTaskStatus(t.Context(), second))

	update, err := svc.service.LatestTaskStatus(t.Context(), "task-123")
	require.NoError(t, err)
	require.NotNil(t, update)
	require.Equal(t, second, *update)
}
