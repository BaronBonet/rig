package core

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTaskServiceContract_ExposesStatusMethods(t *testing.T) {
	var _ interface {
		GetTaskActivity(context.Context, string, int) ([]TaskActivityEvent, error)
		LatestTaskStatus(context.Context, string) (*TaskStatusUpdate, error)
		SubscribeTaskStatus(context.Context, string) (<-chan TaskStatusUpdate, error)
		HandleHookEvent(context.Context, HookEventInput) error
	} = (TaskService)(nil)
}

func TestTaskFrontendContract_ExposesCreateAndStatusReadMethods(t *testing.T) {
	var _ interface {
		AttachTaskSession(context.Context, *Task) error
		CreateTaskStream(context.Context, CreateTaskInput) (<-chan TaskCreateEvent, error)
		GetTaskActivity(context.Context, string, int) ([]TaskActivityEvent, error)
		LatestTaskStatus(context.Context, string) (*TaskStatusUpdate, error)
		SubscribeTaskStatus(context.Context, string) (<-chan TaskStatusUpdate, error)
	} = (TaskFrontend)(nil)
}

func TestTaskStatusService_GetTaskActivityReturnsRepositoryEvents(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.activityByTask = map[string][]TaskActivityEvent{
		"task-123": {
			{
				TaskID:     "task-123",
				EventName:  "UserPromptSubmit",
				Role:       TaskActivityRoleUser,
				Text:       "restore the task detail previews",
				ObservedAt: time.Date(2026, time.April, 23, 10, 0, 0, 0, time.UTC),
			},
			{
				TaskID:     "task-123",
				EventName:  "Stop",
				Role:       TaskActivityRoleAssistant,
				Text:       "Rewired the detail panel to show recent task activity.",
				ObservedAt: time.Date(2026, time.April, 23, 10, 1, 0, 0, time.UTC),
			},
		},
	}

	events, err := svc.service.GetTaskActivity(t.Context(), "task-123", 5)
	require.NoError(t, err)
	require.Equal(t, []TaskActivityEvent{
		{
			TaskID:     "task-123",
			EventName:  "UserPromptSubmit",
			Role:       TaskActivityRoleUser,
			Text:       "restore the task detail previews",
			ObservedAt: time.Date(2026, time.April, 23, 10, 0, 0, 0, time.UTC),
		},
		{
			TaskID:     "task-123",
			EventName:  "Stop",
			Role:       TaskActivityRoleAssistant,
			Text:       "Rewired the detail panel to show recent task activity.",
			ObservedAt: time.Date(2026, time.April, 23, 10, 1, 0, 0, time.UTC),
		},
	}, events)
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

	require.NoError(t, svc.taskRepoMock.UpsertTaskStatus(t.Context(), TaskStatusUpdate{
		TaskID:       "task-999",
		Provider:     ProviderCodex,
		Phase:        TaskStatusPhaseWorking,
		RawEventName: "PreToolUse",
		ObservedAt:   time.Date(2026, time.April, 19, 11, 0, 0, 0, time.UTC),
	}))

	require.NoError(t, svc.taskRepoMock.UpsertTaskStatus(t.Context(), TaskStatusUpdate{
		TaskID:       "task-123",
		Provider:     ProviderCodex,
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
		Provider:     ProviderCodex,
		Phase:        TaskStatusPhaseStarting,
		RawEventName: "SessionStart",
		ObservedAt:   time.Date(2026, time.April, 19, 11, 2, 0, 0, time.UTC),
	}
	second := TaskStatusUpdate{
		TaskID:       "task-123",
		Provider:     ProviderCodex,
		Phase:        TaskStatusPhaseWaitingForInput,
		RawEventName: "Stop",
		ObservedAt:   time.Date(2026, time.April, 19, 11, 3, 0, 0, time.UTC),
	}

	require.NoError(t, svc.taskRepoMock.UpsertTaskStatus(t.Context(), first))
	require.NoError(t, svc.taskRepoMock.UpsertTaskStatus(t.Context(), second))

	update, err := svc.service.LatestTaskStatus(t.Context(), "task-123")
	require.NoError(t, err)
	require.NotNil(t, update)
	require.Equal(t, second, *update)
}

func TestTaskStatusService_LatestReturnsStoppedWhenTaskSessionIsNotRunningProvider(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.listTasks = []*Task{{
		ID:          "task-123",
		Provider:    ProviderCodex,
		TmuxSession: "repo_task",
	}}
	svc.sessionClient.inspectState = TaskSessionRuntimeState{
		Exists:         true,
		ActiveCommands: []string{"zsh"},
	}

	latestHookStatus := TaskStatusUpdate{
		TaskID:       "task-123",
		Provider:     ProviderCodex,
		Phase:        TaskStatusPhaseWorking,
		RawEventName: "PreToolUse",
		ObservedAt:   time.Date(2026, time.April, 19, 11, 2, 0, 0, time.UTC),
	}
	require.NoError(t, svc.taskRepoMock.UpsertTaskStatus(t.Context(), latestHookStatus))

	update, err := svc.service.LatestTaskStatus(t.Context(), "task-123")
	require.NoError(t, err)
	require.NotNil(t, update)
	require.Equal(t, TaskStatusPhaseStopped, update.Phase)
	require.Equal(t, "TaskSessionStopped", update.RawEventName)
	require.Equal(t, latestHookStatus.ObservedAt, update.ObservedAt)
}

func TestTaskStatusService_LatestStaysWorkingWhenAnyTaskPaneRunsProvider(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.listTasks = []*Task{{
		ID:          "task-123",
		Provider:    ProviderCodex,
		TmuxSession: "repo_task",
	}}
	svc.sessionClient.inspectState = TaskSessionRuntimeState{
		Exists:         true,
		ActiveCommands: []string{"zsh", "codex"},
	}

	latestHookStatus := TaskStatusUpdate{
		TaskID:       "task-123",
		Provider:     ProviderCodex,
		Phase:        TaskStatusPhaseWorking,
		RawEventName: "PreToolUse",
		ObservedAt:   time.Date(2026, time.April, 19, 11, 2, 0, 0, time.UTC),
	}
	require.NoError(t, svc.taskRepoMock.UpsertTaskStatus(t.Context(), latestHookStatus))

	update, err := svc.service.LatestTaskStatus(t.Context(), "task-123")
	require.NoError(t, err)
	require.NotNil(t, update)
	require.Equal(t, latestHookStatus, *update)
}

func TestTaskStatusService_LatestStaysWorkingWhenCodexPaneRunsPlatformBinary(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.listTasks = []*Task{{
		ID:          "task-123",
		Provider:    ProviderCodex,
		TmuxSession: "repo_task",
	}}
	svc.sessionClient.inspectState = TaskSessionRuntimeState{
		Exists:         true,
		ActiveCommands: []string{"codex-aarch64-a"},
	}

	latestHookStatus := TaskStatusUpdate{
		TaskID:       "task-123",
		Provider:     ProviderCodex,
		Phase:        TaskStatusPhaseWorking,
		RawEventName: "PreToolUse",
		ObservedAt:   time.Date(2026, time.April, 19, 11, 2, 0, 0, time.UTC),
	}
	require.NoError(t, svc.taskRepoMock.UpsertTaskStatus(t.Context(), latestHookStatus))

	update, err := svc.service.LatestTaskStatus(t.Context(), "task-123")
	require.NoError(t, err)
	require.NotNil(t, update)
	require.Equal(t, latestHookStatus, *update)
}

func TestTaskStatusService_HandleHookEventResolvesTaskIDAndPublishesMappedUpdate(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.listTasks = []*Task{{
		ID:           "task-123",
		WorktreePath: "/tmp/repo-task",
	}}
	svc.providerRepo.hookUpdate = &TaskStatusUpdate{
		Phase:        TaskStatusPhaseStarting,
		RawEventName: "SessionStart",
	}

	err := svc.service.HandleHookEvent(t.Context(), HookEventInput{
		OccurredAt: time.Date(2026, time.April, 20, 9, 0, 0, 0, time.UTC),
		Provider:   ProviderCodex,
		Cwd:        "/tmp/repo-task",
		EventName:  "SessionStart",
	})
	require.NoError(t, err)
	require.Equal(t, "task-123", svc.providerRepo.hookInput.TaskID)
	require.Equal(t, ProviderCodex, svc.providerRepo.hookInput.Provider)

	update, err := svc.service.LatestTaskStatus(t.Context(), "task-123")
	require.NoError(t, err)
	require.NotNil(t, update)
	require.Equal(t, TaskStatusUpdate{
		TaskID:       "task-123",
		Provider:     ProviderCodex,
		Phase:        TaskStatusPhaseStarting,
		RawEventName: "SessionStart",
		ObservedAt:   time.Date(2026, time.April, 20, 9, 0, 0, 0, time.UTC),
	}, *update)
}

func TestTaskStatusService_HandleHookEventPersistsResumeMetadataWhenSessionIDPresent(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.listTasks = []*Task{{
		ID:           "task-123",
		WorktreePath: "/tmp/repo-task",
	}}

	err := svc.service.HandleHookEvent(t.Context(), HookEventInput{
		OccurredAt: time.Date(2026, time.April, 20, 9, 5, 0, 0, time.UTC),
		Provider:   ProviderCodex,
		Cwd:        "/tmp/repo-task",
		EventName:  "SessionStart",
		SessionID:  "sess-1",
	})
	require.NoError(t, err)
	require.NotNil(t, svc.taskRepo.savedResumeMetadata)
	require.Equal(t, TaskResumeMetadata{
		TaskID:     "task-123",
		Provider:   ProviderCodex,
		SessionID:  "sess-1",
		ObservedAt: time.Date(2026, time.April, 20, 9, 5, 0, 0, time.UTC),
	}, *svc.taskRepo.savedResumeMetadata)
}

func TestTaskStatusService_HandleHookEventRecordsActivity(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.listTasks = []*Task{{
		ID:           "task-123",
		WorktreePath: "/tmp/repo-task",
	}}

	require.NoError(t, svc.service.HandleHookEvent(t.Context(), HookEventInput{
		OccurredAt: time.Date(2026, time.April, 23, 10, 0, 0, 0, time.UTC),
		Provider:   ProviderCodex,
		Cwd:        "/tmp/repo-task",
		EventName:  "UserPromptSubmit",
		TurnID:     "turn-1",
		PromptText: "bring the task preview back",
	}))
	require.NoError(t, svc.service.HandleHookEvent(t.Context(), HookEventInput{
		OccurredAt:        time.Date(2026, time.April, 23, 10, 0, 30, 0, time.UTC),
		Provider:          ProviderCodex,
		Cwd:               "/tmp/repo-task",
		EventName:         "PostToolUse",
		TurnID:            "turn-1",
		CommandText:       "rg -n task detail",
		SessionID:         "sess-1",
		PromptText:        "bring the task preview back",
		CommandResultText: "internal/adapters/handler/tui/render.go",
	}))
	require.NoError(t, svc.service.HandleHookEvent(t.Context(), HookEventInput{
		OccurredAt:           time.Date(2026, time.April, 23, 10, 1, 0, 0, time.UTC),
		Provider:             ProviderCodex,
		Cwd:                  "/tmp/repo-task",
		EventName:            "Stop",
		TurnID:               "turn-1",
		SessionID:            "sess-1",
		LastAssistantMessage: "Restored the detail panel message preview.",
	}))

	events, err := svc.service.GetTaskActivity(t.Context(), "task-123", 10)
	require.NoError(t, err)
	require.Equal(t, []TaskActivityEvent{
		{
			TaskID:     "task-123",
			TurnID:     "turn-1",
			EventName:  "UserPromptSubmit",
			Role:       TaskActivityRoleUser,
			Text:       "bring the task preview back",
			ObservedAt: time.Date(2026, time.April, 23, 10, 0, 0, 0, time.UTC),
		},
		{
			TaskID:     "task-123",
			TurnID:     "turn-1",
			EventName:  "PostToolUse",
			Role:       TaskActivityRoleAssistant,
			Text:       "rg -n task detail",
			ObservedAt: time.Date(2026, time.April, 23, 10, 0, 30, 0, time.UTC),
		},
		{
			TaskID:     "task-123",
			TurnID:     "turn-1",
			EventName:  "Stop",
			Role:       TaskActivityRoleAssistant,
			Text:       "Restored the detail panel message preview.",
			ObservedAt: time.Date(2026, time.April, 23, 10, 1, 0, 0, time.UTC),
		},
	}, events)
}
