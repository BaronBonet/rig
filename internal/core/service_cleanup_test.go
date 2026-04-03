package core

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServiceDeleteTaskResources_CleansRunningTaskResources(t *testing.T) {
	worktree := t.TempDir()
	svc := newTestService()
	svc.taskRepo.getTask = cleanupTestTask(worktree)
	svc.gitRepo.branchExists = true
	svc.tmuxRepo.sessionExists = true

	task, err := svc.service.DeleteTaskResources(t.Context(), "billing-retry-flow")

	require.NoError(t, err)
	require.Equal(t, TaskStatusCleaned, task.Status)
	require.False(t, task.WorktreeExists)
	require.True(t, task.BranchExists)
	require.False(t, task.SessionExists)
	require.Empty(t, task.LastError)
	require.Equal(t, []string{"repo-billing-retry-flow"}, svc.tmuxRepo.killedSessions)
	require.Equal(t, []string{worktree}, svc.gitRepo.removedWorktrees)
}

func TestServiceDeleteTaskResources_CleansWorktreeWhenSessionAlreadyMissing(t *testing.T) {
	worktree := t.TempDir()
	svc := newTestService()
	svc.taskRepo.getTask = cleanupTestTask(worktree)
	svc.gitRepo.branchExists = true
	svc.tmuxRepo.sessionExists = false

	task, err := svc.service.DeleteTaskResources(t.Context(), "billing-retry-flow")

	require.NoError(t, err)
	require.Equal(t, TaskStatusCleaned, task.Status)
	require.False(t, task.WorktreeExists)
	require.False(t, task.SessionExists)
	require.Empty(t, svc.tmuxRepo.killedSessions)
	require.Equal(t, []string{worktree}, svc.gitRepo.removedWorktrees)
}

func TestServiceDeleteTaskResources_CleansSessionWhenWorktreeAlreadyMissing(t *testing.T) {
	worktree := filepath.Join(t.TempDir(), "gone")
	svc := newTestService()
	svc.taskRepo.getTask = cleanupTestTask(worktree)
	svc.gitRepo.branchExists = true
	svc.tmuxRepo.sessionExists = true

	task, err := svc.service.DeleteTaskResources(t.Context(), "billing-retry-flow")

	require.NoError(t, err)
	require.Equal(t, TaskStatusCleaned, task.Status)
	require.False(t, task.WorktreeExists)
	require.False(t, task.SessionExists)
	require.Equal(t, []string{"repo-billing-retry-flow"}, svc.tmuxRepo.killedSessions)
	require.Empty(t, svc.gitRepo.removedWorktrees)
}

func TestServiceDeleteTaskResources_MarksTaskBrokenWhenWorktreeRemovalFailsAfterTmuxCleanup(t *testing.T) {
	worktree := t.TempDir()
	svc := newTestService()
	svc.taskRepo.getTask = cleanupTestTask(worktree)
	svc.gitRepo.branchExists = true
	svc.gitRepo.removeWorktreeErr = errors.New("remove worktree: permission denied")
	svc.tmuxRepo.sessionExists = true

	task, err := svc.service.DeleteTaskResources(t.Context(), "billing-retry-flow")

	require.Error(t, err)
	require.Equal(t, TaskStatusBroken, task.Status)
	require.True(t, task.WorktreeExists)
	require.False(t, task.SessionExists)
	require.Contains(t, task.LastError, "permission denied")
}

func TestServiceDeleteTaskResources_AppendsCleanupLifecycleEvents(t *testing.T) {
	worktree := t.TempDir()
	svc := newTestService()
	svc.taskRepo.getTask = cleanupTestTask(worktree)
	svc.gitRepo.branchExists = true
	svc.tmuxRepo.sessionExists = true

	_, err := svc.service.DeleteTaskResources(t.Context(), "billing-retry-flow")

	require.NoError(t, err)
	require.Len(t, svc.taskRepo.appendedEvents, 2)
	require.Equal(t, "cleanup_requested", svc.taskRepo.appendedEvents[0].eventType)
	require.Equal(t, "cleanup_completed", svc.taskRepo.appendedEvents[1].eventType)
	require.Equal(t, "cleaned", svc.taskRepo.appendedEvents[1].payload)
}

func TestServiceDeleteTaskResources_ReturnsIntermediateUpdateFailure(t *testing.T) {
	worktree := t.TempDir()
	svc := newTestService()
	svc.taskRepo.getTask = cleanupTestTask(worktree)
	svc.taskRepo.updateErrAt = 2
	svc.taskRepo.updateErr = errors.New("update failed")
	svc.gitRepo.branchExists = true
	svc.tmuxRepo.sessionExists = true

	task, err := svc.service.DeleteTaskResources(t.Context(), "billing-retry-flow")

	require.ErrorContains(t, err, "update failed")
	require.NotNil(t, task)
	require.Empty(t, svc.gitRepo.removedWorktrees)
}

