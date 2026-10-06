package codex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BaronBonet/rig/internal/adapters/client/providerkit"
	"github.com/BaronBonet/rig/internal/core"
	"github.com/BaronBonet/rig/internal/pkg/subprocess"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestRepositoryBuildTaskSessionLaunchSpec_StartsCodexAndPrefillsTaskPrompt(t *testing.T) {
	codexHome := t.TempDir()
	repo := New(subprocess.NewMockRunner(t), Config{Binary: "codex"}, HookForwardingConfig{}).(*repository)
	repo.codexHomeDir = func() (string, error) { return codexHome, nil }

	launch, err := repo.BuildTaskSessionLaunchSpec(&core.Task{Prompt: "add billing retry flow"})
	require.NoError(t, err)
	require.Equal(t, core.TaskSessionLaunchSpec{
		Command:      []string{"env", "CODEX_HOME=" + codexHome, "codex"},
		ReadyMarker:  "›",
		PrefillInput: []string{"add billing retry flow"},
	}, launch)
}

func TestRepositoryBuildReconnectTaskSessionLaunchSpec_UsesCodexResume(t *testing.T) {
	codexHome := t.TempDir()
	repo := New(subprocess.NewMockRunner(t), Config{Binary: "codex"}, HookForwardingConfig{}).(*repository)
	repo.codexHomeDir = func() (string, error) { return codexHome, nil }

	launch, err := repo.BuildReconnectTaskSessionLaunchSpec(&core.Task{}, "sess-1")
	require.NoError(t, err)
	require.Equal(t, core.TaskSessionLaunchSpec{
		Command:     []string{"env", "CODEX_HOME=" + codexHome, "codex", "resume", "sess-1"},
		ReadyMarker: "›",
	}, launch)
}

func TestRepositoryLaunchSpecs_RunCodexInTheTasksHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := New(subprocess.NewMockRunner(t), Config{Binary: "codex"}, HookForwardingConfig{}).(*repository)
	repo.codexHomeDir = func() (string, error) { return "/daemon/codex", nil }

	resume, err := repo.BuildReconnectTaskSessionLaunchSpec(&core.Task{
		ProviderEnv: core.ProviderEnv{"CODEX_HOME": "/home/me/.codex-work"},
	}, "sess-1")
	require.NoError(t, err)
	require.Equal(t, []string{"env", "CODEX_HOME=/home/me/.codex-work", "codex", "resume", "sess-1"}, resume.Command)

	start, err := repo.BuildTaskSessionLaunchSpec(&core.Task{ProviderEnv: core.ProviderEnv{"CODEX_HOME": ""}})
	require.NoError(t, err)
	require.Equal(t, []string{"env", "CODEX_HOME=" + filepath.Join(home, ".codex"), "codex"}, start.Command,
		"a window without CODEX_HOME uses ~/.codex, not the daemon's home")
}

func TestRepositoryEnsureTaskSessionEnvironment_RegistersHooksInTheTasksHome(t *testing.T) {
	daemonHome, taskHome := t.TempDir(), t.TempDir()
	repo := New(subprocess.NewMockRunner(t), Config{Binary: "codex"}, HookForwardingConfig{}).(*repository)
	repo.codexHomeDir = func() (string, error) { return daemonHome, nil }

	require.NoError(t, repo.EnsureTaskSessionEnvironment(t.Context(), core.ProviderEnv{"CODEX_HOME": taskHome}))

	require.FileExists(t, filepath.Join(taskHome, "hooks.json"), "codex reads hooks from the home it runs with")
	require.NoFileExists(t, filepath.Join(daemonHome, "hooks.json"))
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
		HookSecret:   "secret-token",
	}).(*repository)
	repo.codexHomeDir = func() (string, error) { return tempDir, nil }

	err := repo.EnsureTaskSessionEnvironment(t.Context(), nil)

	require.NoError(t, err)

	hooksJSON, err := os.ReadFile(filepath.Join(tempDir, "hooks.json"))
	require.NoError(t, err)

	var cfg providerkit.HookConfig
	require.NoError(t, json.Unmarshal(hooksJSON, &cfg))
	require.Contains(t, cfg.Hooks, "SessionStart")
	require.Contains(t, cfg.Hooks, "UserPromptSubmit")
	require.Contains(t, cfg.Hooks, "Stop")
	require.Contains(t, cfg.Hooks, "PreToolUse")
	require.Contains(t, cfg.Hooks, "PermissionRequest")
	require.Contains(t, cfg.Hooks, "PostToolUse")
	require.Len(t, cfg.Hooks["PermissionRequest"], 1)
	require.Empty(t, cfg.Hooks["PermissionRequest"][0].Matcher)

	scriptPath := filepath.Join(tempDir, "hooks", "forward-to-rig.sh")
	scriptBytes, err := os.ReadFile(scriptPath)
	require.NoError(t, err)
	require.Contains(t, string(scriptBytes), "http://127.0.0.1:4124/codex-hook")
	require.Contains(t, string(scriptBytes), `X-Rig-Hook-Secret: $hook_secret`)
	require.Contains(t, string(scriptBytes), "secret-token")
	require.Contains(t, string(hooksJSON), scriptPath)
}

