package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"agent/internal/core"

	"github.com/stretchr/testify/require"
)

func newTestRepository(t *testing.T) *Repository {
	t.Helper()

	repo, err := NewRepository(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)

	return repo
}

func TestRepositoryCreateAndGetTask(t *testing.T) {
	repo := newTestRepository(t)

	task := &core.Task{
		ID:            "task-1",
		Prompt:        "add billing retry flow",
		DisplayName:   "billing retry flow",
		Slug:          "billing-retry-flow",
		RepoRoot:      "/tmp/repo",
		BaseBranch:    "main",
		BranchName:    "feat/billing-retry-flow",
		WorktreePath:  "/tmp/repo-billing-retry-flow",
		TmuxSession:   "repo-billing-retry-flow",
		Provider:      "codex",
		Status:        core.TaskStatusCreating,
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
		WorktreeExists: true,
		BranchExists:   true,
		SessionExists:  false,
	}

	require.NoError(t, repo.CreateTask(context.Background(), task))

	got, err := repo.GetTask(context.Background(), "billing-retry-flow")
	require.NoError(t, err)
	require.Equal(t, task.DisplayName, got.DisplayName)
	require.Equal(t, task.BranchName, got.BranchName)
	require.Equal(t, task.Status, got.Status)
}

func TestRepositoryListTasks_OrdersByUpdatedAtDescending(t *testing.T) {
	repo := newTestRepository(t)

	older := &core.Task{
		ID:            "task-1",
		Prompt:        "first prompt",
		DisplayName:   "first task",
		Slug:          "first-task",
		RepoRoot:      "/tmp/repo",
		BaseBranch:    "main",
		BranchName:    "feat/first-task",
		WorktreePath:  "/tmp/repo-first-task",
		TmuxSession:   "repo-first-task",
		Provider:      "codex",
		Status:        core.TaskStatusReady,
		CreatedAt:     time.Now().Add(-2 * time.Hour).UTC(),
		UpdatedAt:     time.Now().Add(-2 * time.Hour).UTC(),
	}
	newer := &core.Task{
		ID:            "task-2",
		Prompt:        "second prompt",
		DisplayName:   "second task",
		Slug:          "second-task",
		RepoRoot:      "/tmp/repo",
		BaseBranch:    "main",
		BranchName:    "feat/second-task",
		WorktreePath:  "/tmp/repo-second-task",
		TmuxSession:   "repo-second-task",
		Provider:      "codex",
		Status:        core.TaskStatusRunning,
		CreatedAt:     time.Now().Add(-1 * time.Hour).UTC(),
		UpdatedAt:     time.Now().Add(-1 * time.Hour).UTC(),
	}

	require.NoError(t, repo.CreateTask(context.Background(), older))
	require.NoError(t, repo.CreateTask(context.Background(), newer))

	tasks, err := repo.ListTasks(context.Background())
	require.NoError(t, err)
	require.Len(t, tasks, 2)
	require.Equal(t, "second-task", tasks[0].Slug)
	require.Equal(t, "first-task", tasks[1].Slug)
}

func TestNewRepository_CreatesParentDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state.db")

	repo, err := NewRepository(path)
	require.NoError(t, err)
	require.NotNil(t, repo)
}
