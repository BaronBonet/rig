# TUI Hook Observability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Persist Codex hook activity in SQLite, derive richer per-task session summaries, and surface them in the TUI with optional live push updates and fallback to the existing tmux runtime path.

**Architecture:** Add a hook observability store alongside the existing task store inside the SQLite repository, with a collector-side ingestion path that maps incoming hook payloads to managed tasks and updates a summary row per task. Extend the core service to return TUI-oriented task views and expose a local update stream, then render a denser list row plus a selected-task detail pane that prefers hook-derived metadata when available and falls back cleanly when not.

**Tech Stack:** Go, Bubble Tea, Lip Gloss, SQLite (`modernc.org/sqlite`), local Unix domain sockets, existing Codex hook collector wiring.

---

## File Structure

- Modify: `internal/core/domain.go`
  - add hook observability domain types such as runtime phases, task views, hook session summaries, and hook events
- Modify: `internal/core/ports.go`
  - add repository and service-facing interfaces for hook ingestion, hook summary reads, recent hook events, and update subscriptions
- Modify: `internal/core/service.go`
  - enrich task list output with hook summaries, expose selected-task detail reads, and define fallback behavior
- Create: `internal/core/service_hook_observability_test.go`
  - focused service tests for hook-summary enrichment and fallback behavior
- Modify: `internal/adapters/repository/sqlite/repository.go`
  - add schema init for hook tables plus methods for ingestion, summary reads, event reads, and subscriber notifications
- Modify: `internal/adapters/repository/sqlite/repository_test.go`
  - schema, derivation, mapping, and live-update repository tests
- Create: `internal/adapters/repository/sqlite/hook_observability.go`
  - isolate hook-event parsing, preview extraction, phase derivation, and summary update helpers from the main repository file
- Create: `internal/adapters/repository/sqlite/hook_observability_test.go`
  - focused unit tests for phase derivation and preview extraction
- Modify: `cmd/hook-collector/main.go`
  - replace JSONL-only collector wiring with SQLite-backed collector wiring and socket publishing
- Modify: `cmd/hook-collector/server.go`
  - map incoming hook payloads to managed tasks and call the repository ingestion path
- Modify: `cmd/hook-collector/server_test.go`
  - ingestion, task mapping, invalid payload, and notification tests
- Modify: `internal/adapters/handler/cli/root.go`
  - extend the TUI service interface for task views, task details, and live updates
- Modify: `internal/adapters/handler/cli/tui_model.go`
  - render hook-derived list rows, selected-task detail pane, and live-update handling
- Modify: `internal/adapters/handler/cli/tui_style.go`
  - add style helpers for hook runtime phases and detail-pane blocks
- Modify: `internal/adapters/handler/cli/tui_model_test.go`
  - cover hook-aware row rendering, fallback rendering, and selected-task details
- Modify: `internal/adapters/handler/cli/mock_task_service.go`
  - regenerate or manually extend the mock service for the new TUI-facing methods

### Task 1: Add Hook Observability Domain Types And Service Contracts

**Files:**
- Modify: `internal/core/domain.go`
- Modify: `internal/core/ports.go`
- Modify: `internal/adapters/handler/cli/root.go`
- Test: `internal/core/service_hook_observability_test.go`

- [ ] **Step 1: Write the failing service contract test**

```go
func TestServiceListTaskViews_UsesHookSummaryWhenAvailable(t *testing.T) {
	h := newServiceTestHarness(t)
	task := h.existingTask("task-1")
	summary := &HookSessionSummary{
		TaskID:          task.ID,
		SessionID:       "sess-1",
		RuntimePhase:    HookRuntimePhaseRunningCommand,
		LastCommandText: "go test ./...",
	}

	h.taskRepoMock.EXPECT().ListTasks(mock.Anything).Return([]*Task{task}, nil)
	h.hookRepoMock.EXPECT().ListHookSessionSummaries(mock.Anything, []string{task.ID}).
		Return(map[string]*HookSessionSummary{task.ID: summary}, nil)

	views, err := h.service.ListTaskViews(t.Context())
	require.NoError(t, err)
	require.Len(t, views, 1)
	require.Equal(t, HookRuntimePhaseRunningCommand, views[0].HookSession.RuntimePhase)
	require.Equal(t, "go test ./...", views[0].HookSession.LastCommandText)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/core -run TestServiceListTaskViews_UsesHookSummaryWhenAvailable -count=1`

