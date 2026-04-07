package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestServerHandleHook_AppendsJSONLRecord(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "codex-hooks.jsonl")
	srv := newServer(logPath, func() time.Time {
		return time.Date(2026, 4, 7, 11, 0, 0, 0, time.UTC)
	})

	req := httptest.NewRequest(http.MethodPost, "/hook", strings.NewReader(`{"session_id":"sess-1","hook_event_name":"SessionStart","source":"startup"}`))
	req.Header.Set("X-Codex-Hook-Event", "SessionStart")
	rec := httptest.NewRecorder()

	srv.handleHook(rec, req)

	require.Equal(t, http.StatusAccepted, rec.Code)
	body, err := os.ReadFile(logPath)
	require.NoError(t, err)
	require.Contains(t, string(body), `"event_name":"SessionStart"`)
	require.Contains(t, string(body), `"session_id":"sess-1"`)
	require.Contains(t, string(body), `"received_at":"2026-04-07T11:00:00Z"`)
}

func TestServerHandleHook_PreservesInvalidJSONAsRawText(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "codex-hooks.jsonl")
	srv := newServer(logPath, func() time.Time {
		return time.Date(2026, 4, 7, 11, 1, 0, 0, time.UTC)
	})

	req := httptest.NewRequest(http.MethodPost, "/hook", strings.NewReader("{not-json"))
	req.Header.Set("X-Codex-Hook-Event", "Stop")
	rec := httptest.NewRecorder()

	srv.handleHook(rec, req)

	require.Equal(t, http.StatusAccepted, rec.Code)
	body, err := os.ReadFile(logPath)
	require.NoError(t, err)
	require.Contains(t, string(body), `"event_name":"Stop"`)
	require.Contains(t, string(body), `"raw_text":"{not-json"`)
	require.Contains(t, string(body), `"parse_error":"invalid JSON payload"`)
}
