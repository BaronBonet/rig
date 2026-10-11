package codex

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/BaronBonet/rig/internal/adapters/client/providerkit"
	"github.com/BaronBonet/rig/internal/core"
	"github.com/BaronBonet/rig/internal/pkg/subprocess"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Rig launches Codex with --no-daemon so its hooks run in the pane's Codex
// process and report that pane's TMUX_PANE, not the shared server's.
func TestRepositoryBuildTaskSessionLaunchSpec_StartsCodexWithoutSharedDaemonAndPrefillsTaskPrompt(t *testing.T) {
	codexHome := t.TempDir()
	repo := New(subprocess.NewMockRunner(t), Config{Binary: "codex"}, HookForwardingConfig{}).(*repository)
	repo.codexHomeDir = func() (string, error) { return codexHome, nil }

	launch, err := repo.BuildTaskSessionLaunchSpec(&core.Task{Prompt: "add billing retry flow"})
	require.NoError(t, err)
	require.Equal(t, core.TaskSessionLaunchSpec{
		Command:      []string{"env", "CODEX_HOME=" + codexHome, "codex", "--no-daemon"},
		ReadyMarker:  "›",
		PrefillInput: []string{"add billing retry flow"},
	}, launch)
}

func TestRepositoryBuildTaskSessionLaunchSpec_StartsCodexWithoutSharedDaemonWithoutPrompt(t *testing.T) {
	codexHome := t.TempDir()
	repo := New(subprocess.NewMockRunner(t), Config{Binary: "codex"}, HookForwardingConfig{}).(*repository)
	repo.codexHomeDir = func() (string, error) { return codexHome, nil }

	// Promptless reconnects launch without a prompt.
	launch, err := repo.BuildTaskSessionLaunchSpec(&core.Task{})
	require.NoError(t, err)
	require.Equal(t, core.TaskSessionLaunchSpec{
		Command:     []string{"env", "CODEX_HOME=" + codexHome, "codex", "--no-daemon"},
		ReadyMarker: "›",
	}, launch)
}

func TestRepositoryBuildReconnectTaskSessionLaunchSpec_ResumesCodexWithoutSharedDaemon(t *testing.T) {
	codexHome := t.TempDir()
	repo := New(subprocess.NewMockRunner(t), Config{Binary: "codex"}, HookForwardingConfig{}).(*repository)
	repo.codexHomeDir = func() (string, error) { return codexHome, nil }

	launch, err := repo.BuildReconnectTaskSessionLaunchSpec(&core.Task{}, "sess-1")
	require.NoError(t, err)
	require.Equal(t, core.TaskSessionLaunchSpec{
		Command:     []string{"env", "CODEX_HOME=" + codexHome, "codex", "resume", "--no-daemon", "sess-1"},
		ReadyMarker: "›",
	}, launch)
}

// A launch flag is passed only for what the task chose, and only to the
// provider the task was launched on.
func TestRepositoryBuildTaskSessionLaunchSpec_StartsCodexOnTheTasksLaunchChoice(t *testing.T) {
	codexHome := t.TempDir()
	repo := New(subprocess.NewMockRunner(t), Config{Binary: "codex"}, HookForwardingConfig{}).(*repository)
	repo.codexHomeDir = func() (string, error) { return codexHome, nil }
	codex := []string{"env", "CODEX_HOME=" + codexHome, "codex", "--no-daemon"}

	for name, test := range map[string]struct {
		task core.Task
		want []string
	}{
		"model and reasoning level": {
			task: core.Task{Provider: core.ProviderCodex, Model: "gpt-6-luna", ReasoningLevel: "low"},
			want: append(slices.Clone(codex), "-m", "gpt-6-luna", "-c", "model_reasoning_effort=low"),
		},
		"model only": {
			task: core.Task{Provider: core.ProviderCodex, Model: "gpt-6-luna"},
			want: append(slices.Clone(codex), "-m", "gpt-6-luna"),
		},
		"reasoning level only": {
			task: core.Task{Provider: core.ProviderCodex, ReasoningLevel: "xhigh"},
			want: append(slices.Clone(codex), "-c", "model_reasoning_effort=xhigh"),
		},
		"no choice": {
			task: core.Task{Provider: core.ProviderCodex},
			want: codex,
		},
		"another launch provider": {
			task: core.Task{Provider: core.ProviderClaude, Model: "opus", ReasoningLevel: "high"},
			want: codex,
		},
	} {
		t.Run(name, func(t *testing.T) {
			launch, err := repo.BuildTaskSessionLaunchSpec(&test.task)
			require.NoError(t, err)
			require.Equal(t, test.want, launch.Command)
		})
	}
}

