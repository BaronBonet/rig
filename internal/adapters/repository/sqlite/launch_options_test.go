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

func TestRepositoryTasks_PersistParentAndWindow(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	now := time.Date(2026, time.October, 6, 12, 0, 0, 0, time.UTC)
	child := &core.Task{
		ID:            "task-2",
		Slug:          "session-2",
		DisplayName:   "session 2",
		RepoRoot:      "/tmp/repo",
		RepoName:      "repo",
		BranchName:    "feat/billing-retry",
		WorktreePath:  "/tmp/repo_billing-retry",
		TmuxSession:   "repo_billing-retry",
		TmuxWindow:    "s2",
		Provider:      core.ProviderClaude,
		WorkspaceKind: core.WorkspaceKindFolder,
		ParentID:      "task-1",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	require.NoError(t, repo.CreateTask(ctx, child))

	tasks, err := repo.ListTasks(ctx)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.Equal(t, "task-1", tasks[0].ParentID)
	require.Equal(t, "s2", tasks[0].TmuxWindow)
	require.True(t, tasks[0].IsChild())
}

func TestRepositoryTasks_PersistShelvedAt(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	now := time.Date(2026, time.October, 6, 12, 0, 0, 0, time.UTC)
	task := &core.Task{
		ID:            "task-1",
		Slug:          "billing-retry",
		DisplayName:   "billing retry",
		RepoRoot:      "/tmp/repo",
		RepoName:      "repo",
		WorktreePath:  "/tmp/repo",
		TmuxSession:   "repo_billing-retry",
		Provider:      core.ProviderClaude,
		WorkspaceKind: core.WorkspaceKindFolder,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	require.NoError(t, repo.CreateTask(ctx, task))

	task.ShelvedAt = now.Add(time.Hour)
	require.NoError(t, repo.UpdateTask(ctx, task))
	tasks, err := repo.ListTasks(ctx)
	require.NoError(t, err)
	require.True(t, tasks[0].IsShelved())
	require.True(t, now.Add(time.Hour).Equal(tasks[0].ShelvedAt))

	task.ShelvedAt = time.Time{}
	require.NoError(t, repo.UpdateTask(ctx, task))
	tasks, err = repo.ListTasks(ctx)
	require.NoError(t, err)
	require.False(t, tasks[0].IsShelved())
}

func TestRepositoryTasks_PersistSessionTitle(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	now := time.Date(2026, time.October, 7, 12, 0, 0, 0, time.UTC)
	task := &core.Task{
		ID:            "task-1",
		Slug:          "connect-slack-and-jira-mcps",
		DisplayName:   "Set up the linter",
		RepoRoot:      "/tmp/repo",
		RepoName:      "repo",
		WorktreePath:  "/tmp/repo",
		TmuxSession:   "repo_connect-slack-and-jira-mcps",
		Provider:      core.ProviderClaude,
		WorkspaceKind: core.WorkspaceKindFolder,
		SessionTitle:  "mcp-setup",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	require.NoError(t, repo.CreateTask(ctx, task))
	tasks, err := repo.ListTasks(ctx)
	require.NoError(t, err)
	require.Equal(t, "mcp-setup", tasks[0].SessionTitle)

	task.SessionTitle = "flaky-test-investigation"
	require.NoError(t, repo.UpdateTask(ctx, task))
	tasks, err = repo.ListTasks(ctx)
	require.NoError(t, err)
	require.Equal(t, "flaky-test-investigation", tasks[0].SessionTitle)
}
