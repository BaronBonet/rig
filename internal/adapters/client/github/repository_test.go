package github

import (
	"context"
	"errors"
	"testing"
	"time"

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

// expectBranchPullRequests expects one GraphQL query for the pull requests
// whose head is branchName in /tmp/repo's repository, which prints output.
func expectBranchPullRequests(runner *subprocess.MockRunner, branchName string, output string, err error) {
	runner.EXPECT().
		Run(
			withGhTimeout(),
			"/tmp/repo",
			"gh",
			"api",
			"graphql",
			"-f",
			"query="+branchPullRequestsQuery,
			"-F",
			"owner={owner}",
			"-F",
			"repo={repo}",
			"-f",
			"head="+branchName,
		).
		Return(subprocess.Result{Stdout: output}, err).
		Once()
}

// pullRequestsResponse is a GraphQL response listing nodes, a JSON array of
// pull requests.
func pullRequestsResponse(nodes string) string {
	return `{"data": {"repository": {"pullRequests": {"nodes": ` + nodes + `}}}}`
}

func TestRepository_ListBranchPullRequests_ListsThePullRequestsInEveryState(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	expectBranchPullRequests(runner, "feat/billing", pullRequestsResponse(`[
		{"number": 127, "state": "OPEN", "isDraft": false, "headRefName": "feat/billing",
		 "headRefOid": "a7d3f0c2b8e14f6a9c5d2e7b1f0a3c8d6e4b2a90", "baseRefName": "main", "isCrossRepository": false,
		 "commits": {"nodes": [
		   {"commit": {"oid": "f1e2d3c4b5a69788a7b6c5d4e3f2a1b0c9d8e7f6"}},
		   {"commit": {"oid": "a7d3f0c2b8e14f6a9c5d2e7b1f0a3c8d6e4b2a90"}}
		 ]}},
		{"number": 125, "state": "OPEN", "isDraft": true, "headRefName": "feat/billing",
		 "headRefOid": "b2c4e6f8a0b2c4e6f8a0b2c4e6f8a0b2c4e6f8a0", "baseRefName": "develop", "isCrossRepository": false,
		 "commits": {"nodes": []}},
		{"number": 123, "state": "MERGED", "isDraft": false, "headRefName": "feat/billing",
		 "headRefOid": "c3d5e7f9b1c3d5e7f9b1c3d5e7f9b1c3d5e7f9b1", "baseRefName": "main", "isCrossRepository": false},
		{"number": 121, "state": "CLOSED", "isDraft": false, "headRefName": "feat/billing", "isCrossRepository": false}
	]`), nil)

	pullRequests, err := New(runner).ListBranchPullRequests(t.Context(), "/tmp/repo", "feat/billing")

	require.NoError(t, err)
	require.Equal(t, []core.PullRequest{
		{
			Number:     127,
			State:      core.PRStateOpen,
			HeadBranch: "feat/billing",
			HeadCommit: "a7d3f0c2b8e14f6a9c5d2e7b1f0a3c8d6e4b2a90",
			BaseBranch: "main",
			Commits: []string{
				"f1e2d3c4b5a69788a7b6c5d4e3f2a1b0c9d8e7f6",
				"a7d3f0c2b8e14f6a9c5d2e7b1f0a3c8d6e4b2a90",
			},
		},
		{
			Number:     125,
			State:      core.PRStateDraft,
			HeadBranch: "feat/billing",
			HeadCommit: "b2c4e6f8a0b2c4e6f8a0b2c4e6f8a0b2c4e6f8a0",
			BaseBranch: "develop",
		},
		{
			Number:     123,
			State:      core.PRStateMerged,
			HeadBranch: "feat/billing",
			HeadCommit: "c3d5e7f9b1c3d5e7f9b1c3d5e7f9b1c3d5e7f9b1",
			BaseBranch: "main",
		},
		{Number: 121, State: core.PRStateClosed, HeadBranch: "feat/billing"},
	}, pullRequests)
}

// In an open-source repository, the pull requests forks open from their own
// main all have the head main.
func TestRepository_ListBranchPullRequests_LeavesOutPullRequestsFromForks(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	expectBranchPullRequests(runner, "main", pullRequestsResponse(`[
		{"number": 88, "state": "OPEN", "isDraft": false, "headRefName": "main", "isCrossRepository": true},
		{"number": 87, "state": "MERGED", "isDraft": false, "headRefName": "main", "baseRefName": "release",
		 "isCrossRepository": false},
		{"number": 86, "state": "CLOSED", "isDraft": false, "headRefName": "main", "isCrossRepository": true}
	]`), nil)

	pullRequests, err := New(runner).ListBranchPullRequests(t.Context(), "/tmp/repo", "main")

	require.NoError(t, err)
	require.Equal(
		t,
		[]core.PullRequest{{Number: 87, State: core.PRStateMerged, HeadBranch: "main", BaseBranch: "release"}},
		pullRequests,
	)
}

func TestRepository_ListBranchPullRequests_ListsNoneWhenNoPullRequestHasTheBranch(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	expectBranchPullRequests(runner, "feat/auth", pullRequestsResponse("[]"), nil)

	pullRequests, err := New(runner).ListBranchPullRequests(t.Context(), "/tmp/repo", "feat/auth")

	require.NoError(t, err)
	require.Empty(t, pullRequests)
}

func TestRepository_ListBranchPullRequests_FailsWhenGhFails(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	expectBranchPullRequests(runner, "feat/auth", "", errors.New("exit status 4"))

	_, err := New(runner).ListBranchPullRequests(t.Context(), "/tmp/repo", "feat/auth")

	require.ErrorContains(t, err, "exit status 4")
}

func TestRepository_ListBranchPullRequests_FailsOnOutputItCannotRead(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	expectBranchPullRequests(runner, "feat/auth", "To get started with GitHub CLI, please run: gh auth login", nil)

	_, err := New(runner).ListBranchPullRequests(t.Context(), "/tmp/repo", "feat/auth")

	require.ErrorContains(t, err, "decode branch pull requests")
}

// The query asks for each commit's ID only: gh pr list --json commits also
// asks for every commit's authors, which with a hundred pull requests exceeds
// GitHub's GraphQL node limit.
func TestBranchPullRequestsQuery_AsksForNoCommitAuthors(t *testing.T) {
	require.Contains(t, branchPullRequestsQuery, "commits(first: 100) { nodes { commit { oid } } }")
	require.NotContains(t, branchPullRequestsQuery, "authors")
}

// withGhTimeout matches the context of a gh call that is killed once
// ghTimeout has passed.
func withGhTimeout() any {
	return mock.MatchedBy(func(ctx context.Context) bool {
		deadline, ok := ctx.Deadline()
		return ok && time.Until(deadline) <= ghTimeout && time.Until(deadline) > ghTimeout-5*time.Second
	})
}

// expectDefaultBranch expects one GraphQL query for the default branch of
// /tmp/repo's repository, which prints output.
func expectDefaultBranch(runner *subprocess.MockRunner, output string, err error) {
	runner.EXPECT().
		Run(
			withGhTimeout(),
			"/tmp/repo",
			"gh",
			"api",
			"graphql",
			"-f",
			"query="+defaultBranchQuery,
			"-F",
			"owner={owner}",
			"-F",
			"repo={repo}",
		).
		Return(subprocess.Result{Stdout: output}, err).
		Once()
}

func TestRepository_DefaultBranch_AsksGitHubForTheRepositorysDefaultBranch(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	expectDefaultBranch(runner, `{"data": {"repository": {"defaultBranchRef": {"name": "develop"}}}}`, nil)

	branch, err := New(runner).DefaultBranch(t.Context(), "/tmp/repo")

	require.NoError(t, err)
	require.Equal(t, "develop", branch)
}

func TestRepository_DefaultBranch_IsEmptyForARepositoryWithoutOne(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	expectDefaultBranch(runner, `{"data": {"repository": {"defaultBranchRef": null}}}`, nil)

	branch, err := New(runner).DefaultBranch(t.Context(), "/tmp/repo")

	require.NoError(t, err)
	require.Empty(t, branch)
}

func TestRepository_DefaultBranch_FailsWhenGhFails(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	expectDefaultBranch(runner, "", errors.New("none of the git remotes point to a known GitHub host"))

	_, err := New(runner).DefaultBranch(t.Context(), "/tmp/repo")

	require.ErrorContains(t, err, "known GitHub host")
}

// Every gh call is killed after ghTimeout, the doctor's and the pull request
// picker's too.
func TestRepository_TimesGhOut(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	runner.EXPECT().Run(withGhTimeout(), "", "gh", "auth", "status").Return(subprocess.Result{}, nil).Once()
	runner.EXPECT().
		Run(withGhTimeout(), "/tmp/repo", "gh", "pr", "list", "--state", "open", "--json", mock.Anything, "--jq", mock.Anything).
		Return(subprocess.Result{}, nil).
		Once()

	require.NoError(t, New(runner).HealthCheck(t.Context()))
	_, err := New(runner).ListRepoPullRequests(t.Context(), "/tmp/repo")
	require.NoError(t, err)
}
