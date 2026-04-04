package core

import (
	"path/filepath"
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

	err := svc.service.OpenTask(t.Context(), "billing-retry-flow")
	require.NoError(t, err)
	require.Equal(t, "repo-billing-retry-flow", svc.tmuxRepo.attachedSession)
}

func TestServiceOpenTask_AllowsDegradedWhenAgentWindowExists(t *testing.T) {
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
		Status:           TaskStatus("degraded"),
	}
	svc.gitRepo.branchExists = true
	svc.tmuxRepo.sessionExists = true
	svc.tmuxRepo.windowExists = map[string]map[string]bool{
		"repo-billing-retry-flow": {
			"agent": true,
		},
	}

	err := svc.service.OpenTask(t.Context(), "billing-retry-flow")
	require.NoError(t, err)
	require.Equal(t, "repo-billing-retry-flow", svc.tmuxRepo.attachedSession)
}

func TestServiceOpenTask_ReturnsCleanedErrorForCleanedTask(t *testing.T) {
	svc := newTestService()
	svc.taskRepo.getTask = &Task{
		ID:           "task-1",
		Slug:         "billing-retry-flow",
		RepoRoot:     "/tmp/repo",
		BranchName:   "feat/billing-retry-flow",
		WorktreePath: filepath.Join(t.TempDir(), "gone"),
		TmuxSession:  "repo-billing-retry-flow",
		Status:       TaskStatusCleaned,
	}
	svc.gitRepo.branchExists = true
	svc.tmuxRepo.sessionExists = false

	err := svc.service.OpenTask(t.Context(), "billing-retry-flow")

	require.ErrorIs(t, err, ErrCleanedTask)
	require.Empty(t, svc.tmuxRepo.attachedSession)
}
