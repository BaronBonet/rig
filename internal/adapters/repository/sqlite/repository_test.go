package sqlite

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rig/internal/core"

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

func TestNewRepository_ConfiguresSQLiteForConcurrentAccess(t *testing.T) {
	repo, err := NewRepository(Config{Path: filepath.Join(t.TempDir(), "state.db")})
	require.NoError(t, err)

	var journalMode string
	require.NoError(t, repo.db.QueryRowContext(context.Background(), `pragma journal_mode`).Scan(&journalMode))
	require.Equal(t, "wal", strings.ToLower(journalMode))

	var busyTimeout int
	require.NoError(t, repo.db.QueryRowContext(context.Background(), `pragma busy_timeout`).Scan(&busyTimeout))
	require.Greater(t, busyTimeout, 0)
}

func TestNewRepository_CreatesFreshDatabaseWithGooseMigrations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")

	repo, err := NewRepository(Config{Path: path})
	require.NoError(t, err)
	require.NotNil(t, repo)
	require.NoError(t, repo.IsAvailable(context.Background()))

	for _, table := range []string{
		"tasks",
		"task_hook_events",
		"task_hook_sessions",
		"task_observer_summaries",
		"goose_db_version",
	} {
		exists, tableErr := testSchemaObjectExists(t, repo.db, "table", table)
		require.NoError(t, tableErr)
		require.True(t, exists, table)
	}

	exists, tableErr := testSchemaObjectExists(t, repo.db, "table", "events")
	require.NoError(t, tableErr)
	require.False(t, exists)

	exists, indexErr := testSchemaObjectExists(t, repo.db, "index", "idx_tasks_repo_root")
	require.NoError(t, indexErr)
	require.True(t, exists)

	var versionID int64
	var isApplied bool
	require.NoError(t, repo.db.QueryRowContext(
		context.Background(),
		`select version_id, is_applied from goose_db_version order by id desc limit 1`,
	).Scan(&versionID, &isApplied))
	require.EqualValues(t, 4, versionID)
	require.True(t, isApplied)
}

func TestNewRepository_ReopensInitializedDatabaseWithoutReapplyingMigrations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")

	firstRepo, err := NewRepository(Config{Path: path})
	require.NoError(t, err)
	require.NotNil(t, firstRepo)
	require.NoError(t, firstRepo.IsAvailable(context.Background()))

	require.NoError(t, firstRepo.CreateTask(context.Background(), &core.Task{
		ID:               "task-1",
		Prompt:           "keep existing rows",
		DisplayName:      "task 1",
		Slug:             "task-1",
		RepoRoot:         "/tmp/repo",
		RepoName:         "repo",
		BaseBranch:       "main",
		BranchName:       "feat/task-1",
		WorktreePath:     "/tmp/repo-task-1",
		TmuxSession:      "repo-task-1",
		AgentWindowName:  defaultAgentWindowName,
		EditorWindowName: defaultEditorWindowName,
		Provider:         "codex",
		Status:           core.TaskStatusReady,
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}))
	require.NoError(t, firstRepo.db.Close())

	reopenedRepo, err := NewRepository(Config{Path: path})
	require.NoError(t, err)
	require.NotNil(t, reopenedRepo)
	require.NoError(t, reopenedRepo.IsAvailable(context.Background()))

	got, err := reopenedRepo.GetTask(context.Background(), "task-1")
	require.NoError(t, err)
	require.Equal(t, "task-1", got.ID)

	var versionCount int
	require.NoError(t, reopenedRepo.db.QueryRowContext(
		context.Background(),
		`select count(*) from goose_db_version where version_id = 1 and is_applied = 1`,
	).Scan(&versionCount))
	require.Equal(t, 1, versionCount)
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