Expected: FAIL with compile errors for missing `HookSessionSummary`, `HookRuntimePhaseRunningCommand`, `ListTaskViews`, or missing hook repository mock methods.

- [ ] **Step 3: Add the new core types and interfaces**

```go
type HookRuntimePhase string

const (
	HookRuntimePhaseReady          HookRuntimePhase = "ready"
	HookRuntimePhasePrompted       HookRuntimePhase = "prompted"
	HookRuntimePhaseRunningCommand HookRuntimePhase = "running_command"
	HookRuntimePhaseIdle           HookRuntimePhase = "idle"
	HookRuntimePhaseFinished       HookRuntimePhase = "finished"
)

type HookSessionSummary struct {
	TaskID                string
	SessionID             string
	Model                 string
	Cwd                   string
	TranscriptPath        string
	StartSource           string
	CurrentTurnID         string
	LastEventName         string
	RuntimePhase          HookRuntimePhase
	StartedAt             time.Time
	LastActivityAt        time.Time
	LastStopAt            time.Time
	LastPromptText        string
	LastCommandText       string
	LastCommandResultText string
	LastAssistantMessage  string
	CommandCount          int
}

type HookEvent struct {
	ID                   int64
	TaskID               string
	SessionID            string
	TurnID               string
	EventName            string
	OccurredAt           time.Time
	RawPayloadJSON       string
	LastAssistantMessage string
	PromptText           string
	CommandText          string
	CommandResultText    string
	ToolUseID            string
}

type TaskView struct {
	Task        *Task
	HookSession *HookSessionSummary
}
```

```go
type HookEventIngestor interface {
	IngestHookEvent(ctx context.Context, raw HookEventInput) (*HookSessionSummary, error)
}

type HookObservabilityRepository interface {
	ListHookSessionSummaries(ctx context.Context, taskIDs []string) (map[string]*HookSessionSummary, error)
	ListHookEvents(ctx context.Context, taskID string, limit int) ([]HookEvent, error)
	SubscribeHookSessionUpdates(ctx context.Context) (<-chan HookSessionSummary, func(), error)
}
```

```go
type TaskService interface {
	// existing methods...
	ListTaskViews(ctx context.Context) ([]*core.TaskView, error)
	GetTaskHookEvents(ctx context.Context, taskID string, limit int) ([]core.HookEvent, error)
	SubscribeTaskHookUpdates(ctx context.Context) (<-chan core.HookSessionSummary, func(), error)
}
```

- [ ] **Step 4: Run the focused core test**

Run: `go test ./internal/core -run TestServiceListTaskViews_UsesHookSummaryWhenAvailable -count=1`

Expected: FAIL moves from missing types to missing service implementation.

- [ ] **Step 5: Commit the contract changes**

```bash
git add internal/core/domain.go internal/core/ports.go internal/adapters/handler/cli/root.go internal/core/service_hook_observability_test.go
git commit -m "feat: add hook observability service contracts"
```

### Task 2: Add SQLite Hook Tables, Derivation Helpers, And Summary Reads

**Files:**
- Modify: `internal/adapters/repository/sqlite/repository.go`
- Create: `internal/adapters/repository/sqlite/hook_observability.go`
- Modify: `internal/adapters/repository/sqlite/repository_test.go`
- Create: `internal/adapters/repository/sqlite/hook_observability_test.go`

- [ ] **Step 1: Write the failing repository tests**

