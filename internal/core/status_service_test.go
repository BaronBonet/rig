package core

import (
	"context"
	"strconv"
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
		GetTaskTokenUsage(context.Context, string) (*TaskTokenUsage, error)
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

func TestTaskStatusService_GetTaskActivityIncludesRecoveredTranscriptActivity(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.activityByTask["task-123"] = []TaskActivityEvent{
		{
			TaskID:     "task-123",
			EventName:  "UserPromptSubmit",
			Role:       TaskActivityRoleUser,
			Text:       "old prompt",
			ObservedAt: time.Date(2026, time.April, 23, 10, 0, 0, 0, time.UTC),
		},
		{
			TaskID:     "task-123",
			EventName:  "Stop",
			Role:       TaskActivityRoleAssistant,
			Text:       "Old answer.",
			ObservedAt: time.Date(2026, time.April, 23, 10, 1, 0, 0, time.UTC),
		},
	}
	svc.taskRepo.providerSessionsByTask["task-123"] = []TaskProviderSession{{
		TaskID:            "task-123",
		Provider:          ProviderCodex,
		ProviderSessionID: "sess-a",
		TranscriptPath:    "/tmp/codex-a.jsonl",
		LastObservedAt:    time.Date(2026, time.April, 23, 10, 1, 0, 0, time.UTC),
	}}
	recovered := []TaskActivityEvent{
		{
			TaskID:     "task-123",
			EventName:  "TranscriptUserMessage",
			Role:       TaskActivityRoleUser,
			Text:       "do it again",
			ObservedAt: time.Date(2026, time.April, 23, 10, 2, 0, 0, time.UTC),
		},
		{
			TaskID:     "task-123",
			EventName:  "TranscriptFunctionCall",
			Role:       TaskActivityRoleAssistant,
			Text:       "make test",
			ObservedAt: time.Date(2026, time.April, 23, 10, 3, 0, 0, time.UTC),
		},
		{
			TaskID:     "task-123",
			EventName:  "TranscriptAssistantMessage",
			Role:       TaskActivityRoleAssistant,
			Text:       "Ran it again.",
			ObservedAt: time.Date(2026, time.April, 23, 10, 4, 0, 0, time.UTC),
		},
	}
	svc.providerRepo.activityByTranscript = map[string][]TaskActivityEvent{
		"/tmp/codex-a.jsonl": recovered,
	}

	events, err := svc.service.GetTaskActivity(t.Context(), "task-123", 3)

	require.NoError(t, err)
	require.Equal(t, recovered, events)
	require.Equal(t, []providerActivityCall{{
		transcriptPath: "/tmp/codex-a.jsonl",
		after:          time.Date(2026, time.April, 23, 10, 1, 0, 0, time.UTC),
	}}, svc.providerRepo.activityCalls)
}

func TestTaskStatusService_GetTaskTokenUsageSumsLatestTranscriptPerProviderSession(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.providerSessionsByTask["task-123"] = []TaskProviderSession{
		{
			TaskID:            "task-123",
			Provider:          ProviderCodex,
			ProviderSessionID: "sess-a",
			TranscriptPath:    "/tmp/codex-a-old.jsonl",
			LastObservedAt:    time.Date(2026, time.April, 25, 9, 0, 0, 0, time.UTC),
		},
		{
			TaskID:            "task-123",
			Provider:          ProviderCodex,
			ProviderSessionID: "sess-b",
			TranscriptPath:    "/tmp/codex-b.jsonl",
			LastObservedAt:    time.Date(2026, time.April, 25, 9, 5, 0, 0, time.UTC),
		},
		{
			TaskID:            "task-123",
			Provider:          ProviderCodex,
			ProviderSessionID: "sess-a",
			TranscriptPath:    "/tmp/codex-a-resumed.jsonl",
			LastObservedAt:    time.Date(2026, time.April, 25, 9, 10, 0, 0, time.UTC),
		},
		{
			TaskID:            "task-123",
			Provider:          ProviderCodex,
			ProviderSessionID: "sess-b",
			LastObservedAt:    time.Date(2026, time.April, 25, 9, 20, 0, 0, time.UTC),
		},
	}
	svc.providerRepo.usageByTranscript = map[string]*SessionTokenUsage{
		"/tmp/codex-a-old.jsonl": {
			InputTokens:  50,
			OutputTokens: 50,
			TotalTokens:  100,
		},
		"/tmp/codex-a-resumed.jsonl": {
			InputTokens:              100,
			CachedInputTokens:        25,
			CacheCreationInputTokens: 15,
			OutputTokens:             40,
			ReasoningOutputTokens:    10,
			TotalTokens:              140,
		},
		"/tmp/codex-b.jsonl": {
			InputTokens:              30,
			CachedInputTokens:        5,
			CacheCreationInputTokens: 10,
			OutputTokens:             20,
			TotalTokens:              50,
		},
	}

	usage, err := svc.service.GetTaskTokenUsage(t.Context(), "task-123")
	require.NoError(t, err)
	require.Equal(t, &TaskTokenUsage{
		SessionCount:             2,
		InputTokens:              130,
		CachedInputTokens:        30,
		CacheCreationInputTokens: 25,
		OutputTokens:             60,
		ReasoningOutputTokens:    10,
		TotalTokens:              190,
	}, usage)
	require.Equal(t, []providerTokenUsageCall{
		{transcriptPath: "/tmp/codex-b.jsonl"},
		{transcriptPath: "/tmp/codex-a-resumed.jsonl"},
	}, svc.providerRepo.tokenUsageCalls)
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

func TestTaskStatusService_SubscribePublishesRecoveredStatusWithoutHookUpdate(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.listTasks = []*Task{{
		ID:          "task-123",
		Provider:    ProviderCodex,
		TmuxSession: "repo_task",
	}}
	svc.sessionClient.inspectState = TaskSessionRuntimeState{
		Exists:         true,
		ActiveCommands: []string{"codex"},
	}
	svc.taskRepo.providerSessionsByTask["task-123"] = []TaskProviderSession{{
		LastObservedAt:    time.Date(2026, time.April, 19, 11, 3, 0, 0, time.UTC),
		TaskID:            "task-123",
		Provider:          ProviderCodex,
		ProviderSessionID: "session-new",
		TranscriptPath:    "/tmp/codex-new.jsonl",
	}}
	latestHookStatus := TaskStatusUpdate{
		TaskID:       "task-123",
		Provider:     ProviderCodex,
		Phase:        TaskStatusPhaseWaitingForInput,
		RawEventName: "Stop",
		ObservedAt:   time.Date(2026, time.April, 19, 11, 3, 0, 0, time.UTC),
	}
	recovered := TaskStatusUpdate{
		TaskID:       "task-123",
		Provider:     ProviderCodex,
		Phase:        TaskStatusPhaseWorking,
		RawEventName: "TranscriptActivity",
		ObservedAt:   time.Date(2026, time.April, 19, 11, 4, 0, 0, time.UTC),
	}
	svc.providerRepo.statusRecoveryUpdate = &recovered
	require.NoError(t, svc.taskRepoMock.UpsertTaskStatus(t.Context(), latestHookStatus))

	previousInterval := taskStatusRecoveryPollInterval
	taskStatusRecoveryPollInterval = time.Millisecond
	t.Cleanup(func() {
		taskStatusRecoveryPollInterval = previousInterval
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	updates, err := svc.service.SubscribeTaskStatus(ctx, "task-123")
	require.NoError(t, err)

	select {
	case update := <-updates:
		require.Equal(t, recovered, update)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for recovered status update")
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

func TestTaskStatusService_LatestRecoversWorkingStatusFromProviderTranscript(t *testing.T) {
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
	svc.taskRepo.providerSessionsByTask["task-123"] = []TaskProviderSession{
		{
			LastObservedAt:    time.Date(2026, time.April, 19, 11, 2, 0, 0, time.UTC),
			TaskID:            "task-123",
			Provider:          ProviderCodex,
			ProviderSessionID: "session-old",
			TranscriptPath:    "/tmp/codex-old.jsonl",
		},
		{
			LastObservedAt:    time.Date(2026, time.April, 19, 11, 5, 0, 0, time.UTC),
			TaskID:            "task-123",
			Provider:          ProviderCodex,
			ProviderSessionID: "session-new",
			TranscriptPath:    "/tmp/codex-new.jsonl",
		},
	}

	latestHookStatus := TaskStatusUpdate{
		TaskID:       "task-123",
		Provider:     ProviderCodex,
		Phase:        TaskStatusPhaseWorking,
		RawEventName: "PostToolUse",
		ObservedAt:   time.Date(2026, time.April, 19, 11, 3, 0, 0, time.UTC),
	}
	recovered := TaskStatusUpdate{
		TaskID:       "task-123",
		Provider:     ProviderCodex,
		Phase:        TaskStatusPhaseWaitingForInput,
		RawEventName: "TranscriptTaskComplete",
		ObservedAt:   time.Date(2026, time.April, 19, 11, 4, 0, 0, time.UTC),
	}
	svc.providerRepo.statusRecoveryUpdate = &recovered
	require.NoError(t, svc.taskRepoMock.UpsertTaskStatus(t.Context(), latestHookStatus))

	update, err := svc.service.LatestTaskStatus(t.Context(), "task-123")
	require.NoError(t, err)
	require.NotNil(t, update)
	require.Equal(t, recovered, *update)
	require.Equal(t, &latestHookStatus, svc.providerRepo.statusRecoveryCurrent)
	require.Equal(t, svc.taskRepo.providerSessionsByTask["task-123"], svc.providerRepo.statusRecoverySessions)
}

func TestTaskStatusService_LatestStaysWorkingWhenProviderHasNoRecoveredStatus(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.listTasks = []*Task{{
		ID:          "task-123",
		Provider:    ProviderCodex,
		TmuxSession: "repo_task",
	}}
	svc.sessionClient.inspectState = TaskSessionRuntimeState{
		Exists:         true,
		ActiveCommands: []string{"codex"},
	}
	svc.taskRepo.providerSessionsByTask["task-123"] = []TaskProviderSession{{
		LastObservedAt:    time.Date(2026, time.April, 19, 11, 5, 0, 0, time.UTC),
		TaskID:            "task-123",
		Provider:          ProviderCodex,
		ProviderSessionID: "session-new",
		TranscriptPath:    "/tmp/codex-new.jsonl",
	}}

	latestHookStatus := TaskStatusUpdate{
		TaskID:       "task-123",
		Provider:     ProviderCodex,
		Phase:        TaskStatusPhaseWorking,
		RawEventName: "PostToolUse",
		ObservedAt:   time.Date(2026, time.April, 19, 11, 3, 0, 0, time.UTC),
	}
	require.NoError(t, svc.taskRepoMock.UpsertTaskStatus(t.Context(), latestHookStatus))

	update, err := svc.service.LatestTaskStatus(t.Context(), "task-123")
	require.NoError(t, err)
	require.NotNil(t, update)
	require.Equal(t, latestHookStatus, *update)
}

func TestTaskStatusService_LatestStaysWaitingWhenProviderHasNoRecoveredStatus(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.listTasks = []*Task{{
		ID:          "task-123",
		Provider:    ProviderCodex,
		TmuxSession: "repo_task",
	}}
	svc.sessionClient.inspectState = TaskSessionRuntimeState{
		Exists:         true,
		ActiveCommands: []string{"codex"},
	}

	latestHookStatus := TaskStatusUpdate{
		TaskID:       "task-123",
		Provider:     ProviderCodex,
		Phase:        TaskStatusPhaseWaitingForInput,
		RawEventName: "Stop",
		ObservedAt:   time.Date(2026, time.April, 19, 11, 3, 0, 0, time.UTC),
	}
	require.NoError(t, svc.taskRepoMock.UpsertTaskStatus(t.Context(), latestHookStatus))

	update, err := svc.service.LatestTaskStatus(t.Context(), "task-123")
	require.NoError(t, err)
	require.NotNil(t, update)
	require.Equal(t, latestHookStatus, *update)
	require.Equal(t, &latestHookStatus, svc.providerRepo.statusRecoveryCurrent)
}

func TestTaskStatusService_LatestRecoversWaitingStatusFromProviderTranscriptActivity(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.listTasks = []*Task{{
		ID:          "task-123",
		Provider:    ProviderCodex,
		TmuxSession: "repo_task",
	}}
	svc.sessionClient.inspectState = TaskSessionRuntimeState{
		Exists:         true,
		ActiveCommands: []string{"codex"},
	}
	svc.taskRepo.providerSessionsByTask["task-123"] = []TaskProviderSession{{
		LastObservedAt:    time.Date(2026, time.April, 19, 11, 3, 0, 0, time.UTC),
		TaskID:            "task-123",
		Provider:          ProviderCodex,
		ProviderSessionID: "session-new",
		TranscriptPath:    "/tmp/codex-new.jsonl",
	}}

	latestHookStatus := TaskStatusUpdate{
		TaskID:       "task-123",
		Provider:     ProviderCodex,
		Phase:        TaskStatusPhaseWaitingForInput,
		RawEventName: "Stop",
		ObservedAt:   time.Date(2026, time.April, 19, 11, 3, 0, 0, time.UTC),
	}
	recovered := TaskStatusUpdate{
		TaskID:       "task-123",
		Provider:     ProviderCodex,
		Phase:        TaskStatusPhaseWorking,
		RawEventName: "TranscriptActivity",
		ObservedAt:   time.Date(2026, time.April, 19, 11, 4, 0, 0, time.UTC),
	}
	svc.providerRepo.statusRecoveryUpdate = &recovered
	require.NoError(t, svc.taskRepoMock.UpsertTaskStatus(t.Context(), latestHookStatus))

	update, err := svc.service.LatestTaskStatus(t.Context(), "task-123")
	require.NoError(t, err)
	require.NotNil(t, update)
	require.Equal(t, recovered, *update)
	require.Equal(t, &latestHookStatus, svc.providerRepo.statusRecoveryCurrent)
}

func TestTaskStatusService_LatestReturnsStoppedBeforeTranscriptRecoveryWhenProviderIsAbsent(t *testing.T) {
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
	svc.providerRepo.statusRecoveryUpdate = &TaskStatusUpdate{
		TaskID:       "task-123",
		Provider:     ProviderCodex,
		Phase:        TaskStatusPhaseWaitingForInput,
		RawEventName: "TranscriptTaskComplete",
		ObservedAt:   time.Date(2026, time.April, 19, 11, 4, 0, 0, time.UTC),
	}

	latestHookStatus := TaskStatusUpdate{
		TaskID:       "task-123",
		Provider:     ProviderCodex,
		Phase:        TaskStatusPhaseWorking,
		RawEventName: "PostToolUse",
		ObservedAt:   time.Date(2026, time.April, 19, 11, 3, 0, 0, time.UTC),
	}
	require.NoError(t, svc.taskRepoMock.UpsertTaskStatus(t.Context(), latestHookStatus))

	update, err := svc.service.LatestTaskStatus(t.Context(), "task-123")
	require.NoError(t, err)
	require.NotNil(t, update)
	require.Equal(t, TaskStatusPhaseStopped, update.Phase)
	require.Equal(t, "TaskSessionStopped", update.RawEventName)
	require.Nil(t, svc.providerRepo.statusRecoveryCurrent)
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

func TestTaskServiceHandleHookEvent_RecordsMultipleProviderSessionsForTask(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.listTasks = []*Task{{
		ID:           "task-1",
		WorktreePath: "/tmp/repo-task",
		Provider:     ProviderCodex,
	}}

	firstObservedAt := time.Date(2026, time.April, 25, 9, 0, 0, 0, time.UTC)
	secondObservedAt := time.Date(2026, time.April, 25, 9, 5, 0, 0, time.UTC)

	require.NoError(t, svc.service.HandleHookEvent(t.Context(), HookEventInput{
		OccurredAt:     firstObservedAt,
		Provider:       ProviderCodex,
		Cwd:            "/tmp/repo-task",
		EventName:      "SessionStart",
		SessionID:      "sess-a",
		TranscriptPath: "/tmp/codex-a.jsonl",
		StartSource:    "startup",
		Model:          "gpt-5.4-codex",
	}))
	require.NoError(t, svc.service.HandleHookEvent(t.Context(), HookEventInput{
		OccurredAt:     secondObservedAt,
		Provider:       ProviderCodex,
		Cwd:            "/tmp/repo-task",
		EventName:      "SessionStart",
		SessionID:      "sess-b",
		TranscriptPath: "/tmp/codex-b.jsonl",
		StartSource:    "startup",
		Model:          "gpt-5.4-codex",
	}))

	require.Equal(t, []TaskProviderSession{
		{
			TaskID:            "task-1",
			Provider:          ProviderCodex,
			ProviderSessionID: "sess-a",
			TranscriptPath:    "/tmp/codex-a.jsonl",
			StartSource:       "startup",
			Model:             "gpt-5.4-codex",
			Cwd:               "/tmp/repo-task",
			FirstObservedAt:   firstObservedAt,
			LastObservedAt:    firstObservedAt,
			LastEventName:     "SessionStart",
		},
		{
			TaskID:            "task-1",
			Provider:          ProviderCodex,
			ProviderSessionID: "sess-b",
			TranscriptPath:    "/tmp/codex-b.jsonl",
			StartSource:       "startup",
			Model:             "gpt-5.4-codex",
			Cwd:               "/tmp/repo-task",
			FirstObservedAt:   secondObservedAt,
			LastObservedAt:    secondObservedAt,
			LastEventName:     "SessionStart",
		},
	}, svc.taskRepo.savedProviderSessions)
}

func TestTaskServiceHandleHookEvent_SkipsProviderSessionWithoutSessionID(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.listTasks = []*Task{{
		ID:           "task-1",
		WorktreePath: "/tmp/repo-task",
		Provider:     ProviderCodex,
	}}
	svc.providerRepo.hookUpdate = &TaskStatusUpdate{
		Phase:        TaskStatusPhaseStarting,
		RawEventName: "SessionStart",
	}

	err := svc.service.HandleHookEvent(t.Context(), HookEventInput{
		OccurredAt:     time.Date(2026, time.April, 25, 9, 0, 0, 0, time.UTC),
		Provider:       ProviderCodex,
		Cwd:            "/tmp/repo-task",
		EventName:      "SessionStart",
		TranscriptPath: "/tmp/codex-a.jsonl",
		StartSource:    "startup",
		Model:          "gpt-5.4-codex",
	})

	require.NoError(t, err)
	require.Empty(t, svc.taskRepo.savedProviderSessions)
	require.Equal(t, "task-1", svc.providerRepo.hookInput.TaskID)
	require.Equal(t, "SessionStart", svc.providerRepo.hookInput.EventName)
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

func TestTaskStatusService_GetTaskActivityKeepsLastUserPromptOutsideRecentAssistantWindow(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.activityByTask["task-123"] = []TaskActivityEvent{
		{
			TaskID:     "task-123",
			EventName:  "UserPromptSubmit",
			Role:       TaskActivityRoleUser,
			Text:       "older prompt",
			ObservedAt: time.Date(2026, time.April, 23, 9, 0, 0, 0, time.UTC),
		},
		{
			TaskID:     "task-123",
			EventName:  "UserPromptSubmit",
			Role:       TaskActivityRoleUser,
			Text:       "fix the stale status",
			ObservedAt: time.Date(2026, time.April, 23, 10, 0, 0, 0, time.UTC),
		},
	}
	for i := range 7 {
		svc.taskRepo.activityByTask["task-123"] = append(svc.taskRepo.activityByTask["task-123"], TaskActivityEvent{
			TaskID:     "task-123",
			EventName:  "PostToolUse",
			Role:       TaskActivityRoleAssistant,
			Text:       "assistant event " + strconv.Itoa(i+1),
			ObservedAt: time.Date(2026, time.April, 23, 10, i+1, 0, 0, time.UTC),
		})
	}

	events, err := svc.service.GetTaskActivity(t.Context(), "task-123", 6)
	require.NoError(t, err)
	require.Len(t, events, 7)
	require.Equal(t, TaskActivityRoleUser, events[0].Role)
	require.Equal(t, "fix the stale status", events[0].Text)
	require.Equal(t, "assistant event 2", events[1].Text)
	require.Equal(t, "assistant event 7", events[6].Text)
}
