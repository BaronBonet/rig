package infrastructure

import (
	"path/filepath"
	"testing"

	"github.com/BaronBonet/rig/internal/core"

	"github.com/stretchr/testify/require"
)

func TestLoadConfig_DefaultsAndOverrides(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, "config.json")
	t.Setenv("HOME", home)
	t.Setenv("RIG_CONFIG_PATH", configPath)
	t.Setenv("RIG_PROVIDER", "codex")
	t.Setenv("RIG_SQLITE_PATH", filepath.Join(home, "task-custom.db"))
	t.Setenv("RIG_CODEX_BINARY", "codex-custom")
	t.Setenv("RIG_CLAUDE_BINARY", "claude-custom")
	t.Setenv("RIG_DAEMON_SOCKET_PATH", filepath.Join(home, "task-daemon.sock"))
	t.Setenv("RIG_DAEMON_HOOK_LISTEN_ADDRESS", "127.0.0.1:4999")
	require.NoError(t, SaveProviderSetup(configPath, ProviderSetup{
		ConfiguredProviders: []core.Provider{core.ProviderCodex},
		DefaultProvider:     core.ProviderCodex,
	}))

	cfg, err := LoadConfig()
	require.NoError(t, err)

	require.Equal(t, core.ProviderCodex, cfg.Provider)
	require.Equal(t, []core.Provider{core.ProviderCodex}, cfg.ProviderSetup.ConfiguredProviders)
	require.Equal(t, filepath.Join(home, "task-custom.db"), cfg.SQLite.Path)
	require.Equal(t, "codex-custom", cfg.Codex.Binary)
	require.Equal(t, "claude-custom", cfg.Claude.Binary)
	require.Equal(t, filepath.Join(home, "task-daemon.sock"), cfg.Daemon.SocketPath)
	require.Equal(t, "127.0.0.1:4999", cfg.Daemon.HookListenAddr)
}

func TestLoadConfig_DefaultSQLitePathWhenUnset(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, "config.json")
	t.Setenv("HOME", home)
	t.Setenv("RIG_CONFIG_PATH", configPath)
	require.NoError(t, SaveProviderSetup(configPath, ProviderSetup{
		ConfiguredProviders: []core.Provider{core.ProviderCodex},
		DefaultProvider:     core.ProviderCodex,
	}))

	cfg, err := LoadConfig()
	require.NoError(t, err)

	require.Equal(t, filepath.Join(home, ".local", "share", "rig", "tasks.db"), cfg.SQLite.Path)
	require.Equal(t, filepath.Join(home, ".local", "share", "rig", "daemon.sock"), cfg.Daemon.SocketPath)
	require.Equal(t, "127.0.0.1:4124", cfg.Daemon.HookListenAddr)
}

func TestLoadConfig_RejectsUnknownProvider(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("RIG_CONFIG_PATH", configPath)
	t.Setenv("RIG_PROVIDER", "unknown")
	require.NoError(t, SaveProviderSetup(configPath, ProviderSetup{
		ConfiguredProviders: []core.Provider{core.ProviderCodex},
		DefaultProvider:     core.ProviderCodex,
	}))

	cfg, err := LoadConfig()
	require.Nil(t, cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "RIG_PROVIDER")
}

func TestLoadConfig_RequiresProviderSetup(t *testing.T) {
	t.Setenv("RIG_CONFIG_PATH", filepath.Join(t.TempDir(), "missing.json"))

	cfg, err := LoadConfig()
	require.Nil(t, cfg)
	require.ErrorIs(t, err, ErrProviderSetupRequired)
}

func TestLoadConfig_RejectsRuntimeOverrideForUnconfiguredProvider(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("RIG_CONFIG_PATH", configPath)
	t.Setenv("RIG_PROVIDER", "claude")
	require.NoError(t, SaveProviderSetup(configPath, ProviderSetup{
		ConfiguredProviders: []core.Provider{core.ProviderCodex},
		DefaultProvider:     core.ProviderCodex,
	}))

	cfg, err := LoadConfig()
	require.Nil(t, cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "provider is not configured")
}
