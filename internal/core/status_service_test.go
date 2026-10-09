package core

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

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
	svc.taskRepo.listTasks = []*Task{
		{ID: "task-123", Provider: ProviderCodex, TmuxSession: "repo_task", WorktreePath: "/tmp/repo-task"},
		{ID: "task-999", Provider: ProviderCodex, TmuxSession: "repo_other", WorktreePath: "/tmp/repo-other"},
	}
	svc.sessionClient.locateServer = agentTestServer
	svc.sessionClient.locatePanes = []TmuxPane{
		{ID: "%1", Session: "repo_task", Command: "codex"},
		{ID: "%2", Session: "repo_other", Command: "codex"},
	}
	svc.sessionClient.locateTaskSessionPanes = map[string][]string{"task-123": {"%1"}, "task-999": {"%2"}}
	svc.sessionClient.locatePaneByPID = map[int]string{101: "%1", 201: "%2"}

	updates, err := svc.service.SubscribeTaskStatus(t.Context(), "task-123")
	require.NoError(t, err)

	other := agentHook(ProviderCodex, HookEventPreToolUse, "sess-other", 201, "%2", 0)
	other.Cwd = "/tmp/repo-other"
	svc.handleAgentHook(t, other, TaskStatusPhaseWorking)
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventPostToolUse, "sess-1", 101, "%1", 1),
		TaskStatusPhaseWorking)

	select {
	case update := <-updates:
		require.Equal(t, "task-123", update.TaskID)
		require.Equal(t, TaskStatusPhaseWorking, update.Phase)
		require.Equal(t, HookEventPostToolUse, update.RawEventName)
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
	svc.codexAgentTask(AgentSessionStatus{
		Phase:        TaskStatusPhaseWaitingForInput,
		RawEventName: HookEventStop,
		ObservedAt:   time.Date(2026, time.April, 19, 11, 3, 0, 0, time.UTC),
	})
	svc.providerRepo.statusRecoveryUpdate = &AgentSessionStatus{
		Phase:        TaskStatusPhaseWorking,
		RawEventName: "TranscriptActivity",
		ObservedAt:   time.Date(2026, time.April, 19, 11, 4, 0, 0, time.UTC),
	}
	svc.observation.recoveryPollInterval = time.Millisecond

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	updates, err := svc.service.SubscribeTaskStatus(ctx, "task-123")
	require.NoError(t, err)

	select {
	case update := <-updates:
		require.Equal(t, TaskStatusUpdate{
			TaskID:       "task-123",
			Provider:     ProviderCodex,
			Phase:        TaskStatusPhaseWorking,
			RawEventName: "TranscriptActivity",
			ObservedAt:   time.Date(2026, time.April, 19, 11, 4, 0, 0, time.UTC),
		}, taskLevelStatus(update))
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for recovered status update")
	}
}

func TestTaskStatusService_LatestReturnsTheAgentsMostRecentHookStatus(t *testing.T) {
	svc := newTestTaskService(t)
	svc.codexAgentTask(AgentSessionStatus{})
	svc.taskRepo.agentSessions = nil
	svc.sessionClient.locatePaneByPID = map[int]string{101: "%1"}

	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventSessionStart, "session-new", 101, "%1", 0),
		TaskStatusPhaseStarting)
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventStop, "session-new", 101, "%1", 1),
		TaskStatusPhaseWaitingForInput)

	update, err := svc.service.LatestTaskStatus(t.Context(), "task-123")
	require.NoError(t, err)
	require.NotNil(t, update)
	require.Equal(t, TaskStatusUpdate{
		TaskID:       "task-123",
		Provider:     ProviderCodex,
		Phase:        TaskStatusPhaseWaitingForInput,
		RawEventName: HookEventStop,
		ObservedAt:   agentTestStart.Add(time.Minute),
	}, taskLevelStatus(*update))
}

