package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/BaronBonet/rig/internal/core"

	"github.com/pressly/goose/v3"
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
		ID:             "task-1",
		Slug:           "duplicate-name",
		Prompt:         "first prompt",
		DisplayName:    "duplicate name",
		RepoRoot:       "/tmp/repo",
		RepoName:       "repo",
		BranchName:     "feat/one",
		WorktreePath:   "/tmp/repo-one",
		TmuxSession:    "repo_one",
		Provider:       core.ProviderCodex,
		CreationStatus: core.TaskCreationStatusReady,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	second := &core.Task{
		ID:             "task-2",
		Slug:           "duplicate-name-2",
		Prompt:         "second prompt",
		DisplayName:    "duplicate name",
		RepoRoot:       "/tmp/repo",
		RepoName:       "repo",
		BranchName:     "feat/two",
		WorktreePath:   "/tmp/repo-two",
		TmuxSession:    "repo_two",
		Provider:       core.ProviderCodex,
		CreationStatus: core.TaskCreationStatusReady,
		CreatedAt:      now.Add(time.Second),
		UpdatedAt:      now.Add(time.Second),
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
		ID:             "task-1",
		Slug:           "task-name",
		Prompt:         "first prompt",
		DisplayName:    "task name",
		RepoRoot:       "/tmp/repo",
		RepoName:       "repo",
		BranchName:     "feat/task-name",
		WorktreePath:   "/tmp/repo-task-name",
		TmuxSession:    "repo_task_name",
		Provider:       core.ProviderCodex,
		CreationStatus: core.TaskCreationStatusReady,
		CreatedAt:      now,
		UpdatedAt:      now,
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

func TestRepositoryUpdateTask_PersistsCreationFailureMetadata(t *testing.T) {
	repo := newTestRepository(t)
	now := time.Now().UTC()

	task := &core.Task{
		ID:             "task-1",
		Slug:           "task-name",
		Prompt:         "first prompt",
		DisplayName:    "task name",
		RepoRoot:       "/tmp/repo",
		RepoName:       "repo",
		BranchName:     "feat/task-name",
		WorktreePath:   "/tmp/repo-task-name",
		TmuxSession:    "repo_task_name",
		Provider:       core.ProviderCodex,
		CreationStatus: core.TaskCreationStatusCreating,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	require.NoError(t, repo.CreateTask(context.Background(), task))

	task.CreationStatus = core.TaskCreationStatusFailed
	task.CreationStep = core.TaskCreateProgressPreparingWorkspace
	task.CreationError = "setup workspace: docker daemon unavailable"
	task.UpdatedAt = now.Add(time.Minute)
	require.NoError(t, repo.UpdateTask(context.Background(), task))

	tasks, err := repo.ListTasks(context.Background())
	require.NoError(t, err)
	require.Equal(t, []*core.Task{task}, tasks)
}

func TestRepositoryDeleteTask_CascadesTaskProviderSessions(t *testing.T) {
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
	require.NoError(t, repo.CreateTask(context.Background(), task))
	require.NoError(t, repo.UpsertTaskProviderSession(context.Background(), core.TaskProviderSession{
		TaskID:            "task-1",
		Provider:          core.ProviderCodex,
		ProviderSessionID: "sess-a",
		TranscriptPath:    "/tmp/codex-a.jsonl",
		FirstObservedAt:   now,
		LastObservedAt:    now,
		LastEventName:     "SessionStart",
	}))

	require.NoError(t, repo.DeleteTask(context.Background(), "task-1"))

	got, err := repo.ListTaskProviderSessions(context.Background(), "task-1")
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestRepositoryUpsertAndListTaskProviderSessions(t *testing.T) {
	repo := newTestRepository(t)
	now := time.Date(2026, time.April, 25, 10, 0, 0, 0, time.UTC)

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
	require.NoError(t, repo.CreateTask(context.Background(), task))

	first := core.TaskProviderSession{
		TaskID:            "task-1",
		Provider:          core.ProviderCodex,
		ProviderSessionID: "sess-a",
		TranscriptPath:    "/tmp/codex-a.jsonl",
		StartSource:       "startup",
		Model:             "gpt-5-codex",
		Cwd:               "/tmp/repo-task-one",
		FirstObservedAt:   now,
		LastObservedAt:    now,
		LastEventName:     "SessionStart",
	}
	updatedFirst := first
	updatedFirst.LastObservedAt = now.Add(2 * time.Minute)
	updatedFirst.LastEventName = "Stop"
	second := core.TaskProviderSession{
		TaskID:            "task-1",
		Provider:          core.ProviderCodex,
		ProviderSessionID: "sess-b",
		TranscriptPath:    "/tmp/codex-b.jsonl",
		StartSource:       "resume",
		Model:             "gpt-5-codex",
		Cwd:               "/tmp/repo-task-one",
		FirstObservedAt:   now.Add(time.Minute),
		LastObservedAt:    now.Add(time.Minute),
		LastEventName:     "SessionStart",
	}

	require.NoError(t, repo.UpsertTaskProviderSession(context.Background(), first))
	require.NoError(t, repo.UpsertTaskProviderSession(context.Background(), updatedFirst))
	require.NoError(t, repo.UpsertTaskProviderSession(context.Background(), second))

	got, err := repo.ListTaskProviderSessions(context.Background(), "task-1")
	require.NoError(t, err)
	require.Equal(t, []core.TaskProviderSession{updatedFirst, second}, got)
}

func TestRepositoryUpsertTaskProviderSession_PreservesRichMetadataFromSparseEvents(t *testing.T) {
	repo := newTestRepository(t)
	now := time.Date(2026, time.April, 25, 10, 0, 0, 0, time.UTC)

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
	require.NoError(t, repo.CreateTask(context.Background(), task))

	rich := core.TaskProviderSession{
		TaskID:            "task-1",
		Provider:          core.ProviderCodex,
		ProviderSessionID: "sess-a",
		TranscriptPath:    "/tmp/codex-a.jsonl",
		StartSource:       "startup",
		Model:             "gpt-5-codex",
		Cwd:               "/tmp/repo-task-one",
		FirstObservedAt:   now,
		LastObservedAt:    now.Add(time.Minute),
		LastEventName:     "SessionStart",
	}
	sparseOlder := rich
	sparseOlder.StartSource = ""
	sparseOlder.Model = ""
	sparseOlder.Cwd = ""
	sparseOlder.LastObservedAt = now.Add(-time.Minute)
	sparseOlder.LastEventName = "PreToolUse"

	emptyThenFilled := core.TaskProviderSession{
		TaskID:            "task-1",
		Provider:          core.ProviderCodex,
		ProviderSessionID: "sess-b",
		TranscriptPath:    "/tmp/codex-b.jsonl",
		FirstObservedAt:   now,
		LastObservedAt:    now,
		LastEventName:     "PreToolUse",
	}
	filledLater := emptyThenFilled
	filledLater.StartSource = "resume"
	filledLater.Model = "gpt-5-codex"
	filledLater.Cwd = "/tmp/repo-task-one"
	filledLater.LastObservedAt = now.Add(2 * time.Minute)
	filledLater.LastEventName = "Stop"

	require.NoError(t, repo.UpsertTaskProviderSession(context.Background(), rich))
	require.NoError(t, repo.UpsertTaskProviderSession(context.Background(), sparseOlder))
	require.NoError(t, repo.UpsertTaskProviderSession(context.Background(), emptyThenFilled))
	require.NoError(t, repo.UpsertTaskProviderSession(context.Background(), filledLater))

	got, err := repo.ListTaskProviderSessions(context.Background(), "task-1")
	require.NoError(t, err)
	require.Equal(t, []core.TaskProviderSession{rich, filledLater}, got)
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

func TestRepositoryNew_ReturnsErrorForInvalidConfig(t *testing.T) {
	repo, err := New(Config{Path: "state.db"})
	if err == nil {
		t.Fatal("expected constructor error for invalid config")
	}
	if repo != nil {
		t.Fatalf("expected nil repository on constructor error, got %T", repo)
	}
}

func TestRepositoryNew_CreatesPrivateDataDirectoryAndDatabaseFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rig-state", "state.db")

	repo := newTestRepositoryAtPath(t, path)
	require.NoError(t, repo.db.Close())

	dirInfo, err := os.Stat(filepath.Dir(path))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), dirInfo.Mode().Perm())

	dbInfo, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), dbInfo.Mode().Perm())
}

