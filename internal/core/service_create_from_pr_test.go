//go:build legacy

package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestService_CreateTaskFromPRWithProgress_RejectsDuplicateBranchBeforePersist(t *testing.T) {
	h := newTestService(t)
	h.taskRepo.listTasks = []*Task{
		{
			RepoRoot:    "/tmp/repo",
			BranchName:  "feat/auth",
			Slug:        "auth",
			DisplayName: "auth",
		},
	}

	task, err := h.service.CreateTaskFromPRWithProgress(t.Context(), CreateTaskFromPRInput{
		RepoRoot: "/tmp/repo",
		PR: RepoPullRequest{
			Number:     42,
			Title:      "Auth rewrite",
			BranchName: "feat/auth",
			State:      PRStateDraft,
		},
		Provider: "codex",
	}, CreateTaskOptions{}, nil)

	require.Nil(t, task)
	require.EqualError(t, err, "PR already has workspace")
	require.Nil(t, h.taskRepo.createdTask)
	require.Nil(t, h.repoClient.createdTask)
}

func TestService_CreateTaskFromPRWithProgress_RejectsBranchAlreadyUsedByWorktreeBeforePersist(t *testing.T) {
	h := newTestService(t)
	h.repoClient.branchInUse = map[string]bool{"feat/auth": true}

	task, err := h.service.CreateTaskFromPRWithProgress(t.Context(), CreateTaskFromPRInput{
		RepoRoot: "/tmp/repo",
		PR: RepoPullRequest{
			Number:     42,
			Title:      "Auth rewrite",
			BranchName: "feat/auth",
			State:      PRStateDraft,
		},
		Provider: "codex",
	}, CreateTaskOptions{}, nil)

	require.Nil(t, task)
	require.EqualError(t, err, "PR already has workspace")
	require.Nil(t, h.taskRepo.createdTask)
	require.Nil(t, h.repoClient.createdTask)
}
