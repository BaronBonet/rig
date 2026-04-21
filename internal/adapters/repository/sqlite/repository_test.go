package sqlite

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

func TestNew_ReturnsTaskRepository(t *testing.T) {
	var _ core.TaskRepository = &repository{}

	repo, err := New(Config{Path: filepath.Join(t.TempDir(), "state.db")})
	if err != nil {
		t.Fatalf("new repository: %v", err)
	}
	if repo == nil {
		t.Fatal("expected repository")
	}
}

func TestRepositoryCreateTaskAndListTasks_PersistsCoreTaskFields(t *testing.T) {
	repo := newTestRepository(t)
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
		Provider:     core.ProviderCodex,
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
		Provider:     core.ProviderCodex,
		CreatedAt:    now.Add(time.Second),
		UpdatedAt:    now.Add(time.Second),
	}

	if err := repo.CreateTask(context.Background(), first); err != nil {
		t.Fatalf("create first task: %v", err)
	}
	if err := repo.CreateTask(context.Background(), second); err != nil {
		t.Fatalf("create second task: %v", err)
	}

	tasks, err := repo.ListTasks(context.Background())
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if !reflect.DeepEqual(tasks, []*core.Task{first, second}) {
		t.Fatalf("unexpected tasks:\n got: %#v\nwant: %#v", tasks, []*core.Task{first, second})
	}
}

func TestRepositoryUpdateTask_PersistsMutations(t *testing.T) {
	repo := newTestRepository(t)
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
		Provider:     core.ProviderCodex,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := repo.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	task.Prompt = "updated prompt"
	task.DisplayName = "updated task name"
	task.UpdatedAt = now.Add(5 * time.Minute)
	if err := repo.UpdateTask(context.Background(), task); err != nil {
		t.Fatalf("update task: %v", err)
	}

	tasks, err := repo.ListTasks(context.Background())
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if !reflect.DeepEqual(tasks, []*core.Task{task}) {
		t.Fatalf("unexpected tasks after update:\n got: %#v\nwant: %#v", tasks, []*core.Task{task})
	}
}

func TestRepositoryDeleteTask_RemovesTaskAndCascadesLatestStatus(t *testing.T) {
	repo := newTestRepository(t)
	now := time.Now().UTC()

	task := &core.Task{
		ID:           "task-1",
		Slug:         "task-one",
		Prompt:       "prompt",
		DisplayName:  "task one",
		RepoRoot:     "/tmp/repo",
		RepoName:     "repo",
		BranchName:   "feat/task-one",
		WorktreePath: "/tmp/repo-task-one",
		TmuxSession:  "repo_task_one",
		Provider:     core.ProviderCodex,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := repo.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	if err := repo.UpsertTaskStatus(context.Background(), core.TaskStatusUpdate{
		TaskID:       "task-1",
		Provider:     core.ProviderCodex,
		Phase:        core.TaskStatusPhaseWorking,
		RawEventName: "PostToolUse",
		ObservedAt:   now,
	}); err != nil {
		t.Fatalf("upsert status: %v", err)
	}

	if err := repo.DeleteTask(context.Background(), "task-1"); err != nil {
		t.Fatalf("delete task: %v", err)
	}

	tasks, err := repo.ListTasks(context.Background())
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("expected no tasks after delete, got %#v", tasks)
	}

	got, err := repo.LatestTaskStatus(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("latest task status: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil latest status after delete, got %#v", got)
	}
}

