package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExecuteSource_InlinesClientRuntimeFlow(t *testing.T) {
	t.Parallel()

	content, err := os.ReadFile(filepath.Join(".", "main.go"))
	require.NoError(t, err)
	source := string(content)

	for _, forbidden := range []string{
		"type dependencies struct",
		"type taskDaemonRuntime interface",
		"func run(",
		"func newRuntimeDependencies(",
		"func isDaemonMode(",
	} {
		require.NotContains(t, source, forbidden)
	}

	require.Contains(t, source, `os.Getenv(daemonModeEnvKey) == daemonModeEnvValue`)
	require.Contains(t, source, `if err := adapter.EnsureRunning(ctx); err != nil`)
	require.Contains(t, source, `program := tui.NewProgram(`)
}

func TestDaemonHookRoutes_ExposeCodexHooksOnly(t *testing.T) {
	t.Parallel()

	content, err := os.ReadFile(filepath.Join(".", "main.go"))
	require.NoError(t, err)
	source := string(content)

	require.NotContains(t, source, "func daemonHookRoutes(")
	require.Contains(t, source, `{Path: "/hook", Handler: codexHooks}`)
	require.Contains(t, source, `{Path: "/codex-hook", Handler: codexHooks}`)
}

func TestExecuteSource_ConstructsSingleTaskdaemonAdapterForClientPath(t *testing.T) {
	t.Parallel()

	content, err := os.ReadFile(filepath.Join(".", "main.go"))
	require.NoError(t, err)
	source := string(content)

	require.Contains(t, source, `adapter := taskdaemon.New(taskdaemon.Config{`)
	require.Contains(t, source, `frontend := adapter.Frontend()`)
	require.NotContains(t, source, `deps :=`)
	require.NotContains(t, source, `return run(ctx,`)
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

	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", binDir)
	t.Setenv("RIG_PROVIDER", "codex")

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

	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", binDir)
	t.Setenv("RIG_PROVIDER", "codex")

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

	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", binDir)
	t.Setenv("RIG_PROVIDER", "codex")
	t.Setenv("RIG_SQLITE_PATH", dbPath)

	var stdout bytes.Buffer
	err := executeWithArgs([]string{"doctor"}, &stdout, nil)

	require.Error(t, err)
	require.ErrorContains(t, err, "sqlite")
	require.Contains(t, stdout.String(), "FAIL sqlite")
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
