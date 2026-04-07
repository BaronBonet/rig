package core

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestServiceDeleteTaskResources_CleansRunningTaskResources(t *testing.T) {
	worktree := t.TempDir()
	svc := newTestService(t)
	svc.taskRepo.getTask = cleanupTestTask(worktree)
	svc.repoClient.repoResources = RepoResources{WorktreeExists: true, BranchExists: true}
	svc.sessionClient.sessionResources = SessionResources{
		SessionExists:      true,
		AgentWindowExists:  true,
		EditorWindowExists: true,
	}
	before := time.Now().UTC()

	task, err := svc.service.DeleteTaskResources(t.Context(), "billing-retry-flow")
	after := time.Now().UTC()

	require.NoError(t, err)
	require.Equal(t, TaskStatusCleaned, task.Status)
	require.False(t, task.WorktreeExists)
	require.True(t, task.BranchExists)
	require.False(t, task.SessionExists)
	require.False(t, task.AgentWindowExists)
	require.False(t, task.EditorWindowExists)
	require.Empty(t, task.LastError)
	require.Len(t, svc.sessionClient.deletedTasks, 1)
	require.Equal(t, "repo-billing-retry-flow", svc.sessionClient.deletedTasks[0].TmuxSession)
	require.Len(t, svc.repoClient.removedTasks, 1)
	require.Equal(t, worktree, svc.repoClient.removedTasks[0].WorktreePath)
	requireTimeInWindow(t, task.UpdatedAt, before, after)
}

func TestServiceDeleteTaskResources_CleansWorktreeWhenSessionAlreadyMissing(t *testing.T) {
	worktree := t.TempDir()
	svc := newTestService(t)
	svc.taskRepo.getTask = cleanupTestTask(worktree)
	svc.repoClient.repoResources = RepoResources{WorktreeExists: true, BranchExists: true}
	svc.sessionClient.sessionResources = SessionResources{}

	task, err := svc.service.DeleteTaskResources(t.Context(), "billing-retry-flow")

	require.NoError(t, err)
	require.Equal(t, TaskStatusCleaned, task.Status)
	require.False(t, task.WorktreeExists)
	require.False(t, task.SessionExists)
	require.Empty(t, svc.sessionClient.deletedTasks)
	require.Len(t, svc.repoClient.removedTasks, 1)
	require.Equal(t, worktree, svc.repoClient.removedTasks[0].WorktreePath)
}

func TestServiceDeleteTaskResources_CleansSessionWhenWorktreeAlreadyMissing(t *testing.T) {
	worktree := filepath.Join(t.TempDir(), "gone")
	svc := newTestService(t)
	svc.taskRepo.getTask = cleanupTestTask(worktree)
	svc.repoClient.repoResources = RepoResources{WorktreeExists: false, BranchExists: true}
	svc.sessionClient.sessionResources = SessionResources{
		SessionExists:      true,
		AgentWindowExists:  true,
		EditorWindowExists: true,
	}

	task, err := svc.service.DeleteTaskResources(t.Context(), "billing-retry-flow")

	require.NoError(t, err)
	require.Equal(t, TaskStatusCleaned, task.Status)
	require.False(t, task.WorktreeExists)
	require.False(t, task.SessionExists)
	require.Len(t, svc.sessionClient.deletedTasks, 1)
	require.Equal(t, "repo-billing-retry-flow", svc.sessionClient.deletedTasks[0].TmuxSession)
	require.Empty(t, svc.repoClient.removedTasks)
}

