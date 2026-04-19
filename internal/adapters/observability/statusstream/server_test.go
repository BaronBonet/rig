package statusstream

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"rig/internal/core"

	"github.com/stretchr/testify/require"
)

func TestServe_PublishesStatusUpdateFromCodexHook(t *testing.T) {
	t.Parallel()

	socketPath := testSocketPath(t)
	hookListener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer hookListener.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- Serve(ctx, ServerConfig{
			SocketPath:     socketPath,
			HookListenAddr: hookListener.Addr().String(),
			HookListener:   hookListener,
			HookIngestor:   &recordingIngestor{},
			Hub:            NewHub(),
			Now: func() time.Time {
				return time.Date(2026, time.April, 18, 12, 0, 0, 0, time.UTC)
			},
		})
	}()

	waitForSocketServer(t, socketPath)

	updates, cleanup, err := Subscribe(context.Background(), socketPath)
	require.NoError(t, err)
	defer cleanup()

	body, err := json.Marshal(map[string]any{
		"task_id": "task-1",
	})
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("http://%s/hook", hookListener.Addr().String()), bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("X-Codex-Hook-Event", "PreToolUse")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	_ = resp.Body.Close()

	select {
	case update := <-updates:
		require.Equal(t, "task-1", update.TaskID)
		require.Equal(t, core.AgentProviderCodex, update.Provider)
		require.Equal(t, core.TaskStatusPhaseWorking, update.Phase)
		require.Equal(t, "PreToolUse", update.RawEventName)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for published status update")
	}

	cancel()
	require.NoError(t, <-serverErr)
}

func TestServe_PublishesStartingFromCodexSessionStartHook(t *testing.T) {
	t.Parallel()

	socketPath := testSocketPath(t)
	hookListener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer hookListener.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- Serve(ctx, ServerConfig{
			SocketPath:     socketPath,
			HookListenAddr: hookListener.Addr().String(),
			HookListener:   hookListener,
			HookIngestor:   &recordingIngestor{},
			Hub:            NewHub(),
			Now: func() time.Time {
				return time.Date(2026, time.April, 18, 12, 0, 0, 0, time.UTC)
			},
		})
	}()

	waitForSocketServer(t, socketPath)

	updates, cleanup, err := Subscribe(context.Background(), socketPath)
	require.NoError(t, err)
	defer cleanup()

	body, err := json.Marshal(map[string]any{
		"task_id": "task-1",
	})
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("http://%s/hook", hookListener.Addr().String()), bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("X-Codex-Hook-Event", "SessionStart")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	_ = resp.Body.Close()

	select {
	case update := <-updates:
		require.Equal(t, "task-1", update.TaskID)
		require.Equal(t, core.AgentProviderCodex, update.Provider)
		require.Equal(t, core.TaskStatusPhaseStarting, update.Phase)
		require.Equal(t, "SessionStart", update.RawEventName)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for published status update")
	}

	cancel()
	require.NoError(t, <-serverErr)
}

func TestServe_IgnoresUnsupportedCodexHookEvents(t *testing.T) {
	t.Parallel()

	socketPath := testSocketPath(t)
	hookListener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer hookListener.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- Serve(ctx, ServerConfig{
			SocketPath:     socketPath,
			HookListenAddr: hookListener.Addr().String(),
			HookListener:   hookListener,
			HookIngestor:   &recordingIngestor{},
			Hub:            NewHub(),
		})
	}()

	waitForSocketServer(t, socketPath)

	updates, cleanup, err := Subscribe(context.Background(), socketPath)
	require.NoError(t, err)
	defer cleanup()

	body, err := json.Marshal(map[string]any{
		"task_id": "task-1",
	})
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("http://%s/hook", hookListener.Addr().String()), bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("X-Codex-Hook-Event", "PermissionRequest")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	_ = resp.Body.Close()

	select {
	case update := <-updates:
		t.Fatalf("unexpected status update published: %#v", update)
	case <-time.After(200 * time.Millisecond):
	}

	cancel()
	require.NoError(t, <-serverErr)
}

func TestServe_RequiresStatusServerConfig(t *testing.T) {
	t.Parallel()

	err := Serve(context.Background(), ServerConfig{})
	require.EqualError(t, err, "status observer socket path not configured")

	err = Serve(context.Background(), ServerConfig{
		SocketPath: "/tmp/status.sock",
	})
	require.EqualError(t, err, "status hook listen addr not configured")

	err = Serve(context.Background(), ServerConfig{
		SocketPath:     "/tmp/status.sock",
		HookListenAddr: "127.0.0.1:0",
	})
	require.EqualError(t, err, "status hook ingestor not configured")
}

type recordingIngestor struct{}

func (r *recordingIngestor) IngestHookEvent(_ context.Context, input core.HookEventInput) (*core.HookSessionSummary, error) {
	return &core.HookSessionSummary{
		TaskID:         input.TaskID,
		Provider:       input.Provider,
		LastEventName:  input.EventName,
		LastActivityAt: input.OccurredAt,
	}, nil
}
