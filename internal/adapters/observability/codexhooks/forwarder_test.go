package codexhooks

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rig/internal/core"

	"github.com/stretchr/testify/require"
)

type stubForwarderIngestor struct {
	input core.HookEventInput
	err   error
	calls int
}

func (s *stubForwarderIngestor) IngestHookEvent(
	_ context.Context,
	input core.HookEventInput,
) (*core.HookSessionSummary, error) {
	s.calls++
	s.input = input
	return nil, s.err
}

func TestForwarderPrefersCollectorEndpoint(t *testing.T) {
	var capturedEvent string
	var capturedBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedEvent = r.Header.Get("X-Codex-Hook-Event")
		body, err := ioReadAll(r)
		require.NoError(t, err)
		capturedBody = string(body)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	ingestor := &stubForwarderIngestor{}
	forwarder := Forwarder{
		CollectorURL: server.URL,
		Ingestor:     ingestor,
		Now: func() time.Time {
			return time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
		},
	}

	err := forwarder.Forward(
		t.Context(),
		"UserPromptSubmit",
		[]byte(`{"cwd":"/tmp/worktree","prompt":"fix the billing retry flow"}`),
	)
	require.NoError(t, err)
	require.Equal(t, "UserPromptSubmit", capturedEvent)
	require.JSONEq(t, `{"cwd":"/tmp/worktree","prompt":"fix the billing retry flow"}`, capturedBody)
	require.Equal(t, 0, ingestor.calls)
}

func TestForwarderFallsBackToDirectIngestWhenCollectorFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer server.Close()

	ingestor := &stubForwarderIngestor{}
	forwarder := Forwarder{
		CollectorURL: server.URL,
		Ingestor:     ingestor,
		Now: func() time.Time {
			return time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
		},
	}

	err := forwarder.Forward(
		t.Context(),
		"UserPromptSubmit",
		[]byte(`{"cwd":"/tmp/worktree","prompt":"fix the billing retry flow"}`),
	)
	require.NoError(t, err)
	require.Equal(t, 1, ingestor.calls)
	require.Equal(t, "UserPromptSubmit", ingestor.input.EventName)
	require.Equal(t, "/tmp/worktree", ingestor.input.Cwd)
	require.Equal(t, "fix the billing retry flow", ingestor.input.PromptText)
}

func TestForwarderLogsFailuresAndReturnsNil(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "hook-forwarder-errors.log")
	forwarder := Forwarder{
		CollectorURL: "http://127.0.0.1:1/hook",
		Ingestor: &stubForwarderIngestor{
			err: errors.New("sqlite unavailable"),
		},
		ErrorLogPath: logPath,
	}

	err := forwarder.Forward(t.Context(), "Stop", []byte(`{"cwd":"/tmp/worktree"}`))
	require.NoError(t, err)

	rawLog, readErr := os.ReadFile(logPath)
	require.NoError(t, readErr)
	require.Contains(t, string(rawLog), "event=Stop")
	require.True(
		t,
		strings.Contains(string(rawLog), "sqlite unavailable") || strings.Contains(string(rawLog), "connect:"),
	)
}

func ioReadAll(r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	return io.ReadAll(r.Body)
}
