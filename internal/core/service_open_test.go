package core

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServiceOpenTask_AttachesWhenSessionExists(t *testing.T) {
	worktree := t.TempDir()
	svc := newTestService(t)
	svc.taskRepo.getTask = &Task{
		ID:           "task-1",
		Slug:         "billing-retry-flow",
		RepoRoot:     "/tmp/repo",
		BranchName:   "feat/billing-retry-flow",
		WorktreePath: worktree,
		TmuxSession:  "repo-billing-retry-flow",
		Status:       TaskStatusRunning,
	}
	svc.repoClient.repoResources = RepoResources{WorktreeExists: true, BranchExists: true}
	svc.sessionClient.sessionResources = SessionResources{
		SessionExists:      true,
		AgentWindowExists:  true,
		EditorWindowExists: true,
	}

	err := svc.service.OpenTask(t.Context(), "billing-retry-flow")
	require.NoError(t, err)
	require.Equal(t, "repo-billing-retry-flow", svc.sessionClient.openedTask.TmuxSession)
}

func TestServiceOpenTask_AllowsDegradedWhenAgentWindowExists(t *testing.T) {
	worktree := t.TempDir()
	svc := newTestService(t)
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
	svc.repoClient.repoResources = RepoResources{WorktreeExists: true, BranchExists: true}
	svc.sessionClient.sessionResources = SessionResources{SessionExists: true, AgentWindowExists: true}

	err := svc.service.OpenTask(t.Context(), "billing-retry-flow")
	require.NoError(t, err)
	require.Equal(t, "repo-billing-retry-flow", svc.sessionClient.openedTask.TmuxSession)
}

func TestServiceOpenTask_ReturnsCleanedErrorForCleanedTask(t *testing.T) {
	svc := newTestService(t)
	svc.taskRepo.getTask = &Task{
		ID:           "task-1",
		Slug:         "billing-retry-flow",
		RepoRoot:     "/tmp/repo",
		BranchName:   "feat/billing-retry-flow",
		WorktreePath: filepath.Join(t.TempDir(), "gone"),
		TmuxSession:  "repo-billing-retry-flow",
		Status:       TaskStatusCleaned,
	}
	svc.repoClient.repoResources = RepoResources{WorktreeExists: false, BranchExists: true}
	svc.sessionClient.sessionResources = SessionResources{}

	err := svc.service.OpenTask(t.Context(), "billing-retry-flow")

	require.ErrorIs(t, err, ErrCleanedTask)
	require.Nil(t, svc.sessionClient.openedTask)
}