func TestRepositoryNew_ReopensMigratedDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")

	repo := newTestRepositoryAtPath(t, path)
	require.NoError(t, repo.db.Close())

	reopened, err := New(Config{Path: path})
	require.NoError(t, err)
	require.NotNil(t, reopened)
}

func TestRepositoryNew_MigratesDatabaseWithSquashedMigrationHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	repo := newTestRepositoryAtPath(t, path)

	// Rewind to a database created before migrations were squashed into
	// 00001_init.sql: it records goose versions 2-5 as applied, still has the
	// original task_status table, and has no agent sessions.
	for _, statement := range slices.Concat(dropAgentSessionsStatements, dropActivityAgentSessionStatements, []string{
		originalTaskStatusTable,
		originalTaskResumeMetadataTable,
		"delete from goose_db_version where version_id > 1",
		"insert into goose_db_version (version_id, is_applied) values (2, 1), (3, 1), (4, 1), (5, 1)",
	}) {
		_, err := repo.db.ExecContext(context.Background(), statement)
		require.NoError(t, err, statement)
	}
	require.NoError(t, repo.db.Close())

	reopened := newTestRepositoryAtPath(t, path)

	require.Empty(t, tableColumnNames(t, reopened.db, "task_status"))
	require.Contains(t, tableColumnNames(t, reopened.db, "agent_sessions"), "provider_session_id")
}

// originalTaskResumeMetadataTable is the task_resume_metadata table as
// 00001_init.sql created it, before 00011 made its rows agent sessions.
const originalTaskResumeMetadataTable = `create table task_resume_metadata (
  task_id text primary key,
  provider text not null,
  session_id text not null,
  observed_at text not null,
  foreign key(task_id) references tasks(id) on delete cascade
)`

// originalTaskStatusTable is the task_status table as 00001_init.sql created
// it, before later migrations added and then dropped columns.
const originalTaskStatusTable = `create table task_status (
  task_id text primary key,
  provider text not null,
  phase text not null,
  raw_event_name text not null,
  observed_at text not null,
  foreign key(task_id) references tasks(id) on delete cascade
)`

var dropAgentSessionsStatements = []string{
	"drop index idx_agent_sessions_task_open",
	"drop index idx_agent_sessions_open_pane",
	"drop table agent_sessions",
}

// dropActivityAgentSessionStatements undo 00009, which records the agent
// session of each activity event.
var dropActivityAgentSessionStatements = []string{
	"drop index idx_task_activity_agent_session",
	"alter table task_activity drop column agent_session_id",
}