func TestRepositoryDoctorReportsMissingRigHookInstall(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	runner.EXPECT().Run(mock.Anything, "", "codex", "--version").Return(subprocess.Result{}, nil)
	repo := New(runner, Config{Binary: "codex"}, HookForwardingConfig{
		CollectorURL: "http://127.0.0.1:4124/codex-hook",
	}).(*repository)
	repo.codexHomeDir = func() (string, error) { return t.TempDir(), nil }

	err := repo.Doctor(t.Context())

	require.ErrorContains(t, err, "codex rig hook forwarding")
	require.ErrorContains(t, err, "forward-to-rig.sh")
}

func TestRepositoryDoctorAcceptsInstalledRigHooks(t *testing.T) {
	tempDir := t.TempDir()
	runner := subprocess.NewMockRunner(t)
	runner.EXPECT().Run(mock.Anything, "", "codex", "--version").Return(subprocess.Result{}, nil)
	repo := New(runner, Config{Binary: "codex"}, HookForwardingConfig{
		CollectorURL: "http://127.0.0.1:4124/codex-hook",
	}).(*repository)
	repo.codexHomeDir = func() (string, error) { return tempDir, nil }

	require.NoError(t, repo.EnsureTaskSessionEnvironment(t.Context(), nil))

	err := repo.Doctor(t.Context())

	require.NoError(t, err)
}