func TestServiceDeleteTaskResources_TreatsMissingSessionAfterKillErrorAsSuccess(t *testing.T) {
	worktree := t.TempDir()
	svc := newTestService(t)
	svc.taskRepo.getTask = cleanupTestTask(worktree)
	svc.repoClient.repoResources = RepoResources{WorktreeExists: true, BranchExists: true}
	svc.sessionClient.sessionResources = SessionResources{
		SessionExists:      true,
		AgentWindowExists:  true,
		EditorWindowExists: true,
	}
	svc.sessionClient.deleteErr = errors.New("can't find session")
	svc.sessionClient.deleteHook = func(*Task) {
		svc.sessionClient.sessionResources = SessionResources{}
	}

	task, err := svc.service.DeleteTaskResources(t.Context(), "billing-retry-flow")

	require.NoError(t, err)
	require.Equal(t, TaskStatusCleaned, task.Status)
	require.False(t, task.SessionExists)
	require.False(t, task.WorktreeExists)
	require.Empty(t, task.LastError)
	require.Len(t, svc.sessionClient.deletedTasks, 1)
	require.Equal(t, "repo-billing-retry-flow", svc.sessionClient.deletedTasks[0].TmuxSession)
	require.Len(t, svc.repoClient.removedTasks, 1)
	require.Equal(t, worktree, svc.repoClient.removedTasks[0].WorktreePath)
}

func TestServiceDeleteTaskResources_TreatsMissingWorktreeAfterRemoveErrorAsSuccess(t *testing.T) {
	worktree := t.TempDir()
	svc := newTestService(t)
	svc.taskRepo.getTask = cleanupTestTask(worktree)
	svc.repoClient.repoResources = RepoResources{WorktreeExists: true, BranchExists: true}
	svc.repoClient.removeErr = errors.New("already gone")
	svc.repoClient.removeHook = func(*Task) {
		svc.repoClient.repoResources.WorktreeExists = false
	}
	svc.sessionClient.sessionResources = SessionResources{}

	task, err := svc.service.DeleteTaskResources(t.Context(), "billing-retry-flow")

	require.NoError(t, err)
	require.Equal(t, TaskStatusCleaned, task.Status)
	require.False(t, task.SessionExists)
	require.False(t, task.WorktreeExists)
	require.Empty(t, task.LastError)
	require.Empty(t, svc.sessionClient.deletedTasks)
	require.Len(t, svc.repoClient.removedTasks, 1)
	require.Equal(t, worktree, svc.repoClient.removedTasks[0].WorktreePath)
}

func TestServiceDeleteTaskResources_KeepsTaskBrokenWhenWorktreeStateCannotBeVerified(t *testing.T) {
	worktree := "/tmp/bad"
	svc := newTestService(t)
	svc.taskRepo.getTask = cleanupTestTask(worktree)
	svc.repoClient.repoResources = RepoResources{WorktreeExists: true, BranchExists: true}
	svc.repoClient.removeErr = errors.New("permission denied")
	svc.repoClient.removeHook = func(*Task) {
		svc.repoClient.inspectErr = errors.New("inspect failed")
	}
	svc.sessionClient.sessionResources = SessionResources{}

	task, err := svc.service.DeleteTaskResources(t.Context(), "billing-retry-flow")

	require.Error(t, err)
	require.Equal(t, TaskStatusBroken, task.Status)
	require.True(t, task.WorktreeExists)
	require.Contains(t, task.LastError, "permission denied")
}

func TestServiceDeleteTaskResources_MarksTaskBrokenWhenWorktreeRemovalFailsAfterTmuxCleanup(t *testing.T) {
	worktree := t.TempDir()
	svc := newTestService(t)
	svc.taskRepo.getTask = cleanupTestTask(worktree)
	svc.repoClient.repoResources = RepoResources{WorktreeExists: true, BranchExists: true}
	svc.repoClient.removeErr = errors.New("remove worktree: permission denied")
	svc.sessionClient.sessionResources = SessionResources{
		SessionExists:      true,
		AgentWindowExists:  true,
		EditorWindowExists: true,
	}

	task, err := svc.service.DeleteTaskResources(t.Context(), "billing-retry-flow")

	require.Error(t, err)
	require.Equal(t, TaskStatusBroken, task.Status)
	require.True(t, task.WorktreeExists)
	require.False(t, task.SessionExists)
	require.Contains(t, task.LastError, "permission denied")
}