func TestServiceDeleteTaskResources_PreservesCleanupFailureReasonDuringLaterReconcile(t *testing.T) {
	worktree := t.TempDir()
	svc := newTestService()
	svc.taskRepo.getTask = cleanupTestTask(worktree)
	svc.gitRepo.branchExists = true
	svc.gitRepo.removeWorktreeErr = errors.New("remove worktree: permission denied")
	svc.tmuxRepo.sessionExists = true

	task, err := svc.service.DeleteTaskResources(t.Context(), "billing-retry-flow")
	require.Error(t, err)
	require.Contains(t, task.LastError, "permission denied")

	reconciled, reconcileErr := svc.service.GetTask(t.Context(), "billing-retry-flow")
	require.NoError(t, reconcileErr)
	require.Equal(t, TaskStatusBroken, reconciled.Status)
	require.False(t, reconciled.SessionExists)
	require.True(t, reconciled.WorktreeExists)
	require.Contains(t, reconciled.LastError, "permission denied")
}

func TestServiceReconcile_PreservesCleanedWhenResourcesRemainAbsent(t *testing.T) {
	worktree := filepath.Join(t.TempDir(), "gone")
	svc := newTestService()
	svc.taskRepo.getTask = &Task{
		ID:           "task-1",
		Slug:         "billing-retry-flow",
		RepoRoot:     "/tmp/repo",
		BranchName:   "feat/billing-retry-flow",
		WorktreePath: worktree,
		TmuxSession:  "repo-billing-retry-flow",
		Status:       TaskStatusCleaned,
	}
	svc.gitRepo.branchExists = true
	svc.tmuxRepo.sessionExists = false

	task, err := svc.service.GetTask(t.Context(), "billing-retry-flow")

	require.NoError(t, err)
	require.Equal(t, TaskStatusCleaned, task.Status)
	require.False(t, task.WorktreeExists)
	require.False(t, task.SessionExists)
	require.Empty(t, task.LastError)
}

func TestServiceReconcile_TurnsCleanedTaskBrokenWhenResourcesReappear(t *testing.T) {
	t.Run("session reappears", func(t *testing.T) {
		worktree := filepath.Join(t.TempDir(), "gone")
		svc := newTestService()
		svc.taskRepo.getTask = &Task{
			ID:           "task-1",
			Slug:         "billing-retry-flow",
			RepoRoot:     "/tmp/repo",
			BranchName:   "feat/billing-retry-flow",
			WorktreePath: worktree,
			TmuxSession:  "repo-billing-retry-flow",
			Status:       TaskStatusCleaned,
		}
		svc.gitRepo.branchExists = true
		svc.tmuxRepo.sessionExists = true

		task, err := svc.service.GetTask(t.Context(), "billing-retry-flow")

		require.NoError(t, err)
		require.Equal(t, TaskStatusBroken, task.Status)
		require.Contains(t, task.LastError, "unexpected tmux session")
	})

	t.Run("worktree reappears", func(t *testing.T) {
		worktree := t.TempDir()
		svc := newTestService()
		svc.taskRepo.getTask = &Task{
			ID:           "task-1",
			Slug:         "billing-retry-flow",
			RepoRoot:     "/tmp/repo",
			BranchName:   "feat/billing-retry-flow",
			WorktreePath: worktree,
			TmuxSession:  "repo-billing-retry-flow",
			Status:       TaskStatusCleaned,
		}
		svc.gitRepo.branchExists = true
		svc.tmuxRepo.sessionExists = false

		task, err := svc.service.GetTask(t.Context(), "billing-retry-flow")

		require.NoError(t, err)
		require.Equal(t, TaskStatusBroken, task.Status)
		require.Contains(t, task.LastError, "unexpected worktree")
	})
}

func cleanupTestTask(worktree string) *Task {
	return &Task{
		ID:             "task-1",
		Slug:           "billing-retry-flow",
		RepoRoot:       "/tmp/repo",
		BaseBranch:     "main",
		BranchName:     "feat/billing-retry-flow",
		WorktreePath:   worktree,
		TmuxSession:    "repo-billing-retry-flow",
		Status:         TaskStatusRunning,
		WorktreeExists: true,
		BranchExists:   true,
		SessionExists:  true,
	}
}