// rolloutSessionID is the Codex session codex_rollout_model_switch.jsonl
// records: its first turn ran on gpt-6-luna at low, after a /model change its
// second on gpt-6-astra at medium, and its third in plan mode, at plan mode's
// own high.
const rolloutSessionID = "0199c0de-5e55-7000-8000-00000000c0de"

// writeRollout writes content as the rollout of rolloutSessionID into dir of
// the Codex home.
func writeRollout(t *testing.T, codexHome string, dir string, content []byte) {
	t.Helper()
	dir = filepath.Join(codexHome, dir)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	path := filepath.Join(dir, "rollout-2026-10-11T11-55-19-"+rolloutSessionID+".jsonl")
	require.NoError(t, os.WriteFile(path, content, 0o600))
}

func readRolloutFixture(t *testing.T) []byte {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("testdata", "codex_rollout_model_switch.jsonl"))
	require.NoError(t, err)
	return content
}

// rolloutFixtureWithout returns the rollout fixture without the lines drop
// reports.
func rolloutFixtureWithout(t *testing.T, drop func(line string) bool) []byte {
	t.Helper()
	var kept []byte
	for line := range strings.Lines(string(readRolloutFixture(t))) {
		if !drop(line) {
			kept = append(kept, line...)
		}
	}
	return kept
}

func isTurnContextLine(line string) bool {
	return strings.Contains(line, `"type":"turn_context"`)
}

// Codex resumes a session on the model of the user's config, so Reconnect
// passes the model and reasoning level of the session's last turn, whatever
// the task was launched with, and even for a Codex agent session in a task
// launched on another provider. A plan-mode turn runs on plan mode's own
// reasoning level, so the last turn outside plan mode decides.
func TestRepositoryBuildReconnectTaskSessionLaunchSpec_ResumesOnTheLastDefaultModeTurnsModel(t *testing.T) {
	for name, test := range map[string]struct {
		dir  string
		task core.Task
	}{
		"rollout of a codex task": {
			dir:  filepath.Join("sessions", "2026", "10", "11"),
			task: core.Task{Provider: core.ProviderCodex, Model: "gpt-6-luna", ReasoningLevel: "low"},
		},
		"rollout of a claude task": {
			dir:  filepath.Join("sessions", "2026", "10", "11"),
			task: core.Task{Provider: core.ProviderClaude, Model: "opus", ReasoningLevel: "high"},
		},
		"archived rollout": {
			dir:  "archived_sessions",
			task: core.Task{Provider: core.ProviderCodex},
		},
	} {
		t.Run(name, func(t *testing.T) {
			codexHome := t.TempDir()
			repo := New(subprocess.NewMockRunner(t), Config{Binary: "codex"}, HookForwardingConfig{}).(*repository)
			repo.codexHomeDir = func() (string, error) { return codexHome, nil }
			writeRollout(t, codexHome, test.dir, readRolloutFixture(t))

			launch, err := repo.BuildReconnectTaskSessionLaunchSpec(&test.task, rolloutSessionID)

			require.NoError(t, err)
			require.Equal(t, core.TaskSessionLaunchSpec{
				Command: []string{
					"env", "CODEX_HOME=" + codexHome, "codex", "resume", "--no-daemon",
					"-m", "gpt-6-astra", "-c", "model_reasoning_effort=medium", rolloutSessionID,
				},
				ReadyMarker: "›",
			}, launch)
		})
	}
}

