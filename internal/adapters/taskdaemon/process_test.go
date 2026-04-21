package taskdaemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	processTestHelperEnv       = "RIG_TASKDAEMONPROCESS_TEST_HELPER"
	processTestHelperSocketEnv = "RIG_TASKDAEMONPROCESS_TEST_SOCKET"
)

func TestMain(m *testing.M) {
	if os.Getenv(processTestHelperEnv) == "1" {
		runHelperDaemon(os.Getenv(processTestHelperSocketEnv))
		return
	}

	os.Exit(m.Run())
}

func TestStopTaskDaemon_RequiresSocketPath(t *testing.T) {
	t.Parallel()

	err := stopTaskDaemon(context.Background(), "")
	require.EqualError(t, err, "task daemon socket path not configured")
}

func TestStopTaskDaemon_SendsStopRequestAndAcceptsStoppingResponse(t *testing.T) {
	t.Parallel()

	socketPath := processTestSocketPath(t)
	listener, err := listenUnixSocket(t.Context(), socketPath)
	require.NoError(t, err)
	defer listener.Close()

	requestCh := make(chan socketRequest, 1)
	errCh := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			errCh <- acceptErr
			return
		}
		defer conn.Close()

		var req socketRequest
		if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&req); err != nil {
			errCh <- err
			return
		}
		requestCh <- req

		if err := json.NewEncoder(conn).Encode(socketEnvelope{Type: "stopping", OK: true}); err != nil {
			errCh <- err
			return
		}

		errCh <- nil
	}()

	require.NoError(t, stopTaskDaemon(context.Background(), socketPath))
	require.Equal(t, socketRequest{Command: "stop"}, <-requestCh)
	require.NoError(t, <-errCh)
}

func TestSpawnTaskDaemonProcess_StartsDaemonWithDetachedStdio(t *testing.T) {
	t.Parallel()

	socketPath := processTestSocketPath(t)

	require.NoError(t, spawnTaskDaemonProcess(context.Background(), os.Args[0], []string{
		processTestHelperEnv + "=1",
		processTestHelperSocketEnv + "=" + socketPath,
	}))
	require.NoError(t, waitForHealthyTaskDaemon(context.Background(), socketPath))
	require.NoError(t, stopTaskDaemon(context.Background(), socketPath))
	require.NoError(t, waitForSocketRemoval(socketPath, 2*time.Second))
}

func TestAdapterEnsureRunning_ReturnsWhenDaemonAlreadyHealthy(t *testing.T) {
	t.Parallel()

	socketPath := processTestSocketPath(t)
	startHelperDaemonProcess(t, socketPath)

	adapter := New(Config{
		SocketPath: socketPath,
		ExecPath:   filepath.Join(t.TempDir(), "does-not-need-to-exist"),
	})

	require.NoError(t, adapter.EnsureRunning(context.Background()))
	require.NoError(t, stopTaskDaemon(context.Background(), socketPath))
	require.NoError(t, waitForSocketRemoval(socketPath, 2*time.Second))
}

func TestAdapterEnsureRunning_UsesDefaultExecutableAndConfiguredEnvToStartDaemon(t *testing.T) {
	t.Parallel()

	socketPath := processTestSocketPath(t)
	adapter := New(Config{
		SocketPath: socketPath,
		Env: []string{
			processTestHelperEnv + "=1",
			processTestHelperSocketEnv + "=" + socketPath,
		},
	})

	require.NoError(t, adapter.EnsureRunning(context.Background()))
	require.NoError(t, stopTaskDaemon(context.Background(), socketPath))
	require.NoError(t, waitForSocketRemoval(socketPath, 2*time.Second))
}

func TestFrontendBuildVersion_DefaultsToDev(t *testing.T) {
	t.Parallel()

	require.NotEmpty(t, currentFrontendBuildVersion)
	require.Equal(
		t,
		"dev",
		currentFrontendBuildVersion,
		"default build version should be safe for local test binaries",
	)
}

func TestFrontendProtocolVersion_DefaultsToCurrentValue(t *testing.T) {
	t.Parallel()

	require.Equal(t, 2, currentFrontendProtocolVersion)
}

func TestAdapterEnsureRunning_RestartsStaleHealthyDaemonMissingFrontendProtocol(t *testing.T) {
	t.Parallel()

	socketPath := processTestSocketPath(t)
	adapter := New(processTestHelperConfig(t, socketPath))

	manualListener, err := listenUnixSocket(t.Context(), socketPath)
	require.NoError(t, err)
	defer manualListener.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var stopSeen atomic.Bool
	errCh := make(chan error, 1)
	go serveLegacyTestDaemon(ctx, manualListener, &stopSeen, errCh)

	require.NoError(t, adapter.EnsureRunning(context.Background()))
	require.True(t, stopSeen.Load())

	cancel()
	require.NoError(t, <-errCh)
	require.NoError(t, stopTaskDaemon(context.Background(), socketPath))
	require.NoError(t, waitForSocketRemoval(socketPath, 2*time.Second))
}

