package sqlite

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"agent/internal/core"

	"github.com/stretchr/testify/require"
)

func newTestRepository(t *testing.T) *Repository {
	t.Helper()

	repo, err := NewRepository(Config{Path: filepath.Join(t.TempDir(), "state.db")})
	require.NoError(t, err)

	return repo
}

func seedTask(t *testing.T, repo *Repository, task core.Task) *core.Task {
	t.Helper()

	now := time.Date(2026, 4, 8, 9, 0, 0, 0, time.UTC)
	if task.Prompt == "" {
		task.Prompt = "fix the failing test"
	}
	if task.DisplayName == "" {
		task.DisplayName = task.ID
	}
	if task.Slug == "" {
		task.Slug = task.ID
	}
	if task.RepoRoot == "" {
		task.RepoRoot = "/tmp/repo"
	}
	if task.RepoName == "" {
		task.RepoName = "repo"
	}
	if task.BaseBranch == "" {
		task.BaseBranch = "main"
	}
	if task.BranchName == "" {
		task.BranchName = "feat/" + task.Slug
	}
	if task.WorktreePath == "" {
		task.WorktreePath = filepath.Join("/tmp", task.Slug)
	}
	if task.TmuxSession == "" {
		task.TmuxSession = task.Slug
	}
	if task.AgentWindowName == "" {
		task.AgentWindowName = defaultAgentWindowName
	}
	if task.EditorWindowName == "" {
		task.EditorWindowName = defaultEditorWindowName
	}
	if task.Provider == "" {
		task.Provider = "codex"
	}
	if task.Status == "" {
		task.Status = core.TaskStatusRunning
	}
	if task.CreatedAt.IsZero() {
		task.CreatedAt = now
	}
	if task.UpdatedAt.IsZero() {
		task.UpdatedAt = task.CreatedAt
	}

	require.NoError(t, repo.CreateTask(context.Background(), &task))
	return &task
}

func TestRepositoryCreateAndGetTask(t *testing.T) {
	repo := newTestRepository(t)

	task := &core.Task{
		ID:                 "task-1",
		Prompt:             "add billing retry flow",
		DisplayName:        "billing retry flow",
		Slug:               "billing-retry-flow",
		RepoRoot:           "/tmp/repo",
		RepoName:           "repo",
		BaseBranch:         "main",
		BranchName:         "feat/billing-retry-flow",
		WorktreePath:       "/tmp/repo-billing-retry-flow",
		TmuxSession:        "repo-billing-retry-flow",
		AgentWindowName:    "agent",
		EditorWindowName:   "editor",
		Provider:           "codex",
		Status:             core.TaskStatusCreating,
		CreatedAt:          time.Now().UTC(),
		UpdatedAt:          time.Now().UTC(),
		WorktreeExists:     true,
		BranchExists:       true,
		SessionExists:      false,
		AgentWindowExists:  true,
		EditorWindowExists: false,
	}

	require.NoError(t, repo.CreateTask(context.Background(), task))

	got, err := repo.GetTask(context.Background(), "billing-retry-flow")
	require.NoError(t, err)
	require.Equal(t, task.DisplayName, got.DisplayName)
	require.Equal(t, task.BranchName, got.BranchName)
	require.Equal(t, task.Status, got.Status)
	require.Equal(t, task.RepoName, got.RepoName)
	require.Equal(t, task.AgentWindowName, got.AgentWindowName)
	require.Equal(t, task.EditorWindowName, got.EditorWindowName)
	require.Equal(t, task.AgentWindowExists, got.AgentWindowExists)
	require.Equal(t, task.EditorWindowExists, got.EditorWindowExists)
}

