package statusstream

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestProcessManagerEnsureRunning_SpawnsAndWaitsForHealth(t *testing.T) {
	t.Parallel()

	socketPath := filepath.Join(t.TempDir(), "status.sock")
	var spawned bool
	var healthy bool

	manager := NewProcessManager(ProcessConfig{
		SocketPath: socketPath,
		ExecPath:   "/tmp/debug-binary",
		CommandArgs: []string{
			"status-observer",
		},
		Spawn: func(context.Context, string, []string, []string) error {
			spawned = true
			healthy = true
			return nil
		},
		Dial: func(context.Context, string) error {
			if healthy {
				return nil
			}
			return errors.New("not healthy")
		},
		Probe: func(context.Context, string) (HealthStatus, error) {
			return HealthStatus{}, nil
		},
		HealthyTimeout: 250 * time.Millisecond,
		RetryInterval:  10 * time.Millisecond,
	})

	require.NoError(t, manager.EnsureRunning(context.Background()))
	require.True(t, spawned)
}

func TestProcessManagerRestart_StopsExistingSocketBeforeSpawning(t *testing.T) {
	t.Parallel()

	socketPath := filepath.Join(t.TempDir(), "status.sock")
	require.NoError(t, os.WriteFile(socketPath, []byte(""), 0o600))

	var stopped bool
	var removed bool
	var spawned bool
	var healthy bool

	manager := NewProcessManager(ProcessConfig{
		SocketPath: socketPath,
		ExecPath:   "/tmp/debug-binary",
		CommandArgs: []string{
			"status-observer",
		},
		Stop: func(context.Context, string) error {
			stopped = true
			healthy = false
			return nil
		},
		Remove: func(path string) error {
			removed = true
			return os.Remove(path)
		},
		Spawn: func(context.Context, string, []string, []string) error {
			spawned = true
			healthy = true
			return nil
		},
		Dial: func(context.Context, string) error {
			if healthy {
				return nil
			}
			return errors.New("not healthy")
		},
		Probe: func(context.Context, string) (HealthStatus, error) {
			return HealthStatus{}, nil
		},
		HealthyTimeout: 250 * time.Millisecond,
		RetryInterval:  10 * time.Millisecond,
	})

	require.NoError(t, manager.Restart(context.Background()))
	require.True(t, stopped)
	require.True(t, removed)
	require.True(t, spawned)
}

func TestConfigureDetachedProcess_SetsDetachedUnixProcessAttributes(t *testing.T) {
	t.Parallel()

	cmd := exec.Command("/bin/sh", "-c", "sleep 1")
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	require.NoError(t, err)
	defer devNull.Close()

	configureDetachedProcess(cmd, devNull)

	require.NotNil(t, cmd.SysProcAttr)
	require.True(t, cmd.SysProcAttr.Setsid)
	require.Same(t, devNull, cmd.Stdin)
	require.Same(t, devNull, cmd.Stdout)
	require.Same(t, devNull, cmd.Stderr)
}
