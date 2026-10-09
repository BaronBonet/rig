package codex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BaronBonet/rig/internal/core"

	"github.com/stretchr/testify/require"
)

func TestRepositoryReadSessionTokenUsage_ReturnsLatestTotals(t *testing.T) {
	repo := &repository{}
	path := writeJSONL(t, []string{
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":20,"output_tokens":10,"reasoning_output_tokens":3,"total_tokens":110}}}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":240,"cached_input_tokens":80,"cache_creation_input_tokens":15,"output_tokens":25,"reasoning_output_tokens":9,"total_tokens":265}}}}`,
	})

	usage, err := repo.ReadSessionTokenUsage(t.Context(), path)

	require.NoError(t, err)
	require.Equal(t, &core.SessionTokenUsage{
		InputTokens:              240,
		CachedInputTokens:        80,
		CacheCreationInputTokens: 15,
		OutputTokens:             25,
		ReasoningOutputTokens:    9,
		TotalTokens:              265,
	}, usage)
}

func TestRepositoryReadSessionTokenUsage_SkipsLargeNonTokenLines(t *testing.T) {
	repo := &repository{}
	path := writeJSONL(t, []string{
		`{"type":"event_msg","payload":{"type":"tool_output","text":"` + strings.Repeat("x", 2*1024*1024) + `"}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":240,"cached_input_tokens":80,"output_tokens":25,"reasoning_output_tokens":9,"total_tokens":265}}}}`,
	})

	usage, err := repo.ReadSessionTokenUsage(t.Context(), path)

	require.NoError(t, err)
	require.Equal(t, &core.SessionTokenUsage{
		InputTokens:           240,
		CachedInputTokens:     80,
		OutputTokens:          25,
		ReasoningOutputTokens: 9,
		TotalTokens:           265,
	}, usage)
}

func TestRepositoryReadSessionTokenUsage_MissingTranscriptReturnsNil(t *testing.T) {
	repo := &repository{}

	usage, err := repo.ReadSessionTokenUsage(t.Context(), "/tmp/does-not-exist.jsonl")

	require.NoError(t, err)
	require.Nil(t, usage)
}

func TestRepositoryReadSessionTokenUsage_OpenErrorReturnsError(t *testing.T) {
	repo := &repository{}

	usage, err := repo.ReadSessionTokenUsage(t.Context(), "bad\x00path.jsonl")

	require.Error(t, err)
	require.Nil(t, usage)
}

func TestRepositoryReadSessionActivity_ReturnsTranscriptActivityAfterTimestamp(t *testing.T) {
	repo := &repository{}
	path := writeJSONL(t, []string{
		`{"timestamp":"2026-04-19T11:00:00Z","type":"event_msg","payload":{"type":"user_message","message":"old prompt"}}`,
		`{"timestamp":"2026-04-19T11:02:00Z","type":"event_msg","payload":{"type":"user_message","message":"do it again"}}`,
		`{"timestamp":"2026-04-19T11:03:00Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"make test\"}"}}`,
		`{"timestamp":"2026-04-19T11:04:00Z","type":"event_msg","payload":{"type":"agent_message","phase":"final_answer","message":"Ran it again."}}`,
		`{"timestamp":"2026-04-19T11:05:00Z","type":"event_msg","payload":{"type":"token_count"}}`,
	})

	events, err := repo.ReadSessionActivity(t.Context(), core.TaskProviderSession{
		TaskID:         "task-123",
		Provider:       core.ProviderCodex,
		TranscriptPath: path,
	}, time.Date(2026, time.April, 19, 11, 1, 0, 0, time.UTC))

	require.NoError(t, err)
	require.Equal(t, []core.TaskActivityEvent{
		{
			ObservedAt: time.Date(2026, time.April, 19, 11, 2, 0, 0, time.UTC),
			TaskID:     "task-123",
			EventName:  "TranscriptUserMessage",
			Role:       core.TaskActivityRoleUser,
			Text:       "do it again",
		},
		{
			ObservedAt: time.Date(2026, time.April, 19, 11, 3, 0, 0, time.UTC),
			TaskID:     "task-123",
			EventName:  "TranscriptFunctionCall",
			Role:       core.TaskActivityRoleAssistant,
			Text:       "make test",
		},
		{
			ObservedAt: time.Date(2026, time.April, 19, 11, 4, 0, 0, time.UTC),
			TaskID:     "task-123",
			EventName:  "TranscriptAssistantMessage",
			Role:       core.TaskActivityRoleAssistant,
			Text:       "Ran it again.",
		},
	}, events)
}

