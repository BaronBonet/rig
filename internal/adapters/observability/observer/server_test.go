package observer

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"agent/internal/core"

	"github.com/stretchr/testify/require"
)

func TestServe_HealthCheckOverUnixSocket(t *testing.T) {
	socketPath := filepath.Join("/tmp", fmt.Sprintf("observer-%d.sock", time.Now().UnixNano()))
	hookListener := mustListenTCP(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, ServerConfig{
			SocketPath:     socketPath,
			HookListenAddr: hookListener.Addr().String(),
			HookListener:   hookListener,
			HookIngestor:   &stubObserverHookIngestor{},
		})
	}()

	require.Eventually(t, func() bool {
		select {
		case err := <-done:
			require.NoError(t, err)
			return false
		default:
		}
		status, err := unixHTTPStatus(socketPath, http.MethodGet, "/healthz", "")
		if err != nil {
			return false
		}
		return status == http.StatusOK
	}, 2*time.Second, 20*time.Millisecond)

	cancel()
	require.Eventually(t, func() bool {
		select {
		case err := <-done:
			require.NoError(t, err)
			return true
		default:
			return false
		}
	}, 2*time.Second, 20*time.Millisecond)

	_, err := os.Stat(socketPath)
	require.Error(t, err)
}

func TestServe_HandlesHookIngestRequests(t *testing.T) {
	socketPath := filepath.Join("/tmp", fmt.Sprintf("observer-%d.sock", time.Now().UnixNano()))
	hookListener := mustListenTCP(t)
	ingestor := &stubObserverHookIngestor{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, ServerConfig{
			SocketPath:     socketPath,
			HookListenAddr: hookListener.Addr().String(),
			HookListener:   hookListener,
			HookIngestor:   ingestor,
		})
	}()

	require.Eventually(t, func() bool {
		select {
		case err := <-done:
			require.NoError(t, err)
			return false
		default:
		}
		status, err := unixHTTPStatus(hookListener.Addr().String(), http.MethodPost, "/hook", `{"cwd":"/tmp/worktree","session_id":"sess-1","hook_event_name":"SessionStart"}`)
		if err != nil {
			return false
		}
		return status == http.StatusAccepted && ingestor.called
	}, 2*time.Second, 20*time.Millisecond)

	cancel()
	require.Eventually(t, func() bool {
		select {
		case err := <-done:
			require.NoError(t, err)
			return true
		default:
			return false
		}
	}, 2*time.Second, 20*time.Millisecond)

	require.Equal(t, "SessionStart", ingestor.input.EventName)
	require.Equal(t, "/tmp/worktree", ingestor.input.Cwd)
	require.Equal(t, "sess-1", ingestor.input.SessionID)
}

func mustListenTCP(t *testing.T) net.Listener {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	return listener
}

func unixHTTPStatus(address, method, target, body string) (int, error) {
	conn, err := net.Dial("unix", address)
	if err != nil {
		conn, err = net.Dial("tcp", address)
		if err != nil {
			return 0, err
		}
	}
	defer conn.Close()

	if method == http.MethodGet {
		body = ""
	}

	request := fmt.Sprintf("%s %s HTTP/1.1\r\nHost: local\r\nConnection: close\r\n", method, target)
	if body != "" {
		request += fmt.Sprintf("Content-Type: application/json\r\nContent-Length: %d\r\n", len(body))
	}
	request += "\r\n" + body

	if _, err := io.WriteString(conn, request); err != nil {
		return 0, err
	}

	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	return resp.StatusCode, nil
}

type stubObserverHookIngestor struct {
	called bool
	input  core.HookEventInput
}

func (s *stubObserverHookIngestor) IngestHookEvent(_ context.Context, input core.HookEventInput) (*core.HookSessionSummary, error) {
	s.called = true
	s.input = input
	return nil, nil
}