// dropAgentSessionLaunchedAtStatements undo 00010, which records when Rig
// launched an agent session's agent.
var dropAgentSessionLaunchedAtStatements = []string{
	"alter table agent_sessions drop column launched_at",
}

func TestRepositoryNew_ReturnsErrorAndPreservesDBWhenSchemaIsStale(t *testing.T) {
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
	_, err = db.ExecContext(context.Background(), `
		insert into tasks (
			id,
			slug,
			prompt,
			display_name,
			repo_root,
			repo_name,
			branch_name,
			worktree_path,
			tmux_session,
			provider,
			status,
			created_at,
			updated_at
		) values (
			'task-1',
			'task-name',
			'preserve this prompt',
			'task name',
			'/tmp/repo',
			'repo',
			'feat/task-name',
			'/tmp/repo-task-name',
			'repo_task_name',
			'codex',
			'ready',
			'2026-04-20T10:00:00Z',
			'2026-04-20T10:00:00Z'
		);
	`)
	if err != nil {
		t.Fatalf("insert stale task: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close stale db: %v", err)
	}

	repo, err := New(Config{Path: path})
	require.Nil(t, repo)
	require.ErrorContains(t, err, "stale sqlite schema")
	require.ErrorContains(t, err, path)

	db, err = sql.Open("sqlite", path)
	require.NoError(t, err)
	defer db.Close()

	var prompt string
	require.NoError(
		t,
		db.QueryRowContext(context.Background(), "select prompt from tasks where id = 'task-1'").Scan(&prompt),
	)
	require.Equal(t, "preserve this prompt", prompt)
}