func TestRepositoryRecoverAgentSessionStatus_ReturnsTheTranscriptsTaskComplete(t *testing.T) {
	repo := &repository{}
	path := writeJSONL(t, []string{
		`{malformed`,
		`{"timestamp":"2026-04-19T11:02:00Z","type":"event_msg","payload":{"type":"token_count"}}`,
		`{"timestamp":"2026-04-19T11:03:00Z","type":"response_item","payload":{"type":"task_complete"}}`,
		`{"timestamp":"2026-04-19T11:04:00Z","type":"event_msg","payload":{"type":"task_complete"}}`,
	})
	current := core.AgentSessionStatus{
		Phase:        core.TaskStatusPhaseWorking,
		RawEventName: "PostToolUse",
		ObservedAt:   time.Date(2026, time.April, 19, 11, 1, 0, 0, time.UTC),
	}

	update, err := repo.RecoverAgentSessionStatus(t.Context(), current, []core.TaskProviderSession{{
		LastObservedAt:    time.Date(2026, time.April, 19, 11, 5, 0, 0, time.UTC),
		TaskID:            "task-123",
		Provider:          core.ProviderCodex,
		ProviderSessionID: "session-123",
		TranscriptPath:    path,
	}}, time.Time{})

	require.NoError(t, err)
	require.Equal(t, &core.AgentSessionStatus{
		Phase:        core.TaskStatusPhaseWaitingForInput,
		RawEventName: "TranscriptTaskComplete",
		ObservedAt:   time.Date(2026, time.April, 19, 11, 4, 0, 0, time.UTC),
	}, update)
}

func TestRepositoryRecoverAgentSessionStatus_UsesRootTranscriptWhenSubagentTranscriptIsNewer(t *testing.T) {
	repo := &repository{}
	rootPath := writeJSONL(t, []string{
		`{"timestamp":"2026-04-19T11:04:00Z","type":"response_item","payload":{"type":"function_call","name":"exec_command"}}`,
	})
	subagentPath := writeJSONL(t, []string{
		`{"timestamp":"2026-04-19T11:05:00Z","type":"event_msg","payload":{"type":"task_complete"}}`,
	})
	current := core.AgentSessionStatus{
		Phase:        core.TaskStatusPhaseWorking,
		RawEventName: "PostToolUse",
		ObservedAt:   time.Date(2026, time.April, 19, 11, 3, 0, 0, time.UTC),
	}

	update, err := repo.RecoverAgentSessionStatus(t.Context(), current, []core.TaskProviderSession{
		{
			LastObservedAt:    time.Date(2026, time.April, 19, 11, 3, 0, 0, time.UTC),
			TaskID:            "task-123",
			Provider:          core.ProviderCodex,
			ProviderSessionID: "session-123",
			TranscriptPath:    rootPath,
			StartSource:       "startup",
		},
		{
			LastObservedAt:    time.Date(2026, time.April, 19, 11, 5, 0, 0, time.UTC),
			TaskID:            "task-123",
			Provider:          core.ProviderCodex,
			ProviderSessionID: "session-123",
			TranscriptPath:    subagentPath,
		},
	}, time.Time{})

	require.NoError(t, err)
	require.Equal(t, &core.AgentSessionStatus{
		Phase:        core.TaskStatusPhaseWorking,
		RawEventName: "TranscriptActivity",
		ObservedAt:   time.Date(2026, time.April, 19, 11, 4, 0, 0, time.UTC),
	}, update)
}

