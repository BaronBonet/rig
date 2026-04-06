package execx

import (
	"errors"
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

	require.Equal(t, "/tmp/repo: command git worktree add failed: exit status 1\nstdout:\n\nstderr:\nfatal: branch already exists", err.Error())
	require.Contains(t, err.Error(), "git worktree add")
	require.Contains(t, err.Error(), "fatal: branch already exists")
}