func TestRepositoryNew_CreatesSchemaForTasksAndTaskState(t *testing.T) {
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
		"creation_status",
		"creation_step",
		"creation_error",
	}
	if !reflect.DeepEqual(names, wantTasks) {
		t.Fatalf("unexpected tasks columns:\n got: %#v\nwant: %#v", names, wantTasks)
	}

	// A task's Runtime status is derived from its agent sessions.
	if statusNames := tableColumnNames(t, repo.db, "task_status"); len(statusNames) != 0 {
		t.Fatalf("expected no task_status table, got columns %#v", statusNames)
	}

	// Reconnect resumes each agent session's own conversation.
	if resumeNames := tableColumnNames(t, repo.db, "task_resume_metadata"); len(resumeNames) != 0 {
		t.Fatalf("expected no task_resume_metadata table, got columns %#v", resumeNames)
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
		"agent_session_id",
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

func createAgentSessionTestTask(t *testing.T, repo *repository, taskID string) {
	t.Helper()
	now := time.Date(2026, time.October, 9, 10, 0, 0, 0, time.UTC)
	require.NoError(t, repo.CreateTask(context.Background(), &core.Task{
		ID:           taskID,
		Slug:         taskID,
		Prompt:       "prompt",
		DisplayName:  taskID,
		RepoRoot:     "/tmp/repo",
		RepoName:     "repo",
		BranchName:   "feat/" + taskID,
		WorktreePath: "/tmp/repo-" + taskID,
		TmuxSession:  "repo_" + taskID,
		Provider:     core.ProviderClaude,
		CreatedAt:    now,
		UpdatedAt:    now,
	}))
}

var agentSessionTestServer = core.TmuxServer{SocketPath: "/private/tmp/tmux-501/default", PID: 4722}

func TestRepositoryAgentSessions_CreateFindListUpdateAndEnd(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	createAgentSessionTestTask(t, repo, "task-1")
	startedAt := time.Date(2026, time.October, 9, 10, 0, 0, 0, time.UTC)

	first := core.AgentSession{
		ID:                "agent-1",
		TaskID:            "task-1",
		Provider:          core.ProviderClaude,
		TmuxServer:        agentSessionTestServer,
		TmuxPane:          "%1",
		PaneTrusted:       true,
		ProviderSessionID: "claude-sess-1",
		StartedAt:         startedAt,
		Status: core.AgentSessionStatus{
			ObservedAt:   startedAt,
			RawEventName: "SessionStart",
			Phase:        core.TaskStatusPhaseStarting,
		},
	}
	// Rig launched the second agent and no hook has come from it yet.
	second := core.AgentSession{
		ID:          "agent-2",
		TaskID:      "task-1",
		Provider:    core.ProviderCodex,
		TmuxServer:  agentSessionTestServer,
		TmuxPane:    "%9",
		PaneTrusted: true,
		StartedAt:   startedAt.Add(time.Minute),
		LaunchedAt:  startedAt.Add(time.Minute),
	}
	require.NoError(t, repo.CreateAgentSession(ctx, second))
	require.NoError(t, repo.CreateAgentSession(ctx, first))

	found, err := repo.OpenAgentSessionOnPane(ctx, agentSessionTestServer, "%1")
	require.NoError(t, err)
	require.Equal(t, &first, found)

	open, err := repo.ListOpenAgentSessions(ctx, "task-1")
	require.NoError(t, err)
	require.Equal(t, []core.AgentSession{first, second}, open)

	first.ProviderSessionID = "claude-sess-2"
	first.Status = core.AgentSessionStatus{
		ObservedAt:     startedAt.Add(2 * time.Minute),
		RawEventName:   "Stop",
		Phase:          core.TaskStatusPhaseWorkingInBackground,
		BackgroundWork: core.TaskBackgroundWork{Subagents: 2, Shells: 1},
	}
	require.NoError(t, repo.UpdateAgentSession(ctx, first))
	found, err = repo.OpenAgentSessionOnPane(ctx, agentSessionTestServer, "%1")
	require.NoError(t, err)
	require.Equal(t, &first, found)

	// The launched agent's first hook gives it a conversation and ends its
	// launch.
	second.ProviderSessionID = "codex-sess-1"
	second.LaunchedAt = time.Time{}
	require.NoError(t, repo.UpdateAgentSession(ctx, second))
	found, err = repo.OpenAgentSessionOnPane(ctx, agentSessionTestServer, "%9")
	require.NoError(t, err)
	require.Equal(t, &second, found)

	endedAt := startedAt.Add(3 * time.Minute)
	require.NoError(t, repo.EndAgentSession(ctx, "agent-1", endedAt))
	found, err = repo.OpenAgentSessionOnPane(ctx, agentSessionTestServer, "%1")
	require.NoError(t, err)
	require.Nil(t, found)
	open, err = repo.ListOpenAgentSessions(ctx, "task-1")
	require.NoError(t, err)
	require.Equal(t, []core.AgentSession{second}, open)

	// An ended agent session stays as it ended.
	first.Status.Phase = core.TaskStatusPhaseWorking
	require.NoError(t, repo.UpdateAgentSession(ctx, first))
	require.NoError(t, repo.EndAgentSession(ctx, "agent-1", endedAt.Add(time.Hour)))
	var phase, ended string
	require.NoError(t, repo.db.QueryRowContext(ctx,
		"select status_phase, ended_at from agent_sessions where id = 'agent-1'").Scan(&phase, &ended))
	require.Equal(t, string(core.TaskStatusPhaseWorkingInBackground), phase)
	require.Equal(t, endedAt.Format(time.RFC3339Nano), ended)
}

// An agent session Rig moves to a new pane and launches its agent in again
// records the launch, and its first hook event there clears it.
func TestRepositoryUpdateAgentSession_SetsAndClearsTheLaunchTime(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	createAgentSessionTestTask(t, repo, "task-1")
	startedAt := time.Date(2026, time.October, 9, 10, 0, 0, 0, time.UTC)
	session := core.AgentSession{
		ID:                "agent-1",
		TaskID:            "task-1",
		Provider:          core.ProviderClaude,
		TmuxServer:        agentSessionTestServer,
		TmuxPane:          "%1",
		PaneTrusted:       true,
		ProviderSessionID: "claude-sess-1",
		StartedAt:         startedAt,
	}
	require.NoError(t, repo.CreateAgentSession(ctx, session))

	session.TmuxPane = "%4"
	session.LaunchedAt = startedAt.Add(time.Hour + 250*time.Millisecond)
	require.NoError(t, repo.UpdateAgentSession(ctx, session))
	open, err := repo.ListOpenAgentSessions(ctx, "task-1")
	require.NoError(t, err)
	require.Equal(t, []core.AgentSession{session}, open)

	session.LaunchedAt = time.Time{}
	require.NoError(t, repo.UpdateAgentSession(ctx, session))
	open, err = repo.ListOpenAgentSessions(ctx, "task-1")
	require.NoError(t, err)
	require.Equal(t, []core.AgentSession{session}, open)
	var launchedAt string
	require.NoError(t, repo.db.QueryRowContext(ctx,
		"select launched_at from agent_sessions where id = 'agent-1'").Scan(&launchedAt))
	require.Empty(t, launchedAt)
}

func TestRepositoryListOpenAgentSessions_OrdersByStartTimeWithinOneSecond(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	createAgentSessionTestTask(t, repo, "task-1")
	second := time.Date(2026, time.October, 9, 10, 0, 0, 0, time.UTC)
	// Stored as "...:00.1Z" and "...:00.15Z", whose text order is reversed.
	for _, session := range []core.AgentSession{
		{ID: "agent-later", StartedAt: second.Add(150 * time.Millisecond), TmuxPane: "%2"},
		{ID: "agent-earlier", StartedAt: second.Add(100 * time.Millisecond), TmuxPane: "%1"},
		{ID: "agent-tie-b", StartedAt: second.Add(200 * time.Millisecond), TmuxPane: "%4"},
		{ID: "agent-tie-a", StartedAt: second.Add(200 * time.Millisecond), TmuxPane: "%3"},
	} {
		session.TaskID = "task-1"
		session.Provider = core.ProviderClaude
		session.TmuxServer = agentSessionTestServer
		require.NoError(t, repo.CreateAgentSession(ctx, session))
	}

	open, err := repo.ListOpenAgentSessions(ctx, "task-1")

	require.NoError(t, err)
	ids := make([]string, 0, len(open))
	for _, session := range open {
		ids = append(ids, session.ID)
	}
	require.Equal(t, []string{"agent-earlier", "agent-later", "agent-tie-a", "agent-tie-b"}, ids)
}

func TestRepositoryCreateAgentSession_AllowsOneOpenAgentSessionPerPane(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	createAgentSessionTestTask(t, repo, "task-1")
	startedAt := time.Date(2026, time.October, 9, 10, 0, 0, 0, time.UTC)
	session := func(id, pane string, server core.TmuxServer) core.AgentSession {
		return core.AgentSession{
			ID:         id,
			TaskID:     "task-1",
			Provider:   core.ProviderCodex,
			TmuxServer: server,
			TmuxPane:   pane,
			StartedAt:  startedAt,
		}
	}

	require.NoError(t, repo.CreateAgentSession(ctx, session("agent-1", "%1", agentSessionTestServer)))
	require.Error(t, repo.CreateAgentSession(ctx, session("agent-2", "%1", agentSessionTestServer)))

	// Pane IDs restart on a new tmux server, so the same ID there is another pane.
	restarted := core.TmuxServer{SocketPath: agentSessionTestServer.SocketPath, PID: 9001}
	require.NoError(t, repo.CreateAgentSession(ctx, session("agent-3", "%1", restarted)))

	// Agent sessions without a pane never conflict.
	require.NoError(t, repo.CreateAgentSession(ctx, session("agent-4", "", core.TmuxServer{})))
	require.NoError(t, repo.CreateAgentSession(ctx, session("agent-5", "", core.TmuxServer{})))

	// Once the pane's agent session ends, the pane can open another.
	require.NoError(t, repo.EndAgentSession(ctx, "agent-1", startedAt.Add(time.Minute)))
	require.NoError(t, repo.CreateAgentSession(ctx, session("agent-6", "%1", agentSessionTestServer)))

	found, err := repo.OpenAgentSessionOnPane(ctx, agentSessionTestServer, "%1")
	require.NoError(t, err)
	require.Equal(t, "agent-6", found.ID)
	found, err = repo.OpenAgentSessionOnPane(ctx, core.TmuxServer{}, "")
	require.NoError(t, err)
	require.Nil(t, found)
}

func TestRepositoryDeleteTask_CascadesAgentSessions(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	createAgentSessionTestTask(t, repo, "task-1")
	require.NoError(t, repo.CreateAgentSession(ctx, core.AgentSession{
		ID:         "agent-1",
		TaskID:     "task-1",
		Provider:   core.ProviderClaude,
		TmuxServer: agentSessionTestServer,
		TmuxPane:   "%1",
		StartedAt:  time.Date(2026, time.October, 9, 10, 0, 0, 0, time.UTC),
	}))

	require.NoError(t, repo.DeleteTask(ctx, "task-1"))

	var remaining int
	require.NoError(t, repo.db.QueryRowContext(ctx, "select count(*) from agent_sessions").Scan(&remaining))
	require.Zero(t, remaining)
}

func TestRepositoryNew_AddsAgentSessionsToAnExistingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	repo := newTestRepositoryAtPath(t, path)
	createAgentSessionTestTask(t, repo, "task-1")

	// Rewind to a database migrated before agent sessions existed, when tasks
	// still had a status row.
	for _, statement := range slices.Concat(dropAgentSessionsStatements, dropActivityAgentSessionStatements, []string{
		originalTaskStatusTable,
		originalTaskResumeMetadataTable,
		"delete from goose_db_version where version_id >= 6",
	}) {
		_, err := repo.db.ExecContext(context.Background(), statement)
		require.NoError(t, err, statement)
	}
	require.NoError(t, repo.db.Close())

	reopened := newTestRepositoryAtPath(t, path)

	tasks, err := reopened.ListTasks(context.Background())
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.Equal(t, []string{
		"id",
		"task_id",
		"provider",
		"tmux_socket_path",
		"tmux_server_pid",
		"tmux_pane",
		"pane_trusted",
		"provider_session_id",
		"started_at",
		"ended_at",
		"status_phase",
		"status_raw_event_name",
		"status_observed_at",
		"background_subagents",
		"background_shells",
		"background_monitors",
		"background_workflows",
		"background_other",
		"launched_at",
	}, tableColumnNames(t, reopened.db, "agent_sessions"))
	require.NoError(t, reopened.CreateAgentSession(context.Background(), core.AgentSession{
		ID:         "agent-1",
		TaskID:     "task-1",
		Provider:   core.ProviderClaude,
		TmuxServer: agentSessionTestServer,
		TmuxPane:   "%1",
	}))
}