func TestRepositoryRecoverAgentSessionStatus_UsesRootTranscriptWhenSessionStartWasMissed(t *testing.T) {
	repo := &repository{}
	rootPath := writeJSONL(t, []string{
		`{"timestamp":"2026-04-19T11:00:00Z","type":"session_meta","payload":{"id":"session-123","source":"cli"}}`,
		`{"timestamp":"2026-04-19T11:04:00Z","type":"response_item","payload":{"type":"function_call","name":"exec_command"}}`,
	})
	subagentPath := writeJSONL(t, []string{
		`{"timestamp":"2026-04-19T11:01:00Z","type":"session_meta","payload":{"id":"agent-456","source":{"subagent":{"thread_spawn":{"parent_thread_id":"session-123"}}},"parent_thread_id":"session-123"}}`,
		`{"timestamp":"2026-04-19T11:01:00Z","type":"session_meta","payload":{"id":"session-123","source":"cli"}}`,
		`{"timestamp":"2026-04-19T11:05:00Z","type":"event_msg","payload":{"type":"task_complete"}}`,
	})
	current := core.AgentSessionStatus{
		Phase:        core.TaskStatusPhaseWorking,
		RawEventName: "PostToolUse",
		ObservedAt:   time.Date(2026, time.April, 19, 11, 3, 0, 0, time.UTC),
	}

	update, err := repo.RecoverAgentSessionStatus(t.Context(), current, []core.TaskProviderSession{
		{
			LastObservedAt:    time.Date(2026, time.April, 19, 11, 3, 0, 0, time.UTC),
			TaskID:            "task-123",
			Provider:          core.ProviderCodex,
			ProviderSessionID: "session-123",
			TranscriptPath:    rootPath,
		},
		{
			LastObservedAt:    time.Date(2026, time.April, 19, 11, 5, 0, 0, time.UTC),
			TaskID:            "task-123",
			Provider:          core.ProviderCodex,
			ProviderSessionID: "session-123",
			TranscriptPath:    subagentPath,
		},
	}, time.Time{})

	require.NoError(t, err)
	require.Equal(t, &core.AgentSessionStatus{
		Phase:        core.TaskStatusPhaseWorking,
		RawEventName: "TranscriptActivity",
		ObservedAt:   time.Date(2026, time.April, 19, 11, 4, 0, 0, time.UTC),
	}, update)
}

func TestReadCodexTranscriptKind_CachesPrefixClassification(t *testing.T) {
	repo := &repository{}
	path := writeJSONL(t, []string{
		`{"timestamp":"2026-04-19T11:00:00Z","type":"session_meta","payload":{"id":"session-123","source":"cli"}}`,
	})

	kind, err := repo.readCodexTranscriptKind(t.Context(), path)
	require.NoError(t, err)
	require.Equal(t, codexTranscriptKindRoot, kind)
	require.NoError(t, os.Remove(path))

	kind, err = repo.readCodexTranscriptKind(t.Context(), path)
	require.NoError(t, err)
	require.Equal(t, codexTranscriptKindRoot, kind)
}

func TestCacheCodexTranscriptKind_BoundsRetainedPaths(t *testing.T) {
	repo := &repository{}

	for i := range maxCodexTranscriptKindCacheEntries + 1 {
		repo.cacheCodexTranscriptKind(strconv.Itoa(i), codexTranscriptKindRoot)
	}

	require.Len(t, repo.transcriptKinds, maxCodexTranscriptKindCacheEntries)
	_, oldestRetained := repo.transcriptKinds["0"]
	require.False(t, oldestRetained)
}

func TestRepositoryRecoverAgentSessionStatus_KeepsWaitingForInputHookDespiteNewerTranscriptActivity(t *testing.T) {
	repo := &repository{}
	path := writeJSONL(t, []string{
		`{"timestamp":"2026-04-19T11:02:00Z","type":"event_msg","payload":{"type":"task_complete"}}`,
		`{"timestamp":"2026-04-19T11:04:00Z","type":"response_item","payload":{"type":"function_call","name":"exec_command"}}`,
	})
	current := core.AgentSessionStatus{
		Phase:        core.TaskStatusPhaseWaitingForInput,
		RawEventName: "Stop",
		ObservedAt:   time.Date(2026, time.April, 19, 11, 3, 0, 0, time.UTC),
	}

	update, err := repo.RecoverAgentSessionStatus(t.Context(), current, []core.TaskProviderSession{{
		LastObservedAt: time.Date(2026, time.April, 19, 11, 3, 0, 0, time.UTC),
		TaskID:         "task-123",
		Provider:       core.ProviderCodex,
		TranscriptPath: path,
	}}, time.Time{})

	require.NoError(t, err)
	require.Nil(t, update)
}