func TestRepositoryIngestHookEvent_UpdatesTaskProviderFromObservedSession(t *testing.T) {
	repo := newTestRepository(t)
	task := seedTask(t, repo, core.Task{
		ID:           "task-1",
		Slug:         "task-1",
		DisplayName:  "task 1",
		WorktreePath: "/tmp/repo-task-1",
		Provider:     "codex",
	})

	_, err := repo.IngestHookEvent(context.Background(), core.HookEventInput{
		Cwd:            task.WorktreePath,
		EventName:      "SessionStart",
		SessionID:      "sess-claude",
		Model:          "claude-sonnet-4-5-20250929",
		TranscriptPath: "/tmp/claude.jsonl",
		StartSource:    "startup",
		OccurredAt:     time.Date(2026, 4, 8, 10, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)

	got, err := repo.GetTask(context.Background(), task.ID)
	require.NoError(t, err)
	require.Equal(t, "claude", got.Provider)
}

func TestRepositoryIngestHookEvent_IgnoresNestedSessionEventsForEstablishedTaskSession(t *testing.T) {
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
		SessionID:      "sess-parent",
		Model:          "gpt-5",
		TranscriptPath: "/tmp/parent.jsonl",
		StartSource:    "startup",
		OccurredAt:     time.Date(2026, 4, 8, 10, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)

	parentSummary, err := repo.IngestHookEvent(context.Background(), core.HookEventInput{
		Cwd:        task.WorktreePath,
		EventName:  "UserPromptSubmit",
		SessionID:  "sess-parent",
		TurnID:     "turn-parent",
		PromptText: "fix the billing retry flow",
		OccurredAt: time.Date(2026, 4, 8, 10, 1, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.NotNil(t, parentSummary)
	require.Equal(t, "sess-parent", parentSummary.SessionID)
	require.Equal(t, "fix the billing retry flow", parentSummary.LastPromptText)

	// Parent dispatches the Agent tool — this sets RuntimePhase to
	// RunningCommand, which is what distinguishes the parent from a new
	// top-level session (whose predecessor would be Idle).
	_, err = repo.IngestHookEvent(context.Background(), core.HookEventInput{
		Cwd:         task.WorktreePath,
		EventName:   "PreToolUse",
		SessionID:   "sess-parent",
		TurnID:      "turn-parent",
		CommandText: "Agent dispatch subagent",
		OccurredAt:  time.Date(2026, 4, 8, 10, 1, 30, 0, time.UTC),
	})
	require.NoError(t, err)

	nestedSummary, err := repo.IngestHookEvent(context.Background(), core.HookEventInput{
		Cwd:            task.WorktreePath,
		EventName:      "SessionStart",
		SessionID:      "sess-child",
		Model:          "gpt-5",
		TranscriptPath: "/tmp/child.jsonl",
		StartSource:    "startup",
		OccurredAt:     time.Date(2026, 4, 8, 10, 2, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.NotNil(t, nestedSummary)

	nestedSummary, err = repo.IngestHookEvent(context.Background(), core.HookEventInput{
		Cwd:        task.WorktreePath,
		EventName:  "UserPromptSubmit",
		SessionID:  "sess-child",
		TurnID:     "turn-child",
		PromptText: "Implement Task 1 from the approved plan",
		OccurredAt: time.Date(2026, 4, 8, 10, 2, 1, 0, time.UTC),
	})
	require.NoError(t, err)
	require.NotNil(t, nestedSummary)

	summaries, err := repo.ListHookSessionSummaries(context.Background(), []string{task.ID})
	require.NoError(t, err)
	require.Contains(t, summaries, task.ID)

	summary := summaries[task.ID]
	require.Equal(t, "sess-parent", summary.SessionID)
	require.Equal(t, "turn-parent", summary.CurrentTurnID)
	require.Equal(t, "fix the billing retry flow", summary.LastPromptText)
	require.Equal(t, "/tmp/parent.jsonl", summary.TranscriptPath)
	require.Equal(t, core.HookRuntimePhaseRunningCommand, summary.RuntimePhase)
}

func TestRepositoryIngestHookEvent_AllowsNewSessionAfterUncleanShutdown(t *testing.T) {
	repo := newTestRepository(t)
	task := seedTask(t, repo, core.Task{
		ID:           "task-1",
		Slug:         "task-1",
		DisplayName:  "task 1",
		WorktreePath: "/tmp/repo-task-1",
	})

	// First session starts and does some work but crashes (no Stop event).
	_, err := repo.IngestHookEvent(context.Background(), core.HookEventInput{
		Cwd:            task.WorktreePath,
		EventName:      "SessionStart",
		SessionID:      "sess-old",
		Model:          "claude-opus-4-5-20251001",
		TranscriptPath: "/tmp/old.jsonl",
		StartSource:    "startup",
		OccurredAt:     time.Date(2026, 4, 8, 10, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)

	_, err = repo.IngestHookEvent(context.Background(), core.HookEventInput{
		Cwd:        task.WorktreePath,
		EventName:  "UserPromptSubmit",
		SessionID:  "sess-old",
		TurnID:     "turn-old",
		PromptText: "fix the billing retry flow",
		OccurredAt: time.Date(2026, 4, 8, 10, 0, 1, 0, time.UTC),
	})
	require.NoError(t, err)

	_, err = repo.IngestHookEvent(context.Background(), core.HookEventInput{
		Cwd:         task.WorktreePath,
		EventName:   "PostToolUse",
		SessionID:   "sess-old",
		TurnID:      "turn-old",
		CommandText: "Read internal/billing/retry.go",
		OccurredAt:  time.Date(2026, 4, 8, 10, 0, 5, 0, time.UTC),
	})
	require.NoError(t, err)
	// Session crashes here — no Stop event.

	// New session starts for the same task with a new session ID.
	_, err = repo.IngestHookEvent(context.Background(), core.HookEventInput{
		Cwd:            task.WorktreePath,
		EventName:      "SessionStart",
		SessionID:      "sess-new",
		Model:          "claude-opus-4-5-20251001",
		TranscriptPath: "/tmp/new.jsonl",
		StartSource:    "startup",
		OccurredAt:     time.Date(2026, 4, 8, 10, 5, 0, 0, time.UTC),
	})
	require.NoError(t, err)

	summary, err := repo.IngestHookEvent(context.Background(), core.HookEventInput{
		Cwd:        task.WorktreePath,
		EventName:  "UserPromptSubmit",
		SessionID:  "sess-new",
		TurnID:     "turn-new",
		PromptText: "fix the billing retry flow (retry)",
		OccurredAt: time.Date(2026, 4, 8, 10, 5, 1, 0, time.UTC),
	})
	require.NoError(t, err)
	require.NotNil(t, summary)

	// The new session must take over: hook phase should reflect the new
	// session's UserPromptSubmit, not the old session's stale PostToolUse.
	summaries, err := repo.ListHookSessionSummaries(context.Background(), []string{task.ID})
	require.NoError(t, err)

	got := summaries[task.ID]
	require.Equal(t, "sess-new", got.SessionID)
	require.Equal(t, "turn-new", got.CurrentTurnID)
	require.Equal(t, "fix the billing retry flow (retry)", got.LastPromptText)
	require.Equal(t, "/tmp/new.jsonl", got.TranscriptPath)
	require.Equal(t, core.HookRuntimePhasePrompted, got.RuntimePhase)
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

func TestRepositoryListHookSessionSummaries_FiltersRequestedTaskIDs(t *testing.T) {
	repo := newTestRepository(t)
	taskOne := seedTask(t, repo, core.Task{
		ID:           "task-1",
		Slug:         "task-1",
		DisplayName:  "task 1",
		WorktreePath: "/tmp/repo-task-1",
	})
	taskTwo := seedTask(t, repo, core.Task{
		ID:           "task-2",
		Slug:         "task-2",
		DisplayName:  "task 2",
		WorktreePath: "/tmp/repo-task-2",
	})

	_, err := repo.IngestHookEvent(context.Background(), core.HookEventInput{
		Cwd:        taskOne.WorktreePath,
		EventName:  "UserPromptSubmit",
		SessionID:  "sess-1",
		TurnID:     "turn-1",
		PromptText: "fix test A",
		OccurredAt: time.Date(2026, 4, 8, 10, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)

	_, err = repo.IngestHookEvent(context.Background(), core.HookEventInput{
		Cwd:        taskTwo.WorktreePath,
		EventName:  "UserPromptSubmit",
		SessionID:  "sess-2",
		TurnID:     "turn-2",
		PromptText: "fix test B",
		OccurredAt: time.Date(2026, 4, 8, 10, 1, 0, 0, time.UTC),
	})
	require.NoError(t, err)

	summaries, err := repo.ListHookSessionSummaries(context.Background(), []string{taskOne.ID})
	require.NoError(t, err)
	require.Len(t, summaries, 1)
	require.Contains(t, summaries, taskOne.ID)
	require.NotContains(t, summaries, taskTwo.ID)
	require.Equal(t, "fix test A", summaries[taskOne.ID].LastPromptText)
}

func TestRepositoryListObserverSummaries_FiltersRequestedTaskIDs(t *testing.T) {
	repo := newTestRepository(t)
	taskOne := seedTask(t, repo, core.Task{
		ID:           "task-1",
		Slug:         "task-1",
		DisplayName:  "task 1",
		WorktreePath: "/tmp/repo-task-1",
	})
	taskTwo := seedTask(t, repo, core.Task{
		ID:           "task-2",
		Slug:         "task-2",
		DisplayName:  "task 2",
		WorktreePath: "/tmp/repo-task-2",
	})

	require.NoError(t, repo.UpsertObserverSummary(context.Background(), &core.ObserverSummary{
		TaskID:                taskOne.ID,
		DisplayStatus:         core.DisplayStatusWorking,
		DisplayActivity:       core.DisplayActivityCommand,
		ProcessAlive:          true,
		LastRuntimeObservedAt: time.Date(2026, 4, 8, 10, 0, 0, 0, time.UTC),
	}))
	require.NoError(t, repo.UpsertObserverSummary(context.Background(), &core.ObserverSummary{
		TaskID:                taskTwo.ID,
		DisplayStatus:         core.DisplayStatusFinished,
		DisplayActivity:       core.DisplayActivityNone,
		ProcessAlive:          false,
		LastRuntimeObservedAt: time.Date(2026, 4, 8, 10, 1, 0, 0, time.UTC),
	}))

	summaries, err := repo.ListObserverSummaries(context.Background(), []string{taskOne.ID})
	require.NoError(t, err)
	require.Len(t, summaries, 1)
	require.Contains(t, summaries, taskOne.ID)
	require.NotContains(t, summaries, taskTwo.ID)
	require.Equal(t, core.DisplayStatusWorking, summaries[taskOne.ID].DisplayStatus)
	require.Equal(t, core.DisplayActivityCommand, summaries[taskOne.ID].DisplayActivity)
	require.True(t, summaries[taskOne.ID].ProcessAlive)
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

func testSchemaObjectExists(t *testing.T, db *sql.DB, objectType, name string) (bool, error) {
	t.Helper()

	var count int
	err := db.QueryRowContext(
		context.Background(),
		`select count(*) from sqlite_master where type = ? and name = ?`,
		objectType,
		name,
	).Scan(&count)
	return count > 0, err
}