func TestRepositoryNew_DropsTaskStatusFromAnExistingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	repo := newTestRepositoryAtPath(t, path)
	createAgentSessionTestTask(t, repo, "task-1")

	// Rewind to a database migrated before task status moved to agent
	// sessions, with a status row for the task.
	for _, statement := range slices.Concat(dropAgentSessionLaunchedAtStatements, dropActivityAgentSessionStatements, []string{
		originalTaskStatusTable,
		originalTaskResumeMetadataTable,
		"insert into task_status (task_id, provider, phase, raw_event_name, observed_at) " +
			"values ('task-1', 'claude', 'working', 'PostToolUse', '2026-10-09T10:00:00Z')",
		"delete from goose_db_version where version_id >= 8",
	}) {
		_, err := repo.db.ExecContext(context.Background(), statement)
		require.NoError(t, err, statement)
	}
	require.NoError(t, repo.db.Close())

	reopened := newTestRepositoryAtPath(t, path)

	require.Empty(t, tableColumnNames(t, reopened.db, "task_status"))
	tasks, err := reopened.ListTasks(context.Background())
	require.NoError(t, err)
	require.Len(t, tasks, 1)
}

func TestRepositoryNew_KeepsAgentSessionsOfAnExistingDatabaseAsHookOpened(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	repo := newTestRepositoryAtPath(t, path)
	ctx := context.Background()
	createAgentSessionTestTask(t, repo, "task-1")

	// Rewind to a database migrated before Rig recorded agent launches, with
	// an open agent session a hook event opened.
	for _, statement := range slices.Concat(dropAgentSessionLaunchedAtStatements, []string{
		originalTaskResumeMetadataTable,
		"insert into agent_sessions (id, task_id, provider, tmux_socket_path, tmux_server_pid, tmux_pane, " +
			"pane_trusted, provider_session_id, started_at) values ('agent-1', 'task-1', 'claude', " +
			"'/private/tmp/tmux-501/default', 4722, '%1', 1, 'claude-sess-1', '2026-10-09T10:00:00Z')",
		"delete from goose_db_version where version_id >= 10",
	}) {
		_, err := repo.db.ExecContext(ctx, statement)
		require.NoError(t, err, statement)
	}
	require.NoError(t, repo.db.Close())

	reopened := newTestRepositoryAtPath(t, path)

	open, err := reopened.ListOpenAgentSessions(ctx, "task-1")
	require.NoError(t, err)
	require.Equal(t, []core.AgentSession{{
		ID:                "agent-1",
		TaskID:            "task-1",
		Provider:          core.ProviderClaude,
		TmuxServer:        agentSessionTestServer,
		TmuxPane:          "%1",
		PaneTrusted:       true,
		ProviderSessionID: "claude-sess-1",
		StartedAt:         time.Date(2026, time.October, 9, 10, 0, 0, 0, time.UTC),
	}}, open)
}