```go
func TestRepositoryIngestHookEvent_UpdatesSummaryForRunningCommand(t *testing.T) {
	repo := newTestRepository(t)

	task := seedTask(t, repo, core.Task{
		ID:           "task-1",
		WorktreePath: "/tmp/repo-task-1",
		Provider:     "codex",
		Status:       core.TaskStatusRunning,
	})

	_, err := repo.IngestHookEvent(t.Context(), core.HookEventInput{
		Cwd:        task.WorktreePath,
		EventName:  "PreToolUse",
		SessionID:  "sess-1",
		TurnID:     "turn-1",
		ToolUseID:  "tool-1",
		Command:    "go test ./...",
		OccurredAt: time.Date(2026, 4, 8, 10, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)

	summaries, err := repo.ListHookSessionSummaries(t.Context(), []string{task.ID})
	require.NoError(t, err)
	require.Equal(t, core.HookRuntimePhaseRunningCommand, summaries[task.ID].RuntimePhase)
	require.Equal(t, "go test ./...", summaries[task.ID].LastCommandText)
	require.Equal(t, 1, summaries[task.ID].CommandCount)
}
```

```go
func TestDeriveRuntimePhase_MarksPromptedBeforeToolUse(t *testing.T) {
	summary := deriveHookSessionSummary(nil, hookRecord{
		EventName: "UserPromptSubmit",
		TurnID:    "turn-1",
		Prompt:    "fix the failing test",
	})

	require.Equal(t, core.HookRuntimePhasePrompted, summary.RuntimePhase)
	require.Equal(t, "fix the failing test", summary.LastPromptText)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/adapters/repository/sqlite -run 'TestRepositoryIngestHookEvent_UpdatesSummaryForRunningCommand|TestDeriveRuntimePhase_MarksPromptedBeforeToolUse' -count=1`

Expected: FAIL with missing schema, missing ingest methods, or missing derivation helpers.

- [ ] **Step 3: Add schema initialization and hook storage helpers**

```go
create table if not exists task_hook_events (
  id integer primary key autoincrement,
  task_id text not null,
  session_id text not null default '',
  turn_id text not null default '',
  event_name text not null,
  occurred_at text not null,
  raw_payload_json text not null default '',
  last_assistant_message text not null default '',
  prompt_preview text not null default '',
  command_preview text not null default '',
  command_result_preview text not null default '',
  tool_use_id text not null default ''
);

create table if not exists task_hook_sessions (
  task_id text primary key,
  session_id text not null default '',
  model text not null default '',
  cwd text not null default '',
  transcript_path text not null default '',
  start_source text not null default '',
  current_turn_id text not null default '',
  last_event_name text not null default '',
  runtime_phase text not null default '',
  started_at text not null default '',
  last_activity_at text not null default '',
  last_stop_at text not null default '',
  last_prompt_preview text not null default '',
  last_command_preview text not null default '',
  last_command_result_preview text not null default '',
  last_assistant_message text not null default '',
  command_count integer not null default 0,
  updated_at text not null default ''
);
```

```go
func (r *Repository) ListHookSessionSummaries(ctx context.Context, taskIDs []string) (map[string]*core.HookSessionSummary, error) {
	// query by task_id and scan summaries into a map
}

func (r *Repository) ListHookEvents(ctx context.Context, taskID string, limit int) ([]core.HookEvent, error) {
	// order by occurred_at desc, id desc
}
```

```go
func deriveHookSessionSummary(previous *core.HookSessionSummary, event hookRecord) *core.HookSessionSummary {
	next := cloneHookSummary(previous)
	next.LastEventName = event.EventName
	next.LastActivityAt = event.OccurredAt

	switch event.EventName {
	case "SessionStart":
		next.StartSource = event.StartSource
		next.RuntimePhase = core.HookRuntimePhaseReady
		next.StartedAt = firstNonZero(next.StartedAt, event.OccurredAt)
	case "UserPromptSubmit":
		next.CurrentTurnID = event.TurnID
		next.LastPromptText = trimPreview(event.Prompt)
		next.RuntimePhase = core.HookRuntimePhasePrompted
	case "PreToolUse":
		next.CurrentTurnID = event.TurnID
		next.LastCommandText = trimPreview(event.Command)
		next.RuntimePhase = core.HookRuntimePhaseRunningCommand
		next.CommandCount++
	case "PostToolUse", "Stop":
		next.RuntimePhase = core.HookRuntimePhaseIdle
	}

	return next
}
```

