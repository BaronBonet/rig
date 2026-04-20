package github

import (
	"context"
	"testing"

	"rig/internal/core"
	"rig/internal/pkg/subprocess"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestGHPRChecker_ReturnsPROpen(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	runner.EXPECT().
		Run(mock.Anything, "/tmp/repo", "gh", "pr", "view", "feat/auth", "--json", "number,state,isDraft", "--jq", ".number,.state,.isDraft").
		Return(subprocess.Result{Stdout: "42\nOPEN\nfalse\n"}, nil).
		Once()

	checker := NewPRStatusChecker(runner)
	status, err := checker.CheckPRStatus(context.Background(), "/tmp/repo", "feat/auth")

	require.NoError(t, err)
	require.Equal(t, core.PRStateOpen, status.State)
	require.Equal(t, 42, status.Number)
}

func TestGHPRChecker_ReturnsPRMerged(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	runner.EXPECT().
		Run(mock.Anything, "/tmp/repo", "gh", "pr", "view", "feat/auth", "--json", "number,state,isDraft", "--jq", ".number,.state,.isDraft").
		Return(subprocess.Result{Stdout: "42\nMERGED\nfalse\n"}, nil).
		Once()

	checker := NewPRStatusChecker(runner)
	status, err := checker.CheckPRStatus(context.Background(), "/tmp/repo", "feat/auth")

	require.NoError(t, err)
	require.Equal(t, core.PRStateMerged, status.State)
	require.Equal(t, 42, status.Number)
}

func TestGHPRChecker_ReturnsNoneWhenNoPR(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	runner.EXPECT().
		Run(mock.Anything, "/tmp/repo", "gh", "pr", "view", "feat/auth", "--json", "number,state,isDraft", "--jq", ".number,.state,.isDraft").
		Return(subprocess.Result{Stderr: "no pull requests found"}, &subprocess.CommandError{Err: context.Canceled}).
		Once()

	checker := NewPRStatusChecker(runner)
	status, err := checker.CheckPRStatus(context.Background(), "/tmp/repo", "feat/auth")

	require.NoError(t, err)
	require.Equal(t, core.PRStateNone, status.State)
	require.Equal(t, 0, status.Number)
}

func TestParsePROutput_Draft(t *testing.T) {
	result := parsePROutput("42\nopen\ntrue")
	require.Equal(t, core.PRStateDraft, result.State)
	require.Equal(t, 42, result.Number)
}

func TestParsePROutput_Closed(t *testing.T) {
	result := parsePROutput("36\nclosed\nfalse")
	require.Equal(t, core.PRStateClosed, result.State)
	require.Equal(t, 36, result.Number)
}

func TestGHPRChecker_IsAvailable_Succeeds(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	runner.EXPECT().
		Run(mock.Anything, "", "gh", "--version").
		Return(subprocess.Result{Stdout: "gh version 2.50.0\n"}, nil).
		Once()

	checker := NewPRStatusChecker(runner)
	err := checker.IsAvailable(context.Background())

	require.NoError(t, err)
}

func TestGHPRChecker_IsAvailable_ReturnsError(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	runner.EXPECT().
		Run(mock.Anything, "", "gh", "--version").
		Return(subprocess.Result{}, &subprocess.CommandError{Err: context.Canceled}).
		Once()

	checker := NewPRStatusChecker(runner)
	err := checker.IsAvailable(context.Background())

	require.Error(t, err)
}