func TestRepositoryLatestAgentSession_ReturnsTheMostRecentlyStartedOpenOrEnded(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	createAgentSessionTestTask(t, repo, "task-1")

	latest, err := repo.LatestAgentSession(ctx, "task-1")
	require.NoError(t, err)
	require.Nil(t, latest)

	second := time.Date(2026, time.October, 9, 10, 0, 0, 0, time.UTC)
	// Stored as RFC 3339 text, .15s sorts before .1s, so only time order
	// finds the newest of these.
	for index, startedAt := range []time.Time{
		second.Add(-time.Minute),
		second.Add(150 * time.Millisecond),
		second.Add(100 * time.Millisecond),
	} {
		require.NoError(t, repo.CreateAgentSession(ctx, core.AgentSession{
			ID:         fmt.Sprintf("agent-%d", index),
			TaskID:     "task-1",
			Provider:   core.ProviderClaude,
			TmuxServer: agentSessionTestServer,
			TmuxPane:   fmt.Sprintf("%%%d", index),
			StartedAt:  startedAt,
		}))
	}
	require.NoError(t, repo.EndAgentSession(ctx, "agent-1", second.Add(time.Minute)))

	latest, err = repo.LatestAgentSession(ctx, "task-1")
	require.NoError(t, err)
	require.NotNil(t, latest)
	require.Equal(t, "agent-1", latest.ID)
	require.False(t, latest.IsOpen())
}

func TestRepositoryRecordTaskActivity_KeepsTheAgentSessionItCameFrom(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	createAgentSessionTestTask(t, repo, "task-1")
	at := time.Date(2026, time.October, 9, 10, 0, 0, 0, time.UTC)

	require.NoError(t, repo.RecordTaskActivity(ctx, core.TaskActivityEvent{
		TaskID:         "task-1",
		AgentSessionID: "agent-1",
		EventName:      core.HookEventUserPromptSubmit,
		Role:           core.TaskActivityRoleUser,
		Text:           "add retries",
		ObservedAt:     at,
	}))
	require.NoError(t, repo.RecordTaskActivity(ctx, core.TaskActivityEvent{
		TaskID:     "task-1",
		EventName:  core.HookEventStop,
		Role:       core.TaskActivityRoleAssistant,
		Text:       "recovered from a transcript",
		ObservedAt: at.Add(time.Minute),
	}))

	events, err := repo.GetTaskActivity(ctx, "task-1", 0)
	require.NoError(t, err)
	require.Len(t, events, 2)
	require.Equal(t, "agent-1", events[0].AgentSessionID)
	require.Empty(t, events[1].AgentSessionID)
}

func TestRepositoryListLatestAgentSessionPrompts_ReturnsEachOpenAgentSessionsNewestPrompt(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	createAgentSessionTestTask(t, repo, "task-1")
	createAgentSessionTestTask(t, repo, "task-2")
	for _, session := range []core.AgentSession{
		{ID: "agent-a", TaskID: "task-1", TmuxPane: "%1"},
		{ID: "agent-b", TaskID: "task-1", TmuxPane: "%2"},
		{ID: "agent-ended", TaskID: "task-1", TmuxPane: "%3"},
		{ID: "agent-silent", TaskID: "task-1", TmuxPane: "%4"},
		{ID: "agent-other", TaskID: "task-2", TmuxPane: "%5"},
	} {
		session.Provider = core.ProviderClaude
		session.TmuxServer = agentSessionTestServer
		require.NoError(t, repo.CreateAgentSession(ctx, session))
	}
	require.NoError(t, repo.EndAgentSession(ctx, "agent-ended", time.Time{}))

	second := time.Date(2026, time.October, 9, 10, 0, 0, 0, time.UTC)
	record := func(taskID, agentSessionID string, role core.TaskActivityRole, text string, at time.Time) {
		require.NoError(t, repo.RecordTaskActivity(ctx, core.TaskActivityEvent{
			TaskID:         taskID,
			AgentSessionID: agentSessionID,
			EventName:      core.HookEventUserPromptSubmit,
			Role:           role,
			Text:           text,
			ObservedAt:     at,
		}))
	}
	// Stored as RFC 3339 text, .15s sorts before .1s, so only time order
	// finds the newest prompt.
	record("task-1", "agent-a", core.TaskActivityRoleUser, "newest", second.Add(150*time.Millisecond))
	record("task-1", "agent-a", core.TaskActivityRoleUser, "older", second.Add(100*time.Millisecond))
	record("task-1", "agent-a", core.TaskActivityRoleUser, "oldest", second.Add(-time.Minute))
	record("task-1", "agent-a", core.TaskActivityRoleAssistant, "not a prompt", second.Add(time.Minute))
	// Prompts observed at the same time go to the one recorded last.
	record("task-1", "agent-b", core.TaskActivityRoleUser, "b's earlier row", second)
	record("task-1", "agent-b", core.TaskActivityRoleUser, "b's prompt", second)
	record("task-1", "agent-ended", core.TaskActivityRoleUser, "ended", second)
	record("task-1", "", core.TaskActivityRoleUser, "unattributed", second.Add(time.Hour))
	record("task-2", "agent-other", core.TaskActivityRoleUser, "other task", second)

	prompts, err := repo.ListLatestAgentSessionPrompts(ctx, "task-1")
	require.NoError(t, err)
	byAgentSession := make(map[string]string, len(prompts))
	for _, prompt := range prompts {
		require.Equal(t, "task-1", prompt.TaskID)
		byAgentSession[prompt.AgentSessionID] = prompt.Text
	}
	require.Equal(t, map[string]string{"agent-a": "newest", "agent-b": "b's prompt"}, byAgentSession)
}

