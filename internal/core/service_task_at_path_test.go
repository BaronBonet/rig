package core

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTaskServiceTaskAtPath_ReturnsTheTaskWhoseWorktreeContainsThePath(t *testing.T) {
	frontend := &Task{ID: "task-frontend", RepoRoot: "/work/frontend", WorktreePath: "/work/frontend_call-endpoint"}
	unprepared := &Task{ID: "task-unprepared", RepoRoot: "/work/frontend"}

	tests := []struct {
		name   string
		path   string
		wantID string
	}{
		{name: "worktree root", path: "/work/backend_add-retries", wantID: "task-backend"},
		{name: "subdirectory", path: "/work/backend_add-retries/internal/api", wantID: "task-backend"},
		{name: "path that is not clean", path: " /work/backend_add-retries/internal/../ ", wantID: "task-backend"},
		{name: "another repository's task", path: "/work/frontend_call-endpoint/src", wantID: "task-frontend"},
		{name: "main checkout", path: "/work/backend"},
		{name: "directory beside the worktree sharing its name as a prefix", path: "/work/backend_add-retries-ui"},
		{name: "parent of the worktree", path: "/work"},
		{name: "relative path", path: "backend_add-retries"},
		{name: "empty path"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestTaskService(t)
			svc.taskRepo.listTasks = []*Task{unprepared, backendTask(), frontend}

			task, err := svc.service.TaskAtPath(t.Context(), tt.path)

			require.NoError(t, err)
			if tt.wantID == "" {
				require.Nil(t, task)
				return
			}
			require.NotNil(t, task)
			require.Equal(t, tt.wantID, task.ID)
		})
	}
}

func TestTaskServiceTaskAtPath_ReturnsTheDeepestOfNestedWorktreesInAnyOrder(t *testing.T) {
	outer := &Task{ID: "task-outer", WorktreePath: "/work/frontend_call-endpoint"}
	inner := &Task{ID: "task-inner", WorktreePath: "/work/frontend_call-endpoint/packages/web"}

	tests := []struct {
		name  string
		tasks []*Task
	}{
		{name: "deepest first", tasks: []*Task{inner, outer}},
		{name: "deepest last", tasks: []*Task{outer, inner}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestTaskService(t)
			svc.taskRepo.listTasks = tt.tasks

			task, err := svc.service.TaskAtPath(t.Context(), "/work/frontend_call-endpoint/packages/web/src")

			require.NoError(t, err)
			require.NotNil(t, task)
			require.Equal(t, "task-inner", task.ID)
		})
	}
}

func TestTaskServiceTaskAtPath_FailsWhenTheTasksCannotBeListed(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.listErr = errors.New("database is locked")

	task, err := svc.service.TaskAtPath(t.Context(), "/work/backend_add-retries")

	require.Nil(t, task)
	require.EqualError(t, err, "list tasks: database is locked")
}