func TestAdapterRestart_StopsExistingDaemonAndStartsFreshProcess(t *testing.T) {
	t.Parallel()

	socketPath := processTestSocketPath(t)
	adapter := New(processTestHelperConfig(t, socketPath))

	manualListener, err := listenUnixSocket(t.Context(), socketPath)
	require.NoError(t, err)
	defer manualListener.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var stopSeen atomic.Bool
	errCh := make(chan error, 1)
	go serveTestDaemon(ctx, manualListener, &stopSeen, errCh)

	require.NoError(t, adapter.Restart(context.Background()))
	require.True(t, stopSeen.Load())
	cancel()
	require.NoError(t, <-errCh)
	require.NoError(t, stopTaskDaemon(context.Background(), socketPath))
	require.NoError(t, waitForSocketRemoval(socketPath, 2*time.Second))
}

func TestAdapterRestart_ReturnsStopErrorWithoutRemovingSocket(t *testing.T) {
	t.Parallel()

	socketPath := processTestSocketPath(t)
	listener, err := listenUnixSocket(t.Context(), socketPath)
	require.NoError(t, err)
	defer listener.Close()

	errCh := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			errCh <- acceptErr
			return
		}
		defer conn.Close()

		var req socketRequest
		if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&req); err != nil {
			errCh <- err
			return
		}

		if req != (socketRequest{Command: "stop"}) {
			errCh <- fmt.Errorf("unexpected request: %#v", req)
			return
		}
		if err := json.NewEncoder(conn).Encode(socketEnvelope{
			Type:  "stopping",
			OK:    false,
			Error: "stop failed",
		}); err != nil {
			errCh <- err
			return
		}

		errCh <- nil
	}()

	adapter := New(processTestHelperConfig(t, socketPath))

	err = adapter.Restart(context.Background())
	require.EqualError(t, err, "stop failed")
	require.NoError(t, <-errCh)

	_, statErr := os.Stat(socketPath)
	require.NoError(t, statErr)
}

func TestAdapterRestart_RemovesStaleSocketPathAndStartsFreshProcess(t *testing.T) {
	t.Parallel()

	socketPath := processTestSocketPath(t)
	listener, err := listenUnixSocket(t.Context(), socketPath)
	require.NoError(t, err)
	require.NoError(t, listener.Close())

	adapter := New(processTestHelperConfig(t, socketPath))

	require.NoError(t, adapter.Restart(context.Background()))
	require.NoError(t, waitForHealthyTaskDaemon(context.Background(), socketPath))
	require.NoError(t, stopTaskDaemon(context.Background(), socketPath))
	require.NoError(t, waitForSocketRemoval(socketPath, 2*time.Second))
}

func TestWaitForHealthyTaskDaemon_IncludesLastHealthErrorOnTimeout(t *testing.T) {
	t.Parallel()

	socketPath := processTestSocketPath(t)
	listener, err := listenUnixSocket(t.Context(), socketPath)
	require.NoError(t, err)
	defer listener.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		for {
			if err := listener.(*net.UnixListener).SetDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
				errCh <- err
				return
			}

			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				if isTimeoutNetError(acceptErr) {
					select {
					case <-ctx.Done():
						errCh <- nil
						return
					default:
						continue
					}
				}
				errCh <- acceptErr
				return
			}

			if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&socketRequest{}); err != nil {
				errCh <- err
				_ = conn.Close()
				return
			}
			if err := json.NewEncoder(conn).Encode(socketEnvelope{
				Type:  "health",
				OK:    false,
				Error: "daemon unhealthy for test",
			}); err != nil {
				errCh <- err
				_ = conn.Close()
				return
			}
			_ = conn.Close()
		}
	}()

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer waitCancel()

	err = waitForHealthyTaskDaemon(waitCtx, socketPath)
	require.Error(t, err)
	require.ErrorContains(t, err, "task daemon did not become healthy")
	require.ErrorContains(t, err, context.DeadlineExceeded.Error())
	require.ErrorContains(t, err, "daemon unhealthy for test")

	cancel()
	require.NoError(t, <-errCh)
}

func processTestHelperConfig(t *testing.T, socketPath string) Config {
	t.Helper()

	return Config{
		SocketPath: socketPath,
		ExecPath:   os.Args[0],
		Env: []string{
			processTestHelperEnv + "=1",
			processTestHelperSocketEnv + "=" + socketPath,
		},
	}
}