func TestServiceDeleteTaskResources_AppendsCleanupLifecycleEvents(t *testing.T) {
	worktree := t.TempDir()
	svc := newTestService(t)
	svc.taskRepo.getTask = cleanupTestTask(worktree)
	svc.repoClient.repoResources = RepoResources{WorktreeExists: true, BranchExists: true}
	svc.sessionClient.sessionResources = SessionResources{
		SessionExists:      true,
		AgentWindowExists:  true,
		EditorWindowExists: true,
	}

	_, err := svc.service.DeleteTaskResources(t.Context(), "billing-retry-flow")

	require.NoError(t, err)
	require.Len(t, svc.taskRepo.appendedEvents, 2)
	require.Equal(t, "cleanup_requested", svc.taskRepo.appendedEvents[0].eventType)
	require.Equal(t, "cleanup_completed", svc.taskRepo.appendedEvents[1].eventType)
	require.Equal(t, "cleaned", svc.taskRepo.appendedEvents[1].payload)
}

func TestServiceDeleteTaskResources_ReturnsIntermediateUpdateFailure(t *testing.T) {
	worktree := t.TempDir()
	svc := newTestService(t)
	svc.taskRepo.getTask = cleanupTestTask(worktree)
	svc.taskRepo.updateErrAt = 2
	svc.taskRepo.updateErr = errors.New("update failed")
	svc.repoClient.repoResources = RepoResources{WorktreeExists: true, BranchExists: true}
	svc.sessionClient.sessionResources = SessionResources{
		SessionExists:      true,
		AgentWindowExists:  true,
		EditorWindowExists: true,
	}

	task, err := svc.service.DeleteTaskResources(t.Context(), "billing-retry-flow")

	require.ErrorContains(t, err, "update failed")
	require.NotNil(t, task)
	require.Empty(t, svc.repoClient.removedTasks)
}

func TestServiceDeleteTaskResources_PreservesCleanupFailureReasonDuringLaterReconcile(t *testing.T) {
	worktree := t.TempDir()
	svc := newTestService(t)
	svc.taskRepo.getTask = cleanupTestTask(worktree)
	svc.repoClient.repoResources = RepoResources{WorktreeExists: true, BranchExists: true}
	svc.repoClient.removeErr = errors.New("remove worktree: permission denied")
	svc.sessionClient.sessionResources = SessionResources{
		SessionExists:      true,
		AgentWindowExists:  true,
		EditorWindowExists: true,
	}

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
	svc := newTestService(t)
	svc.taskRepo.getTask = &Task{
		ID:           "task-1",
		Slug:         "billing-retry-flow",
		RepoRoot:     "/tmp/repo",
		BranchName:   "feat/billing-retry-flow",
		WorktreePath: worktree,
		TmuxSession:  "repo-billing-retry-flow",
		Status:       TaskStatusCleaned,
	}
	svc.repoClient.repoResources = RepoResources{WorktreeExists: false, BranchExists: true}
	svc.sessionClient.sessionResources = SessionResources{}

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
		svc := newTestService(t)
		svc.taskRepo.getTask = &Task{
			ID:           "task-1",
			Slug:         "billing-retry-flow",
			RepoRoot:     "/tmp/repo",
			BranchName:   "feat/billing-retry-flow",
			WorktreePath: worktree,
			TmuxSession:  "repo-billing-retry-flow",
			Status:       TaskStatusCleaned,
		}
		svc.repoClient.repoResources = RepoResources{WorktreeExists: false, BranchExists: true}
		svc.sessionClient.sessionResources = SessionResources{SessionExists: true}

		task, err := svc.service.GetTask(t.Context(), "billing-retry-flow")

		require.NoError(t, err)
		require.Equal(t, TaskStatusBroken, task.Status)
		require.Contains(t, task.LastError, "unexpected tmux session")
	})

	t.Run("worktree reappears", func(t *testing.T) {
		worktree := t.TempDir()
		svc := newTestService(t)
		svc.taskRepo.getTask = &Task{
			ID:           "task-1",
			Slug:         "billing-retry-flow",
			RepoRoot:     "/tmp/repo",
			BranchName:   "feat/billing-retry-flow",
			WorktreePath: worktree,
			TmuxSession:  "repo-billing-retry-flow",
			Status:       TaskStatusCleaned,
		}
		svc.repoClient.repoResources = RepoResources{WorktreeExists: true, BranchExists: true}
		svc.sessionClient.sessionResources = SessionResources{}

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