func TestRepositoryRecoverAgentSessionStatus_DoesNotUseTaskCompleteWhenNewerActivityExists(t *testing.T) {
	repo := &repository{}
	path := writeJSONL(t, []string{
		`{"timestamp":"2026-04-19T11:04:00Z","type":"event_msg","payload":{"type":"task_complete"}}`,
		`{"timestamp":"2026-04-19T11:05:00Z","type":"event_msg","payload":{"type":"agent_message","message":"still working"}}`,
	})
	current := core.AgentSessionStatus{
		Phase:        core.TaskStatusPhaseWorking,
		RawEventName: "PostToolUse",
		ObservedAt:   time.Date(2026, time.April, 19, 11, 3, 0, 0, time.UTC),
	}

	update, err := repo.RecoverAgentSessionStatus(t.Context(), current, []core.TaskProviderSession{{
		LastObservedAt: time.Date(2026, time.April, 19, 11, 5, 0, 0, time.UTC),
		TaskID:         "task-123",
		Provider:       core.ProviderCodex,
		TranscriptPath: path,
	}}, time.Time{})

	require.NoError(t, err)
	require.Equal(t, &core.AgentSessionStatus{
		Phase:        core.TaskStatusPhaseWorking,
		RawEventName: "TranscriptActivity",
		ObservedAt:   time.Date(2026, time.April, 19, 11, 5, 0, 0, time.UTC),
	}, update)
}

func TestRepositoryRecoverAgentSessionStatus_RecoversNeedsInputAfterInterruptedRootTurn(t *testing.T) {
	repo := &repository{}
	path := writeJSONL(t, []string{
		`{"timestamp":"2026-04-19T11:00:00Z","type":"session_meta","payload":{"id":"session-123","source":"cli"}}`,
		`{"timestamp":"2026-04-19T11:01:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}`,
		`{"timestamp":"2026-04-19T11:02:00Z","type":"response_item","payload":{"type":"function_call","name":"exec_command"}}`,
		`{"timestamp":"2026-04-19T11:03:00Z","type":"event_msg","payload":{"type":"turn_aborted","turn_id":"turn-1","reason":"interrupted"}}`,
	})
	current := core.AgentSessionStatus{
		Phase:        core.TaskStatusPhaseWorking,
		RawEventName: "PostToolUse",
		ObservedAt:   time.Date(2026, time.April, 19, 11, 2, 0, 0, time.UTC),
	}

	update, err := repo.RecoverAgentSessionStatus(t.Context(), current, []core.TaskProviderSession{{
		LastObservedAt:    time.Date(2026, time.April, 19, 11, 2, 0, 0, time.UTC),
		TaskID:            "task-123",
		Provider:          core.ProviderCodex,
		ProviderSessionID: "session-123",
		TranscriptPath:    path,
		StartSource:       "startup",
	}}, time.Time{})

	require.NoError(t, err)
	require.Equal(t, &core.AgentSessionStatus{
		Phase:        core.TaskStatusPhaseWaitingForInput,
		RawEventName: "TranscriptTurnAborted",
		ObservedAt:   time.Date(2026, time.April, 19, 11, 3, 0, 0, time.UTC),
	}, update)
}

// The background-subagent fixtures reproduce a real rollout: the root agent
// spawned subagents, then ended its turn at 10:52:23 while they kept running.
var (
	codexProcessStartedAt = time.Date(2026, time.September, 22, 10, 39, 0, 0, time.UTC)
	codexRootStopAt       = time.Date(2026, time.September, 22, 10, 52, 23, 0, time.UTC)
)

