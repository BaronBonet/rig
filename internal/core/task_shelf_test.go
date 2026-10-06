package core

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func shelfGroupFixture() (*Task, *Task) {
	parent := readyTaskFixture()
	child := newChildTaskRecord(parent, ProviderCodex, "session 2", "session-2", "s2")
	child.ID = "task-2"
	child.CreationStatus = TaskCreationStatusReady
	return parent, child
}

func TestTaskServiceShelveTask_ClosesTheSessionAndKeepsTheGroup(t *testing.T) {
	svc := newTestTaskService(t)
	parent, child := shelfGroupFixture()
	svc.taskRepo.listTasks = []*Task{parent, child}

	require.NoError(t, svc.service.ShelveTask(t.Context(), "task-1"))

	require.Equal(t, "task-1", svc.sessionClient.deletedTask.ID, "the parent's session, child windows with it")
	require.Len(t, svc.taskRepo.updatedTasks, 2)
	for _, task := range svc.taskRepo.updatedTasks {
		require.True(t, task.IsShelved(), task.ID)
	}
	require.Nil(t, svc.repoClient.removedTask, "the worktree stays")
	require.Empty(t, svc.taskRepo.deletedTaskIDs, "the records stay")
}

func TestTaskServiceShelveTask_KeepsWhenAChildWasShelvedOnItsOwn(t *testing.T) {
	svc := newTestTaskService(t)
	parent, child := shelfGroupFixture()
	child.ShelvedAt = time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC)
	svc.taskRepo.listTasks = []*Task{parent, child}

	require.NoError(t, svc.service.ShelveTask(t.Context(), "task-1"))

	require.Len(t, svc.taskRepo.updatedTasks, 1)
	require.Equal(t, "task-1", svc.taskRepo.updatedTasks[0].ID)
}

func TestTaskServiceShelveTask_AChildClosesOnlyItsWindow(t *testing.T) {
	svc := newTestTaskService(t)
	parent, child := shelfGroupFixture()
	svc.taskRepo.listTasks = []*Task{parent, child}

	require.NoError(t, svc.service.ShelveTask(t.Context(), "task-2"))

	require.Equal(t, "task-2", svc.sessionClient.deletedTask.ID)
	require.Equal(t, "s2", svc.sessionClient.deletedTask.TmuxWindow)
	require.Len(t, svc.taskRepo.updatedTasks, 1)
	require.Equal(t, "task-2", svc.taskRepo.updatedTasks[0].ID)
}

func TestTaskServiceShelveTask_RefusesWhileASessionOfTheGroupIsWorking(t *testing.T) {
	svc := newTestTaskService(t)
	parent, child := shelfGroupFixture()
	svc.taskRepo.listTasks = []*Task{parent, child}
	svc.taskRepo.latestByTask["task-2"] = TaskStatusUpdate{TaskID: "task-2", Phase: TaskStatusPhaseWorkingInBackground}

	err := svc.service.ShelveTask(t.Context(), "task-1")

	require.ErrorIs(t, err, ErrProviderSessionActive)
	require.ErrorContains(t, err, `"session 2" is working`)
	require.Nil(t, svc.sessionClient.deletedTask)
	require.Empty(t, svc.taskRepo.updatedTasks)
}

func TestTaskServiceShelveTask_RefusesATaskStillBeingCreated(t *testing.T) {
	svc := newTestTaskService(t)
	task := readyTaskFixture()
	task.CreationStatus = TaskCreationStatusCreating
	svc.taskRepo.listTasks = []*Task{task}

	require.ErrorContains(t, svc.service.ShelveTask(t.Context(), "task-1"), "still being created")
	require.Nil(t, svc.sessionClient.deletedTask)
}

func TestTaskServiceUnshelveTask_PutsTheGroupBackWithoutStartingIt(t *testing.T) {
	svc := newTestTaskService(t)
	parent, child := shelfGroupFixture()
	shelvedAt := time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC)
	parent.ShelvedAt, child.ShelvedAt = shelvedAt, shelvedAt
	svc.taskRepo.listTasks = []*Task{parent, child}

	require.NoError(t, svc.service.UnshelveTask(t.Context(), "task-1"))

	require.Len(t, svc.taskRepo.updatedTasks, 2)
	for _, task := range svc.taskRepo.updatedTasks {
		require.False(t, task.IsShelved(), task.ID)
	}
	require.Nil(t, svc.sessionClient.startedTask, "it starts when opened")
}
