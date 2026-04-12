package github

import (
	"context"
	"testing"

	"rig/internal/core"
	"rig/internal/pkg/execx"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestGHPRChecker_ReturnsPROpen(t *testing.T) {
	runner := execx.NewMockRunner(t)
	runner.EXPECT().
		Run(mock.Anything, "/tmp/repo", "gh", "pr", "view", "feat/auth", "--json", "number,state", "--jq", ".number,.state").
		Return(execx.Result{Stdout: "42\nOPEN\n"}, nil).
		Once()

	checker := NewPRStatusChecker(runner)
	status, err := checker.CheckPRStatus(context.Background(), "/tmp/repo", "feat/auth")

	require.NoError(t, err)
	require.Equal(t, core.PRStateOpen, status.State)
	require.Equal(t, 42, status.Number)
}

func TestGHPRChecker_ReturnsPRMerged(t *testing.T) {
	runner := execx.NewMockRunner(t)
	runner.EXPECT().
		Run(mock.Anything, "/tmp/repo", "gh", "pr", "view", "feat/auth", "--json", "number,state", "--jq", ".number,.state").
		Return(execx.Result{Stdout: "42\nMERGED\n"}, nil).
		Once()

	checker := NewPRStatusChecker(runner)
	status, err := checker.CheckPRStatus(context.Background(), "/tmp/repo", "feat/auth")

	require.NoError(t, err)
	require.Equal(t, core.PRStateMerged, status.State)
	require.Equal(t, 42, status.Number)
}

func TestGHPRChecker_ReturnsNoneWhenNoPR(t *testing.T) {
	runner := execx.NewMockRunner(t)
	runner.EXPECT().
		Run(mock.Anything, "/tmp/repo", "gh", "pr", "view", "feat/auth", "--json", "number,state", "--jq", ".number,.state").
		Return(execx.Result{Stderr: "no pull requests found"}, &execx.CommandError{Err: context.Canceled}).
		Once()

	checker := NewPRStatusChecker(runner)
	status, err := checker.CheckPRStatus(context.Background(), "/tmp/repo", "feat/auth")

	require.NoError(t, err)
	require.Equal(t, core.PRStateNone, status.State)
	require.Equal(t, 0, status.Number)
}

func TestGHPRChecker_IsAvailable_Succeeds(t *testing.T) {
	runner := execx.NewMockRunner(t)
	runner.EXPECT().
		Run(mock.Anything, "", "gh", "--version").
		Return(execx.Result{Stdout: "gh version 2.50.0\n"}, nil).
		Once()

	checker := NewPRStatusChecker(runner)
	err := checker.IsAvailable(context.Background())

	require.NoError(t, err)
}

func TestGHPRChecker_IsAvailable_ReturnsError(t *testing.T) {
	runner := execx.NewMockRunner(t)
	runner.EXPECT().
		Run(mock.Anything, "", "gh", "--version").
		Return(execx.Result{}, &execx.CommandError{Err: context.Canceled}).
		Once()

	checker := NewPRStatusChecker(runner)
	err := checker.IsAvailable(context.Background())

	require.Error(t, err)
}
