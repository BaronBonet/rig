package main

import (
	"path/filepath"
	"testing"

	"agent/internal/core"

	"github.com/stretchr/testify/require"
)

func TestBuildDependencies_ReturnsConcreteService(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AGENT_PROVIDER", "codex")
	t.Setenv("AGENT_SQLITE_PATH", filepath.Join(home, "state.db"))
	t.Setenv("AGENT_CODEX_BINARY", "codex")
	t.Setenv("AGENT_CLAUDE_BINARY", "claude")

	deps, err := buildDependencies()
	require.NoError(t, err)
	require.IsType(t, &core.Service{}, deps.Service)
}
