package codexhooks

import (
	"os"
	"path/filepath"
	"testing"

	"agent/internal/core"

	"github.com/stretchr/testify/require"
)

func TestBootstrapperBootstrapTaskWorkspace_WritesCodexHookAssets(t *testing.T) {
	worktree := t.TempDir()
	bootstrapper := NewBootstrapper("/tmp/state.db", "http://127.0.0.1:4123/hook", "/tmp/agent-bin", "/tmp/agent-src")

	err := bootstrapper.BootstrapTaskWorkspace(t.Context(), &core.Task{
		Provider:     "codex",
		WorktreePath: worktree,
	})
	require.NoError(t, err)

	hooksJSON := filepath.Join(worktree, ".codex", "hooks.json")
	scriptPath := filepath.Join(worktree, ".codex", "hooks", "forward-to-collector.sh")

	rawHooks, err := os.ReadFile(hooksJSON)
	require.NoError(t, err)
	require.Contains(t, string(rawHooks), `"SessionStart"`)
	require.Contains(t, string(rawHooks), `.codex/hooks/forward-to-collector.sh`)

	script, err := os.ReadFile(scriptPath)
	require.NoError(t, err)
	require.Contains(t, string(script), `collector_url='http://127.0.0.1:4123/hook'`)
	require.Contains(t, string(script), `sqlite_path='/tmp/state.db'`)
	require.Contains(t, string(script), `agent_exec='/tmp/agent-bin'`)
	require.Contains(t, string(script), `agent_source_root='/tmp/agent-src'`)
	require.Contains(t, string(script), `agent hook-ingest "$event_name"`)
	require.Contains(t, string(script), `"$agent_exec" hook-ingest "$event_name"`)
	require.Contains(t, string(script), `cd "$agent_source_root"`)

	info, err := os.Stat(scriptPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o755), info.Mode().Perm())
}

func TestBootstrapperBootstrapTaskWorkspace_NoopsForNonCodexTasks(t *testing.T) {
	worktree := t.TempDir()
	bootstrapper := NewBootstrapper("/tmp/state.db", "http://127.0.0.1:4123/hook", "/tmp/agent-bin", "/tmp/agent-src")

	err := bootstrapper.BootstrapTaskWorkspace(t.Context(), &core.Task{
		Provider:     "claude",
		WorktreePath: worktree,
	})
	require.NoError(t, err)

	_, statErr := os.Stat(filepath.Join(worktree, ".codex"))
	require.Error(t, statErr)
	require.True(t, os.IsNotExist(statErr))
}