func TestRepositoryRecoverAgentSessionStatus_ReportsSubagentRunningAfterRootStop(t *testing.T) {
	repo := &repository{}
	rootPath := writeCodexRootTranscript(t)
	subagentPath := writeCodexSubagentTranscript(t, "session-root",
		codexTurnRecord("2026-09-22T10:45:00.100Z", "task_started"),
	)

	update, err := repo.RecoverAgentSessionStatus(
		t.Context(),
		codexRootStopStatus(),
		codexBackgroundSessions(rootPath, subagentPath),
		codexProcessStartedAt,
	)

	require.NoError(t, err)
	require.Equal(t, &core.AgentSessionStatus{
		Phase:          core.TaskStatusPhaseWorkingInBackground,
		RawEventName:   "TranscriptSubagentsRunning",
		ObservedAt:     codexRootStopAt,
		BackgroundWork: core.TaskBackgroundWork{Subagents: 1},
	}, update)
}

func TestRepositoryRecoverAgentSessionStatus_CountsOnlyRunningSubagents(t *testing.T) {
	repo := &repository{}
	rootPath := writeCodexRootTranscript(t)
	running := writeCodexSubagentTranscript(t, "session-root",
		codexTurnRecord("2026-09-22T10:45:00.100Z", "task_started"),
	)
	runningSecondTurn := writeCodexSubagentTranscript(t, "session-root",
		codexTurnRecord("2026-09-22T10:45:00.100Z", "task_started"),
		codexTurnRecord("2026-09-22T10:48:00Z", "task_complete"),
		codexTurnRecord("2026-09-22T10:50:00Z", "task_started"),
	)
	finished := writeCodexSubagentTranscript(t, "session-root",
		codexTurnRecord("2026-09-22T10:45:00.100Z", "task_started"),
		codexTurnRecord("2026-09-22T10:51:00Z", "task_complete"),
	)
	aborted := writeCodexSubagentTranscript(t, "session-root",
		codexTurnRecord("2026-09-22T10:45:00.100Z", "task_started"),
		codexTurnRecord("2026-09-22T10:51:30Z", "turn_aborted"),
	)

	update, err := repo.RecoverAgentSessionStatus(
		t.Context(),
		codexRootStopStatus(),
		codexBackgroundSessions(rootPath, running, runningSecondTurn, finished, aborted),
		codexProcessStartedAt,
	)

	require.NoError(t, err)
	require.NotNil(t, update)
	require.Equal(t, core.TaskStatusPhaseWorkingInBackground, update.Phase)
	require.Equal(t, core.TaskBackgroundWork{Subagents: 2}, update.BackgroundWork)
}

func TestRepositoryRecoverAgentSessionStatus_FallsBackToNeedsInputWhenSubagentsFinished(t *testing.T) {
	repo := &repository{}
	rootPath := writeCodexRootTranscript(t)
	finished := writeCodexSubagentTranscript(t, "session-root",
		codexTurnRecord("2026-09-22T10:45:00.100Z", "task_started"),
		codexTurnRecord("2026-09-22T10:53:20Z", "task_complete"),
	)
	aborted := writeCodexSubagentTranscript(t, "session-root",
		codexTurnRecord("2026-09-22T10:45:00.100Z", "task_started"),
		codexTurnRecord("2026-09-22T10:53:30Z", "turn_aborted"),
	)

	update, err := repo.RecoverAgentSessionStatus(
		t.Context(),
		codexRootStopStatus(),
		codexBackgroundSessions(rootPath, finished, aborted),
		codexProcessStartedAt,
	)

	require.NoError(t, err)
	require.Nil(t, update)
}

func TestRepositoryRecoverAgentSessionStatus_KeepsPermissionRequestWhileSubagentsRun(t *testing.T) {
	repo := &repository{}
	rootPath := writeCodexRootTranscript(t)
	running := writeCodexSubagentTranscript(t, "session-root",
		codexTurnRecord("2026-09-22T10:45:00.100Z", "task_started"),
	)
	// Root and subagent permission requests persist the same status.
	current := codexRootStopStatus()
	current.RawEventName = core.HookEventPermissionRequest

	update, err := repo.RecoverAgentSessionStatus(
		t.Context(),
		current,
		codexBackgroundSessions(rootPath, running),
		codexProcessStartedAt,
	)

	require.NoError(t, err)
	require.Nil(t, update)
}

