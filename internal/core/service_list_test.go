package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServiceListTasks_MarksMissingTmuxSessionAsBroken(t *testing.T) {
	worktree := t.TempDir()
	svc := newTestService()
	svc.taskRepo.listTasks = []*Task{{
		ID:          "task-1",
		Slug:        "billing-retry-flow",
		RepoRoot:    "/tmp/repo",
		BranchName:  "feat/billing-retry-flow",
		WorktreePath: worktree,
		TmuxSession: "repo:billing-retry-flow",
		Status:      TaskStatusRunning,
	}}
	svc.gitRepo.branchExists = true
	svc.tmuxRepo.sessionExists = false

	tasks, err := svc.service.ListTasks(t.Context())
	require.NoError(t, err)
	require.Equal(t, TaskStatusBroken, tasks[0].Status)
	require.Contains(t, tasks[0].LastError, "missing tmux session")
}