func TestRepositoryUpdateTask_RoundTripsNewMetadata(t *testing.T) {
	repo := newTestRepository(t)

	task := &core.Task{
		ID:                 "task-1",
		Prompt:             "add billing retry flow",
		DisplayName:        "billing retry flow",
		Slug:               "billing-retry-flow",
		RepoRoot:           "/tmp/repo",
		RepoName:           "repo",
		BaseBranch:         "main",
		BranchName:         "feat/billing-retry-flow",
		WorktreePath:       "/tmp/repo-billing-retry-flow",
		TmuxSession:        "repo-billing-retry-flow",
		AgentWindowName:    "agent",
		EditorWindowName:   "editor",
		Provider:           "codex",
		Status:             core.TaskStatusCreating,
		CreatedAt:          time.Now().UTC(),
		UpdatedAt:          time.Now().UTC(),
		WorktreeExists:     true,
		BranchExists:       true,
		SessionExists:      false,
		AgentWindowExists:  true,
		EditorWindowExists: false,
	}

	require.NoError(t, repo.CreateTask(context.Background(), task))

	task.RepoName = "repo-updated"
	task.AgentWindowName = "agent-updated"
	task.EditorWindowName = "editor-updated"
	task.AgentWindowExists = false
	task.EditorWindowExists = true
	task.UpdatedAt = time.Now().Add(time.Minute).UTC()

	require.NoError(t, repo.UpdateTask(context.Background(), task))

	got, err := repo.GetTask(context.Background(), task.ID)
	require.NoError(t, err)
	require.Equal(t, task.RepoName, got.RepoName)
	require.Equal(t, task.AgentWindowName, got.AgentWindowName)
	require.Equal(t, task.EditorWindowName, got.EditorWindowName)
	require.Equal(t, task.AgentWindowExists, got.AgentWindowExists)
	require.Equal(t, task.EditorWindowExists, got.EditorWindowExists)
}

func TestRepositoryListTasks_OrdersByCreatedAtAscending(t *testing.T) {
	repo := newTestRepository(t)

	older := &core.Task{
		ID:           "task-1",
		Prompt:       "first prompt",
		DisplayName:  "first task",
		Slug:         "first-task",
		RepoRoot:     "/tmp/repo",
		RepoName:     "repo",
		BaseBranch:   "main",
		BranchName:   "feat/first-task",
		WorktreePath: "/tmp/repo-first-task",
		TmuxSession:  "repo-first-task",
		Provider:     "codex",
		Status:       core.TaskStatusReady,
		CreatedAt:    time.Now().Add(-2 * time.Hour).UTC(),
		UpdatedAt:    time.Now().Add(-2 * time.Hour).UTC(),
	}
	newer := &core.Task{
		ID:           "task-2",
		Prompt:       "second prompt",
		DisplayName:  "second task",
		Slug:         "second-task",
		RepoRoot:     "/tmp/repo",
		RepoName:     "repo",
		BaseBranch:   "main",
		BranchName:   "feat/second-task",
		WorktreePath: "/tmp/repo-second-task",
		TmuxSession:  "repo-second-task",
		Provider:     "codex",
		Status:       core.TaskStatusRunning,
		CreatedAt:    time.Now().Add(-1 * time.Hour).UTC(),
		UpdatedAt:    time.Now().Add(-1 * time.Hour).UTC(),
	}

	require.NoError(t, repo.CreateTask(context.Background(), older))
	require.NoError(t, repo.CreateTask(context.Background(), newer))

	tasks, err := repo.ListTasks(context.Background())
	require.NoError(t, err)
	require.Len(t, tasks, 2)
	require.Equal(t, "first-task", tasks[0].Slug)
	require.Equal(t, "second-task", tasks[1].Slug)
	require.Equal(t, "repo", tasks[0].RepoName)
	require.Equal(t, "repo", tasks[1].RepoName)
}

func TestNewRepository_CreatesParentDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state.db")

	repo, err := NewRepository(Config{Path: path})
	require.NoError(t, err)
	require.NotNil(t, repo)
}

func TestValidateConfig_CreatesParentDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state.db")

	require.NoError(t, ValidateConfig(Config{Path: path}))
	_, err := os.Stat(filepath.Dir(path))
	require.NoError(t, err)
}

