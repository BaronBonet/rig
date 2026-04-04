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
		TmuxSession:  "repo-billing-retry-flow",
		Status:       TaskStatusRunning,
	}
	svc.gitRepo.branchExists = true
	svc.tmuxRepo.sessionExists = true
	svc.tmuxRepo.windowExists = map[string]map[string]bool{
		"repo-billing-retry-flow": {
			"agent":  true,
			"editor": true,
		},
	}

	task, err := svc.service.GetTask(t.Context(), "billing-retry-flow")
	require.NoError(t, err)
	require.True(t, task.WorktreeExists)
	require.True(t, task.BranchExists)
	require.True(t, task.SessionExists)
	require.True(t, task.AgentWindowExists)
	require.True(t, task.EditorWindowExists)
	require.Equal(t, TaskStatusRunning, task.Status)
}

func TestServiceGetTask_MarksTaskDegradedWhenEditorWindowMissing(t *testing.T) {
	worktree := t.TempDir()
	svc := newTestService()
	svc.taskRepo.getTask = &Task{
		ID:               "task-1",
		Slug:             "billing-retry-flow",
		RepoRoot:         "/tmp/repo",
		BranchName:       "feat/billing-retry-flow",
		WorktreePath:     worktree,
		TmuxSession:      "repo-billing-retry-flow",
		AgentWindowName:  "agent",
		EditorWindowName: "editor",
		Status:           TaskStatusRunning,
	}
	svc.gitRepo.branchExists = true
	svc.tmuxRepo.sessionExists = true
	svc.tmuxRepo.windowExists = map[string]map[string]bool{
		"repo-billing-retry-flow": {
			"agent": true,
		},
	}

	task, err := svc.service.GetTask(t.Context(), "billing-retry-flow")
	require.NoError(t, err)
	require.True(t, task.SessionExists)
	require.True(t, task.AgentWindowExists)
	require.False(t, task.EditorWindowExists)
	require.Equal(t, TaskStatus("degraded"), task.Status)
}

func TestServiceGetTask_MarksTaskBrokenWhenAgentWindowMissing(t *testing.T) {
	worktree := t.TempDir()
	svc := newTestService()
	svc.taskRepo.getTask = &Task{
		ID:               "task-1",
		Slug:             "billing-retry-flow",
		RepoRoot:         "/tmp/repo",
		BranchName:       "feat/billing-retry-flow",
		WorktreePath:     worktree,
		TmuxSession:      "repo-billing-retry-flow",
		AgentWindowName:  "agent",
		EditorWindowName: "editor",
		Status:           TaskStatusRunning,
	}
	svc.gitRepo.branchExists = true
	svc.tmuxRepo.sessionExists = true
	svc.tmuxRepo.windowExists = map[string]map[string]bool{
		"repo-billing-retry-flow": {
			"editor": true,
		},
	}

	task, err := svc.service.GetTask(t.Context(), "billing-retry-flow")
	require.NoError(t, err)
	require.Equal(t, TaskStatusBroken, task.Status)
	require.Contains(t, task.LastError, "missing tmux agent window")
}

func TestServiceGetTask_MarksTaskBrokenWhenSessionMissing(t *testing.T) {
	worktree := t.TempDir()
	svc := newTestService()
	svc.taskRepo.getTask = &Task{
		ID:               "task-1",
		Slug:             "billing-retry-flow",
		RepoRoot:         "/tmp/repo",
		BranchName:       "feat/billing-retry-flow",
		WorktreePath:     worktree,
		TmuxSession:      "repo-billing-retry-flow",
		AgentWindowName:  "agent",
		EditorWindowName: "editor",
		Status:           TaskStatusRunning,
	}
	svc.gitRepo.branchExists = true
	svc.tmuxRepo.sessionExists = false

	task, err := svc.service.GetTask(t.Context(), "billing-retry-flow")
	require.NoError(t, err)
	require.Equal(t, TaskStatusBroken, task.Status)
	require.Contains(t, task.LastError, "missing tmux session")
}