func TestTaskStatusService_LatestReturnsStoppedWhenTheAgentHasExited(t *testing.T) {
	svc := newTestTaskService(t)
	working := AgentSessionStatus{
		Phase:        TaskStatusPhaseWorking,
		RawEventName: HookEventPreToolUse,
		ObservedAt:   time.Date(2026, time.April, 19, 11, 2, 0, 0, time.UTC),
	}
	svc.codexAgentTask(working)
	svc.sessionClient.locatePanes[0].Children = nil

	update, err := svc.service.LatestTaskStatus(t.Context(), "task-123")
	require.NoError(t, err)
	require.NotNil(t, update)
	require.Equal(t, TaskStatusUpdate{
		TaskID:       "task-123",
		Provider:     ProviderCodex,
		Phase:        TaskStatusPhaseStopped,
		RawEventName: "TaskSessionStopped",
		ObservedAt:   working.ObservedAt,
	}, *update)
	require.False(t, svc.agentSessionsWithoutIDs()[0].IsOpen())
}

func TestTaskStatusService_LatestStaysWorkingWhileTheAgentPaneRunsItsProvider(t *testing.T) {
	svc := newTestTaskService(t)
	working := AgentSessionStatus{
		Phase:        TaskStatusPhaseWorking,
		RawEventName: HookEventPreToolUse,
		ObservedAt:   time.Date(2026, time.April, 19, 11, 2, 0, 0, time.UTC),
	}
	svc.codexAgentTask(working)

	update, err := svc.service.LatestTaskStatus(t.Context(), "task-123")
	require.NoError(t, err)
	require.NotNil(t, update)
	require.Equal(t, TaskStatusUpdate{
		TaskID:       "task-123",
		Provider:     ProviderCodex,
		Phase:        TaskStatusPhaseWorking,
		RawEventName: HookEventPreToolUse,
		ObservedAt:   working.ObservedAt,
	}, taskLevelStatus(*update))
	require.True(t, svc.agentSessionsWithoutIDs()[0].IsOpen())
}

func TestTaskStatusService_LatestRecoversStatusFromTheAgentsOwnConversation(t *testing.T) {
	svc := newTestTaskService(t)
	working := AgentSessionStatus{
		Phase:        TaskStatusPhaseWorking,
		RawEventName: HookEventPostToolUse,
		ObservedAt:   time.Date(2026, time.April, 19, 11, 3, 0, 0, time.UTC),
	}
	svc.codexAgentTask(working)
	ownConversation := TaskProviderSession{
		LastObservedAt:    time.Date(2026, time.April, 19, 11, 2, 0, 0, time.UTC),
		TaskID:            "task-123",
		Provider:          ProviderCodex,
		ProviderSessionID: "session-new",
		TranscriptPath:    "/tmp/codex-new.jsonl",
	}
	svc.taskRepo.providerSessionsByTask["task-123"] = []TaskProviderSession{
		// A newer conversation of another agent in the same task.
		{
			LastObservedAt:    time.Date(2026, time.April, 19, 11, 5, 0, 0, time.UTC),
			TaskID:            "task-123",
			Provider:          ProviderCodex,
			ProviderSessionID: "session-other",
			TranscriptPath:    "/tmp/codex-other.jsonl",
		},
		ownConversation,
	}
	svc.providerRepo.statusRecoveryUpdate = &AgentSessionStatus{
		Phase:        TaskStatusPhaseWaitingForInput,
		RawEventName: "TranscriptTaskComplete",
		ObservedAt:   time.Date(2026, time.April, 19, 11, 4, 0, 0, time.UTC),
	}

	update, err := svc.service.LatestTaskStatus(t.Context(), "task-123")
	require.NoError(t, err)
	require.NotNil(t, update)
	require.Equal(t, TaskStatusUpdate{
		TaskID:       "task-123",
		Provider:     ProviderCodex,
		Phase:        TaskStatusPhaseWaitingForInput,
		RawEventName: "TranscriptTaskComplete",
		ObservedAt:   time.Date(2026, time.April, 19, 11, 4, 0, 0, time.UTC),
	}, taskLevelStatus(*update))
	require.Equal(t, &working, svc.providerRepo.statusRecoveryCurrent)
	require.Equal(t, []TaskProviderSession{ownConversation}, svc.providerRepo.statusRecoverySessions)
}

