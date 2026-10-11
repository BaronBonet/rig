package claude

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BaronBonet/rig/internal/adapters/client/providerkit"
	"github.com/BaronBonet/rig/internal/core"
	"github.com/BaronBonet/rig/internal/pkg/subprocess"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newTestRepository(t *testing.T, runner subprocess.Runner) (*repository, string) {
	t.Helper()
	dataDir := t.TempDir()
	repo := New(runner, Config{Binary: "claude"}, HookForwardingConfig{
		CollectorURL: "http://127.0.0.1:4124/claude-hook",
		HookSecret:   "secret-token",
	}).(*repository)
	repo.rigDataDir = func() (string, error) { return dataDir, nil }
	configDir := t.TempDir()
	repo.claudeConfigDir = func() (string, error) { return configDir, nil }
	return repo, dataDir
}

func TestRepositoryBuildTaskSessionLaunchSpec_StartsClaudeAndPrefillsTaskPrompt(t *testing.T) {
	repo, _ := newTestRepository(t, subprocess.NewMockRunner(t))

	launch, err := repo.BuildTaskSessionLaunchSpec(&core.Task{Prompt: "add billing retry flow"})
	require.NoError(t, err)
	require.Equal(t, core.TaskSessionLaunchSpec{
		Command:      []string{"claude"},
		ReadyMarker:  "❯",
		PrefillInput: []string{"add billing retry flow"},
	}, launch)
}

func TestRepositoryBuildTaskSessionLaunchSpec_OmitsPrefillForEmptyPrompt(t *testing.T) {
	repo, _ := newTestRepository(t, subprocess.NewMockRunner(t))

	launch, err := repo.BuildTaskSessionLaunchSpec(&core.Task{})
	require.NoError(t, err)
	require.Equal(t, []string{"claude"}, launch.Command)
	require.Empty(t, launch.PrefillInput)
}

func TestRepositoryBuildReconnectTaskSessionLaunchSpec_ResumesByRecordedSessionID(t *testing.T) {
	repo, _ := newTestRepository(t, subprocess.NewMockRunner(t))

	launch, err := repo.BuildReconnectTaskSessionLaunchSpec(&core.Task{}, "sess-1")
	require.NoError(t, err)
	require.Equal(t, core.TaskSessionLaunchSpec{
		Command:     []string{"claude", "--resume", "sess-1"},
		ReadyMarker: "❯",
	}, launch)
}

// A launch flag is passed only for what the task chose, and only to the
// provider the task was launched on.
func TestRepositoryBuildTaskSessionLaunchSpec_StartsClaudeOnTheTasksLaunchChoice(t *testing.T) {
	repo, _ := newTestRepository(t, subprocess.NewMockRunner(t))

	for name, test := range map[string]struct {
		task core.Task
		want []string
	}{
		"model and reasoning level": {
			task: core.Task{Provider: core.ProviderClaude, Model: "opus[1m]", ReasoningLevel: "high"},
			want: []string{"claude", "--model", "opus[1m]", "--effort", "high"},
		},
		"model only": {
			task: core.Task{Provider: core.ProviderClaude, Model: "sonnet"},
			want: []string{"claude", "--model", "sonnet"},
		},
		"reasoning level only": {
			task: core.Task{Provider: core.ProviderClaude, ReasoningLevel: "low"},
			want: []string{"claude", "--effort", "low"},
		},
		"no choice": {
			task: core.Task{Provider: core.ProviderClaude},
			want: []string{"claude"},
		},
		"another launch provider": {
			task: core.Task{Provider: core.ProviderCodex, Model: "gpt-6-luna", ReasoningLevel: "high"},
			want: []string{"claude"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			launch, err := repo.BuildTaskSessionLaunchSpec(&test.task)
			require.NoError(t, err)
			require.Equal(t, test.want, launch.Command)
		})
	}
}

// Claude resumes a session on its own model, so Reconnect never passes one;
// it passes only the reasoning level Claude would otherwise lose.
func TestRepositoryBuildReconnectTaskSessionLaunchSpec_PassesOnlyTheLaunchReasoningLevel(t *testing.T) {
	repo, _ := newTestRepository(t, subprocess.NewMockRunner(t))

	for name, test := range map[string]struct {
		task core.Task
		want []string
	}{
		"model and reasoning level": {
			task: core.Task{Provider: core.ProviderClaude, Model: "opus", ReasoningLevel: "xhigh"},
			want: []string{"claude", "--resume", "sess-1", "--effort", "xhigh"},
		},
		"model only": {
			task: core.Task{Provider: core.ProviderClaude, Model: "opus"},
			want: []string{"claude", "--resume", "sess-1"},
		},
		"another launch provider": {
			task: core.Task{Provider: core.ProviderCodex, Model: "gpt-6-luna", ReasoningLevel: "high"},
			want: []string{"claude", "--resume", "sess-1"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			launch, err := repo.BuildReconnectTaskSessionLaunchSpec(&test.task, "sess-1")
			require.NoError(t, err)
			require.Equal(t, test.want, launch.Command)
			require.NotContains(t, launch.Command, "--model")
		})
	}
}