// Without a rollout turn to read, a task launched on Codex resumes on its
// launch choice, and a Codex agent session of any other task on the user's
// config.
func TestRepositoryBuildReconnectTaskSessionLaunchSpec_FallsBackToTheTasksLaunchChoice(t *testing.T) {
	for name, writeRolloutOf := range map[string]func(t *testing.T, codexHome string){
		"no rollout": func(*testing.T, string) {},
		"no turn_context": func(t *testing.T, codexHome string) {
			writeRollout(t, codexHome, "sessions", rolloutFixtureWithout(t, isTurnContextLine))
		},
		"only plan-mode turns": func(t *testing.T, codexHome string) {
			writeRollout(t, codexHome, "sessions", rolloutFixtureWithout(t, func(line string) bool {
				return isTurnContextLine(line) && !strings.Contains(line, `"mode":"plan"`)
			}))
		},
		"unreadable rollout": func(t *testing.T, codexHome string) {
			dir := filepath.Join(codexHome, "sessions")
			require.NoError(t, os.MkdirAll(dir, 0o755))
			require.NoError(t, os.Symlink(
				filepath.Join(codexHome, "gone.jsonl"),
				filepath.Join(dir, "rollout-2026-10-11T11-55-19-"+rolloutSessionID+".jsonl"),
			))
		},
	} {
		t.Run(name, func(t *testing.T) {
			codexHome := t.TempDir()
			repo := New(subprocess.NewMockRunner(t), Config{Binary: "codex"}, HookForwardingConfig{}).(*repository)
			repo.codexHomeDir = func() (string, error) { return codexHome, nil }
			writeRolloutOf(t, codexHome)
			resume := []string{"env", "CODEX_HOME=" + codexHome, "codex", "resume", "--no-daemon"}

			launch, err := repo.BuildReconnectTaskSessionLaunchSpec(&core.Task{
				Provider:       core.ProviderCodex,
				Model:          "gpt-6-luna",
				ReasoningLevel: "low",
			}, rolloutSessionID)
			require.NoError(t, err)
			require.Equal(t, append(slices.Clone(resume),
				"-m", "gpt-6-luna", "-c", "model_reasoning_effort=low", rolloutSessionID,
			), launch.Command)

			launch, err = repo.BuildReconnectTaskSessionLaunchSpec(&core.Task{
				Provider:       core.ProviderClaude,
				Model:          "opus",
				ReasoningLevel: "high",
			}, rolloutSessionID)
			require.NoError(t, err)
			require.Equal(t, append(slices.Clone(resume), rolloutSessionID), launch.Command)
		})
	}
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

	err := repo.EnsureTaskSessionEnvironment(t.Context())

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
	require.Contains(t, cfg.Hooks, "SubagentStart")
	require.Len(t, cfg.Hooks["SubagentStart"], 1)
	require.Empty(t, cfg.Hooks["SubagentStart"][0].Matcher)
	// Every source that starts a conversation in the agent, but not compact,
	// which can fire mid-turn.
	require.Equal(t, "startup|resume|clear", cfg.Hooks["SessionStart"][0].Matcher)
	require.Len(t, cfg.Hooks["PermissionRequest"], 1)
	require.Empty(t, cfg.Hooks["PermissionRequest"][0].Matcher)

	scriptPath := filepath.Join(tempDir, "hooks", "forward-to-rig.sh")
	// SessionEnd fires for every reason, with Codex's longest SessionEnd
	// timeout instead of its 1s default. Other hooks keep Codex's default.
	require.Equal(t, []providerkit.HookRule{{
		Hooks: []providerkit.HookCommand{{
			Type:    "command",
			Command: repo.commandForEvent(scriptPath, "SessionEnd"),
			Timeout: 3,
		}},
	}}, cfg.Hooks["SessionEnd"])
	require.Zero(t, cfg.Hooks["Stop"][0].Hooks[0].Timeout)
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

	require.NoError(t, repo.EnsureTaskSessionEnvironment(t.Context()))

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

	require.ErrorContains(t, err, "missing SessionEnd hook")
}