func TestTaskStatusService_LatestPassesTheAgentProcessStartToRecovery(t *testing.T) {
	svc := newTestTaskService(t)
	svc.codexAgentTask(AgentSessionStatus{
		Phase:        TaskStatusPhaseWaitingForInput,
		RawEventName: HookEventStop,
		ObservedAt:   time.Date(2026, time.October, 1, 14, 30, 0, 0, time.UTC),
	})
	providerStartedAt := time.Date(2026, time.October, 1, 14, 0, 0, 0, time.UTC)
	svc.sessionClient.locatePanes[0].Children = []PaneProcess{
		{Command: "/opt/homebrew/bin/codex", StartedAt: providerStartedAt},
		{Command: "node", StartedAt: providerStartedAt.Add(time.Hour)},
	}

	_, err := svc.service.LatestTaskStatus(t.Context(), "task-123")

	require.NoError(t, err)
	require.Equal(t, providerStartedAt, svc.providerRepo.statusRecoveryStartedAt)
}

func TestTaskStatusService_LatestKeepsTheHookStatusWhenProviderHasNoRecoveredStatus(t *testing.T) {
	for _, status := range []AgentSessionStatus{
		{
			Phase:        TaskStatusPhaseWorking,
			RawEventName: HookEventPostToolUse,
			ObservedAt:   time.Date(2026, time.April, 19, 11, 3, 0, 0, time.UTC),
		},
		{
			Phase:        TaskStatusPhaseWaitingForInput,
			RawEventName: HookEventStop,
			ObservedAt:   time.Date(2026, time.April, 19, 11, 3, 0, 0, time.UTC),
		},
	} {
		svc := newTestTaskService(t)
		svc.codexAgentTask(status)

		update, err := svc.service.LatestTaskStatus(t.Context(), "task-123")
		require.NoError(t, err)
		require.NotNil(t, update)
		require.Equal(t, TaskStatusUpdate{
			TaskID:       "task-123",
			Provider:     ProviderCodex,
			Phase:        status.Phase,
			RawEventName: status.RawEventName,
			ObservedAt:   status.ObservedAt,
		}, taskLevelStatus(*update))
		require.Equal(t, &status, svc.providerRepo.statusRecoveryCurrent)
	}
}

func TestTaskStatusService_LatestReturnsStoppedBeforeTranscriptRecoveryWhenProviderIsAbsent(t *testing.T) {
	svc := newTestTaskService(t)
	svc.codexAgentTask(AgentSessionStatus{
		Phase:        TaskStatusPhaseWorking,
		RawEventName: HookEventPostToolUse,
		ObservedAt:   time.Date(2026, time.April, 19, 11, 3, 0, 0, time.UTC),
	})
	svc.sessionClient.locatePanes[0].Children = nil
	svc.providerRepo.statusRecoveryUpdate = &AgentSessionStatus{
		Phase:        TaskStatusPhaseWaitingForInput,
		RawEventName: "TranscriptTaskComplete",
		ObservedAt:   time.Date(2026, time.April, 19, 11, 4, 0, 0, time.UTC),
	}

	update, err := svc.service.LatestTaskStatus(t.Context(), "task-123")
	require.NoError(t, err)
	require.NotNil(t, update)
	require.Equal(t, TaskStatusPhaseStopped, update.Phase)
	require.Equal(t, "TaskSessionStopped", update.RawEventName)
	require.Nil(t, svc.providerRepo.statusRecoveryCurrent)
}

func TestTaskStatusService_LatestStaysWorkingWhenCodexPaneRunsPlatformBinary(t *testing.T) {
	svc := newTestTaskService(t)
	svc.codexAgentTask(AgentSessionStatus{
		Phase:        TaskStatusPhaseWorking,
		RawEventName: HookEventPreToolUse,
		ObservedAt:   time.Date(2026, time.April, 19, 11, 2, 0, 0, time.UTC),
	})
	svc.sessionClient.locatePanes[0] = TmuxPane{ID: "%1", Session: "repo_task", Command: "codex-aarch64-a"}

	update, err := svc.service.LatestTaskStatus(t.Context(), "task-123")
	require.NoError(t, err)
	require.NotNil(t, update)
	require.Equal(t, TaskStatusPhaseWorking, update.Phase)
}