func TestRepositoryRecoverAgentSessionStatus_IgnoresSubagentTurnsFromBeforeRootResume(t *testing.T) {
	repo := &repository{}
	rootPath := writeJSONL(t, []string{
		`{"timestamp":"2026-09-22T10:40:00Z","type":"session_meta","payload":{"id":"session-root","source":"cli"}}`,
		`{"timestamp":"2026-09-22T10:40:01Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}`,
		`{"timestamp":"2026-09-22T10:52:23Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1"}}`,
		// Codex was killed and resumed at 14:00. A resume appends nothing to
		// the rollout and fires SessionStart only in its first turn, so the
		// cut-off is the resumed Codex process's start time.
		`{"timestamp":"2026-09-22T14:23:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-2"}}`,
		`{"timestamp":"2026-09-22T14:30:00Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-2"}}`,
	})
	killedMidTurn := writeCodexSubagentTranscript(t, "session-root",
		codexTurnRecord("2026-09-22T10:45:00.100Z", "task_started"),
	)
	spawnedAfterResume := writeCodexSubagentTranscript(t, "session-root",
		codexTurnRecord("2026-09-22T14:25:00Z", "task_started"),
	)
	sessions := codexBackgroundSessions(rootPath, killedMidTurn, spawnedAfterResume)
	resumedAt := time.Date(2026, time.September, 22, 14, 0, 0, 0, time.UTC)
	current := codexRootStopStatus()
	current.ObservedAt = time.Date(2026, time.September, 22, 14, 30, 0, 0, time.UTC)

	update, err := repo.RecoverAgentSessionStatus(t.Context(), current, sessions, resumedAt)

	require.NoError(t, err)
	require.NotNil(t, update)
	require.Equal(t, core.TaskBackgroundWork{Subagents: 1}, update.BackgroundWork)
	require.Equal(t, current.ObservedAt, update.ObservedAt)
}

func codexRootStopStatus() core.AgentSessionStatus {
	return core.AgentSessionStatus{
		Phase:        core.TaskStatusPhaseWaitingForInput,
		RawEventName: core.HookEventStop,
		ObservedAt:   codexRootStopAt,
	}
}

func writeCodexRootTranscript(t *testing.T) string {
	t.Helper()
	return writeJSONL(t, []string{
		`{"timestamp":"2026-09-22T10:40:00Z","type":"session_meta","payload":{"id":"session-root","source":"cli"}}`,
		`{"timestamp":"2026-09-22T10:40:01Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}`,
		`{"timestamp":"2026-09-22T10:44:59Z","type":"response_item","payload":{"type":"function_call","name":"spawn_agent"}}`,
		`{"timestamp":"2026-09-22T10:52:23Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1"}}`,
	})
}

// writeCodexSubagentTranscript writes a forked subagent rollout: the
// subagent's own session_meta, the parent's copied session_meta and open turn,
// then the subagent's own turn-lifecycle records.
func writeCodexSubagentTranscript(t *testing.T, parentID string, lifecycle ...string) string {
	t.Helper()
	lines := []string{
		`{"timestamp":"2026-09-22T10:45:00Z","type":"session_meta","payload":{"id":"agent-1","session_id":"` +
			parentID + `","source":{"subagent":{"thread_spawn":{"parent_thread_id":"` + parentID +
			`","depth":1}}},"parent_thread_id":"` + parentID + `"}}`,
		`{"timestamp":"2026-09-22T10:45:00Z","type":"session_meta","payload":{"id":"` + parentID + `","source":"cli"}}`,
		`{"timestamp":"2026-09-22T10:45:00Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}`,
	}
	return writeJSONL(t, append(lines, lifecycle...))
}

func codexTurnRecord(timestamp string, eventType string) string {
	return `{"timestamp":"` + timestamp + `","type":"event_msg","payload":{"type":"` + eventType + `"}}`
}

