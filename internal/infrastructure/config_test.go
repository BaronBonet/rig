package infrastructure

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadConfig_DefaultsAndOverrides(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AGENT_PROVIDER", "claude")
	t.Setenv("AGENT_SQLITE_PATH", filepath.Join(home, "custom.db"))

	cfg, err := LoadConfig()
	require.NoError(t, err)

	require.Equal(t, "claude", cfg.Service.Provider)
	require.Equal(t, filepath.Join(home, "custom.db"), cfg.SQLite.Path)
	require.Equal(t, "codex", cfg.Codex.Binary)
	require.Equal(t, "claude", cfg.Claude.Binary)
}