func TestValidateConfig_RejectsFileParent(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "blocker")
	require.NoError(t, os.WriteFile(parent, []byte("x"), 0o644))

	err := ValidateConfig(Config{Path: filepath.Join(parent, "state.db")})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a directory")
}

func TestRepository_IsAvailable(t *testing.T) {
	repo, err := NewRepository(Config{Path: filepath.Join(t.TempDir(), "state.db")})
	require.NoError(t, err)

	require.NoError(t, repo.IsAvailable(context.Background()))
}

func TestNewRepository_ReturnsUnavailableRepositoryForInvalidConfig(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "blocker")
	require.NoError(t, os.WriteFile(parent, []byte("x"), 0o644))

	repo, err := NewRepository(Config{Path: filepath.Join(parent, "state.db")})
	require.NoError(t, err)
	require.NotNil(t, repo)

	err = repo.IsAvailable(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a directory")

	_, err = repo.ListTasks(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a directory")
}

func TestNewRepository_MigratesLegacyTaskRow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")

	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)

	_, err = db.ExecContext(context.Background(), `
create table tasks (
  id text primary key,
  prompt text not null,
  display_name text not null,
  slug text not null unique,
  repo_root text not null,
  base_branch text not null,
  branch_name text not null,
  worktree_path text not null,
  tmux_session text not null,
  provider text not null,
  status text not null,
  worktree_exists integer not null,
  branch_exists integer not null,
  session_exists integer not null,
  last_error text not null default '',
  created_at text not null,
  updated_at text not null,
  last_reconciled_at text not null default ''
);
`)
	require.NoError(t, err)
	_, err = db.ExecContext(context.Background(), `
insert into tasks (
  id, prompt, display_name, slug, repo_root, base_branch, branch_name,
  worktree_path, tmux_session, provider, status, worktree_exists,
  branch_exists, session_exists, last_error, created_at, updated_at, last_reconciled_at
) values (
  'legacy-task',
  'legacy prompt',
  'legacy task',
  'legacy-task',
  '/tmp/repo',
  'main',
  'feat/legacy-task',
  '/tmp/repo-legacy-task',
  'repo-legacy-task',
  'codex',
  'ready',
  1,
  0,
  0,
  '',
  '2026-04-04T10:00:00Z',
  '2026-04-04T10:00:00Z',
  ''
);
`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	repo, err := NewRepository(Config{Path: path})
	require.NoError(t, err)
	require.NotNil(t, repo)

	got, err := repo.GetTask(context.Background(), "legacy-task")
	require.NoError(t, err)
	require.Equal(t, "repo", got.RepoName)
	require.Equal(t, defaultAgentWindowName, got.AgentWindowName)
	require.Equal(t, defaultEditorWindowName, got.EditorWindowName)
	require.False(t, got.AgentWindowExists)
	require.False(t, got.EditorWindowExists)

	got.RepoName = "repo-updated"
	got.AgentWindowName = "agent-updated"
	got.EditorWindowName = "editor-updated"
	got.AgentWindowExists = true
	got.EditorWindowExists = true
	got.UpdatedAt = time.Now().UTC()

	require.NoError(t, repo.UpdateTask(context.Background(), got))

	updated, err := repo.GetTask(context.Background(), "legacy-task")
	require.NoError(t, err)
	require.Equal(t, "repo-updated", updated.RepoName)
	require.Equal(t, "agent-updated", updated.AgentWindowName)
	require.Equal(t, "editor-updated", updated.EditorWindowName)
	require.True(t, updated.AgentWindowExists)
	require.True(t, updated.EditorWindowExists)

	db, err = sql.Open("sqlite", path)
	require.NoError(t, err)
	defer db.Close()

	columns := taskTableColumns(t, db)
	for _, column := range []string{
		"repo_name",
		"agent_window_name",
		"editor_window_name",
		"agent_window_exists",
		"editor_window_exists",
	} {
		require.Contains(t, columns, column)
	}
}

