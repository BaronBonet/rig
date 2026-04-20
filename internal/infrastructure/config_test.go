package infrastructure

import (
	"path/filepath"
	"testing"

	"rig/internal/core"

	"github.com/stretchr/testify/require"
)

func TestLoadConfig_DefaultsAndOverrides(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AGENT_PROVIDER", "claude")
	t.Setenv("AGENT_SQLITE_PATH", filepath.Join(home, "custom.db"))
	t.Setenv("AGENT_TASK_SQLITE_PATH", filepath.Join(home, "task-custom.db"))
	t.Setenv("AGENT_CODEX_BINARY", "codex-custom")
	t.Setenv("AGENT_CLAUDE_BINARY", "claude-custom")
	t.Setenv("TASK_DAEMON_OBSERVER_SOCKET_PATH", filepath.Join(home, "task-daemon.sock"))
	t.Setenv("TASK_DAEMON_HOOK_LISTEN_ADDRESS", "127.0.0.1:4999")

	cfg, err := LoadConfig()
	require.NoError(t, err)

	require.Equal(t, core.AgentProviderClaude, cfg.Provider)
	require.Equal(t, filepath.Join(home, "custom.db"), cfg.SQLite.Path)
	require.Equal(t, filepath.Join(home, "task-custom.db"), cfg.TaskSQLite.Path)
	require.Equal(t, "codex-custom", cfg.Codex.Binary)
	require.Equal(t, "claude-custom", cfg.Claude.Binary)
	require.Equal(t, filepath.Join(home, "task-daemon.sock"), cfg.TaskDaemon.SocketPath)
	require.Equal(t, "127.0.0.1:4999", cfg.TaskDaemon.HookListenAddr)
}

func TestLoadConfig_DefaultSQLitePathWhenUnset(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg, err := LoadConfig()
	require.NoError(t, err)

	require.Equal(t, filepath.Join(home, ".local", "share", "agent", "state.db"), cfg.SQLite.Path)
	require.Equal(t, filepath.Join(home, ".local", "share", "agent", "tasks.db"), cfg.TaskSQLite.Path)
	require.Equal(t, filepath.Join(home, ".local", "share", "agent", "observer.sock"), cfg.TaskDaemon.SocketPath)
	require.Equal(t, "127.0.0.1:4123", cfg.TaskDaemon.HookListenAddr)
}

func TestLoadConfig_RejectsUnknownProvider(t *testing.T) {
	t.Setenv("AGENT_PROVIDER", "unknown")

	cfg, err := LoadConfig()
	require.Nil(t, cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "AGENT_PROVIDER")
}