func TestRepositoryDoctorReportsMissingSubagentStartHook(t *testing.T) {
	tempDir := t.TempDir()
	runner := subprocess.NewMockRunner(t)
	runner.EXPECT().Run(mock.Anything, "", "codex", "--version").Return(subprocess.Result{}, nil)
	repo := New(runner, Config{Binary: "codex"}, HookForwardingConfig{
		CollectorURL: "http://127.0.0.1:4124/codex-hook",
	}).(*repository)
	repo.codexHomeDir = func() (string, error) { return tempDir, nil }
	require.NoError(t, repo.EnsureTaskSessionEnvironment(t.Context()))

	hooksPath := filepath.Join(tempDir, "hooks.json")
	hooksJSON, err := os.ReadFile(hooksPath)
	require.NoError(t, err)
	var cfg providerkit.HookConfig
	require.NoError(t, json.Unmarshal(hooksJSON, &cfg))
	delete(cfg.Hooks, "SubagentStart")
	hooksJSON, err = json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(hooksPath, hooksJSON, 0o644))

	err = repo.Doctor(t.Context())

	require.ErrorContains(t, err, "missing SubagentStart hook")
}

// An install by an earlier Rig has no SessionEnd hook until the next refresh
// rewrites Rig's entries.
func TestRepositoryDoctorReportsMissingSessionEndHook(t *testing.T) {
	tempDir := t.TempDir()
	runner := subprocess.NewMockRunner(t)
	runner.EXPECT().Run(mock.Anything, "", "codex", "--version").Return(subprocess.Result{}, nil)
	repo := New(runner, Config{Binary: "codex"}, HookForwardingConfig{
		CollectorURL: "http://127.0.0.1:4124/codex-hook",
	}).(*repository)
	repo.codexHomeDir = func() (string, error) { return tempDir, nil }
	require.NoError(t, repo.EnsureTaskSessionEnvironment(t.Context()))

	hooksPath := filepath.Join(tempDir, "hooks.json")
	hooksJSON, err := os.ReadFile(hooksPath)
	require.NoError(t, err)
	var cfg providerkit.HookConfig
	require.NoError(t, json.Unmarshal(hooksJSON, &cfg))
	delete(cfg.Hooks, "SessionEnd")
	hooksJSON, err = json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(hooksPath, hooksJSON, 0o644))

	require.ErrorContains(t, repo.Doctor(t.Context()), "missing SessionEnd hook")

	require.NoError(t, repo.EnsureTaskSessionEnvironment(t.Context()))
	require.NoError(t, repo.Doctor(t.Context()))
}