func TestRepositoryDoctorReportsMissingRigHookEvents(t *testing.T) {
	tempDir := t.TempDir()
	hooksDir := filepath.Join(tempDir, "hooks")
	require.NoError(t, os.MkdirAll(hooksDir, 0o700))
	scriptPath := filepath.Join(hooksDir, "forward-to-rig.sh")
	require.NoError(t, os.WriteFile(scriptPath, []byte("#!/bin/sh\n# http://127.0.0.1:4124/codex-hook\n"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "hooks.json"), []byte(`{
  "hooks": {
    "SessionStart": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "`+strings.ReplaceAll(scriptPath, `\`, `\\`)+` SessionStart"
          }
        ]
      }
    ]
  }
}`), 0o644))
	runner := subprocess.NewMockRunner(t)
	runner.EXPECT().Run(mock.Anything, "", "codex", "--version").Return(subprocess.Result{}, nil)
	repo := New(runner, Config{Binary: "codex"}, HookForwardingConfig{
		CollectorURL: "http://127.0.0.1:4124/codex-hook",
	}).(*repository)
	repo.codexHomeDir = func() (string, error) { return tempDir, nil }

	err := repo.Doctor(t.Context())

	require.ErrorContains(t, err, "missing UserPromptSubmit hook")
}

func TestRepositoryDoctorReportsStaleHookCollectorURL(t *testing.T) {
	tempDir := t.TempDir()
	repo := New(subprocess.NewMockRunner(t), Config{Binary: "codex"}, HookForwardingConfig{
		CollectorURL: "http://127.0.0.1:4124/codex-hook",
	}).(*repository)
	repo.codexHomeDir = func() (string, error) { return tempDir, nil }
	require.NoError(t, repo.EnsureTaskSessionEnvironment(t.Context(), nil))

	runner := subprocess.NewMockRunner(t)
	runner.EXPECT().Run(mock.Anything, "", "codex", "--version").Return(subprocess.Result{}, nil)
	staleRepo := New(runner, Config{Binary: "codex"}, HookForwardingConfig{
		CollectorURL: "http://127.0.0.1:4999/codex-hook",
	}).(*repository)
	staleRepo.codexHomeDir = func() (string, error) { return tempDir, nil }

	err := staleRepo.Doctor(t.Context())

	require.ErrorContains(t, err, "collector URL")
	require.ErrorContains(t, err, "http://127.0.0.1:4999/codex-hook")
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

	err := repo.EnsureTaskSessionEnvironment(t.Context(), nil)
	require.NoError(t, err)
	err = repo.EnsureTaskSessionEnvironment(t.Context(), nil)
	require.NoError(t, err)

	hooksJSON, err := os.ReadFile(filepath.Join(tempDir, "hooks.json"))
	require.NoError(t, err)

	var cfg providerkit.HookConfig
	require.NoError(t, json.Unmarshal(hooksJSON, &cfg))
	require.Len(t, cfg.Hooks["SessionStart"], 2)
	require.Contains(t, string(hooksJSON), "/tmp/existing-hook")
	require.Len(t, cfg.Hooks["UserPromptSubmit"], 1)
}

func TestRepositoryEnsureTaskSessionEnvironment_ReplacesStaleRigHookRules(t *testing.T) {
	tempDir := t.TempDir()
	staleScriptPath := filepath.Join(tempDir, "hooks", "forward-to-rig.sh")
	repo := New(subprocess.NewMockRunner(t), Config{Binary: "codex"}, HookForwardingConfig{
		CollectorURL: "http://127.0.0.1:4124/codex-hook",
	}).(*repository)
	repo.codexHomeDir = func() (string, error) { return tempDir, nil }

	staleRigCommand := strings.ReplaceAll(repo.commandForEvent(staleScriptPath, "PermissionRequest"), `\`, `\\`)
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "hooks.json"), []byte(`{
  "hooks": {
    "PermissionRequest": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "`+staleRigCommand+`"
          }
        ]
      },
      {
        "matcher": "mcp__server__tool",
        "hooks": [
          {
            "type": "command",
            "command": "/bin/sh /tmp/existing-permission-hook"
          }
        ]
      }
    ]
  }
}`), 0o644))

	err := repo.EnsureTaskSessionEnvironment(t.Context(), nil)
	require.NoError(t, err)

	hooksJSON, err := os.ReadFile(filepath.Join(tempDir, "hooks.json"))
	require.NoError(t, err)

	var cfg providerkit.HookConfig
	require.NoError(t, json.Unmarshal(hooksJSON, &cfg))
	require.Len(t, cfg.Hooks["PermissionRequest"], 2)
	require.Equal(t, "mcp__server__tool", cfg.Hooks["PermissionRequest"][0].Matcher)
	require.Empty(t, cfg.Hooks["PermissionRequest"][1].Matcher)
	require.Contains(t, cfg.Hooks["PermissionRequest"][1].Hooks[0].Command, staleScriptPath)
}

func TestRepositorySuggestTaskName_DelegatesToCodexProposal(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	runner.EXPECT().RunWithStdin(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, opts subprocess.RunWithStdinOptions) (subprocess.Result, error) {
			require.Empty(t, opts.Cwd)
			require.Equal(t, "codex", opts.Name)
			require.Equal(
				t,
				[]string{"exec", "--skip-git-repo-check", "--output-last-message", opts.Args[3], opts.Args[4]},
				opts.Args,
			)
			require.Equal(t, map[string]string{"CODEX_HOME": "/home/me/.codex-work"}, opts.Env,
				"the suggestion runs as the task's account")
			return subprocess.Result{Stdout: "billing retry flow\n"}, nil
		})
	repo := New(runner, Config{Binary: "codex"}, HookForwardingConfig{})

	suggestion, err := repo.SuggestTaskName(t.Context(), "add billing retry flow",
		core.ProviderEnv{"CODEX_HOME": "/home/me/.codex-work", "CLAUDE_CONFIG_DIR": ""})
	require.NoError(t, err)
	require.Equal(t, "billing retry flow", suggestion.Name)
	require.Equal(t, "feat", suggestion.BranchType)
}

func TestRepositorySuggestTaskName_PrefersOutputFileOverStdout(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	runner.EXPECT().RunWithStdin(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, opts subprocess.RunWithStdinOptions) (subprocess.Result, error) {
			require.NoError(
				t,
				os.WriteFile(opts.Args[3], []byte("{\"name\":\"File Result\",\"branch_type\":\"feat\"}\n"), 0o600),
			)
			return subprocess.Result{Stdout: "stdout result\n"}, nil
		})
	repo := New(runner, Config{Binary: "codex"}, HookForwardingConfig{})

	suggestion, err := repo.SuggestTaskName(t.Context(), "add billing retry flow", nil)
	require.NoError(t, err)
	require.Equal(t, "File Result", suggestion.Name)
	require.Equal(t, "feat", suggestion.BranchType)
}