func TestTaskStatusService_HandleHookEventResolvesTaskIDAndPublishesMappedUpdate(t *testing.T) {
	svc := newTestTaskService(t)
	svc.codexAgentTask(AgentSessionStatus{})
	svc.taskRepo.agentSessions = nil
	svc.sessionClient.locatePaneByPID = map[int]string{101: "%1"}
	svc.providerRepo.hookUpdate = &TaskStatusUpdate{
		Phase:        TaskStatusPhaseStarting,
		RawEventName: "SessionStart",
	}

	err := svc.service.HandleHookEvent(t.Context(), HookEventInput{
		OccurredAt: time.Date(2026, time.April, 20, 9, 0, 0, 0, time.UTC),
		Provider:   ProviderCodex,
		Cwd:        "/tmp/repo-task",
		EventName:  "SessionStart",
		SessionID:  "session-new",
		HookPID:    101,
		TmuxPane:   "%1",
		TmuxServer: agentTestServer,
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
	}, taskLevelStatus(*update))
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

// codexAgentTask lays out task-123, a Codex task in /tmp/repo-task whose agent
// session runs in pane %1 of its Session with status, as session-new.
func (h *testTaskServiceHarness) codexAgentTask(status AgentSessionStatus) {
	h.taskRepo.listTasks = []*Task{{
		ID:           "task-123",
		Provider:     ProviderCodex,
		TmuxSession:  "repo_task",
		WorktreePath: "/tmp/repo-task",
	}}
	h.sessionClient.locateServer = agentTestServer
	h.sessionClient.locatePanes = []TmuxPane{{
		ID:       "%1",
		Session:  "repo_task",
		Command:  "zsh",
		Children: []PaneProcess{{Command: "codex"}},
	}}
	h.sessionClient.locateTaskSessionPanes = map[string][]string{"task-123": {"%1"}}
	h.taskRepo.agentSessions = []AgentSession{{
		ID:                "agent-1",
		TaskID:            "task-123",
		Provider:          ProviderCodex,
		TmuxServer:        agentTestServer,
		TmuxPane:          "%1",
		PaneTrusted:       true,
		ProviderSessionID: "session-new",
		StartedAt:         time.Date(2026, time.April, 19, 11, 0, 0, 0, time.UTC),
		Status:            status,
	}}
	h.taskRepo.providerSessionsByTask["task-123"] = []TaskProviderSession{{
		LastObservedAt:    time.Date(2026, time.April, 19, 11, 0, 0, 0, time.UTC),
		TaskID:            "task-123",
		Provider:          ProviderCodex,
		ProviderSessionID: "session-new",
		TranscriptPath:    "/tmp/codex-new.jsonl",
	}}
}

func TestTaskStatusService_AnAgentSessionsEntryCarriesItsRecoveredStatus(t *testing.T) {
	svc := newTestTaskService(t)
	svc.codexAgentTask(AgentSessionStatus{
		Phase:        TaskStatusPhaseWorking,
		RawEventName: HookEventPostToolUse,
		ObservedAt:   time.Date(2026, time.April, 19, 11, 3, 0, 0, time.UTC),
	})
	svc.taskRepo.providerSessionsByTask["task-123"] = []TaskProviderSession{{
		LastObservedAt:    time.Date(2026, time.April, 19, 11, 2, 0, 0, time.UTC),
		TaskID:            "task-123",
		Provider:          ProviderCodex,
		ProviderSessionID: "session-new",
		TranscriptPath:    "/tmp/codex-new.jsonl",
	}}
	recovered := AgentSessionStatus{
		Phase:        TaskStatusPhaseWaitingForInput,
		RawEventName: "TranscriptTaskComplete",
		ObservedAt:   time.Date(2026, time.April, 19, 11, 4, 0, 0, time.UTC),
	}
	svc.providerRepo.statusRecoveryUpdate = &recovered

	update, err := svc.service.LatestTaskStatus(t.Context(), "task-123")
	require.NoError(t, err)
	require.Len(t, update.AgentSessions, 1)
	require.Equal(t, recovered, update.AgentSessions[0].Status)
}
