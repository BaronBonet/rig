package subprocess

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCommandError_ErrorIncludesCommandAndStderr(t *testing.T) {
	err := CommandError{
		Cwd:    "/tmp/repo",
		Name:   "git",
		Args:   []string{"worktree", "add"},
		Stdout: "",
		Stderr: "fatal: branch already exists",
		Err:    errors.New("exit status 1"),
	}

	require.Equal(
		t,
		"/tmp/repo: command git worktree add failed: exit status 1\nstdout:\n\nstderr:\nfatal: branch already exists",
		err.Error(),
	)
	require.Contains(t, err.Error(), "git worktree add")
	require.Contains(t, err.Error(), "fatal: branch already exists")
}

func TestExecRunner_RunsWithTheEnvironmentOverrides(t *testing.T) {
	t.Setenv("RIG_TEST_INHERITED", "daemon")
	t.Setenv("RIG_TEST_REMOVED", "daemon")

	result, err := ExecRunner{}.RunWithStdin(t.Context(), RunWithStdinOptions{
		Env:  map[string]string{"RIG_TEST_REMOVED": "", "RIG_TEST_SET": "task"},
		Name: "sh",
		Args: []string{"-c", `echo "${RIG_TEST_INHERITED-unset} ${RIG_TEST_REMOVED-unset} ${RIG_TEST_SET-unset}"`},
	})

	require.NoError(t, err)
	require.Equal(t, "daemon unset task", strings.TrimSpace(result.Stdout))
}
