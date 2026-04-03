package execx

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFakeRunnerRun_RecordsCallAndReturnsConfiguredResult(t *testing.T) {
	runner := NewFakeRunner([]Result{
		{Stdout: "hello\n"},
	})

	result, err := runner.Run(context.Background(), "/tmp/repo", "git", "status")
	require.NoError(t, err)
	require.Equal(t, "hello\n", result.Stdout)
	require.Len(t, runner.Calls, 1)
	require.Equal(t, "git", runner.Calls[0].Name)
	require.Equal(t, []string{"status"}, runner.Calls[0].Args)
}

func TestCommandError_ErrorIncludesCommandAndStderr(t *testing.T) {
	err := CommandError{
		Cwd:    "/tmp/repo",
		Name:   "git",
		Args:   []string{"worktree", "add"},
		Stdout: "",
		Stderr: "fatal: branch already exists",
		Err:    errors.New("exit status 1"),
	}

	require.Contains(t, err.Error(), "git worktree add")
	require.Contains(t, err.Error(), "fatal: branch already exists")
}
