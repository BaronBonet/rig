package sqlite

import (
	"testing"
	"time"

	"rig/internal/core"

	"github.com/stretchr/testify/require"
)

func TestDeriveHookSessionSummary_MarksPromptedBeforeToolUse(t *testing.T) {
	summary := deriveHookSessionSummary(nil, hookRecord{
		EventName:  "UserPromptSubmit",
		SessionID:  "sess-1",
		TurnID:     "turn-1",
		PromptText: "fix the failing test",
		OccurredAt: time.Date(2026, 4, 8, 10, 0, 0, 0, time.UTC),
	})

	require.Equal(t, "sess-1", summary.SessionID)
	require.Equal(t, "turn-1", summary.CurrentTurnID)
	require.Equal(t, core.HookRuntimePhasePrompted, summary.RuntimePhase)
	require.Equal(t, "fix the failing test", summary.LastPromptText)
}

func TestDeriveHookSessionSummary_MarksIdleAfterStopAndTracksStopTime(t *testing.T) {
	occurredAt := time.Date(2026, 4, 8, 10, 1, 0, 0, time.UTC)

	summary := deriveHookSessionSummary(&core.HookSessionSummary{
		TaskID:          "task-1",
		SessionID:       "sess-1",
		RuntimePhase:    core.HookRuntimePhaseRunningCommand,
		CommandCount:    1,
		LastCommandText: "go test ./...",
	}, hookRecord{
		EventName:            "Stop",
		SessionID:            "sess-1",
		TurnID:               "turn-1",
		LastAssistantMessage: "I finished the change",
		OccurredAt:           occurredAt,
	})

	require.Equal(t, core.HookRuntimePhaseIdle, summary.RuntimePhase)
	require.Equal(t, occurredAt, summary.LastStopAt)
	require.Equal(t, "I finished the change", summary.LastAssistantMessage)
	require.Equal(t, 1, summary.CommandCount)
	require.Equal(t, "go test ./...", summary.LastCommandText)
}

func TestDeriveHookSessionSummary_UserPromptSubmitClearsPriorTurnOutput(t *testing.T) {
	summary := deriveHookSessionSummary(&core.HookSessionSummary{
		TaskID:                "task-1",
		SessionID:             "sess-1",
		RuntimePhase:          core.HookRuntimePhaseIdle,
		LastPromptText:        "first prompt",
		LastAssistantMessage:  "first answer",
		LastCommandText:       "go test ./...",
		LastCommandResultText: "PASS",
	}, hookRecord{
		EventName:  "UserPromptSubmit",
		SessionID:  "sess-1",
		TurnID:     "turn-2",
		PromptText: "second prompt",
		OccurredAt: time.Date(2026, 4, 10, 10, 0, 0, 0, time.UTC),
	})

	require.Equal(t, "turn-2", summary.CurrentTurnID)
	require.Equal(t, "second prompt", summary.LastPromptText)
	require.Equal(t, "", summary.LastAssistantMessage)
	require.Equal(t, "", summary.LastCommandText)
	require.Equal(t, "", summary.LastCommandResultText)
	require.Equal(t, core.HookRuntimePhasePrompted, summary.RuntimePhase)
}

func TestDeriveHookSessionSummary_UserPromptSubmitClearsAssistantTextEvenWhenPayloadIncludesIt(t *testing.T) {
	summary := deriveHookSessionSummary(&core.HookSessionSummary{
		TaskID:                "task-1",
		SessionID:             "sess-1",
		RuntimePhase:          core.HookRuntimePhaseIdle,
		LastPromptText:        "first prompt",
		LastAssistantMessage:  "first answer",
		LastCommandText:       "go test ./...",
		LastCommandResultText: "PASS",
	}, hookRecord{
		EventName:            "UserPromptSubmit",
		SessionID:            "sess-1",
		TurnID:               "turn-2",
		PromptText:           "second prompt",
		LastAssistantMessage: "current answer",
		OccurredAt:           time.Date(2026, 4, 10, 10, 0, 0, 0, time.UTC),
	})

	require.Equal(t, "turn-2", summary.CurrentTurnID)
	require.Equal(t, "second prompt", summary.LastPromptText)
	require.Equal(t, "", summary.LastAssistantMessage)
	require.Equal(t, "", summary.LastCommandText)
	require.Equal(t, "", summary.LastCommandResultText)
	require.Equal(t, core.HookRuntimePhasePrompted, summary.RuntimePhase)
}

func TestDeriveHookSessionSummary_MarksWaitingPermissionOnPermissionRequest(t *testing.T) {
	summary := deriveHookSessionSummary(&core.HookSessionSummary{
		TaskID:          "task-1",
		SessionID:       "sess-1",
		RuntimePhase:    core.HookRuntimePhaseRunningCommand,
		CommandCount:    1,
		LastCommandText: "git log --all",
	}, hookRecord{
		EventName:   "PermissionRequest",
		SessionID:   "sess-1",
		TurnID:      "turn-1",
		CommandText: "git log --all --oneline",
		OccurredAt:  time.Date(2026, 4, 8, 10, 0, 30, 0, time.UTC),
	})

	require.Equal(t, core.HookRuntimePhaseWaitingPermission, summary.RuntimePhase)
	require.Equal(t, "git log --all --oneline", summary.LastCommandText)
}

