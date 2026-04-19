package tasksqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"rig/internal/core"
)

func TestRepositoryCreateTask_AllowsMultipleTasksWithoutDomainSlug(t *testing.T) {
	repo, err := New(Config{Path: filepath.Join(t.TempDir(), "state.db")})
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

func TestRepositoryNew_CreatesParentDirectoryAndAppliesMigrations(t *testing.T) {
	repo, err := New(Config{Path: filepath.Join(t.TempDir(), "nested", "state.db")})
	if err != nil {
		t.Fatalf("new repository: %v", err)
	}

	now := time.Now().UTC()
	task := &core.Task{
		ID:           "task-1",
		Prompt:       "first prompt",
		DisplayName:  "task name",
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

	if err := repo.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("create task after New bootstrap: %v", err)
	}
}

func TestRepositoryNew_CreatesTasksTableWithCoreTaskColumnsOnly(t *testing.T) {
	store, err := New(Config{Path: filepath.Join(t.TempDir(), "state.db")})
	if err != nil {
		t.Fatalf("new repository: %v", err)
	}

	repo, ok := store.(*repository)
	if !ok {
		t.Fatalf("expected concrete repository, got %T", store)
	}

	rows, err := repo.db.QueryContext(context.Background(), "pragma table_info(tasks)")
	if err != nil {
		t.Fatalf("table info: %v", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var (
			cid        int
			name       string
			columnType string
			notNull    int
			defaultVal sql.NullString
			pk         int
		)
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultVal, &pk); err != nil {
			t.Fatalf("scan table info: %v", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("table info rows: %v", err)
	}

	want := []string{
		"id",
		"prompt",
		"display_name",
		"repo_root",
		"repo_name",
		"branch_name",
		"worktree_path",
		"tmux_session",
		"provider",
		"status",
		"created_at",
		"updated_at",
	}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("unexpected task columns:\n got: %#v\nwant: %#v", names, want)
	}
}

func TestRepositoryGetTask_ReturnsStoredTask(t *testing.T) {
	repo, err := New(Config{Path: filepath.Join(t.TempDir(), "state.db")})
	if err != nil {
		t.Fatalf("new repository: %v", err)
	}

	now := time.Now().UTC()
	task := &core.Task{
		ID:           "task-1",
		Prompt:       "first prompt",
		DisplayName:  "task name",
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

	if err := repo.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	got, err := repo.GetTask(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if !reflect.DeepEqual(got, task) {
		t.Fatalf("unexpected task:\n got: %#v\nwant: %#v", got, task)
	}
}

func TestRepositoryListTasks_ReturnsStoredTasks(t *testing.T) {
	repo, err := New(Config{Path: filepath.Join(t.TempDir(), "state.db")})
	if err != nil {
		t.Fatalf("new repository: %v", err)
	}

	now := time.Now().UTC()
	for _, task := range []*core.Task{
		{
			ID:           "task-1",
			Prompt:       "first prompt",
			DisplayName:  "task one",
			RepoRoot:     "/tmp/repo",
			RepoName:     "repo",
			BranchName:   "feat/one",
			WorktreePath: "/tmp/repo-one",
			TmuxSession:  "repo_one",
			Provider:     core.AgentProviderCodex,
			Status:       core.TaskStatusCreating,
			CreatedAt:    now,
			UpdatedAt:    now,
		},
		{
			ID:           "task-2",
			Prompt:       "second prompt",
			DisplayName:  "task two",
			RepoRoot:     "/tmp/repo",
			RepoName:     "repo",
			BranchName:   "feat/two",
			WorktreePath: "/tmp/repo-two",
			TmuxSession:  "repo_two",
			Provider:     core.AgentProviderCodex,
			Status:       core.TaskStatusCreating,
			CreatedAt:    now.Add(time.Second),
			UpdatedAt:    now.Add(time.Second),
		},
	} {
		if err := repo.CreateTask(context.Background(), task); err != nil {
			t.Fatalf("create task %s: %v", task.ID, err)
		}
	}

	tasks, err := repo.ListTasks(context.Background())
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(tasks))
	}
}
