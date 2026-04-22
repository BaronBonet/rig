package github

import (
	"testing"

	"rig/internal/core"
	"rig/internal/pkg/subprocess"

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