func codexBackgroundSessions(rootPath string, subagentPaths ...string) []core.TaskProviderSession {
	sessions := []core.TaskProviderSession{{
		FirstObservedAt:   time.Date(2026, time.September, 22, 10, 40, 0, 0, time.UTC),
		LastObservedAt:    codexRootStopAt,
		TaskID:            "task-123",
		Provider:          core.ProviderCodex,
		ProviderSessionID: "session-root",
		TranscriptPath:    rootPath,
		StartSource:       "startup",
		LastEventName:     core.HookEventStop,
	}}
	for _, path := range subagentPaths {
		sessions = append(sessions, core.TaskProviderSession{
			FirstObservedAt:   time.Date(2026, time.September, 22, 10, 45, 0, 0, time.UTC),
			LastObservedAt:    time.Date(2026, time.September, 22, 10, 45, 0, 0, time.UTC),
			TaskID:            "task-123",
			Provider:          core.ProviderCodex,
			ProviderSessionID: "session-root",
			TranscriptPath:    path,
			LastEventName:     core.HookEventSubagentStart,
		})
	}
	return sessions
}

func TestTranscriptIndex_UnchangedReadsNoOldBytesAndAppendReadsOnlyNewBytes(t *testing.T) {
	repo := &repository{}
	path := writeJSONL(t, []string{
		`{"timestamp":"2026-04-19T11:00:00Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}}}}`,
	})

	usage, err := repo.ReadSessionTokenUsage(t.Context(), path)
	require.NoError(t, err)
	require.Equal(t, 12, usage.TotalTokens)
	first := repo.transcriptStats()
	require.Equal(t, uint64(1), first.Rebuilds)
	require.Positive(t, first.BytesRead)

	usage, err = repo.ReadSessionTokenUsage(t.Context(), path)
	require.NoError(t, err)
	require.Equal(t, 12, usage.TotalTokens)
	unchanged := repo.transcriptStats()
	require.Equal(t, first.BytesRead, unchanged.BytesRead)

	appended := `{"timestamp":"2026-04-19T11:01:00Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":20,"output_tokens":5,"total_tokens":25}}}}` + "\n"
	appendTranscript(t, path, appended)

	usage, err = repo.ReadSessionTokenUsage(t.Context(), path)
	require.NoError(t, err)
	require.Equal(t, 25, usage.TotalTokens)
	afterAppend := repo.transcriptStats()
	require.Equal(t, uint64(len(appended)), afterAppend.BytesRead-unchanged.BytesRead)
}

func TestTranscriptIndex_IncompleteLineCommitsOnlyAfterNewline(t *testing.T) {
	repo := &repository{}
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	record := `{"timestamp":"2026-04-19T11:00:00Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}}}}`
	require.NoError(t, os.WriteFile(path, []byte(record), 0o644))

	usage, err := repo.ReadSessionTokenUsage(t.Context(), path)
	require.NoError(t, err)
	require.Nil(t, usage)
	beforeNewline := repo.transcriptStats()

	appendTranscript(t, path, "\n")
	usage, err = repo.ReadSessionTokenUsage(t.Context(), path)
	require.NoError(t, err)
	require.Equal(t, 12, usage.TotalTokens)
	afterNewline := repo.transcriptStats()
	require.Equal(t, uint64(1), afterNewline.BytesRead-beforeNewline.BytesRead)
}

func TestTranscriptIndex_RebuildsAfterTruncationAndReplacement(t *testing.T) {
	repo := &repository{}
	path := writeJSONL(t, []string{
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"output_tokens":20,"total_tokens":120}}}}`,
		`{"timestamp":"2026-04-19T11:00:00Z","type":"event_msg","payload":{"type":"user_message","message":"make this file longer before truncation"}}`,
	})
	usage, err := repo.ReadSessionTokenUsage(t.Context(), path)
	require.NoError(t, err)
	require.Equal(t, 120, usage.TotalTokens)

	truncated := `{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":3,"output_tokens":1,"total_tokens":4}}}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(truncated), 0o644))
	usage, err = repo.ReadSessionTokenUsage(t.Context(), path)
	require.NoError(t, err)
	require.Equal(t, 4, usage.TotalTokens)

	replacement := filepath.Join(t.TempDir(), "replacement.jsonl")
	replaced := `{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":8,"output_tokens":2,"total_tokens":10}}}}` + "\n"
	require.NoError(t, os.WriteFile(replacement, []byte(replaced), 0o644))
	require.NoError(t, os.Rename(replacement, path))
	usage, err = repo.ReadSessionTokenUsage(t.Context(), path)
	require.NoError(t, err)
	require.Equal(t, 10, usage.TotalTokens)
	require.Equal(t, uint64(3), repo.transcriptStats().Rebuilds)
}

