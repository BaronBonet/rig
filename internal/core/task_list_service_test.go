package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTaskFrontendContract_ExposesCreateListAndStatusMethods(t *testing.T) {
	var _ interface {
		CreateTask(context.Context, CreateTaskInput) (*Task, error)
		ListTasks(context.Context) ([]*Task, error)
		LatestTaskStatus(context.Context, string) (*TaskStatusUpdate, error)
		SubscribeTaskStatus(context.Context, string) (<-chan TaskStatusUpdate, error)
	} = (TaskFrontend)(nil)
}

func TestTaskServiceContract_ExposesListTasks(t *testing.T) {
	var _ interface {
		ListTasks(context.Context) ([]*Task, error)
	} = (TaskService)(nil)
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