- [ ] **Step 4: Run the repository package tests**

Run: `go test ./internal/adapters/repository/sqlite -count=1`

Expected: PASS for new repository and derivation tests.

- [ ] **Step 5: Commit the SQLite hook store**

```bash
git add internal/adapters/repository/sqlite/repository.go internal/adapters/repository/sqlite/repository_test.go internal/adapters/repository/sqlite/hook_observability.go internal/adapters/repository/sqlite/hook_observability_test.go
git commit -m "feat: add sqlite hook observability store"
```

### Task 3: Upgrade The Hook Collector To Ingest SQLite Events And Publish Local Updates

**Files:**
- Modify: `cmd/hook-collector/main.go`
- Modify: `cmd/hook-collector/server.go`
- Modify: `cmd/hook-collector/server_test.go`
- Modify: `internal/core/ports.go`
- Modify: `internal/adapters/repository/sqlite/repository.go`

- [ ] **Step 1: Write the failing collector tests**

```go
func TestServerHandleHook_IngestsManagedTaskEvent(t *testing.T) {
	repo := newTestRepository(t)
	seedTask(t, repo, core.Task{
		ID:           "task-1",
		WorktreePath: "/tmp/repo-task-1",
		Provider:     "codex",
		Status:       core.TaskStatusRunning,
	})

	srv := newServer(repo, fixedClock(time.Date(2026, 4, 8, 10, 0, 0, 0, time.UTC)))
	req := httptest.NewRequest(http.MethodPost, "/hook", strings.NewReader(`{
	  "session_id":"sess-1",
	  "cwd":"/tmp/repo-task-1",
	  "hook_event_name":"UserPromptSubmit",
	  "turn_id":"turn-1",
	  "prompt":"check the failing test"
	}`))
	req.Header.Set("X-Codex-Hook-Event", "UserPromptSubmit")

	rec := httptest.NewRecorder()
	srv.handleHook(rec, req)

	require.Equal(t, http.StatusAccepted, rec.Code)
	summaries, err := repo.ListHookSessionSummaries(context.Background(), []string{"task-1"})
	require.NoError(t, err)
	require.Equal(t, core.HookRuntimePhasePrompted, summaries["task-1"].RuntimePhase)
}
```

```go
func TestServerHandleHook_IgnoresUnmanagedTaskCWD(t *testing.T) {
	repo := newTestRepository(t)
	srv := newServer(repo, fixedClock(time.Now().UTC()))

	req := httptest.NewRequest(http.MethodPost, "/hook", strings.NewReader(`{
	  "session_id":"sess-x",
	  "cwd":"/tmp/unmanaged",
	  "hook_event_name":"SessionStart"
	}`))

	rec := httptest.NewRecorder()
	srv.handleHook(rec, req)

	require.Equal(t, http.StatusAccepted, rec.Code)
	summaries, err := repo.ListHookSessionSummaries(context.Background(), nil)
	require.NoError(t, err)
	require.Empty(t, summaries)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/hook-collector -run 'TestServerHandleHook_IngestsManagedTaskEvent|TestServerHandleHook_IgnoresUnmanagedTaskCWD' -count=1`

Expected: FAIL because `newServer` still writes JSONL and has no repository-backed ingestion.

- [ ] **Step 3: Replace JSONL append logic with repository-backed ingestion and socket publisher**

