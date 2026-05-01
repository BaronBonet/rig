package codex

import (
	"os"
	"path/filepath"
	"strings"
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

func TestRepositoryRecoverLatestTaskStatus_ReturnsTaskCompleteFromNewestTranscript(t *testing.T) {
	repo := &repository{}
	oldPath := writeJSONL(t, []string{
		`{"timestamp":"2026-04-19T11:00:00Z","type":"event_msg","payload":{"type":"task_complete"}}`,
	})
	newPath := writeJSONL(t, []string{
		`{malformed`,
		`{"timestamp":"2026-04-19T11:02:00Z","type":"event_msg","payload":{"type":"token_count"}}`,
		`{"timestamp":"2026-04-19T11:03:00Z","type":"response_item","payload":{"type":"task_complete"}}`,
		`{"timestamp":"2026-04-19T11:04:00Z","type":"event_msg","payload":{"type":"task_complete"}}`,
	})
	current := core.TaskStatusUpdate{
		TaskID:       "task-123",
		Provider:     core.ProviderCodex,
		Phase:        core.TaskStatusPhaseWorking,
		RawEventName: "PostToolUse",
		ObservedAt:   time.Date(2026, time.April, 19, 11, 1, 0, 0, time.UTC),
	}

	update, err := repo.RecoverLatestTaskStatus(t.Context(), current, []core.TaskProviderSession{
		{
			LastObservedAt: time.Date(2026, time.April, 19, 11, 5, 0, 0, time.UTC),
			TaskID:         "task-123",
			Provider:       core.ProviderCodex,
			TranscriptPath: newPath,
		},
		{
			LastObservedAt: time.Date(2026, time.April, 19, 11, 0, 0, 0, time.UTC),
			TaskID:         "task-123",
			Provider:       core.ProviderCodex,
			TranscriptPath: oldPath,
		},
	})

	require.NoError(t, err)
	require.Equal(t, &core.TaskStatusUpdate{
		TaskID:       "task-123",
		Provider:     core.ProviderCodex,
		Phase:        core.TaskStatusPhaseWaitingForInput,
		RawEventName: "TranscriptTaskComplete",
		ObservedAt:   time.Date(2026, time.April, 19, 11, 4, 0, 0, time.UTC),
	}, update)
}

func TestRepositoryRecoverLatestTaskStatus_ReturnsWorkingFromNewerTranscriptActivity(t *testing.T) {
	repo := &repository{}
	path := writeJSONL(t, []string{
		`{"timestamp":"2026-04-19T11:02:00Z","type":"event_msg","payload":{"type":"task_complete"}}`,
		`{"timestamp":"2026-04-19T11:04:00Z","type":"response_item","payload":{"type":"function_call","name":"exec_command"}}`,
	})
	current := core.TaskStatusUpdate{
		TaskID:       "task-123",
		Provider:     core.ProviderCodex,
		Phase:        core.TaskStatusPhaseWaitingForInput,
		RawEventName: "Stop",
		ObservedAt:   time.Date(2026, time.April, 19, 11, 3, 0, 0, time.UTC),
	}

	update, err := repo.RecoverLatestTaskStatus(t.Context(), current, []core.TaskProviderSession{{
		LastObservedAt: time.Date(2026, time.April, 19, 11, 3, 0, 0, time.UTC),
		TaskID:         "task-123",
		Provider:       core.ProviderCodex,
		TranscriptPath: path,
	}})

	require.NoError(t, err)
	require.Equal(t, &core.TaskStatusUpdate{
		TaskID:       "task-123",
		Provider:     core.ProviderCodex,
		Phase:        core.TaskStatusPhaseWorking,
		RawEventName: "TranscriptActivity",
		ObservedAt:   time.Date(2026, time.April, 19, 11, 4, 0, 0, time.UTC),
	}, update)
}

func TestRepositoryRecoverLatestTaskStatus_DoesNotUseTaskCompleteWhenNewerActivityExists(t *testing.T) {
	repo := &repository{}
	path := writeJSONL(t, []string{
		`{"timestamp":"2026-04-19T11:04:00Z","type":"event_msg","payload":{"type":"task_complete"}}`,
		`{"timestamp":"2026-04-19T11:05:00Z","type":"event_msg","payload":{"type":"agent_message","message":"still working"}}`,
	})
	current := core.TaskStatusUpdate{
		TaskID:       "task-123",
		Provider:     core.ProviderCodex,
		Phase:        core.TaskStatusPhaseWorking,
		RawEventName: "PostToolUse",
		ObservedAt:   time.Date(2026, time.April, 19, 11, 3, 0, 0, time.UTC),
	}

	update, err := repo.RecoverLatestTaskStatus(t.Context(), current, []core.TaskProviderSession{{
		LastObservedAt: time.Date(2026, time.April, 19, 11, 5, 0, 0, time.UTC),
		TaskID:         "task-123",
		Provider:       core.ProviderCodex,
		TranscriptPath: path,
	}})

	require.NoError(t, err)
	require.Equal(t, &core.TaskStatusUpdate{
		TaskID:       "task-123",
		Provider:     core.ProviderCodex,
		Phase:        core.TaskStatusPhaseWorking,
		RawEventName: "TranscriptActivity",
		ObservedAt:   time.Date(2026, time.April, 19, 11, 5, 0, 0, time.UTC),
	}, update)
}

func writeJSONL(t *testing.T, lines []string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	content := strings.Join(lines, "\n") + "\n"
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}