func TestRepositoryUpsertTaskStatus_PersistsLatestAndPublishesToSubscribers(t *testing.T) {
	repo := newTestRepository(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	task := &core.Task{
		ID:           "task-1",
		Slug:         "task-one",
		Prompt:       "prompt",
		DisplayName:  "task one",
		RepoRoot:     "/tmp/repo",
		RepoName:     "repo",
		BranchName:   "feat/task-one",
		WorktreePath: "/tmp/repo-task-one",
		TmuxSession:  "repo_task_one",
		Provider:     core.ProviderCodex,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	if err := repo.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	updates, err := repo.SubscribeTaskStatus(ctx, "task-1")
	if err != nil {
		t.Fatalf("subscribe task status: %v", err)
	}

	first := core.TaskStatusUpdate{
		TaskID:       "task-1",
		Provider:     core.ProviderCodex,
		Phase:        core.TaskStatusPhaseStarting,
		RawEventName: "SessionStart",
		ObservedAt:   time.Date(2026, time.April, 19, 12, 0, 0, 0, time.UTC),
	}
	second := core.TaskStatusUpdate{
		TaskID:       "task-1",
		Provider:     core.ProviderCodex,
		Phase:        core.TaskStatusPhaseWaitingForInput,
		RawEventName: "Stop",
		ObservedAt:   time.Date(2026, time.April, 19, 12, 1, 0, 0, time.UTC),
	}

	if err := repo.UpsertTaskStatus(context.Background(), first); err != nil {
		t.Fatalf("upsert first status: %v", err)
	}
	select {
	case got := <-updates:
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("unexpected first update:\n got: %#v\nwant: %#v", got, first)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first update")
	}

	if err := repo.UpsertTaskStatus(context.Background(), second); err != nil {
		t.Fatalf("upsert second status: %v", err)
	}
	select {
	case got := <-updates:
		if !reflect.DeepEqual(got, second) {
			t.Fatalf("unexpected second update:\n got: %#v\nwant: %#v", got, second)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for second update")
	}

	got, err := repo.LatestTaskStatus(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("latest task status: %v", err)
	}
	if got == nil || !reflect.DeepEqual(*got, second) {
		t.Fatalf("unexpected latest status:\n got: %#v\nwant: %#v", got, second)
	}
}

func TestRepositorySubscribeTaskStatus_ClosesChannelWhenContextCancelled(t *testing.T) {
	repo := newTestRepository(t)
	ctx, cancel := context.WithCancel(context.Background())

	updates, err := repo.SubscribeTaskStatus(ctx, "task-1")
	if err != nil {
		t.Fatalf("subscribe task status: %v", err)
	}

	cancel()

	select {
	case _, ok := <-updates:
		if ok {
			t.Fatal("expected closed updates channel")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for subscription channel to close")
	}
}

func TestRepositoryLatestTaskStatus_ReturnsNilWhenTaskHasNoStatus(t *testing.T) {
	repo := newTestRepository(t)

	got, err := repo.LatestTaskStatus(context.Background(), "missing")
	if err != nil {
		t.Fatalf("latest task status: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil latest status, got %#v", got)
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

func TestRepositoryNew_ResetsDisposableDBWhenSchemaIsStale(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open stale db: %v", err)
	}
	_, err = db.Exec(`
		create table tasks (
			id text primary key,
			slug text not null,
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

	repo := newTestRepositoryAtPath(t, path)
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
		Provider:     core.ProviderCodex,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}

	if err := repo.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("create task after reset: %v", err)
	}
}

func TestRepositoryNew_CreatesSchemaForTasksAndLatestStatuses(t *testing.T) {
	repo := newTestRepository(t)

	names := tableColumnNames(t, repo.db, "tasks")
	wantTasks := []string{
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
		"created_at",
		"updated_at",
	}
	if !reflect.DeepEqual(names, wantTasks) {
		t.Fatalf("unexpected tasks columns:\n got: %#v\nwant: %#v", names, wantTasks)
	}

	statusNames := tableColumnNames(t, repo.db, "task_status")
	wantStatus := []string{
		"task_id",
		"provider",
		"phase",
		"raw_event_name",
		"observed_at",
	}
	if !reflect.DeepEqual(statusNames, wantStatus) {
		t.Fatalf("unexpected task_status columns:\n got: %#v\nwant: %#v", statusNames, wantStatus)
	}
}

func newTestRepository(t *testing.T) *repository {
	t.Helper()
	return newTestRepositoryAtPath(t, filepath.Join(t.TempDir(), "state.db"))
}

func newTestRepositoryAtPath(t *testing.T, path string) *repository {
	t.Helper()

	repo, err := New(Config{Path: path})
	if err != nil {
		t.Fatalf("new repository: %v", err)
	}

	concrete, ok := repo.(*repository)
	if !ok {
		t.Fatalf("expected concrete repository, got %T", repo)
	}
	return concrete
}

func tableColumnNames(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()

	rows, err := db.QueryContext(context.Background(), "pragma table_info("+table+")")
	if err != nil {
		t.Fatalf("table info %s: %v", table, err)
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
			t.Fatalf("scan table info %s: %v", table, err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("table info rows %s: %v", table, err)
	}
	return names
}