func serveTestDaemon(
	ctx context.Context,
	listener net.Listener,
	stopSeen *atomic.Bool,
	errCh chan<- error,
) {
	defer listener.Close()
	defer close(errCh)

	for {
		if err := listener.(*net.UnixListener).SetDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
			errCh <- err
			return
		}

		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			if isTimeoutNetError(acceptErr) {
				select {
				case <-ctx.Done():
					errCh <- nil
					return
				default:
					continue
				}
			}
			errCh <- acceptErr
			return
		}

		if err := handleTestDaemonConnection(conn, stopSeen); err != nil {
			_ = conn.Close()
			errCh <- err
			return
		}
		_ = conn.Close()

		if stopSeen.Load() {
			errCh <- nil
			return
		}
	}
}

func serveLegacyTestDaemon(
	ctx context.Context,
	listener net.Listener,
	stopSeen *atomic.Bool,
	errCh chan<- error,
) {
	defer listener.Close()
	defer close(errCh)

	for {
		if err := listener.(*net.UnixListener).SetDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
			errCh <- err
			return
		}

		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			if isTimeoutNetError(acceptErr) {
				select {
				case <-ctx.Done():
					errCh <- nil
					return
				default:
					continue
				}
			}
			errCh <- acceptErr
			return
		}

		if err := handleLegacyTestDaemonConnection(conn, stopSeen); err != nil {
			_ = conn.Close()
			errCh <- err
			return
		}
		_ = conn.Close()

		if stopSeen.Load() {
			errCh <- nil
			return
		}
	}
}

func handleTestDaemonConnection(conn net.Conn, stopSeen *atomic.Bool) error {
	var req socketRequest
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&req); err != nil {
		return err
	}

	resp := socketEnvelope{OK: true}
	switch req.Command {
	case "health":
		resp.Type = "health"
	case "protocol_version":
		resp.Type = "protocol_version"
		resp.ProtocolVersion = currentFrontendProtocolVersion
	case "frontend_build_version":
		resp.Type = "frontend_build_version"
		resp.Version = currentFrontendBuildVersion
	case "stop":
		resp.Type = "stopping"
		stopSeen.Store(true)
	default:
		resp.Type = "error"
		resp.OK = false
		resp.Error = "unexpected command"
	}

	return json.NewEncoder(conn).Encode(resp)
}

func handleLegacyTestDaemonConnection(conn net.Conn, stopSeen *atomic.Bool) error {
	var req socketRequest
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&req); err != nil {
		return err
	}

	resp := socketEnvelope{OK: true}
	switch req.Command {
	case "health":
		resp.Type = "health"
	case "stop":
		resp.Type = "stopping"
		stopSeen.Store(true)
	default:
		resp.Type = "error"
		resp.OK = false
		resp.Error = "unsupported command"
	}

	return json.NewEncoder(conn).Encode(resp)
}

func runHelperDaemon(socketPath string) {
	if socketPath == "" {
		os.Exit(2)
	}

	_ = os.Remove(socketPath)
	listener, err := listenUnixSocket(context.Background(), socketPath)
	if err != nil {
		os.Exit(3)
	}
	defer func() {
		_ = listener.Close()
		_ = os.Remove(socketPath)
	}()

	var stopSeen atomic.Bool
	errCh := make(chan error, 1)
	go serveTestDaemon(context.Background(), listener, &stopSeen, errCh)

	if err := <-errCh; err != nil {
		os.Exit(4)
	}
}

func startHelperDaemonProcess(t *testing.T, socketPath string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), os.Args[0])
	cmd.Env = append(os.Environ(),
		processTestHelperEnv+"=1",
		processTestHelperSocketEnv+"="+socketPath,
	)
	require.NoError(t, cmd.Start())

	t.Cleanup(func() {
		shutdownHelperProcess(socketPath, cmd)
	})

	require.NoError(t, waitForHealthyTaskDaemon(context.Background(), socketPath))
}

func shutdownHelperProcess(socketPath string, cmd *exec.Cmd) {
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer stopCancel()

	_ = stopTaskDaemon(stopCtx, socketPath)
	_ = waitForSocketRemoval(socketPath, 250*time.Millisecond)

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- cmd.Wait()
	}()

	select {
	case <-time.After(250 * time.Millisecond):
		_ = cmd.Process.Kill()
		<-waitDone
	case <-waitDone:
	}
}

func waitForSocketRemoval(socketPath string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_, err := os.Stat(socketPath)
		if os.IsNotExist(err) {
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}

	return os.ErrDeadlineExceeded
}

func processTestSocketPath(t *testing.T) string {
	t.Helper()

	path := filepath.Join(
		os.TempDir(),
		"rig-taskdaemonprocess-"+time.Now().UTC().Format("20060102150405.000000000")+".sock",
	)
	t.Cleanup(func() {
		_ = os.Remove(path)
	})

	return path
}