func TestRepositoryNew_AddsAgentSessionsToActivityAndRollsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	repo := newTestRepositoryAtPath(t, path)
	ctx := context.Background()
	createAgentSessionTestTask(t, repo, "task-1")
	require.NoError(t, repo.RecordTaskActivity(ctx, core.TaskActivityEvent{
		TaskID:         "task-1",
		AgentSessionID: "agent-1",
		Role:           core.TaskActivityRoleUser,
		Text:           "add retries",
		ObservedAt:     time.Date(2026, time.October, 9, 10, 0, 0, 0, time.UTC),
	}))

	migrationsFS, err := fs.Sub(sqlFiles, "migrations")
	require.NoError(t, err)
	provider, err := goose.NewProvider(goose.DialectSQLite3, repo.db, migrationsFS)
	require.NoError(t, err)
	_, err = provider.DownTo(ctx, 8)
	require.NoError(t, err)
	require.NotContains(t, tableColumnNames(t, repo.db, "task_activity"), "agent_session_id")
	var text string
	row := repo.db.QueryRowContext(ctx, "select text from task_activity where task_id = 'task-1'")
	require.NoError(t, row.Scan(&text))
	require.Equal(t, "add retries", text)
	require.NoError(t, repo.db.Close())

	reopened := newTestRepositoryAtPath(t, path)
	activity, err := reopened.GetTaskActivity(ctx, "task-1", 0)
	require.NoError(t, err)
	require.Len(t, activity, 1)
	require.Empty(t, activity[0].AgentSessionID)
	require.Contains(t, tableColumnNames(t, reopened.db, "task_activity"), "agent_session_id")
}

func TestRepositoryNew_MakesEachTasksLastConversationAnAgentSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	repo := newTestRepositoryAtPath(t, path)
	ctx := context.Background()
	for _, taskID := range []string{"task-1", "task-2", "task-3"} {
		createAgentSessionTestTask(t, repo, taskID)
	}
	require.NoError(t, repo.CreateAgentSession(ctx, core.AgentSession{
		ID:                "agent-2",
		TaskID:            "task-2",
		Provider:          core.ProviderClaude,
		TmuxServer:        agentSessionTestServer,
		TmuxPane:          "%2",
		PaneTrusted:       true,
		ProviderSessionID: "claude-now",
		StartedAt:         time.Date(2026, time.October, 9, 11, 0, 0, 0, time.UTC),
	}))

	// Rewind to a database migrated before Reconnect restored agent sessions,
	// where task-1 and task-2 have resume metadata for their last
	// conversation.
	for _, statement := range []string{
		originalTaskResumeMetadataTable,
		"insert into task_resume_metadata (task_id, provider, session_id, observed_at) values " +
			"('task-1', 'codex', 'codex-sess-1', '2026-10-09T10:05:00.123456789Z'), " +
			"('task-2', 'claude', 'claude-old', '2026-10-09T09:00:00Z')",
		"delete from goose_db_version where version_id >= 11",
	} {
		_, err := repo.db.ExecContext(ctx, statement)
		require.NoError(t, err, statement)
	}
	require.NoError(t, repo.db.Close())

	reopened := newTestRepositoryAtPath(t, path)

	// task-1 reconnects to its last conversation through an agent session
	// without a pane.
	open, err := reopened.ListOpenAgentSessions(ctx, "task-1")
	require.NoError(t, err)
	require.Equal(t, []core.AgentSession{{
		ID:                "task-1",
		TaskID:            "task-1",
		Provider:          core.ProviderCodex,
		ProviderSessionID: "codex-sess-1",
		StartedAt:         time.Date(2026, time.October, 9, 10, 5, 0, 123456789, time.UTC),
	}}, open)
	// task-2's own agent sessions are better evidence, so it keeps just those.
	open, err = reopened.ListOpenAgentSessions(ctx, "task-2")
	require.NoError(t, err)
	require.Len(t, open, 1)
	require.Equal(t, "agent-2", open[0].ID)
	open, err = reopened.ListOpenAgentSessions(ctx, "task-3")
	require.NoError(t, err)
	require.Empty(t, open)
	require.Empty(t, tableColumnNames(t, reopened.db, "task_resume_metadata"))
}