func TestRepositoryDoctorReportsStaleHookCollectorURL(t *testing.T) {
	tempDir := t.TempDir()
	repo := New(subprocess.NewMockRunner(t), Config{Binary: "codex"}, HookForwardingConfig{
		CollectorURL: "http://127.0.0.1:4124/codex-hook",
	}).(*repository)
	repo.codexHomeDir = func() (string, error) { return tempDir, nil }
	require.NoError(t, repo.EnsureTaskSessionEnvironment(t.Context()))

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

func TestRepositoryInstallRigSkill_WritesTheSkillToTheUsersAgentsSkills(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Codex reads user skills from ~/.agents/skills, not from Codex home.
	codexHome := filepath.Join(home, "codex-home")
	t.Setenv("CODEX_HOME", codexHome)
	repo := New(subprocess.NewMockRunner(t), Config{Binary: "codex"}, HookForwardingConfig{})

	require.NoError(t, repo.InstallRigSkill(t.Context()))

	installed, err := os.ReadFile(filepath.Join(home, ".agents", "skills", "rig", "SKILL.md"))
	require.NoError(t, err)
	require.Equal(t, providerkit.RigSkill(), installed)
	require.NoDirExists(t, codexHome)
	require.NoError(t, repo.CheckRigSkill(t.Context()))
}

func TestRepositoryInstallRigSkill_ReplacesAnOutdatedSkill(t *testing.T) {
	home := t.TempDir()
	repo := New(subprocess.NewMockRunner(t), Config{Binary: "codex"}, HookForwardingConfig{}).(*repository)
	repo.userHomeDir = func() (string, error) { return home, nil }
	skillPath := filepath.Join(home, ".agents", "skills", "rig", "SKILL.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(skillPath), 0o755))
	require.NoError(t, os.WriteFile(skillPath, []byte("An earlier rig skill.\n"), 0o644))
	require.ErrorContains(t, repo.CheckRigSkill(t.Context()), "run rig setup to update it")

	require.NoError(t, repo.InstallRigSkill(t.Context()))

	require.NoError(t, repo.CheckRigSkill(t.Context()))
}

func TestRepositoryCheckRigSkill_ReportsAMissingSkill(t *testing.T) {
	home := t.TempDir()
	repo := New(subprocess.NewMockRunner(t), Config{Binary: "codex"}, HookForwardingConfig{}).(*repository)
	repo.userHomeDir = func() (string, error) { return home, nil }

	err := repo.CheckRigSkill(t.Context())

	require.ErrorContains(t, err, "not installed at")
	require.ErrorContains(t, err, "run rig setup to install it")
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

	err := repo.EnsureTaskSessionEnvironment(t.Context())
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

// hooks.json from an earlier Rig, whose SessionStart did not match /clear and
// which had no SessionEnd, next to a SessionEnd hook of the user's own.
func TestRepositoryEnsureTaskSessionEnvironment_UpgradesAnEarlierRigRegistration(t *testing.T) {
	tempDir := t.TempDir()
	scriptPath := filepath.Join(tempDir, "hooks", "forward-to-rig.sh")
	repo := New(subprocess.NewMockRunner(t), Config{Binary: "codex"}, HookForwardingConfig{
		CollectorURL: "http://127.0.0.1:4124/codex-hook",
	}).(*repository)
	repo.codexHomeDir = func() (string, error) { return tempDir, nil }
	rigCommand := func(eventName string) string { return repo.commandForEvent(scriptPath, eventName) }
	earlier, err := json.Marshal(providerkit.HookConfig{Hooks: map[string][]providerkit.HookRule{
		"SessionStart": {{
			Matcher: "startup|resume",
			Hooks:   []providerkit.HookCommand{{Type: "command", Command: rigCommand("SessionStart")}},
		}},
		"SessionEnd": {{
			Hooks: []providerkit.HookCommand{{Type: "command", Command: "/usr/local/bin/save-notes", Timeout: 2}},
		}},
	}})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "hooks.json"), earlier, 0o644))

	require.NoError(t, repo.EnsureTaskSessionEnvironment(t.Context()))
	require.NoError(t, repo.EnsureTaskSessionEnvironment(t.Context()))

	hooksJSON, err := os.ReadFile(filepath.Join(tempDir, "hooks.json"))
	require.NoError(t, err)
	var cfg providerkit.HookConfig
	require.NoError(t, json.Unmarshal(hooksJSON, &cfg))
	require.Equal(t, []providerkit.HookRule{{
		Matcher: "startup|resume|clear",
		Hooks:   []providerkit.HookCommand{{Type: "command", Command: rigCommand("SessionStart")}},
	}}, cfg.Hooks["SessionStart"])
	require.Equal(t, []providerkit.HookRule{
		{Hooks: []providerkit.HookCommand{{Type: "command", Command: "/usr/local/bin/save-notes", Timeout: 2}}},
		{Hooks: []providerkit.HookCommand{{Type: "command", Command: rigCommand("SessionEnd"), Timeout: 3}}},
	}, cfg.Hooks["SessionEnd"])
}

func expectCodexSuggestion(runner *subprocess.MockRunner) *subprocess.MockRunner_Run_Call {
	return runner.EXPECT().Run(
		mock.Anything,
		"/repo",
		"codex",
		"exec",
		"--sandbox",
		"read-only",
		"--ephemeral",
		"--skip-git-repo-check",
		"--output-last-message",
		mock.Anything,
		mock.Anything,
	)
}

