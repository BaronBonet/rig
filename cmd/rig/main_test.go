package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BaronBonet/rig/internal/core"
	"github.com/BaronBonet/rig/internal/infrastructure"

	"github.com/stretchr/testify/require"
)

func TestExecuteSource_UsesCobraCommandRuntime(t *testing.T) {
	t.Parallel()

	content, err := os.ReadFile(filepath.Join(".", "main.go"))
	require.NoError(t, err)
	source := string(content)

	require.Contains(t, source, `cmd := newRootCommand(newProductionCommandRuntime(stdout, stderr))`)
	require.Contains(t, source, `os.Getenv(daemonModeEnvKey) == daemonModeEnvValue`)
	require.Contains(t, source, `if err := adapter.EnsureRunning(ctx); err != nil`)
	require.Contains(t, source, `displayVersion := taskdaemon.FrontendBuildVersion()`)
	require.Contains(t, source, `program := tui.NewProgramWithVersionAndProvider(`)
	require.NotContains(t, source, `return run(ctx,`)
}

func TestDaemonHookRoutes_AreComposedFromConfiguredProviders(t *testing.T) {
	t.Parallel()

	content, err := os.ReadFile(filepath.Join(".", "main.go"))
	require.NoError(t, err)
	source := string(content)

	require.NotContains(t, source, "func daemonHookRoutes(")
	require.NotContains(t, source, `"/codex-hook"`)
	require.NotContains(t, source, `"/hook"`)
	require.NotContains(t, source, "CollectorURL:")
	require.Contains(t, source, "codex.NewHookForwardingConfig(")
	require.Contains(t, source, "claude.NewHookForwardingConfig(")
	require.Contains(t, source, "newConfiguredHookRoutes(")
}

func TestExecuteSource_ConstructsSingleTaskdaemonAdapterForClientPath(t *testing.T) {
	t.Parallel()

	content, err := os.ReadFile(filepath.Join(".", "main.go"))
	require.NoError(t, err)
	source := string(content)

	require.Contains(t, source, `return taskdaemon.New(taskdaemon.Config{`)
	require.Contains(t, source, `frontend := adapter.Frontend()`)
	require.NotContains(t, source, `deps :=`)
	require.NotContains(t, source, `func newRuntimeDependencies(`)
}

func TestRootCommandRunsTUIByDefault(t *testing.T) {
	t.Parallel()

	var calls []string
	cmd := newRootCommand(commandRuntime{
		version: "test-version",
		runTUI: func() error {
			calls = append(calls, "tui")
			return nil
		},
	})
	cmd.SetArgs([]string{})

	require.NoError(t, cmd.Execute())
	require.Equal(t, []string{"tui"}, calls)
}

func TestRootCommandPrintsBareVersion(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	cmd := newRootCommand(commandRuntime{
		stdout:  &stdout,
		version: "test-version",
		runTUI: func() error {
			t.Fatal("root TUI should not run for --version")
			return nil
		},
	})
	cmd.SetArgs([]string{"--version"})

	require.NoError(t, cmd.Execute())
	require.Equal(t, "test-version\n", stdout.String())
}

func TestRootCommandRoutesDoctor(t *testing.T) {
	t.Parallel()

	var calls []string
	cmd := newRootCommand(commandRuntime{
		version: "test-version",
		runDoctor: func() error {
			calls = append(calls, "doctor")
			return nil
		},
	})
	cmd.SetArgs([]string{"doctor"})

	require.NoError(t, cmd.Execute())
	require.Equal(t, []string{"doctor"}, calls)
}

func TestRootCommandRoutesSetup(t *testing.T) {
	t.Parallel()

	var got setupOptions
	cmd := newRootCommand(commandRuntime{
		version: "test-version",
		runSetup: func(options setupOptions) error {
			got = options
			return nil
		},
	})
	cmd.SetArgs([]string{"setup", "--provider", "codex,claude", "--default-provider", "claude"})

	require.NoError(t, cmd.Execute())
	require.Equal(t, []core.Provider{core.ProviderCodex, core.ProviderClaude}, got.Providers)
	require.Equal(t, core.ProviderClaude, got.DefaultProvider)
}

func TestRootCommandSetupRejectsUnknownProvider(t *testing.T) {
	t.Parallel()

	cmd := newRootCommand(commandRuntime{
		version: "test-version",
		runSetup: func(setupOptions) error {
			t.Fatal("setup should not run for an unknown provider")
			return nil
		},
	})
	cmd.SetArgs([]string{"setup", "--provider", "gemini"})

	err := cmd.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown provider")
}

