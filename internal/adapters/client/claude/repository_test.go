package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/BaronBonet/rig/internal/core"
	"github.com/BaronBonet/rig/internal/pkg/subprocess"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestRepositoryBuildTaskSessionLaunchSpec_StartsClaudeAndPrefillsTaskPrompt(t *testing.T) {
	repo := New(subprocess.NewMockRunner(t), Config{Binary: "claude"}, HookForwardingConfig{})

	launch, err := repo.BuildTaskSessionLaunchSpec(&core.Task{Prompt: "add billing retry flow"})
	require.NoError(t, err)
	require.Equal(t, core.TaskSessionLaunchSpec{
		Command:      []string{"claude"},
		ReadyMarker:  ">",
		PrefillInput: []string{"add billing retry flow"},
	}, launch)
}

func TestRepositoryBuildReconnectTaskSessionLaunchSpec_UsesClaudeResume(t *testing.T) {
	repo := New(subprocess.NewMockRunner(t), Config{Binary: "claude"}, HookForwardingConfig{})

	launch, err := repo.BuildReconnectTaskSessionLaunchSpec(&core.Task{}, "sess-1")
	require.NoError(t, err)
	require.Equal(t, core.TaskSessionLaunchSpec{
		Command:     []string{"claude", "--resume", "sess-1"},
		ReadyMarker: ">",
	}, launch)
}

func TestRepositoryTaskSessionCommandNameUsesConfiguredBinaryBase(t *testing.T) {
	repo := New(subprocess.NewMockRunner(t), Config{Binary: "/opt/homebrew/bin/claude-custom"}, HookForwardingConfig{})

	require.Equal(t, "claude-custom", repo.TaskSessionCommandName())
}

func TestRepositoryEnsureTaskSessionEnvironment_InstallsRigHooksIntoClaudeHome(t *testing.T) {
	tempDir := t.TempDir()
	repo := New(subprocess.NewMockRunner(t), Config{Binary: "claude"}, HookForwardingConfig{
		CollectorURL: "http://127.0.0.1:4124/claude-hook",
		HookSecret:   "secret-token",
	}).(*repository)
	repo.claudeHomeDir = func() (string, error) { return tempDir, nil }

	err := repo.EnsureTaskSessionEnvironment(t.Context())

	require.NoError(t, err)

	settingsJSON, err := os.ReadFile(filepath.Join(tempDir, "settings.json"))
	require.NoError(t, err)

	var cfg hookConfig
	require.NoError(t, json.Unmarshal(settingsJSON, &cfg))
	require.Contains(t, cfg.Hooks, "SessionStart")
	require.Contains(t, cfg.Hooks, "UserPromptSubmit")
	require.Contains(t, cfg.Hooks, "Stop")
	require.Contains(t, cfg.Hooks, "PreToolUse")
	require.Contains(t, cfg.Hooks, "PostToolUse")

	scriptPath := filepath.Join(tempDir, "hooks", "forward-to-rig.sh")
	scriptBytes, err := os.ReadFile(scriptPath)
	require.NoError(t, err)
	require.Contains(t, string(scriptBytes), "http://127.0.0.1:4124/claude-hook")
	require.Contains(t, string(scriptBytes), "secret-token")
	require.Contains(t, string(settingsJSON), scriptPath)
}

func TestRepositoryDoctorReportsMissingRigHookInstall(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	runner.EXPECT().Run(mock.Anything, "", "claude", "--version").Return(subprocess.Result{}, nil)
	repo := New(runner, Config{Binary: "claude"}, HookForwardingConfig{
		CollectorURL: "http://127.0.0.1:4124/claude-hook",
	}).(*repository)
	repo.claudeHomeDir = func() (string, error) { return t.TempDir(), nil }

	err := repo.Doctor(t.Context())

	require.ErrorContains(t, err, "claude rig hook forwarding")
	require.ErrorContains(t, err, "forward-to-rig.sh")
}

func TestRepositoryDoctorAcceptsInstalledRigHooks(t *testing.T) {
	tempDir := t.TempDir()
	runner := subprocess.NewMockRunner(t)
	runner.EXPECT().Run(mock.Anything, "", "claude", "--version").Return(subprocess.Result{}, nil)
	repo := New(runner, Config{Binary: "claude"}, HookForwardingConfig{
		CollectorURL: "http://127.0.0.1:4124/claude-hook",
	}).(*repository)
	repo.claudeHomeDir = func() (string, error) { return tempDir, nil }

	require.NoError(t, repo.EnsureTaskSessionEnvironment(t.Context()))

	err := repo.Doctor(t.Context())

	require.NoError(t, err)
}
