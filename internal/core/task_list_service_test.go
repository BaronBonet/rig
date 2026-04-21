package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTaskFrontendContract_ExposesCreateListAndStatusMethods(t *testing.T) {
	var _ interface {
		OpenTaskSession(context.Context, *Task) error
		CreateTaskStream(context.Context, CreateTaskInput) (<-chan TaskCreateEvent, error)
		DeleteTask(context.Context, string) error
		ListTasks(context.Context) ([]*Task, error)
		LatestTaskStatus(context.Context, string) (*TaskStatusUpdate, error)
		SubscribeTaskStatus(context.Context, string) (<-chan TaskStatusUpdate, error)
	} = (TaskFrontend)(nil)
}

func TestTaskServiceContract_ExposesListTasks(t *testing.T) {
	var _ interface {
		CreateTaskWithProgress(context.Context, CreateTaskInput, TaskCreateProgressReporter) (*Task, error)
		DeleteTask(context.Context, string) error
		ListTasks(context.Context) ([]*Task, error)
	} = (TaskService)(nil)
}

func TestTmuxSessionClientContract_OnlyRequiresTaskLifecycleMethods(t *testing.T) {
	var _ TmuxSessionClient = (*MockTmuxSessionClient)(nil)
}

func TestTaskService_ListTasksReturnsRepositoryTasks(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.listTasks = []*Task{
		{ID: "task-1", Slug: "repo-a-task"},
		{ID: "task-2", Slug: "repo-b-task"},
	}

	tasks, err := svc.service.ListTasks(t.Context())
	require.NoError(t, err)
	require.Len(t, tasks, 2)
	require.Equal(t, []string{"task-1", "task-2"}, []string{tasks[0].ID, tasks[1].ID})
}

func TestTaskService_DeleteTaskRemovesSessionWorkspaceAndRecord(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.listTasks = []*Task{{
		ID:           "task-1",
		Slug:         "repo-a-task",
		DisplayName:  "repo a task",
		RepoRoot:     "/tmp/repo",
		BranchName:   "feat/repo-a-task",
		WorktreePath: "/tmp/repo-a-task",
		TmuxSession:  "repo_a_task",
	}}

	err := svc.service.DeleteTask(t.Context(), "task-1")

	require.NoError(t, err)
	require.NotNil(t, svc.sessionClient.deletedTask)
	require.Equal(t, "task-1", svc.sessionClient.deletedTask.ID)
	require.NotNil(t, svc.repoClient.removedTask)
	require.Equal(t, "/tmp/repo-a-task", svc.repoClient.removedTask.WorktreePath)
	require.Equal(t, "task-1", svc.taskRepo.deletedTaskID)
}

func TestTaskService_DeleteTaskReturnsNotFoundWhenTaskDoesNotExist(t *testing.T) {
	svc := newTestTaskService(t)

	err := svc.service.DeleteTask(t.Context(), "missing")

	require.ErrorIs(t, err, ErrTaskNotFound)
	require.Nil(t, svc.sessionClient.deletedTask)
	require.Nil(t, svc.repoClient.removedTask)
	require.Empty(t, svc.taskRepo.deletedTaskID)
}
