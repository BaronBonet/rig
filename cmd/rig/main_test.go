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