func TestDeriveHookSessionSummary_UserPromptSubmitSetsLastPromptSubmittedAt(t *testing.T) {
	ts := time.Date(2026, 4, 12, 10, 30, 0, 0, time.UTC)
	summary := deriveHookSessionSummary(&core.HookSessionSummary{
		TaskID: "task-1",
	}, hookRecord{
		EventName:  "UserPromptSubmit",
		OccurredAt: ts,
		PromptText: "fix the bug",
	})
	require.Equal(t, ts, summary.LastPromptSubmittedAt)
}

func TestDeriveObserverSummary_CodexPromptedOverridesStaleDisconnectedState(t *testing.T) {
	ts := time.Date(2026, 4, 15, 12, 30, 0, 0, time.UTC)

	summary := deriveObserverSummary(&core.ObserverSummary{
		TaskID:                "task-1",
		DisplayStatus:         core.DisplayStatusDisconnected,
		DisplayActivity:       core.DisplayActivityNone,
		ProcessAlive:          false,
		LastRuntimeObservedAt: ts.Add(-2 * time.Minute),
	}, &core.HookSessionSummary{
		TaskID:         "task-1",
		Model:          "gpt-5.4",
		RuntimePhase:   core.HookRuntimePhasePrompted,
		LastEventName:  "UserPromptSubmit",
		LastActivityAt: ts,
	})

	require.Equal(t, "task-1", summary.TaskID)
	require.Equal(t, core.DisplayStatusWorking, summary.DisplayStatus)
	require.Equal(t, core.DisplayActivityNone, summary.DisplayActivity)
	require.True(t, summary.ProcessAlive)
	require.Equal(t, ts, summary.LastRuntimeObservedAt)
}

func TestDeriveObserverSummary_CodexRunningCommandSetsCommandActivity(t *testing.T) {
	ts := time.Date(2026, 4, 15, 12, 31, 0, 0, time.UTC)

	summary := deriveObserverSummary(nil, &core.HookSessionSummary{
		TaskID:         "task-1",
		Model:          "gpt-5.4",
		RuntimePhase:   core.HookRuntimePhaseRunningCommand,
		LastEventName:  "PreToolUse",
		LastActivityAt: ts,
	})

	require.Equal(t, core.DisplayStatusWorking, summary.DisplayStatus)
	require.Equal(t, core.DisplayActivityCommand, summary.DisplayActivity)
	require.True(t, summary.ProcessAlive)
	require.Equal(t, ts, summary.LastRuntimeObservedAt)
}

func TestDeriveObserverSummary_StopYieldsNeedsInput(t *testing.T) {
	ts := time.Date(2026, 4, 15, 12, 32, 0, 0, time.UTC)

	summary := deriveObserverSummary(nil, &core.HookSessionSummary{
		TaskID:         "task-1",
		Model:          "gpt-5.4",
		RuntimePhase:   core.HookRuntimePhaseIdle,
		LastEventName:  "Stop",
		LastActivityAt: ts,
	})

	require.Equal(t, core.DisplayStatusNeedsInput, summary.DisplayStatus)
	require.Equal(t, core.DisplayActivityNone, summary.DisplayActivity)
	require.True(t, summary.ProcessAlive)
	require.Equal(t, ts, summary.LastRuntimeObservedAt)
}

func TestTrimPreview_NormalizesWhitespaceAndTruncates(t *testing.T) {
	long := "  line one\n\tline two  " + repeatString("x", hookPreviewMaxLen)

	preview := trimPreview(long)

	require.NotContains(t, preview, "\n")
	require.NotContains(t, preview, "\t")
	require.LessOrEqual(t, len(preview), hookPreviewMaxLen)
	require.True(t, len(preview) > 0)
}

func TestHookSubscriber_CloseWaitsForActivePublisher(t *testing.T) {
	subscriber := newHookSubscriber(1)
	subscriber.mu.RLock()

	closed := make(chan struct{})
	go func() {
		subscriber.close()
		close(closed)
	}()

	select {
	case <-closed:
		t.Fatal("subscriber closed while publish lock was still held")
	case <-time.After(20 * time.Millisecond):
	}

	subscriber.mu.RUnlock()

	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for subscriber close")
	}

	_, ok := <-subscriber.ch
	require.False(t, ok)
}

func TestHookSubscriber_PublishAfterCloseIsSafe(t *testing.T) {
	subscriber := newHookSubscriber(1)
	subscriber.close()

	require.False(t, subscriber.publish(core.HookSessionSummary{TaskID: "task-1"}))
}

func repeatString(s string, count int) string {
	result := ""
	for i := 0; i < count; i++ {
		result += s
	}
	return result
}