```go
type server struct {
	repo core.HookEventIngestor
	now  func() time.Time
}

func newServer(repo core.HookEventIngestor, now func() time.Time) *server {
	if now == nil {
		now = time.Now
	}
	return &server{repo: repo, now: now}
}

func (s *server) handleHook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	input := decodeHookEventInput(s.now(), r.Header.Get("X-Codex-Hook-Event"), body)
	if _, err := s.repo.IngestHookEvent(r.Context(), input); err != nil && !errors.Is(err, core.ErrUnmanagedHookEvent) {
		http.Error(w, "ingest hook event: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}
```

```go
repo, err := sqlite.NewRepository(sqlite.Config{Path: dbPath})
if err != nil {
	log.Fatal(err)
}

srv := newServer(repo, nil)
```

- [ ] **Step 4: Run the collector tests**

Run: `go test ./cmd/hook-collector -count=1`

Expected: PASS with managed-task ingestion and unmanaged-task ignore behavior covered.

- [ ] **Step 5: Commit the collector ingestion path**

```bash
git add cmd/hook-collector/main.go cmd/hook-collector/server.go cmd/hook-collector/server_test.go internal/core/ports.go internal/adapters/repository/sqlite/repository.go
git commit -m "feat: ingest hook events into sqlite"
```

### Task 4: Enrich The Core Service With Task Views, Detail Events, And Live Subscriptions

**Files:**
- Modify: `internal/core/service.go`
- Create: `internal/core/service_hook_observability_test.go`
- Modify: `internal/core/test_helpers_test.go`

- [ ] **Step 1: Write the failing service behavior tests**

```go
func TestServiceListTaskViews_FallsBackToRuntimeStateWithoutHookSummary(t *testing.T) {
	h := newServiceTestHarness(t)
	task := h.existingTask("task-1")
	task.Provider = "codex"

	h.taskRepoMock.EXPECT().ListTasks(mock.Anything).Return([]*Task{task}, nil)
	h.hookRepoMock.EXPECT().ListHookSessionSummaries(mock.Anything, []string{task.ID}).
		Return(map[string]*HookSessionSummary{}, nil)
	h.providerRepoMock.EXPECT().DetectRuntimeState(mock.Anything).Return(RuntimeStateNeedsInput)

	views, err := h.service.ListTaskViews(t.Context())
	require.NoError(t, err)
	require.Equal(t, RuntimeStateNeedsInput, views[0].Task.RuntimeState)
	require.Nil(t, views[0].HookSession)
}
```

```go
func TestServiceGetTaskHookEvents_ReturnsNewestFirst(t *testing.T) {
	h := newServiceTestHarness(t)
	h.hookRepoMock.EXPECT().ListHookEvents(mock.Anything, "task-1", 5).
		Return([]HookEvent{{EventName: "Stop"}, {EventName: "PostToolUse"}}, nil)

	events, err := h.service.GetTaskHookEvents(t.Context(), "task-1", 5)
	require.NoError(t, err)
	require.Equal(t, "Stop", events[0].EventName)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/core -run 'TestServiceListTaskViews_FallsBackToRuntimeStateWithoutHookSummary|TestServiceGetTaskHookEvents_ReturnsNewestFirst' -count=1`

Expected: FAIL due to missing service methods or missing hook repository plumbing.

- [ ] **Step 3: Implement service enrichment and subscription pass-through**

```go
func (s *Service) ListTaskViews(ctx context.Context) ([]*TaskView, error) {
	tasks, err := s.ListTasks(ctx)
	if err != nil {
		return nil, err
	}

	taskIDs := make([]string, 0, len(tasks))
	for _, task := range tasks {
		taskIDs = append(taskIDs, task.ID)
	}

	summaries, err := s.hooks.ListHookSessionSummaries(ctx, taskIDs)
	if err != nil {
		return nil, err
	}

	views := make([]*TaskView, 0, len(tasks))
	for _, task := range tasks {
		views = append(views, &TaskView{
			Task:        task,
			HookSession: summaries[task.ID],
		})
	}

	return views, nil
}

func (s *Service) GetTaskHookEvents(ctx context.Context, taskID string, limit int) ([]HookEvent, error) {
	return s.hooks.ListHookEvents(ctx, taskID, limit)
}

func (s *Service) SubscribeTaskHookUpdates(ctx context.Context) (<-chan HookSessionSummary, func(), error) {
	return s.hooks.SubscribeHookSessionUpdates(ctx)
}
```

