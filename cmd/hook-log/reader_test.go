package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRenderSummary_UsesReceivedAtForLastAssistantMessage(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "codex-hooks.jsonl")
	require.NoError(t, os.WriteFile(logPath, buildJSONL(t,
		map[string]any{
			"received_at": "2026-04-07T12:00:05Z",
			"event_name":  "Stop",
			"raw_payload": map[string]any{
				"session_id":             "sess-1",
				"hook_event_name":        "Stop",
				"last_assistant_message": "newer message",
			},
		},
		map[string]any{
			"received_at": "2026-04-07T12:00:01Z",
			"event_name":  "Stop",
			"raw_payload": map[string]any{
				"session_id":             "sess-1",
				"hook_event_name":        "Stop",
				"last_assistant_message": "older message",
			},
		},
		map[string]any{
			"received_at": "2026-04-07T12:00:02Z",
			"event_name":  "SessionStart",
			"raw_payload": map[string]any{
				"hook_event_name": "SessionStart",
			},
		},
	), 0o644))

	summary, err := renderSummary(logPath)
	require.NoError(t, err)
	require.Equal(t, ""+
		"session (unknown session)\n"+
		"  SessionStart: 1\n"+
		"session sess-1\n"+
		"  Stop: 2\n"+
		"  last assistant message: newer message", summary)
}

func TestRenderSummary_SanitizesAssistantMessageAndReadsLargeLines(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "codex-hooks.jsonl")
	require.NoError(t, os.WriteFile(logPath, buildJSONL(t, map[string]any{
		"received_at": "2026-04-07T12:00:00Z",
		"event_name":  "Stop",
		"raw_payload": map[string]any{
			"session_id":             "sess-2",
			"hook_event_name":        "Stop",
			"last_assistant_message": "first line\nsecond line\tthird line",
			"filler":                 strings.Repeat("x", 70000),
		},
	}), 0o644))

	summary, err := renderSummary(logPath)
	require.NoError(t, err)
	require.Contains(t, summary, "session sess-2")
	require.Contains(t, summary, "Stop: 1")
	require.Contains(t, summary, "last assistant message: first line second line third line")
	require.NotContains(t, summary, "first line\nsecond line")
}

func TestRenderSummary_HandlesOversizedLogRecord(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "codex-hooks.jsonl")
	require.NoError(t, os.WriteFile(logPath, buildJSONL(t, map[string]any{
		"received_at": "2026-04-07T12:00:00Z",
		"event_name":  "Stop",
		"raw_payload": map[string]any{
			"session_id":             "sess-big",
			"hook_event_name":        "Stop",
			"last_assistant_message": "oversized message",
			"filler":                 strings.Repeat("x", 1024*1024+1),
		},
	}), 0o644))

	summary, err := renderSummary(logPath)
	require.NoError(t, err)
	require.Contains(t, summary, "session sess-big")
	require.Contains(t, summary, "Stop: 1")
	require.Contains(t, summary, "last assistant message: oversized message")
}

func buildJSONL(t *testing.T, records ...map[string]any) []byte {
	t.Helper()

	var b strings.Builder
	for _, record := range records {
		data, err := json.Marshal(record)
		require.NoError(t, err)
		b.Write(data)
		b.WriteByte('\n')
	}
	return []byte(b.String())
}
