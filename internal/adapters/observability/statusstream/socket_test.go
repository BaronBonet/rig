package statusstream

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"rig/internal/core"

	"github.com/stretchr/testify/require"
)

func TestSocketSubscribe_ReceivesPublishedUpdates(t *testing.T) {
	t.Parallel()

	socketPath := testSocketPath(t)
	hub := NewHub()
	server := NewSocketServer(SocketServerConfig{
		SocketPath:  socketPath,
		Hub:         hub,
		Fingerprint: "test-fingerprint",
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(ctx)
	}()
	waitForSocketServer(t, socketPath)

	updates, cleanup, err := Subscribe(context.Background(), socketPath)
	require.NoError(t, err)
	defer cleanup()

	expected := core.TaskStatusUpdate{
		TaskID:       "task-1",
		Provider:     core.AgentProviderCodex,
		Phase:        core.TaskStatusPhaseWorking,
		RawEventName: "PreToolUse",
		ObservedAt:   time.Now().UTC(),
	}

	hub.Publish(expected)

	select {
	case got := <-updates:
		require.Equal(t, expected, got)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for streamed status update")
	}

	cancel()
	require.NoError(t, <-errCh)
}

func TestSocketHealth_ReturnsFingerprint(t *testing.T) {
	t.Parallel()

	socketPath := testSocketPath(t)
	server := NewSocketServer(SocketServerConfig{
		SocketPath:  socketPath,
		Hub:         NewHub(),
		Fingerprint: "health-fingerprint",
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(ctx)
	}()
	waitForSocketServer(t, socketPath)

	status, err := probeSocketHealth(context.Background(), socketPath)
	require.NoError(t, err)
	require.Equal(t, "health-fingerprint", status.Fingerprint)

	cancel()
	require.NoError(t, <-errCh)
}

func TestSocketStop_InvokesStopCallback(t *testing.T) {
	t.Parallel()

	socketPath := testSocketPath(t)
	var stopped atomic.Bool
	server := NewSocketServer(SocketServerConfig{
		SocketPath: socketPath,
		Hub:        NewHub(),
		Stop: func() {
			stopped.Store(true)
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(ctx)
	}()
	waitForSocketServer(t, socketPath)

	conn, err := net.Dial("unix", socketPath)
	require.NoError(t, err)
	defer conn.Close()

	require.NoError(t, json.NewEncoder(conn).Encode(socketRequest{Command: "stop"}))

	var resp socketEnvelope
	require.NoError(t, json.NewDecoder(conn).Decode(&resp))
	require.Equal(t, "stopping", resp.Type)
	require.True(t, resp.OK)
	require.True(t, stopped.Load())

	cancel()
	require.NoError(t, <-errCh)
}

func TestSocketIngestHookEvent_InvokesServerIngestor(t *testing.T) {
	t.Parallel()

	socketPath := testSocketPath(t)
	recorder := &socketRecordingIngestor{}
	server := NewSocketServer(SocketServerConfig{
		SocketPath:   socketPath,
		Hub:          NewHub(),
		HookIngestor: recorder,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(ctx)
	}()
	waitForSocketServer(t, socketPath)

	input := core.HookEventInput{
		TaskID:     "task-1",
		EventName:  "PreToolUse",
		Provider:   string(core.AgentProviderCodex),
		OccurredAt: time.Now().UTC(),
	}

	require.NoError(t, IngestHookEvent(context.Background(), socketPath, input))
	require.Equal(t, input.TaskID, recorder.lastInput.TaskID)
	require.Equal(t, input.EventName, recorder.lastInput.EventName)

	cancel()
	require.NoError(t, <-errCh)
}

func waitForSocketServer(t *testing.T, socketPath string) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := dialSocketHealth(context.Background(), socketPath); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("socket server at %s did not become healthy", socketPath)
}

func testSocketPath(t *testing.T) string {
	t.Helper()

	path := filepath.Join(os.TempDir(), fmt.Sprintf("rig-%d.sock", time.Now().UnixNano()))
	t.Cleanup(func() {
		_ = os.Remove(path)
	})

	return path
}

type socketRecordingIngestor struct {
	lastInput core.HookEventInput
}

func (r *socketRecordingIngestor) IngestHookEvent(_ context.Context, input core.HookEventInput) (*core.HookSessionSummary, error) {
	r.lastInput = input
	return &core.HookSessionSummary{TaskID: input.TaskID, LastEventName: input.EventName}, nil
}