func TestRepositoryBuildReconnectTaskSessionLaunchSpec_RequiresSessionID(t *testing.T) {
	repo, _ := newTestRepository(t, subprocess.NewMockRunner(t))

	_, err := repo.BuildReconnectTaskSessionLaunchSpec(&core.Task{}, "  ")
	require.ErrorContains(t, err, "session ID is required")
}

func TestRepositoryTaskSessionCommandNameUsesConfiguredBinaryBase(t *testing.T) {
	repo := New(
		subprocess.NewMockRunner(t),
		Config{Binary: "/opt/homebrew/bin/claude-custom"},
		HookForwardingConfig{},
	)

	require.Equal(t, "claude-custom", repo.TaskSessionCommandName())
}

func TestRepositoryEnsureTaskSessionEnvironment_InstallsSharedForwarderScriptOnly(t *testing.T) {
	repo, dataDir := newTestRepository(t, subprocess.NewMockRunner(t))
	// A sentinel user-level Claude settings file must never be modified.
	userSettings := filepath.Join(dataDir, "claude-user-settings.json")
	require.NoError(t, os.WriteFile(userSettings, []byte(`{"user":"untouched"}`), 0o600))

	err := repo.EnsureTaskSessionEnvironment(t.Context())

	require.NoError(t, err)
	scriptPath := filepath.Join(dataDir, "claude", "hooks", "forward-to-rig.sh")
	scriptBytes, err := os.ReadFile(scriptPath)
	require.NoError(t, err)
	require.Contains(t, string(scriptBytes), "http://127.0.0.1:4124/claude-hook")
	require.Contains(t, string(scriptBytes), `X-Claude-Hook-Event: $event_name`)
	require.Contains(t, string(scriptBytes), "secret-token")

	info, err := os.Stat(scriptPath)
	require.NoError(t, err)
	require.NotZero(t, info.Mode().Perm()&0o111)

	untouched, err := os.ReadFile(userSettings)
	require.NoError(t, err)
	require.JSONEq(t, `{"user":"untouched"}`, string(untouched))
}

func TestRepositoryBuildWorkspaceBootstrapSpec_EmitsWorkspaceScopedHookSettings(t *testing.T) {
	repo, dataDir := newTestRepository(t, subprocess.NewMockRunner(t))

	spec, err := repo.BuildWorkspaceBootstrapSpec(&core.Task{})

	require.NoError(t, err)
	require.Len(t, spec.Files, 1)
	file := spec.Files[0]
	require.Equal(t, ".claude/settings.local.json", file.Path)
	require.Equal(t, os.FileMode(0o600), file.FileMode)

	var settings providerkit.HookConfig
	require.NoError(t, json.Unmarshal(file.Content, &settings))
	for _, event := range []string{
		"SessionStart", "SessionEnd", "UserPromptSubmit", "PreToolUse", "PostToolUse", "Notification", "Stop",
		"StopFailure",
	} {
		require.Contains(t, settings.Hooks, event)
	}
	// Every source that starts a conversation in the agent, but not compact,
	// which can fire mid-turn.
	require.Equal(t, "startup|resume|clear|fork", settings.Hooks["SessionStart"][0].Matcher)
	// Tool hooks must fire for every tool, not just Bash, so that non-Bash
	// work (Read, Edit, ...) keeps the task status at working.
	require.Empty(t, settings.Hooks["PreToolUse"][0].Matcher)
	require.Empty(t, settings.Hooks["PostToolUse"][0].Matcher)
	require.Equal(
		t,
		"permission_prompt|worker_permission_prompt|elicitation_dialog|elicitation_url_dialog|agent_needs_input",
		settings.Hooks["Notification"][0].Matcher,
	)
	// Every API error type ends the turn, so StopFailure is unmatched.
	require.Empty(t, settings.Hooks["StopFailure"][0].Matcher)

	scriptPath := filepath.Join(dataDir, "claude", "hooks", "forward-to-rig.sh")
	require.Contains(t, settings.Hooks["SessionStart"][0].Hooks[0].Command, scriptPath)
	require.Contains(t, settings.Hooks["Stop"][0].Hooks[0].Command, "'Stop'")
	require.Contains(t, settings.Hooks["StopFailure"][0].Hooks[0].Command, "'StopFailure'")
	// SessionEnd fires for every reason, with a timeout above Claude's 1.5s
	// SessionEnd budget. Other hooks keep Claude's default timeout.
	require.Equal(t, []providerkit.HookRule{{
		Hooks: []providerkit.HookCommand{{
			Type:    "command",
			Command: "/bin/sh '" + scriptPath + "' 'SessionEnd'",
			Timeout: 3,
		}},
	}}, settings.Hooks["SessionEnd"])
	require.Zero(t, settings.Hooks["Stop"][0].Hooks[0].Timeout)
}

