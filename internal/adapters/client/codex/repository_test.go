package codex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"rig/internal/core"
	"rig/internal/pkg/subprocess"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestRepositoryBuildTaskSessionLaunchSpec_StartsCodexAndPrefillsTaskPrompt(t *testing.T) {
	repo := New(subprocess.NewMockRunner(t), Config{Binary: "codex"}, HookForwardingConfig{})

	launch, err := repo.BuildTaskSessionLaunchSpec(&core.Task{Prompt: "add billing retry flow"})
	require.NoError(t, err)
	require.Equal(t, core.TaskSessionLaunchSpec{
		Command:      []string{"codex"},
		ReadyMarker:  "›",
		PrefillInput: []string{"add billing retry flow"},
	}, launch)
}

func TestRepositoryBuildReconnectTaskSessionLaunchSpec_UsesCodexResume(t *testing.T) {
	repo := New(subprocess.NewMockRunner(t), Config{Binary: "codex"}, HookForwardingConfig{})

	launch, err := repo.BuildReconnectTaskSessionLaunchSpec(&core.Task{}, "sess-1")
	require.NoError(t, err)
	require.Equal(t, core.TaskSessionLaunchSpec{
		Command:     []string{"codex", "resume", "sess-1"},
		ReadyMarker: "›",
	}, launch)
}

func TestRepositoryTaskSessionCommandNameUsesConfiguredBinaryBase(t *testing.T) {
	repo := New(subprocess.NewMockRunner(t), Config{Binary: "/opt/homebrew/bin/codex-custom"}, HookForwardingConfig{})

	require.Equal(t, "codex-custom", repo.TaskSessionCommandName())
}

func TestRepositoryBuildWorkspaceBootstrapSpec_ReturnsNoWorkspaceFiles(t *testing.T) {
	repo := New(subprocess.NewMockRunner(t), Config{
		Binary: "codex",
	}, HookForwardingConfig{
		CollectorURL: "http://127.0.0.1:4124/codex-hook",
	})

	spec, err := repo.BuildWorkspaceBootstrapSpec(&core.Task{})
	require.NoError(t, err)
	require.Empty(t, spec.Files)
}

func TestRepositoryEnsureTaskSessionEnvironment_InstallsRigHooksIntoCodexHome(t *testing.T) {
	tempDir := t.TempDir()
	repo := New(subprocess.NewMockRunner(t), Config{Binary: "codex"}, HookForwardingConfig{
		CollectorURL: "http://127.0.0.1:4124/codex-hook",
	}).(*repository)
	repo.codexHomeDir = func() (string, error) { return tempDir, nil }

	err := repo.EnsureTaskSessionEnvironment(t.Context())

	require.NoError(t, err)

	hooksJSON, err := os.ReadFile(filepath.Join(tempDir, "hooks.json"))
	require.NoError(t, err)

	var cfg hookConfig
	require.NoError(t, json.Unmarshal(hooksJSON, &cfg))
	require.Contains(t, cfg.Hooks, "SessionStart")
	require.Contains(t, cfg.Hooks, "UserPromptSubmit")
	require.Contains(t, cfg.Hooks, "Stop")
	require.Contains(t, cfg.Hooks, "PreToolUse")
	require.Contains(t, cfg.Hooks, "PostToolUse")

	scriptPath := filepath.Join(tempDir, "hooks", "forward-to-rig.sh")
	scriptBytes, err := os.ReadFile(scriptPath)
	require.NoError(t, err)
	require.Contains(t, string(scriptBytes), "http://127.0.0.1:4124/codex-hook")
	require.Contains(t, string(hooksJSON), scriptPath)
}

func TestRepositoryEnsureTaskSessionEnvironment_MergesExistingHooks(t *testing.T) {
	tempDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "hooks.json"), []byte(`{
  "hooks": {
    "SessionStart": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "/bin/sh -c 'echo existing >> /tmp/existing-hook'"
          }
        ]
      }
    ]
  }
}`), 0o644))

	repo := New(subprocess.NewMockRunner(t), Config{Binary: "codex"}, HookForwardingConfig{
		CollectorURL: "http://127.0.0.1:4124/codex-hook",
	}).(*repository)
	repo.codexHomeDir = func() (string, error) { return tempDir, nil }

	err := repo.EnsureTaskSessionEnvironment(t.Context())
	require.NoError(t, err)
	err = repo.EnsureTaskSessionEnvironment(t.Context())
	require.NoError(t, err)

	hooksJSON, err := os.ReadFile(filepath.Join(tempDir, "hooks.json"))
	require.NoError(t, err)

	var cfg hookConfig
	require.NoError(t, json.Unmarshal(hooksJSON, &cfg))
	require.Len(t, cfg.Hooks["SessionStart"], 2)
	require.Contains(t, string(hooksJSON), "/tmp/existing-hook")
	require.Len(t, cfg.Hooks["UserPromptSubmit"], 1)
}

func TestRepositorySuggestTaskName_DelegatesToCodexProposal(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	runner.EXPECT().
		Run(
			mock.Anything,
			"",
			"codex",
			"exec",
			"--skip-git-repo-check",
			"--output-last-message",
			mock.Anything,
			mock.Anything,
		).
		RunAndReturn(func(_ context.Context, cwd string, name string, args ...string) (subprocess.Result, error) {
			require.Empty(t, cwd)
			require.Equal(t, "codex", name)
			require.Equal(
				t,
				[]string{"exec", "--skip-git-repo-check", "--output-last-message", args[3], args[4]},
				args,
			)
			return subprocess.Result{Stdout: "billing retry flow\n"}, nil
		})
	repo := New(runner, Config{Binary: "codex"}, HookForwardingConfig{})

	suggestion, err := repo.SuggestTaskName(t.Context(), "add billing retry flow")
	require.NoError(t, err)
	require.Equal(t, "billing retry flow", suggestion.Name)
	require.Equal(t, "feat", suggestion.BranchType)
}

func TestRepositorySuggestTaskName_PrefersOutputFileOverStdout(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	runner.EXPECT().
		Run(
			mock.Anything,
			"",
			"codex",
			"exec",
			"--skip-git-repo-check",
			"--output-last-message",
			mock.Anything,
			mock.Anything,
		).
		RunAndReturn(func(_ context.Context, _ string, _ string, args ...string) (subprocess.Result, error) {
			require.NoError(
				t,
				os.WriteFile(args[3], []byte("{\"name\":\"File Result\",\"branch_type\":\"feat\"}\n"), 0o600),
			)
			return subprocess.Result{Stdout: "stdout result\n"}, nil
		})
	repo := New(runner, Config{Binary: "codex"}, HookForwardingConfig{})

	suggestion, err := repo.SuggestTaskName(t.Context(), "add billing retry flow")
	require.NoError(t, err)
	require.Equal(t, "File Result", suggestion.Name)
	require.Equal(t, "feat", suggestion.BranchType)
}
