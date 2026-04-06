package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"agent/internal/adapters/repository/claude"
	"agent/internal/adapters/repository/codex"
	"agent/internal/adapters/repository/sqlite"
	"agent/internal/core"
	"agent/internal/infrastructure"
	"agent/internal/pkg/execx"

	"github.com/stretchr/testify/require"
)

func TestBuildDependencies_WiresSharedCodexRuntimeMonitor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AGENT_PROVIDER", "codex")
	t.Setenv("AGENT_SQLITE_PATH", filepath.Join(home, "state.db"))
	t.Setenv("AGENT_CODEX_BINARY", "codex")
	t.Setenv("AGENT_CLAUDE_BINARY", "claude")

	deps, err := buildDependencies()
	require.NoError(t, err)

	svc, ok := deps.Service.(*runtimeService)
	require.True(t, ok)
	require.NotNil(t, svc.runtimeMonitor)
	require.Contains(t, svc.runtimeDetectors, "codex")
	require.Contains(t, svc.runtimeDetectors, "claude")

	service1, err := svc.newService(false)
	require.NoError(t, err)
	require.NotNil(t, service1)

	service2, err := svc.newService(false)
	require.NoError(t, err)
	require.NotNil(t, service2)
}

func TestRuntimeServiceDoctor_UsesSQLiteConfig(t *testing.T) {
	parent := t.TempDir()
	blocker := filepath.Join(parent, "blocker")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o644))

	svc := &runtimeService{
		runner: execx.ExecRunner{},
		cfg: infrastructure.Config{
			Service: core.Config{Provider: "codex"},
			SQLite:  sqlite.Config{Path: filepath.Join(blocker, "state.db")},
			Codex:   codex.Config{Binary: "codex"},
			Claude:  claude.Config{Binary: "claude"},
		},
		runtimeMonitor: &noopRuntimeMonitor{},
		runtimeDetectors: map[string]core.RuntimeStateDetector{
			"codex":  noopRuntimeDetector{},
			"claude": noopRuntimeDetector{},
		},
	}

	result, err := svc.Doctor(context.Background(), "")
	require.NoError(t, err)
	require.Contains(t, result.Failures, "database: mkdir "+blocker+": not a directory")
}

type noopRuntimeMonitor struct{}

func (noopRuntimeMonitor) Snapshot(context.Context, *core.Task) (core.RuntimeSnapshot, error) {
	return core.RuntimeSnapshot{}, nil
}

func (noopRuntimeMonitor) Close() error { return nil }

type noopRuntimeDetector struct{}

func (noopRuntimeDetector) Detect(core.RuntimeSnapshot) core.RuntimeState {
	return core.RuntimeStateNone
}
