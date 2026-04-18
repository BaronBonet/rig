package tasksqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	sqliterepo "rig/internal/adapters/repository/sqlite"
	"rig/internal/core"
)

func TestRepositoryCreateTask_AllowsMultipleTasksWithoutDomainSlug(t *testing.T) {
	repo, err := New(sqliterepo.Config{Path: filepath.Join(t.TempDir(), "state.db")})
	if err != nil {
		t.Fatalf("new repository: %v", err)
	}

	now := time.Now().UTC()
	first := &core.Task{
		ID:           "task-1",
		Prompt:       "first prompt",
		DisplayName:  "duplicate name",
		RepoRoot:     "/tmp/repo",
		RepoName:     "repo",
		BranchName:   "feat/one",
		WorktreePath: "/tmp/repo-one",
		TmuxSession:  "repo_one",
		Provider:     core.AgentProviderCodex,
		Status:       core.TaskStatusCreating,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	second := &core.Task{
		ID:           "task-2",
		Prompt:       "second prompt",
		DisplayName:  "duplicate name",
		RepoRoot:     "/tmp/repo",
		RepoName:     "repo",
		BranchName:   "feat/two",
		WorktreePath: "/tmp/repo-two",
		TmuxSession:  "repo_two",
		Provider:     core.AgentProviderCodex,
		Status:       core.TaskStatusCreating,
		CreatedAt:    now.Add(time.Second),
		UpdatedAt:    now.Add(time.Second),
	}

	if err := repo.CreateTask(context.Background(), first); err != nil {
		t.Fatalf("create first task: %v", err)
	}
	if err := repo.CreateTask(context.Background(), second); err != nil {
		t.Fatalf("create second task: %v", err)
	}
}
