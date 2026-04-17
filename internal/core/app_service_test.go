package core

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type stubCreateTaskService func(context.Context, CreateTaskInput) (*Task, error)

func (f stubCreateTaskService) CreateTask(ctx context.Context, input CreateTaskInput) (*Task, error) {
	return f(ctx, input)
}

func TestAppServiceCreateTask_DelegatesToNewTaskService(t *testing.T) {
	var received CreateTaskInput
	expected := &Task{ID: "task-1", DisplayName: "billing retry flow"}
	app := NewAppService(
		stubCreateTaskService(func(_ context.Context, input CreateTaskInput) (*Task, error) {
			received = input
			return expected, nil
		}),
		newTestService(t).service,
	)

	task, err := app.CreateTask(t.Context(), CreateTaskInput{
		Cwd:      "/tmp/repo",
		Prompt:   "add billing retry flow",
		Provider: "codex",
	})

	require.NoError(t, err)
	require.Equal(t, expected, task)
	require.Equal(t, "/tmp/repo", received.Cwd)
	require.Equal(t, "add billing retry flow", received.Prompt)
}

func TestAppServiceCreateTask_PassesThroughErrorsFromNewTaskService(t *testing.T) {
	expectedErr := errors.New("boom")
	app := NewAppService(
		stubCreateTaskService(func(context.Context, CreateTaskInput) (*Task, error) {
			return nil, expectedErr
		}),
		newTestService(t).service,
	)

	task, err := app.CreateTask(t.Context(), CreateTaskInput{Prompt: "ship it"})

	require.Nil(t, task)
	require.ErrorIs(t, err, expectedErr)
}

func TestAppServiceListTasks_DelegatesToLegacyService(t *testing.T) {
	legacy := newTestService(t)
	worktree := t.TempDir()
	legacy.taskRepo.listTasks = []*Task{{
		ID:           "task-1",
		Slug:         "billing-retry-flow",
		RepoRoot:     "/tmp/repo",
		BranchName:   "feat/billing-retry-flow",
		WorktreePath: worktree,
		TmuxSession:  "repo-billing-retry-flow",
		Status:       TaskStatusRunning,
	}}
	legacy.repoClient.repoResources = RepoResources{WorktreeExists: true, BranchExists: true}
	legacy.sessionClient.sessionResources = SessionResources{
		SessionExists:      true,
		AgentWindowExists:  true,
		EditorWindowExists: true,
	}

	app := NewAppService(
		stubCreateTaskService(func(context.Context, CreateTaskInput) (*Task, error) {
			t.Fatal("create service should not be called by ListTasks")
			return nil, nil
		}),
		legacy.service,
	)

	tasks, err := app.ListTasks(t.Context())

	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.Equal(t, "billing-retry-flow", tasks[0].Slug)
}