func TestRepositorySuggestTaskName_DelegatesToCodexProposal(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	expectCodexSuggestion(runner).
		RunAndReturn(func(ctx context.Context, _ string, _ string, args ...string) (subprocess.Result, error) {
			_, hasDeadline := ctx.Deadline()
			require.True(t, hasDeadline, "ticket lookup must be time-boxed")
			require.Contains(t, args[7], "gh issue view")
			require.Contains(t, args[7], "Task description: add billing retry flow")
			return subprocess.Result{Stdout: "{\"name\":\"billing retry flow\",\"branch_type\":\"feat\"}\n"}, nil
		})
	repo := New(runner, Config{Binary: "codex"}, HookForwardingConfig{})

	suggestion, err := repo.SuggestTaskName(t.Context(), "/repo", "add billing retry flow")
	require.NoError(t, err)
	require.Equal(t, "billing retry flow", suggestion.Name)
	require.Equal(t, "feat", suggestion.BranchType)
}

func TestRepositorySuggestTaskName_AcceptsPlainTextTitleOnlyWithoutTools(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	attempts := 0
	expectCodexSuggestion(runner).
		RunAndReturn(func(_ context.Context, _ string, _ string, _ ...string) (subprocess.Result, error) {
			attempts++
			if attempts == 1 {
				return subprocess.Result{Stdout: "I could not find ABC-123, so I named it from the description.\n"}, nil
			}
			return subprocess.Result{Stdout: "billing retry flow\n"}, nil
		}).
		Times(2)
	repo := New(runner, Config{Binary: "codex"}, HookForwardingConfig{})

	suggestion, err := repo.SuggestTaskName(t.Context(), "/repo", "add billing retry flow")
	require.NoError(t, err)
	require.Equal(t, core.TaskSuggestion{Name: "billing retry flow", BranchType: "feat"}, suggestion)
}

func TestRepositorySuggestTaskName_PrefersOutputFileOverStdout(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	expectCodexSuggestion(runner).
		RunAndReturn(func(_ context.Context, _ string, _ string, args ...string) (subprocess.Result, error) {
			require.NoError(
				t,
				os.WriteFile(
					args[6],
					[]byte("{\"name\":\"File Result\",\"branch_type\":\"fix\",\"ticket\":\"GH #18\"}\n"),
					0o600,
				),
			)
			return subprocess.Result{Stdout: "stdout result\n"}, nil
		})
	repo := New(runner, Config{Binary: "codex"}, HookForwardingConfig{})

	suggestion, err := repo.SuggestTaskName(t.Context(), "/repo", "implement issue 18")
	require.NoError(t, err)
	require.Equal(t, core.TaskSuggestion{Name: "File Result", BranchType: "fix", Ticket: "GH #18"}, suggestion)
}

func TestRepositorySuggestTaskName_NamesWithoutToolsWhenTicketLookupFails(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	var prompts []string
	expectCodexSuggestion(runner).
		RunAndReturn(func(_ context.Context, _ string, _ string, args ...string) (subprocess.Result, error) {
			prompts = append(prompts, args[7])
			if len(prompts) == 1 {
				return subprocess.Result{}, errors.New("tracker MCP server timed out")
			}
			return subprocess.Result{
				Stdout: "{\"name\":\"Login retries\",\"branch_type\":\"feat\",\"ticket\":\"ABC-123\"}\n",
			}, nil
		}).
		Times(2)
	repo := New(runner, Config{Binary: "codex"}, HookForwardingConfig{})

	suggestion, err := repo.SuggestTaskName(t.Context(), "/repo", "implement ABC-123: add login retries")
	require.NoError(t, err)
	require.Equal(t, core.TaskSuggestion{Name: "Login retries", BranchType: "feat", Ticket: "ABC-123"}, suggestion)
	require.Len(t, prompts, 2)
	require.Contains(t, prompts[0], "gh issue view")
	require.Contains(t, prompts[1], "Do not use any tools.")
}