func TestNewRepository_CreatesHookObservabilityTables(t *testing.T) {
	repo := newTestRepository(t)

	eventColumns := tableColumns(t, repo.db, "task_hook_events")
	for _, column := range []string{
		"id",
		"task_id",
		"session_id",
		"turn_id",
		"event_name",
		"occurred_at",
		"raw_payload_json",
		"last_assistant_message",
		"prompt_preview",
		"command_preview",
		"command_result_preview",
		"tool_use_id",
	} {
		require.Contains(t, eventColumns, column)
	}

	sessionColumns := tableColumns(t, repo.db, "task_hook_sessions")
	for _, column := range []string{
		"task_id",
		"session_id",
		"model",
		"cwd",
		"transcript_path",
		"start_source",
		"current_turn_id",
		"last_event_name",
		"runtime_phase",
		"started_at",
		"last_activity_at",
		"last_stop_at",
		"last_prompt_preview",
		"last_command_preview",
		"last_command_result_preview",
		"last_assistant_message",
		"command_count",
		"updated_at",
	} {
		require.Contains(t, sessionColumns, column)
	}
}

func TestRepositoryIngestHookEvent_UpdatesSummaryForRunningCommand(t *testing.T) {
	repo := newTestRepository(t)
	task := seedTask(t, repo, core.Task{
		ID:           "task-1",
		Slug:         "task-1",
		DisplayName:  "task 1",
		WorktreePath: "/tmp/repo-task-1",
		Provider:     "codex",
		Status:       core.TaskStatusRunning,
	})

	summary, err := repo.IngestHookEvent(context.Background(), core.HookEventInput{
		Cwd:            task.WorktreePath,
		EventName:      "PreToolUse",
		SessionID:      "sess-1",
		TurnID:         "turn-1",
		ToolUseID:      "tool-1",
		CommandText:    "go test ./...",
		OccurredAt:     time.Date(2026, 4, 8, 10, 0, 0, 0, time.UTC),
		RawPayloadJSON: `{"hook_event_name":"PreToolUse"}`,
	})
	require.NoError(t, err)
	require.NotNil(t, summary)
	require.Equal(t, task.ID, summary.TaskID)
	require.Equal(t, core.HookRuntimePhaseRunningCommand, summary.RuntimePhase)
	require.Equal(t, "go test ./...", summary.LastCommandText)
	require.Equal(t, 1, summary.CommandCount)

	summaries, err := repo.ListHookSessionSummaries(context.Background(), []string{task.ID})
	require.NoError(t, err)
	require.Contains(t, summaries, task.ID)
	require.Equal(t, core.HookRuntimePhaseRunningCommand, summaries[task.ID].RuntimePhase)
	require.Equal(t, "go test ./...", summaries[task.ID].LastCommandText)
	require.Equal(t, 1, summaries[task.ID].CommandCount)

	events, err := repo.ListHookEvents(context.Background(), task.ID, 10)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "PreToolUse", events[0].EventName)
	require.Equal(t, task.ID, events[0].TaskID)
	require.Equal(t, "go test ./...", events[0].CommandText)
	require.Equal(t, `{"hook_event_name":"PreToolUse"}`, events[0].RawPayloadJSON)
}

