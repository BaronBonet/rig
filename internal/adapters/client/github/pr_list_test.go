package github

import (
	"context"
	"testing"

	"rig/internal/core"
	"rig/internal/pkg/subprocess"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestGHPRChecker_ListRepoPullRequests_ReturnsOpenAndDraftPRs(t *testing.T) {
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
		Return(subprocess.Result{Stdout: "42\tBilling retry\tfeat/billing\tfalse\n43\tAuth rewrite\tfeat/auth\ttrue\n"}, nil).
		Once()

	checker := NewPRStatusChecker(runner)
	prs, err := checker.ListRepoPullRequests(context.Background(), "/tmp/repo")

	require.NoError(t, err)
	require.Equal(t, []core.RepoPullRequest{
		{Number: 42, Title: "Billing retry", BranchName: "feat/billing", State: core.PRStateOpen},
		{Number: 43, Title: "Auth rewrite", BranchName: "feat/auth", State: core.PRStateDraft},
	}, prs)
}

func TestParsePRListOutput_ReturnsOpenAndDraftPRs(t *testing.T) {
	prs := parsePRListOutput("42\tBilling retry\tfeat/billing\tfalse\n43\tAuth rewrite\tfeat/auth\ttrue\n")

	require.Equal(t, []core.RepoPullRequest{
		{Number: 42, Title: "Billing retry", BranchName: "feat/billing", State: core.PRStateOpen},
		{Number: 43, Title: "Auth rewrite", BranchName: "feat/auth", State: core.PRStateDraft},
	}, prs)
}
