package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/BaronBonet/rig/internal/core"

	"github.com/stretchr/testify/require"
)

func TestRepositoryTasks_PersistLaunchOptions(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	now := time.Date(2026, time.October, 6, 12, 0, 0, 0, time.UTC)
	task := &core.Task{
		ID:           "task-1",
		Slug:         "billing-retry",
		Prompt:       "add retries",
		DisplayName:  "billing retry",
		RepoRoot:     "/tmp/repo",
		RepoName:     "repo",
		BranchName:   "feat/billing-retry",
		WorktreePath: "/tmp/repo_billing-retry",
		TmuxSession:  "repo_billing-retry",
		Provider:     core.ProviderClaude,
		Model:        "opus",
		Effort:       "xhigh",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	require.NoError(t, repo.CreateTask(ctx, task))

	tasks, err := repo.ListTasks(ctx)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.Equal(t, core.LaunchOptions{Model: "opus", Effort: "xhigh"}, tasks[0].Launch())

	task.Model, task.Effort = "", "max"
	require.NoError(t, repo.UpdateTask(ctx, task))

	tasks, err = repo.ListTasks(ctx)
	require.NoError(t, err)
	require.Equal(t, core.LaunchOptions{Effort: "max"}, tasks[0].Launch())
}
