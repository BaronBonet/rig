package main

import (
	"context"
	"os"
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
	require.NotNil(t, deps.HookIngestor)
	require.NotNil(t, deps.StartHookServer)
	require.Equal(t, "codex", deps.DefaultProvider)
}

func TestBuildDependencies_PassesConfiguredDefaultProviderToCLI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AGENT_PROVIDER", "claude")
	t.Setenv("AGENT_SQLITE_PATH", filepath.Join(home, "state.db"))
	t.Setenv("AGENT_CODEX_BINARY", "codex")
	t.Setenv("AGENT_CLAUDE_BINARY", "claude")

	deps, err := buildDependencies()
	require.NoError(t, err)
	require.Equal(t, "claude", deps.DefaultProvider)
}

func TestBuildDependencies_PreservesDoctorStorageFailures(t *testing.T) {
	home := t.TempDir()
	blocker := filepath.Join(home, "blocker")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o644))

	t.Setenv("HOME", home)
	t.Setenv("AGENT_PROVIDER", "codex")
	t.Setenv("AGENT_SQLITE_PATH", filepath.Join(blocker, "state.db"))
	t.Setenv("AGENT_CODEX_BINARY", "true")
	t.Setenv("AGENT_CLAUDE_BINARY", "true")

	deps, err := buildDependencies()
	require.NoError(t, err)
	require.IsType(t, &core.Service{}, deps.Service)

	result, err := deps.Service.Doctor(context.Background(), "")
	require.NoError(t, err)
	require.Contains(t, result.Failures, "storage: mkdir "+blocker+": not a directory")
}