func TestRootCommandRoutesDaemonCommands(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "start", args: []string{"daemon", "start"}, want: "daemon-start"},
		{name: "stop", args: []string{"daemon", "stop"}, want: "daemon-stop"},
		{name: "restart", args: []string{"daemon", "restart"}, want: "daemon-restart"},
		{name: "status", args: []string{"daemon", "status"}, want: "daemon-status"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var calls []string
			cmd := newRootCommand(commandRuntime{
				version: "test-version",
				runDaemonStart: func() error {
					calls = append(calls, "daemon-start")
					return nil
				},
				runDaemonStop: func() error {
					calls = append(calls, "daemon-stop")
					return nil
				},
				runDaemonRestart: func() error {
					calls = append(calls, "daemon-restart")
					return nil
				},
				runDaemonStatus: func() error {
					calls = append(calls, "daemon-status")
					return nil
				},
			})
			cmd.SetArgs(tt.args)

			require.NoError(t, cmd.Execute())
			require.Equal(t, []string{tt.want}, calls)
		})
	}
}

func TestExecuteWithArgs_VersionFlagPrintsConfiguredVersion(t *testing.T) {
	t.Parallel()

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	originalVersion := version
	version = "v9.9.9"
	t.Cleanup(func() {
		version = originalVersion
	})

	err := executeWithArgs([]string{"--version"}, stdout, stderr)
	require.NoError(t, err)
	require.Equal(t, "v9.9.9\n", stdout.String())
	require.Empty(t, stderr.String())
}

func TestExecuteWithArgsDoctorReportsHealthyEnvironment(t *testing.T) {
	binDir := t.TempDir()
	writeExecutable(t, binDir, "git")
	writeExecutable(t, binDir, "tmux")
	writeExecutable(t, binDir, "codex")

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", binDir)
	t.Setenv("RIG_PROVIDER", "codex")
	installRigProviderSetupFixture(t, home, []core.Provider{core.ProviderCodex}, core.ProviderCodex)
	installRigCodexHooksFixture(t, home, "http://127.0.0.1:4124/codex-hook")

	var stdout bytes.Buffer
	err := executeWithArgs([]string{"doctor"}, &stdout, nil)

	require.NoError(t, err)
	require.Contains(t, stdout.String(), "Rig doctor")
	require.Contains(t, stdout.String(), "OK   git")
	require.Contains(t, stdout.String(), "OK   tmux")
	require.Contains(t, stdout.String(), "OK   codex")
	require.Contains(t, stdout.String(), "OK   sqlite")
	require.Contains(t, stdout.String(), "WARN gh")
	require.Contains(t, stdout.String(), "Provider: codex")
}

func TestExecuteWithArgsDoctorFailsWhenRequiredCommandMissing(t *testing.T) {
	binDir := t.TempDir()
	writeExecutable(t, binDir, "git")
	writeExecutable(t, binDir, "codex")

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", binDir)
	t.Setenv("RIG_PROVIDER", "codex")
	installRigProviderSetupFixture(t, home, []core.Provider{core.ProviderCodex}, core.ProviderCodex)

	var stdout bytes.Buffer
	err := executeWithArgs([]string{"doctor"}, &stdout, nil)

	require.Error(t, err)
	require.ErrorContains(t, err, "tmux")
	require.Contains(t, stdout.String(), "FAIL tmux")
}

func TestExecuteWithArgsDoctorFailsWhenSQLiteIsUnhealthy(t *testing.T) {
	binDir := t.TempDir()
	writeExecutable(t, binDir, "git")
	writeExecutable(t, binDir, "tmux")
	writeExecutable(t, binDir, "codex")

	dbPath := filepath.Join(t.TempDir(), "tasks.db")
	require.NoError(t, os.WriteFile(dbPath, []byte("not a sqlite database"), 0o644))

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", binDir)
	t.Setenv("RIG_PROVIDER", "codex")
	t.Setenv("RIG_SQLITE_PATH", dbPath)
	installRigProviderSetupFixture(t, home, []core.Provider{core.ProviderCodex}, core.ProviderCodex)

	var stdout bytes.Buffer
	err := executeWithArgs([]string{"doctor"}, &stdout, nil)

	require.Error(t, err)
	require.ErrorContains(t, err, "sqlite")
	require.Contains(t, stdout.String(), "FAIL sqlite")
}