func TestTranscriptIndex_RebuildsAfterSameInodeEqualSizeRewrite(t *testing.T) {
	repo := &repository{}
	path := writeJSONL(t, []string{
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}}}}`,
	})
	usage, err := repo.ReadSessionTokenUsage(t.Context(), path)
	require.NoError(t, err)
	require.Equal(t, 12, usage.TotalTokens)

	rewritten := `{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":30,"output_tokens":4,"total_tokens":34}}}}` + "\n"
	fileInfo, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, fileInfo.Size(), int64(len(rewritten)))
	require.NoError(t, os.WriteFile(path, []byte(rewritten), 0o644))
	modifiedAt := fileInfo.ModTime().Add(time.Second)
	require.NoError(t, os.Chtimes(path, modifiedAt, modifiedAt))

	usage, err = repo.ReadSessionTokenUsage(t.Context(), path)
	require.NoError(t, err)
	require.Equal(t, 34, usage.TotalTokens)
	require.Equal(t, uint64(2), repo.transcriptStats().Rebuilds)
}

func TestTranscriptIndex_ConcurrentReadersParseTranscriptOnce(t *testing.T) {
	repo := &repository{}
	path := writeJSONL(t, []string{
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}}}}`,
	})
	fileInfo, err := os.Stat(path)
	require.NoError(t, err)

	const readerCount = 24
	var wait sync.WaitGroup
	wait.Add(readerCount)
	errs := make(chan error, readerCount)
	for range readerCount {
		go func() {
			defer wait.Done()
			usage, readErr := repo.ReadSessionTokenUsage(t.Context(), path)
			if readErr == nil && (usage == nil || usage.TotalTokens != 12) {
				readErr = errors.New("unexpected token projection")
			}
			errs <- readErr
		}()
	}
	wait.Wait()
	close(errs)
	for readErr := range errs {
		require.NoError(t, readErr)
	}

	stats := repo.transcriptStats()
	require.Equal(t, uint64(fileInfo.Size()), stats.BytesRead)
	require.Equal(t, uint64(1), stats.Rebuilds)
	require.Equal(t, uint64(readerCount-1), stats.CacheHits)
}

func TestTranscriptIndex_CancellationPreservesLastValidProjectionForRetry(t *testing.T) {
	repo := &repository{}
	path := writeJSONL(t, []string{
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}}}}`,
	})
	usage, err := repo.ReadSessionTokenUsage(t.Context(), path)
	require.NoError(t, err)
	require.Equal(t, 12, usage.TotalTokens)

	appended := `{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":20,"output_tokens":5,"total_tokens":25}}}}` + "\n"
	appendTranscript(t, path, appended)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	usage, err = repo.ReadSessionTokenUsage(cancelled, path)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, usage)

	usage, err = repo.ReadSessionTokenUsage(t.Context(), path)
	require.NoError(t, err)
	require.Equal(t, 25, usage.TotalTokens)
}

func TestTranscriptIndex_LRUEvictionRebuildsOnNextRead(t *testing.T) {
	repo := &repository{transcripts: newCodexTranscriptIndex(1)}
	path := writeJSONL(t, []string{
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}}}}`,
	})
	fileInfo, err := os.Stat(path)
	require.NoError(t, err)

	for range 2 {
		usage, readErr := repo.ReadSessionTokenUsage(t.Context(), path)
		require.NoError(t, readErr)
		require.Equal(t, 12, usage.TotalTokens)
	}

	stats := repo.transcriptStats()
	require.Equal(t, uint64(2), stats.Rebuilds)
	require.Equal(t, uint64(2*fileInfo.Size()), stats.BytesRead)
	require.Equal(t, uint64(2), stats.Evictions)
}

func writeJSONL(t *testing.T, lines []string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	content := strings.Join(lines, "\n") + "\n"
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func appendTranscript(t *testing.T, path string, content string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	require.NoError(t, err)
	_, err = file.WriteString(content)
	require.NoError(t, err)
	require.NoError(t, file.Close())
}