- [ ] **Step 4: Run the core package tests**

Run: `go test ./internal/core -count=1`

Expected: PASS with hook-aware service behavior and fallback behavior covered.

- [ ] **Step 5: Commit the service integration**

```bash
git add internal/core/service.go internal/core/service_hook_observability_test.go internal/core/test_helpers_test.go
git commit -m "feat: expose hook-aware task views"
```

### Task 5: Render Hook-Aware List Rows, Detail Pane, And Live Updates In The TUI

**Files:**
- Modify: `internal/adapters/handler/cli/tui_model.go`
- Modify: `internal/adapters/handler/cli/tui_style.go`
- Modify: `internal/adapters/handler/cli/tui_model_test.go`
- Modify: `internal/adapters/handler/cli/mock_task_service.go`

- [ ] **Step 1: Write the failing TUI tests**

```go
func TestModelView_RendersHookPhaseAndPreviewInTaskList(t *testing.T) {
	task := tuiTask("task-1")
	view := &core.TaskView{
		Task: task,
		HookSession: &core.HookSessionSummary{
			RuntimePhase:   core.HookRuntimePhaseRunningCommand,
			LastCommandText: "go test ./internal/core",
		},
	}

	m := newLoadedTUIModelWithViews(t, NewMockTaskService(t), view)
	rendered := m.View()

	require.Contains(t, rendered, "running command")
	require.Contains(t, rendered, "go test ./internal/core")
}
```

```go
func TestModelView_RendersSelectedTaskHookDetails(t *testing.T) {
	task := tuiTask("task-1")
	svc := NewMockTaskService(t)
	svc.EXPECT().GetTaskHookEvents(mock.Anything, task.ID, 5).Return([]core.HookEvent{
		{EventName: "Stop", LastAssistantMessage: "I finished the change"},
	}, nil)

	m := newLoadedTUIModelWithViews(t, svc, &core.TaskView{
		Task: task,
		HookSession: &core.HookSessionSummary{
			SessionID:            "sess-1",
			Model:                "gpt-5-codex",
			LastAssistantMessage: "I finished the change",
		},
	})

	rendered := m.View()
	require.Contains(t, rendered, "session id")
	require.Contains(t, rendered, "gpt-5-codex")
	require.Contains(t, rendered, "I finished the change")
	require.Contains(t, rendered, "Stop")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/adapters/handler/cli -run 'TestModelView_RendersHookPhaseAndPreviewInTaskList|TestModelView_RendersSelectedTaskHookDetails' -count=1`

Expected: FAIL because the model still renders only task rows with no hook view or detail pane.

- [ ] **Step 3: Update the TUI model to use task views and live updates**

```go
type model struct {
	service      TaskService
	taskViews    []*core.TaskView
	hookEvents   map[string][]core.HookEvent
	hookUpdates  <-chan core.HookSessionSummary
	unsubscribe  func()
	// existing fields...
}
```

```go
func (m model) listView() string {
	// left pane renders compact list rows
	// right pane renders selected-task details
	// hook badge + preview comes from view.HookSession when present
	// fallback badge comes from view.Task.RuntimeState or status otherwise
}
```

```go
func hookPreview(summary *core.HookSessionSummary) string {
	switch {
	case summary == nil:
		return ""
	case summary.RuntimePhase == core.HookRuntimePhaseRunningCommand && summary.LastCommandText != "":
		return summary.LastCommandText
	case summary.LastAssistantMessage != "":
		return summary.LastAssistantMessage
	default:
		return summary.LastPromptText
	}
}
```

- [ ] **Step 4: Run the TUI package tests**

