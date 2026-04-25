package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"rig/internal/core"

	"github.com/stretchr/testify/require"
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

func TestRepositoryHealthCheck_VerifiesInitializedDatabase(t *testing.T) {
	repo := newTestRepository(t)

	require.NoError(t, repo.HealthCheck(context.Background()))
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

func TestRepositoryRecordTaskActivityAndGetTaskActivity_ReturnsNewestWindowOldestFirst(t *testing.T) {
	repo := newTestRepository(t)
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
	require.NoError(t, repo.CreateTask(context.Background(), task))

	first := core.TaskActivityEvent{
		TaskID:     task.ID,
		TurnID:     "turn-1",
		EventName:  "UserPromptSubmit",
		Role:       core.TaskActivityRoleUser,
		Text:       "bring back the preview",
		ObservedAt: time.Date(2026, time.April, 23, 10, 0, 0, 0, time.UTC),
	}
	second := core.TaskActivityEvent{
		TaskID:     task.ID,
		TurnID:     "turn-1",
		EventName:  "PostToolUse",
		Role:       core.TaskActivityRoleAssistant,
		Text:       "rg -n message preview",
		ObservedAt: time.Date(2026, time.April, 23, 10, 0, 30, 0, time.UTC),
	}
	third := core.TaskActivityEvent{
		TaskID:     task.ID,
		TurnID:     "turn-1",
		EventName:  "Stop",
		Role:       core.TaskActivityRoleAssistant,
		Text:       "Restored the task detail activity block.",
		ObservedAt: time.Date(2026, time.April, 23, 10, 1, 0, 0, time.UTC),
	}

	require.NoError(t, repo.RecordTaskActivity(context.Background(), first))
	require.NoError(t, repo.RecordTaskActivity(context.Background(), second))
	require.NoError(t, repo.RecordTaskActivity(context.Background(), third))

	got, err := repo.GetTaskActivity(context.Background(), task.ID, 2)
	require.NoError(t, err)
	require.Equal(t, []core.TaskActivityEvent{second, third}, got)
}

func TestRepositoryGetTaskActivity_FiltersRequestedTaskID(t *testing.T) {
	repo := newTestRepository(t)
	firstTask := &core.Task{
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
	secondTask := &core.Task{
		ID:           "task-2",
		Slug:         "task-two",
		Prompt:       "prompt",
		DisplayName:  "task two",
		RepoRoot:     "/tmp/repo",
		RepoName:     "repo",
		BranchName:   "feat/task-two",
		WorktreePath: "/tmp/repo-task-two",
		TmuxSession:  "repo_task_two",
		Provider:     core.ProviderCodex,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	require.NoError(t, repo.CreateTask(context.Background(), firstTask))
	require.NoError(t, repo.CreateTask(context.Background(), secondTask))

	require.NoError(t, repo.RecordTaskActivity(context.Background(), core.TaskActivityEvent{
		TaskID:     firstTask.ID,
		TurnID:     "turn-1",
		EventName:  "UserPromptSubmit",
		Role:       core.TaskActivityRoleUser,
		Text:       "first task prompt",
		ObservedAt: time.Date(2026, time.April, 23, 10, 0, 0, 0, time.UTC),
	}))
	require.NoError(t, repo.RecordTaskActivity(context.Background(), core.TaskActivityEvent{
		TaskID:     secondTask.ID,
		TurnID:     "turn-2",
		EventName:  "UserPromptSubmit",
		Role:       core.TaskActivityRoleUser,
		Text:       "second task prompt",
		ObservedAt: time.Date(2026, time.April, 23, 10, 1, 0, 0, time.UTC),
	}))

	got, err := repo.GetTaskActivity(context.Background(), firstTask.ID, 10)
	require.NoError(t, err)
	require.Equal(t, []core.TaskActivityEvent{{
		TaskID:     firstTask.ID,
		TurnID:     "turn-1",
		EventName:  "UserPromptSubmit",
		Role:       core.TaskActivityRoleUser,
		Text:       "first task prompt",
		ObservedAt: time.Date(2026, time.April, 23, 10, 0, 0, 0, time.UTC),
	}}, got)
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

func TestRepositoryUpsertAndLatestTaskResumeMetadata(t *testing.T) {
	repo := newTestRepository(t)
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

	first := core.TaskResumeMetadata{
		TaskID:     "task-1",
		Provider:   core.ProviderCodex,
		SessionID:  "sess-1",
		ObservedAt: time.Date(2026, time.April, 20, 10, 0, 0, 0, time.UTC),
	}
	second := core.TaskResumeMetadata{
		TaskID:     "task-1",
		Provider:   core.ProviderCodex,
		SessionID:  "sess-2",
		ObservedAt: time.Date(2026, time.April, 20, 10, 1, 0, 0, time.UTC),
	}

	if err := repo.UpsertTaskResumeMetadata(context.Background(), first); err != nil {
		t.Fatalf("upsert first resume metadata: %v", err)
	}
	if err := repo.UpsertTaskResumeMetadata(context.Background(), second); err != nil {
		t.Fatalf("upsert second resume metadata: %v", err)
	}

	got, err := repo.LatestTaskResumeMetadata(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("latest task resume metadata: %v", err)
	}
	if got == nil || !reflect.DeepEqual(*got, second) {
		t.Fatalf("unexpected latest resume metadata:\n got: %#v\nwant: %#v", got, second)
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
	_, err = db.ExecContext(context.Background(), `
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

	resumeNames := tableColumnNames(t, repo.db, "task_resume_metadata")
	wantResume := []string{
		"task_id",
		"provider",
		"session_id",
		"observed_at",
	}
	if !reflect.DeepEqual(resumeNames, wantResume) {
		t.Fatalf("unexpected task_resume_metadata columns:\n got: %#v\nwant: %#v", resumeNames, wantResume)
	}

	activityNames := tableColumnNames(t, repo.db, "task_activity")
	wantActivity := []string{
		"id",
		"task_id",
		"turn_id",
		"event_name",
		"role",
		"text",
		"observed_at",
	}
	if !reflect.DeepEqual(activityNames, wantActivity) {
		t.Fatalf("unexpected task_activity columns:\n got: %#v\nwant: %#v", activityNames, wantActivity)
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