func TestRepositoryDoctorReportsMissingForwarderScript(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	runner.EXPECT().Run(mock.Anything, "", "claude", "--version").Return(subprocess.Result{}, nil)
	repo, _ := newTestRepository(t, runner)

	err := repo.Doctor(t.Context())

	require.ErrorContains(t, err, "claude rig hook forwarding")
	require.ErrorContains(t, err, "forward-to-rig.sh")
}

func TestRepositoryDoctorAcceptsInstalledForwarderScript(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	runner.EXPECT().Run(mock.Anything, "", "claude", "--version").Return(subprocess.Result{}, nil)
	repo, _ := newTestRepository(t, runner)
	require.NoError(t, repo.EnsureTaskSessionEnvironment(t.Context()))

	require.NoError(t, repo.Doctor(t.Context()))
}

func TestRepositoryInstallRigSkill_WritesTheSkillToTheUsersClaudeSkills(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	repo := New(subprocess.NewMockRunner(t), Config{Binary: "claude"}, HookForwardingConfig{})

	require.NoError(t, repo.InstallRigSkill(t.Context()))

	installed, err := os.ReadFile(filepath.Join(home, ".claude", "skills", "rig", "SKILL.md"))
	require.NoError(t, err)
	require.Equal(t, providerkit.RigSkill(), installed)
	require.NoError(t, repo.CheckRigSkill(t.Context()))
}

func TestRepositoryInstallRigSkill_FollowsClaudeConfigDir(t *testing.T) {
	configDir := filepath.Join(t.TempDir(), "claude-config")
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	repo := New(subprocess.NewMockRunner(t), Config{Binary: "claude"}, HookForwardingConfig{})

	require.NoError(t, repo.InstallRigSkill(t.Context()))

	installed, err := os.ReadFile(filepath.Join(configDir, "skills", "rig", "SKILL.md"))
	require.NoError(t, err)
	require.Equal(t, providerkit.RigSkill(), installed)
}

func TestRepositoryInstallRigSkill_RejectsAClaudeConfigDirThatIsNotAbsolute(t *testing.T) {
	for _, configDir := range []string{"claude-config", "~/.claude-work"} {
		t.Run(configDir, func(t *testing.T) {
			workingDir := t.TempDir()
			t.Chdir(workingDir)
			t.Setenv("HOME", t.TempDir())
			t.Setenv("CLAUDE_CONFIG_DIR", configDir)
			repo := New(subprocess.NewMockRunner(t), Config{Binary: "claude"}, HookForwardingConfig{})

			err := repo.InstallRigSkill(t.Context())

			require.ErrorContains(t, err, "CLAUDE_CONFIG_DIR must be an absolute path")
			require.ErrorContains(t, repo.CheckRigSkill(t.Context()), "CLAUDE_CONFIG_DIR must be an absolute path")
			// Nothing is written relative to the working directory.
			entries, err := os.ReadDir(workingDir)
			require.NoError(t, err)
			require.Empty(t, entries)
		})
	}
}

