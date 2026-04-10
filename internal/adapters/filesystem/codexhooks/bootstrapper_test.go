package codexhooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"agent/internal/core"

	"github.com/stretchr/testify/require"
)

func TestBootstrapperWritesHooksJSONForObserverForwardHook(t *testing.T) {
	worktree := t.TempDir()
	bootstrapper := NewBootstrapper("/tmp/agent-bin", "/tmp/agent-src")

	err := bootstrapper.BootstrapTaskWorkspace(t.Context(), &core.Task{
		Provider:     "codex",
		WorktreePath: worktree,
	})
	require.NoError(t, err)

	hooksJSON := filepath.Join(worktree, ".codex", "hooks.json")
	rawHooks, err := os.ReadFile(hooksJSON)
	require.NoError(t, err)

	var hooksConfig hookConfig
	require.NoError(t, json.Unmarshal(rawHooks, &hooksConfig))
	require.Contains(t, hooksConfig.Hooks, "SessionStart")
	require.Contains(t, hooksConfig.Hooks, "UserPromptSubmit")
	require.Contains(t, hooksConfig.Hooks, "Stop")
	require.Len(t, hooksConfig.Hooks["SessionStart"], 1)
	command := hooksConfig.Hooks["SessionStart"][0].Hooks[0].Command
	require.Contains(t, command, `agent observer forward-hook`)
	require.Contains(t, command, `'/tmp/agent-bin'`)
	require.Contains(t, command, `go run ./cmd/agent observer forward-hook`)
	require.Contains(t, command, `SessionStart`)
	require.NotContains(t, string(rawHooks), `forward-to-collector.sh`)

	_, statErr := os.Stat(filepath.Join(worktree, ".codex", "hooks", "forward-to-collector.sh"))
	require.Error(t, statErr)
	require.True(t, os.IsNotExist(statErr))
}

func TestBootstrapperBootstrapTaskWorkspace_NoopsForNonCodexTasks(t *testing.T) {
	worktree := t.TempDir()
	bootstrapper := NewBootstrapper("/tmp/agent-bin", "/tmp/agent-src")

	err := bootstrapper.BootstrapTaskWorkspace(t.Context(), &core.Task{
		Provider:     "claude",
		WorktreePath: worktree,
	})
	require.NoError(t, err)

	_, statErr := os.Stat(filepath.Join(worktree, ".codex"))
	require.Error(t, statErr)
	require.True(t, os.IsNotExist(statErr))
}
