package subprocess

import (
	"context"
	"errors"
	"testing"
	"time"

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

func TestExecRunner_CancelledRunDoesNotWaitForGrandchildHoldingOutput(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()

	_, err := ExecRunner{}.Run(ctx, "", "sh", "-c", "sleep 10; echo done")

	require.Error(t, err)
	require.Less(t, time.Since(start), 5*time.Second)
}

func TestExecRunner_RunWithStdinAddsToTheInheritedEnvironment(t *testing.T) {
	t.Setenv("RIG_RUNNER_TEST_INHERITED", "kept")
	t.Setenv("RIG_RUNNER_TEST_REPLACED", "before")

	result, err := ExecRunner{}.RunWithStdin(t.Context(), RunWithStdinOptions{
		Name: "sh",
		Args: []string{
			"-c",
			`cat; echo "$RIG_RUNNER_TEST_INHERITED $RIG_RUNNER_TEST_REPLACED $RIG_RUNNER_TEST_ADDED"`,
		},
		Stdin: "in ",
		Env:   []string{"RIG_RUNNER_TEST_REPLACED=after", "RIG_RUNNER_TEST_ADDED=added"},
	})

	require.NoError(t, err)
	require.Equal(t, "in kept after added\n", result.Stdout)
}
