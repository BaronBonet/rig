package core

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServiceNewTask_CreatesWorktreeSessionAndPersistsTask(t *testing.T) {
	svc := newTestService()
	svc.codexRepo.proposedName = "billing retry flow"

	task, err := svc.service.NewTask(t.Context(), NewTaskInput{
		Cwd:                  "/tmp/repo",
		Prompt:               "add billing retry flow",
		ConfirmedDisplayName: "billing retry flow",
	})

	require.NoError(t, err)
	require.Equal(t, "feat/billing-retry-flow", task.BranchName)
	require.Equal(t, "/tmp/repo-billing-retry-flow", task.WorktreePath)
	require.Equal(t, "repo:billing-retry-flow", task.TmuxSession)
	require.Equal(t, TaskStatusRunning, task.Status)
	require.Equal(t, "/tmp/repo-billing-retry-flow", svc.gitRepo.createWorktreeInput.WorktreePath)
	require.Equal(t, "repo:billing-retry-flow", svc.tmuxRepo.createdSession.SessionName)
	require.Equal(t, []string{"codex", "add billing retry flow"}, svc.tmuxRepo.sentCommand)
	require.Equal(t, "billing retry flow", svc.taskRepo.createdTask.DisplayName)
}

func TestServiceNewTask_FallsBackWhenCodexNameProposalFails(t *testing.T) {
	svc := newTestService()
	svc.codexRepo.proposeErr = errors.New("codex unavailable")

	task, err := svc.service.NewTask(t.Context(), NewTaskInput{
		Cwd:    "/tmp/repo",
		Prompt: "add billing retry flow",
	})

	require.NoError(t, err)
	require.Equal(t, "billing retry flow", task.DisplayName)
}

func TestServiceNewTask_PersistsBrokenTaskWhenTmuxCreationFails(t *testing.T) {
	svc := newTestService()
	svc.codexRepo.proposedName = "billing retry flow"
	svc.tmuxRepo.createSessionErr = errors.New("tmux failed")

	task, err := svc.service.NewTask(t.Context(), NewTaskInput{
		Cwd:                  "/tmp/repo",
		Prompt:               "add billing retry flow",
		ConfirmedDisplayName: "billing retry flow",
	})

	require.Error(t, err)
	require.Equal(t, TaskStatusBroken, task.Status)
	require.Contains(t, task.LastError, "tmux failed")
	require.Equal(t, TaskStatusBroken, svc.taskRepo.updatedTask.Status)
}