func TestRepositoryNew_LeavesOutATaskWhoseIDAnAgentSessionHolds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	repo := newTestRepositoryAtPath(t, path)
	ctx := context.Background()
	createAgentSessionTestTask(t, repo, "task-1")
	createAgentSessionTestTask(t, repo, "task-2")
	require.NoError(t, repo.CreateAgentSession(ctx, core.AgentSession{
		ID:        "task-2",
		TaskID:    "task-1",
		Provider:  core.ProviderClaude,
		StartedAt: time.Date(2026, time.October, 9, 10, 0, 0, 0, time.UTC),
	}))
	for _, statement := range []string{
		originalTaskResumeMetadataTable,
		"insert into task_resume_metadata (task_id, provider, session_id, observed_at) values " +
			"('task-2', 'codex', 'codex-sess-2', '2026-10-09T10:05:00Z')",
		"delete from goose_db_version where version_id >= 11",
	} {
		_, err := repo.db.ExecContext(ctx, statement)
		require.NoError(t, err, statement)
	}
	require.NoError(t, repo.db.Close())

	reopened := newTestRepositoryAtPath(t, path)

	open, err := reopened.ListOpenAgentSessions(ctx, "task-2")
	require.NoError(t, err)
	require.Empty(t, open)
}

func TestRepositoryAgentSessions_RollBackToResumeMetadata(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	createAgentSessionTestTask(t, repo, "task-1")
	at := func(minute int) time.Time { return time.Date(2026, time.October, 9, 10, minute, 0, 0, time.UTC) }
	for _, session := range []core.AgentSession{
		{ID: "agent-1", Provider: core.ProviderClaude, ProviderSessionID: "claude-1", TmuxPane: "%1", StartedAt: at(5)},
		{ID: "agent-2", Provider: core.ProviderCodex, ProviderSessionID: "codex-1", TmuxPane: "%2", StartedAt: at(0)},
		// Rig launched it, and it has sent no hook event yet.
		{ID: "agent-3", Provider: core.ProviderClaude, TmuxPane: "%3", StartedAt: at(10)},
	} {
		session.TaskID = "task-1"
		session.TmuxServer = agentSessionTestServer
		require.NoError(t, repo.CreateAgentSession(ctx, session))
	}
	require.NoError(t, repo.EndAgentSession(ctx, "agent-2", at(20)))
	// An agent session whose task is gone, as foreign keys were off when it
	// was deleted, is left out.
	for _, statement := range []string{
		"pragma foreign_keys = off",
		"insert into agent_sessions (id, task_id, provider, provider_session_id, started_at) " +
			"values ('agent-gone', 'task-gone', 'claude', 'claude-gone', '2026-10-09T10:30:00Z')",
		"pragma foreign_keys = on",
	} {
		_, err := repo.db.ExecContext(ctx, statement)
		require.NoError(t, err, statement)
	}

	migrationsFS, err := fs.Sub(sqlFiles, "migrations")
	require.NoError(t, err)
	provider, err := goose.NewProvider(goose.DialectSQLite3, repo.db, migrationsFS)
	require.NoError(t, err)
	_, err = provider.DownTo(ctx, 10)
	require.NoError(t, err)

	// The task's most recently started agent session with a conversation
	// becomes its resume metadata again.
	var count int
	require.NoError(t, repo.db.QueryRowContext(ctx, "select count(*) from task_resume_metadata").Scan(&count))
	require.Equal(t, 1, count)
	var taskID, providerName, sessionID string
	row := repo.db.QueryRowContext(ctx, "select task_id, provider, session_id from task_resume_metadata")
	require.NoError(t, row.Scan(&taskID, &providerName, &sessionID))
	require.Equal(t, []string{"task-1", "claude", "claude-1"}, []string{taskID, providerName, sessionID})
}

func TestRepositoryNew_MigratesOnlyResumeMetadataItCanResume(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	repo := newTestRepositoryAtPath(t, path)
	ctx := context.Background()
	for _, taskID := range []string{"task-1", "task-2", "task-3"} {
		createAgentSessionTestTask(t, repo, taskID)
	}

	// Rewind to before 00011. task-1's resume metadata has no time, task-2's
	// no provider and task-3's no conversation; the last row's task is gone,
	// as foreign keys were off when it was deleted.
	for _, statement := range []string{
		originalTaskResumeMetadataTable,
		"pragma foreign_keys = off",
		"insert into task_resume_metadata (task_id, provider, session_id, observed_at) values " +
			"('task-1', 'codex', 'codex-sess-1', ''), " +
			"('task-2', '  ', 'claude-sess-2', '2026-10-09T10:00:00Z'), " +
			"('task-3', 'claude', '  ', '2026-10-09T10:00:00Z'), " +
			"('task-gone', 'claude', 'claude-sess-gone', '2026-10-09T10:00:00Z')",
		"pragma foreign_keys = on",
		"delete from goose_db_version where version_id >= 11",
	} {
		_, err := repo.db.ExecContext(ctx, statement)
		require.NoError(t, err, statement)
	}
	require.NoError(t, repo.db.Close())
	migratedAfter := time.Now().UTC().Add(-time.Second)

	reopened := newTestRepositoryAtPath(t, path)

	open, err := reopened.ListOpenAgentSessions(ctx, "task-1")
	require.NoError(t, err)
	require.Len(t, open, 1)
	require.Equal(t, "codex-sess-1", open[0].ProviderSessionID)
	// With no time recorded, it starts when the migration ran.
	require.False(t, open[0].StartedAt.Before(migratedAfter))
	for _, taskID := range []string{"task-2", "task-3", "task-gone"} {
		open, err := reopened.ListOpenAgentSessions(ctx, taskID)
		require.NoError(t, err)
		require.Empty(t, open, taskID)
	}
}
