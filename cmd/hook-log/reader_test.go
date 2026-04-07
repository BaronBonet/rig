package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRenderSummary_GroupsRecordsBySessionAndShowsLastAssistantMessage(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "codex-hooks.jsonl")
	require.NoError(t, os.WriteFile(logPath, []byte(
		`{"received_at":"2026-04-07T12:00:00Z","event_name":"SessionStart","raw_payload":{"session_id":"sess-1","hook_event_name":"SessionStart"}}`+"\n"+
			`{"received_at":"2026-04-07T12:00:01Z","event_name":"UserPromptSubmit","raw_payload":{"session_id":"sess-1","hook_event_name":"UserPromptSubmit"}}`+"\n"+
			`{"received_at":"2026-04-07T12:00:02Z","event_name":"Stop","raw_payload":{"session_id":"sess-1","hook_event_name":"Stop","last_assistant_message":"I fixed the bug."}}`+"\n"+
			`{"received_at":"2026-04-07T12:00:03Z","event_name":"Stop","raw_payload":{"session_id":"sess-1","hook_event_name":"Stop","last_assistant_message":""}}`+"\n"+
			`{"received_at":"2026-04-07T12:00:04Z","event_name":"SessionStart","raw_payload":{"hook_event_name":"SessionStart"}}`+"\n",
	), 0o644))

	summary, err := renderSummary(logPath)
	require.NoError(t, err)
	require.Equal(t, ""+
		"session (unknown session)\n"+
		"  SessionStart: 1\n"+
		"session sess-1\n"+
		"  SessionStart: 1\n"+
		"  Stop: 2\n"+
		"  UserPromptSubmit: 1\n"+
		"  last assistant message: I fixed the bug.", summary)
}
