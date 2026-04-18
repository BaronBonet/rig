//go:build legacy

package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type stubPRChecker struct {
	statusFn func(context.Context, string, string) (*PRStatus, error)
	listFn   func(context.Context, string) ([]RepoPullRequest, error)
}

func (s stubPRChecker) IsAvailable(_ context.Context) error { return nil }

func (s stubPRChecker) CheckPRStatus(ctx context.Context, repoRoot string, branchName string) (*PRStatus, error) {
	if s.statusFn == nil {
		return &PRStatus{State: PRStateNone}, nil
	}
	return s.statusFn(ctx, repoRoot, branchName)
}

func (s stubPRChecker) ListRepoPullRequests(ctx context.Context, repoRoot string) ([]RepoPullRequest, error) {
	if s.listFn == nil {
		return nil, nil
	}
	return s.listFn(ctx, repoRoot)
}

func TestService_ListRepoPullRequests_MarksExistingWorkspaceBranches(t *testing.T) {
	h := newTestService(t)
	h.taskRepo.listTasks = []*Task{
		{
			RepoRoot:    "/tmp/repo",
			BranchName:  "feat/auth",
			Slug:        "auth",
			DisplayName: "auth",
		},
	}
	h.service.SetPRStatusChecker(stubPRChecker{
		listFn: func(context.Context, string) ([]RepoPullRequest, error) {
			return []RepoPullRequest{
				{Number: 41, Title: "Billing", BranchName: "feat/billing", State: PRStateOpen},
				{Number: 42, Title: "Auth", BranchName: "feat/auth", State: PRStateDraft},
			}, nil
		},
	})

	prs, err := h.service.ListRepoPullRequests(t.Context(), "/tmp/repo")

	require.NoError(t, err)
	require.Equal(t, []RepoPullRequest{
		{Number: 41, Title: "Billing", BranchName: "feat/billing", State: PRStateOpen, HasExistingTask: false},
		{Number: 42, Title: "Auth", BranchName: "feat/auth", State: PRStateDraft, HasExistingTask: true},
	}, prs)
}

func TestService_ListRepoPullRequests_MarksBranchesAlreadyUsedByGitWorktrees(t *testing.T) {
	h := newTestService(t)
	h.repoClient.branchInUse = map[string]bool{"feat/auth": true}
	h.service.SetPRStatusChecker(stubPRChecker{
		listFn: func(context.Context, string) ([]RepoPullRequest, error) {
			return []RepoPullRequest{
				{Number: 42, Title: "Auth", BranchName: "feat/auth", State: PRStateDraft},
			}, nil
		},
	})

	prs, err := h.service.ListRepoPullRequests(t.Context(), "/tmp/repo")

	require.NoError(t, err)
	require.Equal(t, []RepoPullRequest{
		{Number: 42, Title: "Auth", BranchName: "feat/auth", State: PRStateDraft, HasExistingTask: true},
	}, prs)
}
