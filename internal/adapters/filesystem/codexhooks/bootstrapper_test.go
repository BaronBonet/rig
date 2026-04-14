package codexhooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"rig/internal/core"

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
	require.Contains(t, command, `repo_root=$(git rev-parse --show-toplevel 2>/dev/null)`)
	require.Contains(t, command, `.codex/hooks/forward-to-rig.sh`)
	require.Contains(t, command, `SessionStart`)
	require.NotContains(t, string(rawHooks), `go run ./cmd/rig observer forward-hook`)
	require.NotContains(t, string(rawHooks), `observer forward-hook`)
}

func TestBootstrapperWritesWorktreeLocalForwarderScript(t *testing.T) {
	worktree := t.TempDir()
	bootstrapper := NewBootstrapper("/tmp/agent-bin", "/tmp/agent-src")

	err := bootstrapper.BootstrapTaskWorkspace(t.Context(), &core.Task{
		Provider:     "codex",
		WorktreePath: worktree,
	})
	require.NoError(t, err)

	rawHooks, err := os.ReadFile(filepath.Join(worktree, ".codex", "hooks.json"))
	require.NoError(t, err)
	require.Contains(t, string(rawHooks), `repo_root=$(git rev-parse --show-toplevel 2>/dev/null)`)
	require.Contains(t, string(rawHooks), `.codex/hooks/forward-to-rig.sh`)
	require.NotContains(t, string(rawHooks), `go run ./cmd/rig observer forward-hook`)
	require.NotContains(t, string(rawHooks), `observer forward-hook`)

	scriptPath := filepath.Join(worktree, ".codex", "hooks", "forward-to-rig.sh")
	rawScript, err := os.ReadFile(scriptPath)
	require.NoError(t, err)
	require.Contains(t, string(rawScript), `collector_url=${CODEX_HOOK_COLLECTOR_URL:-http://127.0.0.1:4123/hook}`)
	require.Contains(t, string(rawScript), `curl -fsS --max-time 2 -X POST`)
	require.Contains(t, string(rawScript), `rig observer ingest "$event_name"`)
	require.Contains(t, string(rawScript), `"$agent_exec" observer ingest "$event_name"`)
	require.Contains(t, string(rawScript), `go run ./cmd/rig observer ingest "$event_name"`)
}

func TestBootstrapperWritesClaudeSettingsAlongsideCodexHooks(t *testing.T) {
	worktree := t.TempDir()
	t.Setenv("AGENT_HOOK_LISTEN_ADDR", "127.0.0.1:4555")
	bootstrapper := NewBootstrapper("/tmp/agent-bin", "/tmp/agent-src")

	err := bootstrapper.BootstrapTaskWorkspace(t.Context(), &core.Task{
		Provider:     "codex",
		WorktreePath: worktree,
	})
	require.NoError(t, err)

	rawSettings, err := os.ReadFile(filepath.Join(worktree, ".claude", "settings.local.json"))
	require.NoError(t, err)
	require.Contains(t, string(rawSettings), `http://127.0.0.1:4555/claude-hook`)
}

func TestBootstrapperBootstrapTaskWorkspace_AlsoBootstrapsClaudeTasks(t *testing.T) {
	worktree := t.TempDir()
	t.Setenv("AGENT_HOOK_LISTEN_ADDR", "127.0.0.1:4555")
	bootstrapper := NewBootstrapper("/tmp/agent-bin", "/tmp/agent-src")

	err := bootstrapper.BootstrapTaskWorkspace(t.Context(), &core.Task{
		Provider:     "claude",
		WorktreePath: worktree,
	})
	require.NoError(t, err)

	_, err = os.Stat(filepath.Join(worktree, ".codex", "hooks.json"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(worktree, ".claude", "settings.local.json"))
	require.NoError(t, err)
}
