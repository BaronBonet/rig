package tasksqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"rig/internal/core"

	_ "modernc.org/sqlite"
)

func TestRepositoryCreateTask_AllowsMultipleTasksWithoutDomainSlug(t *testing.T) {
	repo, err := New(Config{Path: filepath.Join(t.TempDir(), "state.db")})
	if err != nil {
		t.Fatalf("new repository: %v", err)
	}

	now := time.Now().UTC()
	first := &core.Task{
		ID:           "task-1",
		Slug:         "duplicate-name",
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
		Slug:         "duplicate-name-2",
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
		Slug:         "task-name",
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

func TestRepositoryNew_ReturnsErrorForInvalidConfig(t *testing.T) {
	repo, err := New(Config{Path: "state.db"})
	if err == nil {
		t.Fatal("expected constructor error for invalid config")
	}
	if repo != nil {
		t.Fatalf("expected nil repository on constructor error, got %T", repo)
	}
}

func TestRepositoryNew_ResetsDisposableDBWhenTasksSchemaIsStale(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open stale db: %v", err)
	}
	_, err = db.Exec(`
		create table tasks (
			id text primary key,
			prompt text not null,
			display_name text not null,
			repo_root text not null,
			repo_name text not null,
			branch_name text not null,
			worktree_path text not null,
			tmux_session text not null,
			provider text not null,
			status text not null,
			created_at text not null,
			updated_at text not null
		);
	`)
	if err != nil {
		t.Fatalf("create stale tasks table: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close stale db: %v", err)
	}

	repo, err := New(Config{Path: path})
	if err != nil {
		t.Fatalf("new repository: %v", err)
	}

	now := time.Now().UTC()
	task := &core.Task{
		ID:           "task-1",
		Slug:         "task-name",
		Prompt:       "first prompt",
		DisplayName:  "task name",
		RepoRoot:     "/tmp/repo",
		RepoName:     "repo",
		BranchName:   "feat/task-name",
		WorktreePath: "/tmp/repo-task-name",
		TmuxSession:  "repo_task_name",
		Provider:     core.AgentProviderCodex,
		Status:       core.TaskStatusCreating,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := repo.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("create task after reset: %v", err)
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
		"slug",
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
		Slug:         "task-name",
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
			Slug:         "task-one",
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
			Slug:         "task-two",
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
