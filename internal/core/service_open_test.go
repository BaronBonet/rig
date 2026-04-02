package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServiceOpenTask_AttachesWhenSessionExists(t *testing.T) {
	worktree := t.TempDir()
	svc := newTestService()
	svc.taskRepo.getTask = &Task{
		ID:           "task-1",
		Slug:         "billing-retry-flow",
		RepoRoot:     "/tmp/repo",
		BranchName:   "feat/billing-retry-flow",
		WorktreePath: worktree,
		TmuxSession:  "repo:billing-retry-flow",
		Status:       TaskStatusRunning,
	}
	svc.gitRepo.branchExists = true
	svc.tmuxRepo.sessionExists = true

	err := svc.service.OpenTask(t.Context(), "billing-retry-flow")
	require.NoError(t, err)
	require.Equal(t, "repo:billing-retry-flow", svc.tmuxRepo.attachedSession)
}
