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

func TestSocketRejectsUnsupportedCommands(t *testing.T) {
	t.Parallel()

	socketPath := testSocketPath(t)
	server := NewSocketServer(SocketServerConfig{
		SocketPath: socketPath,
		Hub:        NewHub(),
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

	require.NoError(t, json.NewEncoder(conn).Encode(socketRequest{Command: "ingest_hook"}))

	var resp socketEnvelope
	require.NoError(t, json.NewDecoder(conn).Decode(&resp))
	require.Equal(t, "error", resp.Type)
	require.Contains(t, resp.Error, `unsupported command "ingest_hook"`)

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
