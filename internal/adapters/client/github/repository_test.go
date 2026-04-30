package github

import (
	"errors"
	"testing"

	"github.com/BaronBonet/rig/internal/core"
	"github.com/BaronBonet/rig/internal/pkg/subprocess"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestRepository_ListRepoPullRequests_ReturnsOpenAndDraftPRs(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	runner.EXPECT().
		Run(
			mock.Anything,
			"/tmp/repo",
			"gh",
			"pr",
			"list",
			"--state",
			"open",
			"--json",
			"number,title,headRefName,isDraft",
			"--jq",
			".[] | [.number, .title, .headRefName, .isDraft] | @tsv",
		).
		Return(subprocess.Result{
			Stdout: "41\tBilling\tfeat/billing\tfalse\n42\tAuth rewrite\tfeat/auth\ttrue\n",
		}, nil).
		Once()

	prs, err := New(runner).ListRepoPullRequests(t.Context(), "/tmp/repo")

	require.NoError(t, err)
	require.Equal(t, []core.RepoPullRequest{
		{Number: 41, Title: "Billing", BranchName: "feat/billing", State: core.PRStateOpen},
		{Number: 42, Title: "Auth rewrite", BranchName: "feat/auth", State: core.PRStateDraft},
	}, prs)
}

func TestRepository_CheckPullRequestStatus_ReturnsOpenPR(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	runner.EXPECT().
		Run(
			mock.Anything,
			"/tmp/repo",
			"gh",
			"pr",
			"view",
			"feat/auth",
			"--json",
			"number,state,isDraft",
			"--jq",
			".number,.state,.isDraft",
		).
		Return(subprocess.Result{Stdout: "42\nOPEN\nfalse\n"}, nil).
		Once()

	status, err := New(runner).CheckPullRequestStatus(t.Context(), "/tmp/repo", "feat/auth")

	require.NoError(t, err)
	require.Equal(t, &core.PRStatus{State: core.PRStateOpen, Number: 42}, status)
}

func TestRepository_CheckPullRequestStatus_ReturnsMergedPR(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	runner.EXPECT().
		Run(
			mock.Anything,
			"/tmp/repo",
			"gh",
			"pr",
			"view",
			"feat/auth",
			"--json",
			"number,state,isDraft",
			"--jq",
			".number,.state,.isDraft",
		).
		Return(subprocess.Result{Stdout: "42\nMERGED\nfalse\n"}, nil).
		Once()

	status, err := New(runner).CheckPullRequestStatus(t.Context(), "/tmp/repo", "feat/auth")

	require.NoError(t, err)
	require.Equal(t, &core.PRStatus{State: core.PRStateMerged, Number: 42}, status)
}

func TestRepository_CheckPullRequestStatus_ReturnsNoneWhenNoPRMatchesBranch(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	runner.EXPECT().
		Run(mock.Anything, "/tmp/repo", "gh", "pr", "view", "feat/auth", "--json", "number,state,isDraft", "--jq", ".number,.state,.isDraft").
		Return(subprocess.Result{Stderr: "no pull requests found"}, errors.New("exit status 1")).
		Once()

	status, err := New(runner).CheckPullRequestStatus(t.Context(), "/tmp/repo", "feat/auth")

	require.NoError(t, err)
	require.Equal(t, &core.PRStatus{State: core.PRStateNone}, status)
}
