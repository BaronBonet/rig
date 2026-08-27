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

func TestRepositoryRecoverLatestTaskStatus_UsesRootTranscriptWhenSubagentTranscriptIsNewer(t *testing.T) {
	repo := &repository{}
	rootPath := writeJSONL(t, []string{
		`{"timestamp":"2026-04-19T11:04:00Z","type":"response_item","payload":{"type":"function_call","name":"exec_command"}}`,
	})
	subagentPath := writeJSONL(t, []string{
		`{"timestamp":"2026-04-19T11:05:00Z","type":"event_msg","payload":{"type":"task_complete"}}`,
	})
	current := core.TaskStatusUpdate{
		TaskID:       "task-123",
		Provider:     core.ProviderCodex,
		Phase:        core.TaskStatusPhaseWorking,
		RawEventName: "PostToolUse",
		ObservedAt:   time.Date(2026, time.April, 19, 11, 3, 0, 0, time.UTC),
	}

	update, err := repo.RecoverLatestTaskStatus(t.Context(), current, []core.TaskProviderSession{
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
	})

	require.NoError(t, err)
	require.Equal(t, &core.TaskStatusUpdate{
		TaskID:       "task-123",
		Provider:     core.ProviderCodex,
		Phase:        core.TaskStatusPhaseWorking,
		RawEventName: "TranscriptActivity",
		ObservedAt:   time.Date(2026, time.April, 19, 11, 4, 0, 0, time.UTC),
	}, update)
}

func TestRepositoryRecoverLatestTaskStatus_UsesRootTranscriptWhenSessionStartWasMissed(t *testing.T) {
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
	current := core.TaskStatusUpdate{
		TaskID:       "task-123",
		Provider:     core.ProviderCodex,
		Phase:        core.TaskStatusPhaseWorking,
		RawEventName: "PostToolUse",
		ObservedAt:   time.Date(2026, time.April, 19, 11, 3, 0, 0, time.UTC),
	}

	update, err := repo.RecoverLatestTaskStatus(t.Context(), current, []core.TaskProviderSession{
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
	})

	require.NoError(t, err)
	require.Equal(t, &core.TaskStatusUpdate{
		TaskID:       "task-123",
		Provider:     core.ProviderCodex,
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

func TestRepositoryRecoverLatestTaskStatus_KeepsWaitingForInputHookDespiteNewerTranscriptActivity(t *testing.T) {
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
	require.Nil(t, update)
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
