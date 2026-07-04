package claude

import (
	"encoding/json"
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
		"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "Notification", "Stop",
	} {
		require.Contains(t, settings.Hooks, event)
	}
	require.Equal(t, "startup|resume", settings.Hooks["SessionStart"][0].Matcher)
	// Tool hooks must fire for every tool, not just Bash, so that non-Bash
	// work (Read, Edit, ...) keeps the task status at working.
	require.Empty(t, settings.Hooks["PreToolUse"][0].Matcher)
	require.Empty(t, settings.Hooks["PostToolUse"][0].Matcher)

	scriptPath := filepath.Join(dataDir, "claude", "hooks", "forward-to-rig.sh")
	require.Contains(t, settings.Hooks["SessionStart"][0].Hooks[0].Command, scriptPath)
	require.Contains(t, settings.Hooks["Stop"][0].Hooks[0].Command, "'Stop'")
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

func TestRepositorySuggestTaskName_ParsesSuggestionJSON(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	runner.EXPECT().Run(
		mock.Anything, "", "claude", "-p", "--output-format", "text", mock.Anything,
	).Return(subprocess.Result{
		Stdout: "thinking...\n{\"name\": \"Billing retry flow\", \"branch_type\": \"fix\"}\n",
	}, nil)
	repo, _ := newTestRepository(t, runner)

	suggestion, err := repo.SuggestTaskName(t.Context(), "fix billing retries")

	require.NoError(t, err)
	require.Equal(t, core.TaskSuggestion{Name: "Billing retry flow", BranchType: "fix"}, suggestion)
}

func TestRepositorySuggestTaskName_FallsBackToLastTextLine(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	runner.EXPECT().Run(
		mock.Anything, "", "claude", "-p", "--output-format", "text", mock.Anything,
	).Return(subprocess.Result{Stdout: "Billing retry flow\n"}, nil)
	repo, _ := newTestRepository(t, runner)

	suggestion, err := repo.SuggestTaskName(t.Context(), "fix billing retries")

	require.NoError(t, err)
	require.Equal(t, core.TaskSuggestion{Name: "Billing retry flow", BranchType: "feat"}, suggestion)
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

	recovered, err := repo.RecoverLatestTaskStatus(t.Context(), core.TaskStatusUpdate{}, nil)
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
			"SessionEnd": [
				{"hooks": [{"type": "command", "command": "/bin/sh '` + scriptPath + `' 'SessionEnd'"}]},
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
	// The user's own SessionEnd hook survives; Rig's stale SessionEnd rule is gone.
	require.Len(t, settings.Hooks["SessionEnd"], 1)
	require.Contains(t, settings.Hooks["SessionEnd"][0].Hooks[0].Command, "my-own-hook")
	// Rig's current catalog events are registered.
	for _, event := range []string{
		"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "Notification", "Stop",
	} {
		require.Contains(t, settings.Hooks, event)
		require.Contains(t, settings.Hooks[event][0].Hooks[0].Command, scriptPath)
	}
}

func TestRepositoryBuildWorkspaceBootstrapSpec_MergeRejectsUnreadableSettings(t *testing.T) {
	repo, _ := newTestRepository(t, subprocess.NewMockRunner(t))

	spec, err := repo.BuildWorkspaceBootstrapSpec(&core.Task{})
	require.NoError(t, err)

	_, err = spec.Files[0].Merge([]byte("{not json"))
	require.ErrorContains(t, err, "decode existing claude workspace settings")
}