Run: `go test ./internal/adapters/handler/cli -count=1`

Expected: PASS with hook-aware rows, detail pane, and fallback behavior covered.

- [ ] **Step 5: Commit the TUI changes**

```bash
git add internal/adapters/handler/cli/tui_model.go internal/adapters/handler/cli/tui_style.go internal/adapters/handler/cli/tui_model_test.go internal/adapters/handler/cli/mock_task_service.go
git commit -m "feat: render hook observability in tui"
```

### Task 6: Verify End-To-End Wiring And Regression Safety

**Files:**
- Modify: `cmd/hook-collector/server_test.go`
- Modify: `internal/adapters/repository/sqlite/repository_test.go`
- Modify: `internal/adapters/handler/cli/tui_model_test.go`

- [ ] **Step 1: Add a focused end-to-end persistence test**

```go
func TestHookIngestionFlow_PersistsSummaryAndEventsForTUI(t *testing.T) {
	repo := newTestRepository(t)
	task := seedTask(t, repo, core.Task{
		ID:           "task-1",
		WorktreePath: "/tmp/repo-task-1",
		Provider:     "codex",
		Status:       core.TaskStatusRunning,
	})

	for _, raw := range []core.HookEventInput{
		{Cwd: task.WorktreePath, EventName: "SessionStart", SessionID: "sess-1", StartSource: "startup"},
		{Cwd: task.WorktreePath, EventName: "UserPromptSubmit", SessionID: "sess-1", TurnID: "turn-1", Prompt: "fix the failing test"},
		{Cwd: task.WorktreePath, EventName: "PreToolUse", SessionID: "sess-1", TurnID: "turn-1", ToolUseID: "tool-1", Command: "go test ./..."},
		{Cwd: task.WorktreePath, EventName: "PostToolUse", SessionID: "sess-1", TurnID: "turn-1", ToolUseID: "tool-1", Command: "go test ./...", ToolResponse: "ok"},
		{Cwd: task.WorktreePath, EventName: "Stop", SessionID: "sess-1", TurnID: "turn-1", LastAssistantMessage: "Done"},
	} {
		_, err := repo.IngestHookEvent(t.Context(), raw)
		require.NoError(t, err)
	}

	summaries, err := repo.ListHookSessionSummaries(t.Context(), []string{task.ID})
	require.NoError(t, err)
	require.Equal(t, core.HookRuntimePhaseIdle, summaries[task.ID].RuntimePhase)
	require.Equal(t, "Done", summaries[task.ID].LastAssistantMessage)
	require.Equal(t, 1, summaries[task.ID].CommandCount)
}
```

- [ ] **Step 2: Run the full test suite**

Run: `go test ./...`

Expected: PASS

- [ ] **Step 3: Run a manual smoke test with the collector and a real Codex session**

Run:

```bash
go run ./cmd/hook-collector &
/bin/sh scripts/observability/run-codex-with-hooks.sh
```

Expected:

- the collector accepts hook POSTs without error
- `agent` can later read persisted hook summaries from SQLite
- the TUI shows hook-derived phase and detail content for the managed task

- [ ] **Step 4: Commit the verification updates**

```bash
git add cmd/hook-collector/server_test.go internal/adapters/repository/sqlite/repository_test.go internal/adapters/handler/cli/tui_model_test.go
git commit -m "test: cover hook observability flow"
```

## Self-Review Checklist

- Spec coverage:
  - SQLite persistence and summary tables: Tasks 2 and 3
  - local live updates: Tasks 3, 4, and 5
  - hook-derived list badges and detail pane: Task 5
  - fallback to existing tmux/runtime display: Tasks 4 and 5
  - restart-safe persistence: Tasks 2 and 6
- Placeholder scan:
  - no `TODO`, `TBD`, or implicit “handle appropriately” steps remain
- Type consistency:
  - `HookSessionSummary`, `HookEvent`, `TaskView`, and `HookRuntimePhase` are introduced in Task 1 and used consistently afterward
