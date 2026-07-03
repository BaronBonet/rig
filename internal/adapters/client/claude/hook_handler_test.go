package claude

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/BaronBonet/rig/internal/core"
)

func fixedNow() time.Time {
	return time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
}

func TestDecodeHookEventInput_DecodesSessionStartPayload(t *testing.T) {
	body := []byte(`{
		"session_id": "sess-1",
		"transcript_path": "/tmp/transcript.jsonl",
		"cwd": "/tmp/repo-task",
		"hook_event_name": "SessionStart",
		"source": "startup"
	}`)

	input := DecodeHookEventInput(fixedNow, "SessionStart", body)

	require.Equal(t, core.ProviderClaude, input.Provider)
	require.Equal(t, "SessionStart", input.EventName)
	require.Equal(t, "sess-1", input.SessionID)
	require.Equal(t, "/tmp/transcript.jsonl", input.TranscriptPath)
	require.Equal(t, "/tmp/repo-task", input.Cwd)
	require.Equal(t, "startup", input.StartSource)
	require.Empty(t, input.TaskID)
	require.Equal(t, fixedNow(), input.OccurredAt)
}

func TestDecodeHookEventInput_DecodesPromptAndToolPayloads(t *testing.T) {
	prompt := DecodeHookEventInput(fixedNow, "", []byte(`{
		"session_id": "sess-1",
		"hook_event_name": "UserPromptSubmit",
		"prompt": "fix the billing retry flow"
	}`))
	require.Equal(t, "UserPromptSubmit", prompt.EventName)
	require.Equal(t, "fix the billing retry flow", prompt.PromptText)

	tool := DecodeHookEventInput(fixedNow, "PostToolUse", []byte(`{
		"session_id": "sess-1",
		"tool_input": {"command": "go test ./..."},
		"tool_response": "ok"
	}`))
	require.Equal(t, "PostToolUse", tool.EventName)
	require.Equal(t, "go test ./...", tool.CommandText)
	require.Equal(t, "ok", tool.CommandResultText)
}

func TestDecodeHookEventInput_ToleratesMalformedPayload(t *testing.T) {
	input := DecodeHookEventInput(fixedNow, "", []byte(`not-json`))

	require.Equal(t, core.ProviderClaude, input.Provider)
	require.Equal(t, "unknown", input.EventName)
}

func TestHookEventToTaskStatus_MapsClaudeEventsToPhases(t *testing.T) {
	repo := &repository{binary: "claude"}

	cases := []struct {
		event string
		phase core.TaskStatusPhase
	}{
		{"SessionStart", core.TaskStatusPhaseStarting},
		{"UserPromptSubmit", core.TaskStatusPhaseWorking},
		{"PreToolUse", core.TaskStatusPhaseWorking},
		{"PostToolUse", core.TaskStatusPhaseWorking},
		{"Stop", core.TaskStatusPhaseWaitingForInput},
		{"Notification", core.TaskStatusPhaseWaitingForInput},
	}
	for _, tc := range cases {
		update, err := repo.HookEventToTaskStatus(core.HookEventInput{
			TaskID:     "task-1",
			EventName:  tc.event,
			OccurredAt: fixedNow(),
		})
		require.NoError(t, err, tc.event)
		require.NotNil(t, update, tc.event)
		require.Equal(t, tc.phase, update.Phase, tc.event)
		require.Equal(t, core.ProviderClaude, update.Provider, tc.event)
	}
}

func TestHookEventToTaskStatus_IgnoresUnmappedEventsAndMissingTask(t *testing.T) {
	repo := &repository{binary: "claude"}

	update, err := repo.HookEventToTaskStatus(core.HookEventInput{
		TaskID:    "task-1",
		EventName: "SubagentStop",
	})
	require.NoError(t, err)
	require.Nil(t, update)

	_, err = repo.HookEventToTaskStatus(core.HookEventInput{EventName: "Stop"})
	require.ErrorIs(t, err, core.ErrUnmanagedHookEvent)
}
