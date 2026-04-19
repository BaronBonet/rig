package taskdaemon

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestProcessManagerEnsureRunning_DoesNotSpawnWhenHealthy(t *testing.T) {
	t.Parallel()

	socketPath := filepath.Join(t.TempDir(), "task.sock")
	var spawned bool

	manager := NewProcessManager(ProcessConfig{
		SocketPath: socketPath,
		ExecPath:   "/tmp/debug-binary",
		Spawn: func(context.Context, string, []string, []string) error {
			spawned = true
			return nil
		},
		Dial: func(context.Context, string) error {
			return nil
		},
	})

	require.NoError(t, manager.EnsureRunning(context.Background()))
	require.False(t, spawned)
}

func TestProcessManagerEnsureRunning_SpawnsAndWaitsForHealth(t *testing.T) {
	t.Parallel()

	socketPath := filepath.Join(t.TempDir(), "task.sock")
	var spawned bool
	var healthy bool

	manager := NewProcessManager(ProcessConfig{
		SocketPath: socketPath,
		ExecPath:   "/tmp/debug-binary",
		CommandArgs: []string{
			"task-daemon",
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
		HealthyTimeout: 250 * time.Millisecond,
		RetryInterval:  10 * time.Millisecond,
	})

	require.NoError(t, manager.EnsureRunning(context.Background()))
	require.True(t, spawned)
}

func TestProcessManagerRestart_StopsExistingSocketBeforeSpawning(t *testing.T) {
	t.Parallel()

	socketPath := filepath.Join(t.TempDir(), "task.sock")
	require.NoError(t, os.WriteFile(socketPath, []byte(""), 0o600))

	var stopped bool
	var removed bool
	var spawned bool
	var healthy bool

	manager := NewProcessManager(ProcessConfig{
		SocketPath: socketPath,
		ExecPath:   "/tmp/debug-binary",
		CommandArgs: []string{
			"task-daemon",
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
		HealthyTimeout: 250 * time.Millisecond,
		RetryInterval:  10 * time.Millisecond,
	})

	require.NoError(t, manager.Restart(context.Background()))
	require.True(t, stopped)
	require.True(t, removed)
	require.True(t, spawned)
}

func TestProcessManager_SourceDoesNotContainStartupLockOrFingerprintMachinery(t *testing.T) {
	content, err := os.ReadFile(filepath.Join(".", "process.go"))
	require.NoError(t, err)

	for _, needle := range []string{
		"ExpectedFingerprint",
		"Probe",
		"acquireStartupLock",
		"processStartupGuards",
		"startup lock",
	} {
		require.NotContains(t, string(content), needle)
	}
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

func TestProcessManager_SourceDoesNotContainStatusDaemonNaming(t *testing.T) {
	content, err := os.ReadFile(filepath.Join(".", "process.go"))
	require.NoError(t, err)
	require.False(t, strings.Contains(string(content), "status daemon"))
}