func TestExecuteWithArgsSetupUsesProviderBinaryOverrides(t *testing.T) {
	binDir := t.TempDir()
	writeExecutable(t, binDir, "codex-custom")

	home := t.TempDir()
	configPath := filepath.Join(home, "rig-config.json")
	t.Setenv("HOME", home)
	t.Setenv("PATH", binDir)
	t.Setenv("RIG_CONFIG_PATH", configPath)
	t.Setenv("RIG_CODEX_BINARY", "codex-custom")
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))

	var stdout bytes.Buffer
	err := executeWithArgs([]string{"setup", "--provider", "codex"}, &stdout, nil)

	require.NoError(t, err)
	require.Contains(t, stdout.String(), "Rig provider setup complete")

	setup, err := infrastructure.LoadProviderSetup(configPath)
	require.NoError(t, err)
	require.Equal(t, []core.Provider{core.ProviderCodex}, setup.ConfiguredProviders)
	require.Equal(t, core.ProviderCodex, setup.DefaultProvider)
	require.FileExists(t, filepath.Join(home, ".codex", "hooks", "forward-to-rig.sh"))
}

func TestExecuteWithArgsSetupRerunAddsDiscoveredProviderAndPreservesDefault(t *testing.T) {
	binDir := t.TempDir()
	writeExecutable(t, binDir, "codex")
	writeExecutable(t, binDir, "claude")

	home := t.TempDir()
	configPath := filepath.Join(home, "rig-config.json")
	t.Setenv("HOME", home)
	t.Setenv("PATH", binDir)
	t.Setenv("RIG_CONFIG_PATH", configPath)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	require.NoError(t, infrastructure.SaveProviderSetup(configPath, infrastructure.ProviderSetup{
		ConfiguredProviders: []core.Provider{core.ProviderCodex},
		DefaultProvider:     core.ProviderCodex,
	}))

	var stdout bytes.Buffer
	err := executeWithArgs([]string{"setup"}, &stdout, nil)

	require.NoError(t, err)

	setup, err := infrastructure.LoadProviderSetup(configPath)
	require.NoError(t, err)
	require.Equal(t, []core.Provider{core.ProviderClaude, core.ProviderCodex}, setup.ConfiguredProviders)
	require.Equal(t, core.ProviderCodex, setup.DefaultProvider)
	require.FileExists(t, filepath.Join(home, ".claude", "hooks", "forward-to-rig.sh"))
}

func TestTaskDaemonBuildIdentityIncludesExecutableMetadataForDevBuilds(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "rig")
	require.NoError(t, os.WriteFile(path, []byte("first"), 0o755))

	first, err := taskDaemonBuildIdentity(path, "dev")
	require.NoError(t, err)
	require.Contains(t, first, path)

	require.NoError(t, os.WriteFile(path, []byte("second-build"), 0o755))
	second, err := taskDaemonBuildIdentity(path, "dev")
	require.NoError(t, err)

	require.NotEqual(t, first, second)
	require.Equal(t, "v9.9.9", mustTaskDaemonBuildIdentity(t, path, "v9.9.9"))
}

func mustTaskDaemonBuildIdentity(t *testing.T, path string, version string) string {
	t.Helper()

	identity, err := taskDaemonBuildIdentity(path, version)
	require.NoError(t, err)
	return identity
}

func writeExecutable(t *testing.T, dir string, name string) {
	t.Helper()

	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755))
}

func installRigProviderSetupFixture(
	t *testing.T,
	home string,
	providers []core.Provider,
	defaultProvider core.Provider,
) {
	t.Helper()

	configPath := filepath.Join(home, "rig-config.json")
	t.Setenv("RIG_CONFIG_PATH", configPath)
	require.NoError(t, infrastructure.SaveProviderSetup(configPath, infrastructure.ProviderSetup{
		ConfiguredProviders: providers,
		DefaultProvider:     defaultProvider,
	}))
}

func installRigCodexHooksFixture(t *testing.T, home string, collectorURL string) {
	t.Helper()

	codexHome := filepath.Join(home, ".codex")
	hooksDir := filepath.Join(codexHome, "hooks")
	require.NoError(t, os.MkdirAll(hooksDir, 0o700))

	scriptPath := filepath.Join(hooksDir, "forward-to-rig.sh")
	require.NoError(t, os.WriteFile(scriptPath, []byte("#!/bin/sh\n# "+collectorURL+"\n"), 0o700))

	var hooks strings.Builder
	hooks.WriteString(`{"hooks":{`)
	events := []string{"SessionStart", "UserPromptSubmit", "Stop", "PreToolUse", "PermissionRequest", "PostToolUse"}
	for i, eventName := range events {
		if i > 0 {
			hooks.WriteString(",")
		}
		hooks.WriteString(`"` + eventName + `":[{"hooks":[{"type":"command","command":"`)
		hooks.WriteString(scriptPath + " " + eventName)
		hooks.WriteString(`"}]}]`)
	}
	hooks.WriteString("}}\n")

	require.NoError(t, os.WriteFile(filepath.Join(codexHome, "hooks.json"), []byte(hooks.String()), 0o644))
}