func TestRepositoryIngestHookEvent_MapsTaskBySessionID(t *testing.T) {
	repo := newTestRepository(t)
	task := seedTask(t, repo, core.Task{
		ID:           "task-1",
		Slug:         "task-1",
		DisplayName:  "task 1",
		WorktreePath: "/tmp/repo-task-1",
	})

	_, err := repo.IngestHookEvent(context.Background(), core.HookEventInput{
		Cwd:            task.WorktreePath,
		EventName:      "SessionStart",
		SessionID:      "sess-1",
		Model:          "gpt-5",
		TranscriptPath: "/tmp/transcript.jsonl",
		StartSource:    "startup",
		OccurredAt:     time.Date(2026, 4, 8, 10, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)

	summary, err := repo.IngestHookEvent(context.Background(), core.HookEventInput{
		EventName:            "Stop",
		SessionID:            "sess-1",
		TurnID:               "turn-1",
		LastAssistantMessage: "I finished the change",
		OccurredAt:           time.Date(2026, 4, 8, 10, 1, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.NotNil(t, summary)
	require.Equal(t, task.ID, summary.TaskID)
	require.Equal(t, core.HookRuntimePhaseIdle, summary.RuntimePhase)
	require.Equal(t, "I finished the change", summary.LastAssistantMessage)
}

func TestRepositoryListHookEvents_OrdersLatestFirst(t *testing.T) {
	repo := newTestRepository(t)
	task := seedTask(t, repo, core.Task{
		ID:           "task-1",
		Slug:         "task-1",
		DisplayName:  "task 1",
		WorktreePath: "/tmp/repo-task-1",
	})

	for _, input := range []core.HookEventInput{
		{
			Cwd:        task.WorktreePath,
			EventName:  "UserPromptSubmit",
			SessionID:  "sess-1",
			TurnID:     "turn-1",
			PromptText: "fix test A",
			OccurredAt: time.Date(2026, 4, 8, 10, 0, 0, 0, time.UTC),
		},
		{
			Cwd:                  task.WorktreePath,
			EventName:            "Stop",
			SessionID:            "sess-1",
			TurnID:               "turn-1",
			LastAssistantMessage: "done",
			OccurredAt:           time.Date(2026, 4, 8, 10, 1, 0, 0, time.UTC),
		},
	} {
		_, err := repo.IngestHookEvent(context.Background(), input)
		require.NoError(t, err)
	}

	events, err := repo.ListHookEvents(context.Background(), task.ID, 10)
	require.NoError(t, err)
	require.Len(t, events, 2)
	require.Equal(t, "Stop", events[0].EventName)
	require.Equal(t, "UserPromptSubmit", events[1].EventName)
}

func TestRepositorySubscribeHookSessionUpdates_NotifiesOnIngest(t *testing.T) {
	repo := newTestRepository(t)
	task := seedTask(t, repo, core.Task{
		ID:           "task-1",
		Slug:         "task-1",
		DisplayName:  "task 1",
		WorktreePath: "/tmp/repo-task-1",
	})

	updates, cleanup, err := repo.SubscribeHookSessionUpdates(context.Background())
	require.NoError(t, err)
	defer cleanup()

	_, err = repo.IngestHookEvent(context.Background(), core.HookEventInput{
		Cwd:        task.WorktreePath,
		EventName:  "UserPromptSubmit",
		SessionID:  "sess-1",
		TurnID:     "turn-1",
		PromptText: "fix the failing test",
		OccurredAt: time.Date(2026, 4, 8, 10, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)

	select {
	case update := <-updates:
		require.Equal(t, task.ID, update.TaskID)
		require.Equal(t, core.HookRuntimePhasePrompted, update.RuntimePhase)
		require.Equal(t, "fix the failing test", update.LastPromptText)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for hook session update")
	}
}

func taskTableColumns(t *testing.T, db *sql.DB) map[string]struct{} {
	t.Helper()
	return tableColumns(t, db, "tasks")
}

func tableColumns(t *testing.T, db *sql.DB, table string) map[string]struct{} {
	t.Helper()

	rows, err := db.QueryContext(context.Background(), `pragma table_info(`+table+`)`)
	require.NoError(t, err)
	defer rows.Close()

	columns := make(map[string]struct{})
	for rows.Next() {
		var (
			cid          int
			colName      string
			colType      string
			notNull      int
			defaultValue sql.NullString
			pk           int
		)
		require.NoError(t, rows.Scan(&cid, &colName, &colType, &notNull, &defaultValue, &pk))
		columns[colName] = struct{}{}
	}
	require.NoError(t, rows.Err())

	return columns
}
