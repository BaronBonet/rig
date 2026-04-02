package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServiceGetTask_ReconcilesLiveFields(t *testing.T) {
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

	task, err := svc.service.GetTask(t.Context(), "billing-retry-flow")
	require.NoError(t, err)
	require.True(t, task.WorktreeExists)
	require.True(t, task.BranchExists)
	require.True(t, task.SessionExists)
	require.Equal(t, TaskStatusRunning, task.Status)
}
