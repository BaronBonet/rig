package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agent/internal/experimental/hooklog"
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
	record := readJSONLRecord(t, logPath)
	require.Equal(t, "SessionStart", record.EventName)
	require.Equal(t, "sess-1", record.SessionID())
	require.Equal(t, time.Date(2026, 4, 7, 11, 0, 0, 0, time.UTC), record.ReceivedAt)
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
	record := readJSONLRecord(t, logPath)
	require.Equal(t, "Stop", record.EventName)
	require.Equal(t, "{not-json", record.RawText)
	require.Equal(t, "invalid JSON payload", record.ParseError)
	require.Empty(t, record.RawPayload)
}

func TestServerHandleHook_RejectsNonPOSTWithoutAppendingRecord(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "codex-hooks.jsonl")
	srv := newServer(logPath, func() time.Time {
		return time.Date(2026, 4, 7, 11, 2, 0, 0, time.UTC)
	})

	req := httptest.NewRequest(http.MethodGet, "/hook", strings.NewReader(`{"session_id":"sess-1"}`))
	rec := httptest.NewRecorder()

	srv.handleHook(rec, req)

	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	require.Equal(t, http.MethodPost, rec.Header().Get("Allow"))
	_, err := os.Stat(logPath)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestServerHandleHook_UsesHookEventNameWhenHeaderMissing(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "codex-hooks.jsonl")
	srv := newServer(logPath, func() time.Time {
		return time.Date(2026, 4, 7, 11, 3, 0, 0, time.UTC)
	})

	req := httptest.NewRequest(http.MethodPost, "/hook", strings.NewReader(`{"session_id":"sess-2","hook_event_name":"Stop"}`))
	rec := httptest.NewRecorder()

	srv.handleHook(rec, req)

	require.Equal(t, http.StatusAccepted, rec.Code)
	record := readJSONLRecord(t, logPath)
	require.Equal(t, "Stop", record.EventName)
	require.Equal(t, "sess-2", record.SessionID())
}

func readJSONLRecord(t *testing.T, path string) hooklog.Record {
	t.Helper()

	body, err := os.ReadFile(path)
	require.NoError(t, err)

	var record hooklog.Record
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(body), &record))
	return record
}