func TestRepositoryInstallRigSkill_ReplacesAnOutdatedSkill(t *testing.T) {
	repo, _ := newTestRepository(t, subprocess.NewMockRunner(t))
	configDir, err := repo.claudeConfigDir()
	require.NoError(t, err)
	skillPath := filepath.Join(configDir, "skills", "rig", "SKILL.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(skillPath), 0o755))
	require.NoError(t, os.WriteFile(skillPath, []byte("An earlier rig skill.\n"), 0o644))
	require.ErrorContains(t, repo.CheckRigSkill(t.Context()), "run rig setup to update it")

	require.NoError(t, repo.InstallRigSkill(t.Context()))

	require.NoError(t, repo.CheckRigSkill(t.Context()))
}

func TestRepositoryCheckRigSkill_ReportsAMissingSkill(t *testing.T) {
	repo, _ := newTestRepository(t, subprocess.NewMockRunner(t))

	err := repo.CheckRigSkill(t.Context())

	require.ErrorContains(t, err, "not installed at")
	require.ErrorContains(t, err, "run rig setup to install it")
}

func TestRepositorySuggestTaskName_LooksUpTicketsInAutoModeFromRepo(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	runner.EXPECT().Run(
		mock.Anything, "/repo", "claude",
		"-p", "--output-format", "text", "--no-session-persistence",
		"--disallowedTools", "Edit,Write,NotebookEdit", "--permission-mode", "auto", mock.Anything,
	).RunAndReturn(func(ctx context.Context, _ string, _ string, args ...string) (subprocess.Result, error) {
		_, hasDeadline := ctx.Deadline()
		require.True(t, hasDeadline, "ticket lookup must be time-boxed")
		require.Contains(t, args[len(args)-1], "gh issue view")
		require.Contains(t, args[len(args)-1], "Task description: implement issue 18")
		return subprocess.Result{
			Stdout: "Looked up GH #18.\n{\"branch_type\": \"fix\", \"ticket\": \"GH #18\", \"name\": \"Fix the broken thing\"}\n",
		}, nil
	})
	repo, _ := newTestRepository(t, runner)

	suggestion, err := repo.SuggestTaskName(t.Context(), "/repo", "implement issue 18")

	require.NoError(t, err)
	require.Equal(t, core.TaskSuggestion{Name: "Fix the broken thing", BranchType: "fix", Ticket: "GH #18"}, suggestion)
}

func TestRepositorySuggestTaskName_AcceptsPlainTextTitleOnlyWithoutTools(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	runner.EXPECT().Run(
		mock.Anything, "/repo", "claude",
		"-p", "--output-format", "text", "--no-session-persistence",
		"--disallowedTools", "Edit,Write,NotebookEdit", "--permission-mode", "auto", mock.Anything,
	).Return(subprocess.Result{Stdout: "I could not find ABC-123.\nSo this name comes from the description alone.\n"}, nil)
	runner.EXPECT().Run(
		mock.Anything, "/repo", "claude", "-p", "--output-format", "text", "--no-session-persistence", mock.Anything,
	).Return(subprocess.Result{Stdout: "Billing retry flow\n"}, nil)
	repo, _ := newTestRepository(t, runner)

	suggestion, err := repo.SuggestTaskName(t.Context(), "/repo", "fix billing retries")

	require.NoError(t, err)
	require.Equal(t, core.TaskSuggestion{Name: "Billing retry flow", BranchType: "feat"}, suggestion)
}

func TestRepositorySuggestTaskName_NamesWithoutToolsWhenTicketLookupFails(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	runner.EXPECT().Run(
		mock.Anything, "/repo", "claude",
		"-p", "--output-format", "text", "--no-session-persistence",
		"--disallowedTools", "Edit,Write,NotebookEdit", "--permission-mode", "auto", mock.Anything,
	).Return(subprocess.Result{}, errors.New("auto mode unavailable"))
	runner.EXPECT().Run(
		mock.Anything, "/repo", "claude", "-p", "--output-format", "text", "--no-session-persistence", mock.Anything,
	).RunAndReturn(func(_ context.Context, _ string, _ string, args ...string) (subprocess.Result, error) {
		require.Contains(t, args[len(args)-1], "Do not use any tools.")
		return subprocess.Result{Stdout: "{\"branch_type\": \"feat\", \"ticket\": \"ABC-123\", \"name\": \"\"}\n"}, nil
	})
	repo, _ := newTestRepository(t, runner)

	suggestion, err := repo.SuggestTaskName(t.Context(), "/repo", "implement ABC-123")

	require.NoError(t, err)
	require.Equal(t, core.TaskSuggestion{BranchType: "feat", Ticket: "ABC-123"}, suggestion)
}

func TestRepositoryActivityAndTokenUsageDegradeGracefully(t *testing.T) {
	repo, _ := newTestRepository(t, subprocess.NewMockRunner(t))

	activity, err := repo.ReadSessionActivity(t.Context(), core.TaskProviderSession{
		TranscriptPath: "/tmp/does-not-exist.jsonl",
	}, time.Time{})
	require.NoError(t, err)
	require.Empty(t, activity)

	usage, err := repo.ReadSessionTokenUsage(t.Context(), "/tmp/does-not-exist.jsonl")
	require.NoError(t, err)
	require.Nil(t, usage)

	recovered, err := repo.RecoverAgentSessionStatus(t.Context(), core.AgentSessionStatus{}, nil, time.Time{})
	require.NoError(t, err)
	require.Nil(t, recovered)
}

func TestRepositoryBuildWorkspaceBootstrapSpec_MergePreservesUserSettings(t *testing.T) {
	repo, dataDir := newTestRepository(t, subprocess.NewMockRunner(t))
	scriptPath := filepath.Join(dataDir, "claude", "hooks", "forward-to-rig.sh")

	spec, err := repo.BuildWorkspaceBootstrapSpec(&core.Task{})
	require.NoError(t, err)
	require.NotNil(t, spec.Files[0].Merge)

	// A workspace where Claude Code stored permissions and a stale Rig hook
	// registration under a different event set.
	existing := `{
		"permissions": {"allow": ["Bash(go test *)"]},
		"hooks": {
			"PreCompact": [
				{"hooks": [{"type": "command", "command": "/bin/sh '` + scriptPath + `' 'PreCompact'"}]},
				{"hooks": [{"type": "command", "command": "/usr/local/bin/my-own-hook"}]}
			]
		}
	}`

	merged, err := spec.Files[0].Merge([]byte(existing))
	require.NoError(t, err)

	var settings struct {
		Permissions map[string]any                    `json:"permissions"`
		Hooks       map[string][]providerkit.HookRule `json:"hooks"`
	}
	require.NoError(t, json.Unmarshal(merged, &settings))

	// User content survives.
	require.Contains(t, settings.Permissions, "allow")
	// The user's own PreCompact hook survives; Rig's stale PreCompact rule is gone.
	require.Len(t, settings.Hooks["PreCompact"], 1)
	require.Contains(t, settings.Hooks["PreCompact"][0].Hooks[0].Command, "my-own-hook")
	// Rig's current catalog events are registered.
	for _, event := range []string{
		"SessionStart", "SessionEnd", "UserPromptSubmit", "PreToolUse", "PostToolUse", "Notification", "Stop",
		"StopFailure",
	} {
		require.Contains(t, settings.Hooks, event)
		require.Contains(t, settings.Hooks[event][0].Hooks[0].Command, scriptPath)
	}
}

// A task workspace bootstrapped by an earlier Rig, whose SessionStart did not
// match /clear and which had no SessionEnd, next to a SessionEnd hook of the
// user's own.
func TestRepositoryBuildWorkspaceBootstrapSpec_MergeUpgradesAnEarlierRigRegistration(t *testing.T) {
	repo, dataDir := newTestRepository(t, subprocess.NewMockRunner(t))
	scriptPath := filepath.Join(dataDir, "claude", "hooks", "forward-to-rig.sh")
	rigCommand := func(eventName string) string { return "/bin/sh '" + scriptPath + "' '" + eventName + "'" }
	existing := `{
		"hooks": {
			"SessionStart": [
				{"matcher": "startup|resume", "hooks": [{"type": "command", "command": "` + rigCommand("SessionStart") + `"}]}
			],
			"SessionEnd": [
				{"hooks": [{"type": "command", "command": "/usr/local/bin/save-notes", "timeout": 1.5}]}
			]
		}
	}`

	spec, err := repo.BuildWorkspaceBootstrapSpec(&core.Task{})
	require.NoError(t, err)
	merged, err := spec.Files[0].Merge([]byte(existing))
	require.NoError(t, err)
	merged, err = spec.Files[0].Merge(merged)
	require.NoError(t, err)

	var settings providerkit.HookConfig
	require.NoError(t, json.Unmarshal(merged, &settings))
	require.Equal(t, []providerkit.HookRule{{
		Matcher: "startup|resume|clear|fork",
		Hooks:   []providerkit.HookCommand{{Type: "command", Command: rigCommand("SessionStart")}},
	}}, settings.Hooks["SessionStart"])
	require.Equal(t, []providerkit.HookRule{
		{Hooks: []providerkit.HookCommand{{Type: "command", Command: "/usr/local/bin/save-notes", Timeout: 1.5}}},
		{Hooks: []providerkit.HookCommand{{Type: "command", Command: rigCommand("SessionEnd"), Timeout: 3}}},
	}, settings.Hooks["SessionEnd"])
}

func TestRepositoryBuildWorkspaceBootstrapSpec_MergeRejectsUnreadableSettings(t *testing.T) {
	repo, _ := newTestRepository(t, subprocess.NewMockRunner(t))

	spec, err := repo.BuildWorkspaceBootstrapSpec(&core.Task{})
	require.NoError(t, err)

	_, err = spec.Files[0].Merge([]byte("{not json"))
	require.ErrorContains(t, err, "decode existing claude workspace settings")
}
