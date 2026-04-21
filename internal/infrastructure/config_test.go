package infrastructure

import (
	"path/filepath"
	"rig/internal/core"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadConfig_DefaultsAndOverrides(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("RIG_PROVIDER", "codex")
	t.Setenv("RIG_SQLITE_PATH", filepath.Join(home, "task-custom.db"))
	t.Setenv("RIG_CODEX_BINARY", "codex-custom")
	t.Setenv("RIG_DAEMON_SOCKET_PATH", filepath.Join(home, "task-daemon.sock"))
	t.Setenv("RIG_DAEMON_HOOK_LISTEN_ADDRESS", "127.0.0.1:4999")

	cfg, err := LoadConfig()
	require.NoError(t, err)

	require.Equal(t, core.ProviderCodex, cfg.Provider)
	require.Equal(t, filepath.Join(home, "task-custom.db"), cfg.SQLite.Path)
	require.Equal(t, "codex-custom", cfg.Codex.Binary)
	require.Equal(t, filepath.Join(home, "task-daemon.sock"), cfg.Daemon.SocketPath)
	require.Equal(t, "127.0.0.1:4999", cfg.Daemon.HookListenAddr)
}

func TestLoadConfig_DefaultSQLitePathWhenUnset(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg, err := LoadConfig()
	require.NoError(t, err)

	require.Equal(t, filepath.Join(home, ".local", "share", "rig", "tasks.db"), cfg.SQLite.Path)
	require.Equal(t, filepath.Join(home, ".local", "share", "rig", "daemon.sock"), cfg.Daemon.SocketPath)
	require.Equal(t, "127.0.0.1:4124", cfg.Daemon.HookListenAddr)
}

func TestLoadConfig_RejectsUnknownProvider(t *testing.T) {
	t.Setenv("RIG_PROVIDER", "unknown")

	cfg, err := LoadConfig()
	require.Nil(t, cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "RIG_PROVIDER")
}
