package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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

func TestAppendRecord_CreatesPrivateDirectoryAndFile(t *testing.T) {
	logDir := filepath.Join(t.TempDir(), "nested", "logs")
	logPath := filepath.Join(logDir, "codex-hooks.jsonl")

	err := appendRecord(logPath, hooklog.NewRecord(time.Date(2026, 4, 7, 11, 4, 0, 0, time.UTC), "Stop", "127.0.0.1:1234", "/hook", []byte(`{"session_id":"sess-perms"}`)))
	require.NoError(t, err)

	dirInfo, err := os.Stat(logDir)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), dirInfo.Mode().Perm())

	fileInfo, err := os.Stat(logPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fileInfo.Mode().Perm())
}

func TestServerHandleHook_ConcurrentRequestsAppendValidJSONL(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "codex-hooks.jsonl")
	srv := newServer(logPath, func() time.Time {
		return time.Date(2026, 4, 7, 11, 5, 0, 0, time.UTC)
	})

	const requestCount = 32
	var wg sync.WaitGroup
	errs := make(chan error, requestCount)

	for i := 0; i < requestCount; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			req := httptest.NewRequest(http.MethodPost, "/hook", strings.NewReader(`{"session_id":"sess-`+strconv.Itoa(i)+`","hook_event_name":"Stop"}`))
			req.Header.Set("X-Codex-Hook-Event", "Stop")
			rec := httptest.NewRecorder()

			srv.handleHook(rec, req)
			if rec.Code != http.StatusAccepted {
				errs <- fmt.Errorf("status = %d", rec.Code)
			}
		}(i)
	}

	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	body, err := os.ReadFile(logPath)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	require.Len(t, lines, requestCount)
	for _, line := range lines {
		var record hooklog.Record
		require.NoError(t, json.Unmarshal([]byte(line), &record))
	}
}

func readJSONLRecord(t *testing.T, path string) hooklog.Record {
	t.Helper()

	body, err := os.ReadFile(path)
	require.NoError(t, err)

	var record hooklog.Record
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(body), &record))
	return record
}
