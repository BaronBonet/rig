# Agent Control Center Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn `agent` into a TUI-first control center for task sessions, with a stable tmux `agent` window, a seeded `editor` window, and explicit window-aware reconciliation.

**Architecture:** Keep the existing core/adapter split. Extend the persisted task model and SQLite schema with repo and tmux-window metadata, upgrade the tmux adapter from session-only targeting to named-window targeting, update the core service to reconcile the hybrid session contract (`agent` required, `editor` optional), and grow the Bubble Tea UI from cleanup screen into the primary task browser and task-creation surface.

**Tech Stack:** Go, Cobra, Bubble Tea, Bubble Tea `bubbles/textinput`, SQLite, tmux, git, testify

---

## File Structure

Modify or create the following files during implementation.

### Core

- Modify: `internal/core/task.go`
- Modify: `internal/core/status.go`
- Modify: `internal/core/ports.go`
- Modify: `internal/core/service.go`
- Modify: `internal/core/fakes_test.go`
- Modify: `internal/core/service_new_test.go`
- Modify: `internal/core/service_status_test.go`
- Modify: `internal/core/service_open_test.go`
- Modify: `internal/core/service_cleanup_test.go`

### Repository Adapters

- Modify: `internal/adapters/repository/sqlite/repository.go`
- Modify: `internal/adapters/repository/sqlite/repository_test.go`
- Modify: `internal/adapters/repository/tmux/repository.go`
- Modify: `internal/adapters/repository/tmux/repository_test.go`

### CLI And TUI

- Modify: `go.mod`
- Modify: `internal/adapters/handler/cli/root.go`
- Modify: `internal/adapters/handler/cli/new.go`
- Modify: `internal/adapters/handler/cli/list.go`
- Modify: `internal/adapters/handler/cli/status.go`
- Modify: `internal/adapters/handler/cli/tui.go`
- Modify: `internal/adapters/handler/cli/tui_model.go`
- Modify: `internal/adapters/handler/cli/new_test.go`
- Modify: `internal/adapters/handler/cli/list_test.go`
- Modify: `internal/adapters/handler/cli/status_test.go`
- Modify: `internal/adapters/handler/cli/tui_test.go`
- Modify: `internal/adapters/handler/cli/tui_model_test.go`

### Docs

- Modify: `README.md`

## Implementation Notes

- Keep the existing CLI commands, but treat them as the backend surface for the TUI.
- The tmux contract for a healthy task is:
  - session exists
  - worktree exists
  - branch exists
  - `agent` window exists
- A missing `editor` window is `degraded`, not `broken`.
- Cleanup still deletes the tmux session and worktree and keeps the branch.
- The TUI should remain keyboard-first.
- Add `n` to create a task from the TUI.
- Use Bubble Tea `textinput` for prompt entry and suggested-name confirmation instead of inventing raw terminal parsing inside the TUI.
- Keep creation and open behavior on the same backend path by using `CreateTaskWithProgress` from both the CLI command and the TUI.
- Preserve compatibility with existing SQLite databases by adding missing columns during startup instead of requiring manual migration.

## Task 1: Persist The Control-Center Task Contract

**Files:**
- Modify: `internal/core/task.go`
- Modify: `internal/adapters/repository/sqlite/repository.go`
- Modify: `internal/adapters/repository/sqlite/repository_test.go`

- [ ] **Step 1: Write the failing SQLite persistence tests for repo and window metadata**

Add coverage to `internal/adapters/repository/sqlite/repository_test.go`:

```go
func TestRepositoryCreateAndGetTask_PersistsRepoAndWindowFields(t *testing.T) {
	repo := newTestRepository(t)

	task := &core.Task{
		ID:               "task-1",
		Prompt:           "add billing retry flow",
		DisplayName:      "billing retry flow",
		Slug:             "billing-retry-flow",
		RepoRoot:         "/tmp/repo",
		RepoName:         "repo",
		BaseBranch:       "main",
		BranchName:       "feat/billing-retry-flow",
		WorktreePath:     "/tmp/repo-billing-retry-flow",
		TmuxSession:      "repo-billing-retry-flow",
		AgentWindowName:  "agent",
		EditorWindowName: "editor",
		Provider:         "codex",
		Status:           core.TaskStatusRunning,
		WorktreeExists:   true,
		BranchExists:     true,
		SessionExists:    true,
		AgentWindowExists: true,
		EditorWindowExists: true,
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}

	require.NoError(t, repo.CreateTask(context.Background(), task))

	got, err := repo.GetTask(context.Background(), "billing-retry-flow")
	require.NoError(t, err)
	require.Equal(t, "repo", got.RepoName)
	require.Equal(t, "agent", got.AgentWindowName)
	require.Equal(t, "editor", got.EditorWindowName)
	require.True(t, got.AgentWindowExists)
	require.True(t, got.EditorWindowExists)
}
```

Also extend the list-order test fixtures so both inserted tasks set `RepoName`, `AgentWindowName`, and `EditorWindowName`.

- [ ] **Step 2: Run the SQLite package tests to verify red**

Run: `go test ./internal/adapters/repository/sqlite -v`
Expected: FAIL because `core.Task` and the SQLite schema do not have the new repo/window fields yet.

- [ ] **Step 3: Extend the core task model with repo and window fields**

Update `internal/core/task.go`:

```go
type Task struct {
	ID                string
	Prompt            string
	DisplayName       string
	Slug              string
	RepoRoot          string
	RepoName          string
	BaseBranch        string
	BranchName        string
	WorktreePath      string
	TmuxSession       string
	AgentWindowName   string
	EditorWindowName  string
	Provider          string
	Status            TaskStatus
	WorktreeExists    bool
	BranchExists      bool
	SessionExists     bool
	AgentWindowExists bool
	EditorWindowExists bool
	LastError         string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	LastReconciledAt  time.Time
}
```

- [ ] **Step 4: Add schema backfill logic and persist the new fields**

Update `internal/adapters/repository/sqlite/repository.go` so startup adds missing columns to existing databases and all CRUD paths read/write them:

```go
func (r *Repository) initSchema() error {
	if _, err := r.db.Exec(`
create table if not exists tasks (
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

create table if not exists events (
  id integer primary key autoincrement,
  task_id text not null,
  event_type text not null,
  payload text not null,
  created_at text not null
);
`); err != nil {
		return err
	}

	for _, migration := range []struct {
		column string
		stmt   string
	}{
		{column: "repo_name", stmt: `alter table tasks add column repo_name text not null default ''`},
		{column: "agent_window_name", stmt: `alter table tasks add column agent_window_name text not null default 'agent'`},
		{column: "editor_window_name", stmt: `alter table tasks add column editor_window_name text not null default 'editor'`},
		{column: "agent_window_exists", stmt: `alter table tasks add column agent_window_exists integer not null default 0`},
		{column: "editor_window_exists", stmt: `alter table tasks add column editor_window_exists integer not null default 0`},
	} {
		if err := execIfMissingColumn(r.db, "tasks", migration.column, migration.stmt); err != nil {
			return err
		}
	}

	return nil
}
```

Use a helper like:

```go
func execIfMissingColumn(db *sql.DB, table, column, alterStmt string) error {
	rows, err := db.Query(`pragma table_info(` + table + `)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			cid       int
			name      string
			dataType  string
			notNull   int
			defaultV  any
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultV, &primaryKey); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}

	if err := rows.Err(); err != nil {
		return err
	}

	_, err = db.Exec(alterStmt)
	return err
}
```

Then update the insert, update, select, and scan paths so `RepoName`, `AgentWindowName`, `EditorWindowName`, `AgentWindowExists`, and `EditorWindowExists` are persisted exactly like the existing branch/session booleans.

- [ ] **Step 5: Re-run the SQLite tests**

Run: `go test ./internal/adapters/repository/sqlite -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/core/task.go internal/adapters/repository/sqlite/repository.go internal/adapters/repository/sqlite/repository_test.go
git commit -m "feat: persist task repo and tmux window metadata"
```

## Task 2: Make The Tmux Adapter Window-Aware

**Files:**
- Modify: `internal/core/ports.go`
- Modify: `internal/adapters/repository/tmux/repository.go`
- Modify: `internal/adapters/repository/tmux/repository_test.go`
- Modify: `internal/core/fakes_test.go`

- [ ] **Step 1: Write the failing tmux adapter tests for named windows**

Add tests to `internal/adapters/repository/tmux/repository_test.go`:

```go
func TestRepositoryCreateSession_CreatesAgentAndEditorWindows(t *testing.T) {
	runner := execx.NewFakeRunner([]execx.Result{{}, {}})
	repo := NewRepository(runner)

	err := repo.CreateSession(context.Background(), core.CreateSessionInput{
		SessionName:      "repo-billing-retry-flow",
		WorkingDir:       "/tmp/repo-billing-retry-flow",
		AgentWindowName:  "agent",
		EditorWindowName: "editor",
	})

	require.NoError(t, err)
	require.Equal(t, []string{"new-session", "-d", "-s", "repo-billing-retry-flow", "-n", "agent", "-c", "/tmp/repo-billing-retry-flow"}, runner.Calls[0].Args)
	require.Equal(t, []string{"new-window", "-d", "-t", "=repo-billing-retry-flow", "-n", "editor", "-c", "/tmp/repo-billing-retry-flow"}, runner.Calls[1].Args)
}

func TestRepositorySendKeysToWindow_TargetsNamedWindow(t *testing.T) {
	runner := execx.NewFakeRunner([]execx.Result{{}})
	repo := NewRepository(runner)

	err := repo.SendKeysToWindow(context.Background(), "repo-billing-retry-flow", "agent", []string{"codex", "add billing retry flow"})

	require.NoError(t, err)
	require.Equal(t, []string{"send-keys", "-t", "=repo-billing-retry-flow:agent", "codex 'add billing retry flow'", "C-m"}, runner.Calls[0].Args)
}
```

Also add tests for:

- `WindowExists` returning true when `list-windows` includes `agent`
- `WindowExists` returning false when the session exists but the named window does not
- `KillSession` still using the exact session target

- [ ] **Step 2: Run the tmux adapter tests to verify red**

Run: `go test ./internal/adapters/repository/tmux -v`
Expected: FAIL because the port is still session-only and `CreateSessionInput` does not carry window names.

- [ ] **Step 3: Extend the tmux port and test fakes**

Update `internal/core/ports.go`:

```go
type CreateSessionInput struct {
	SessionName      string
	WorkingDir       string
	AgentWindowName  string
	EditorWindowName string
}

type TmuxRepository interface {
	IsAvailable(ctx context.Context) error
	SessionExists(ctx context.Context, session string) (bool, error)
	WindowExists(ctx context.Context, session, window string) (bool, error)
	CreateSession(ctx context.Context, in CreateSessionInput) error
	KillSession(ctx context.Context, session string) error
	AttachOrSwitch(ctx context.Context, session string) error
	SendKeysToWindow(ctx context.Context, session, window string, command []string) error
}
```

Update `internal/core/fakes_test.go` so `fakeTmuxRepository` stores:

- `windowExists map[string]bool`
- `sentWindow string`
- `createdSession CreateSessionInput`

and implements:

```go
func (f *fakeTmuxRepository) WindowExists(_ context.Context, _, window string) (bool, error) {
	return f.windowExists[window], nil
}

func (f *fakeTmuxRepository) SendKeysToWindow(_ context.Context, _, window string, command []string) error {
	f.sentWindow = window
	f.sentCommand = append([]string(nil), command...)
	return f.sendKeysErr
}
```

- [ ] **Step 4: Implement named-window tmux operations**

Update `internal/adapters/repository/tmux/repository.go`:

```go
func (r *Repository) CreateSession(ctx context.Context, in core.CreateSessionInput) error {
	if _, err := r.runner.Run(ctx, "", "tmux", "new-session", "-d", "-s", in.SessionName, "-n", in.AgentWindowName, "-c", in.WorkingDir); err != nil {
		return err
	}

	_, err := r.runner.Run(ctx, "", "tmux", "new-window", "-d", "-t", exactSessionTarget(in.SessionName), "-n", in.EditorWindowName, "-c", in.WorkingDir)
	return err
}

func (r *Repository) WindowExists(ctx context.Context, session, window string) (bool, error) {
	result, err := r.runner.Run(ctx, "", "tmux", "list-windows", "-t", exactSessionTarget(session), "-F", "#{window_name}")
	if err != nil {
		if isMissingSession(result, err) {
			return false, nil
		}
		return false, err
	}

	for _, name := range strings.Split(result.Stdout, "\n") {
		if strings.TrimSpace(name) == window {
			return true, nil
		}
	}
	return false, nil
}

func (r *Repository) SendKeysToWindow(ctx context.Context, session, window string, command []string) error {
	_, err := r.runner.Run(ctx, "", "tmux", "send-keys", "-t", exactWindowTarget(session, window), strings.Join(quoteCommand(command), " "), "C-m")
	return err
}
```

Use a helper:

```go
func exactWindowTarget(session, window string) string {
	return "=" + session + ":" + window
}
```

- [ ] **Step 5: Re-run the tmux adapter tests**

Run: `go test ./internal/adapters/repository/tmux -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/core/ports.go internal/core/fakes_test.go internal/adapters/repository/tmux/repository.go internal/adapters/repository/tmux/repository_test.go
git commit -m "feat: target tmux sessions by named windows"
```

## Task 3: Reconcile Tasks Against The Hybrid Session Contract

**Files:**
- Modify: `internal/core/status.go`
- Modify: `internal/core/service.go`
- Modify: `internal/core/service_new_test.go`
- Modify: `internal/core/service_status_test.go`
- Modify: `internal/core/service_open_test.go`
- Modify: `internal/core/service_cleanup_test.go`
- Modify: `internal/core/fakes_test.go`

- [ ] **Step 1: Write the failing core tests for `agent`/`editor` health**

Extend the service tests with these cases:

```go
func TestServiceNewTask_CreatesNamedAgentAndEditorWindows(t *testing.T) {
	svc := newTestService()

	task, err := svc.service.NewTask(t.Context(), NewTaskInput{
		Cwd: "/tmp/repo",
		Prompt: "add billing retry flow",
		ConfirmedDisplayName: "billing retry flow",
	})

	require.NoError(t, err)
	require.Equal(t, "repo", task.RepoName)
	require.Equal(t, "agent", task.AgentWindowName)
	require.Equal(t, "editor", task.EditorWindowName)
	require.Equal(t, "agent", svc.tmuxRepo.createdSession.AgentWindowName)
	require.Equal(t, "editor", svc.tmuxRepo.createdSession.EditorWindowName)
	require.Equal(t, "agent", svc.tmuxRepo.sentWindow)
}

func TestServiceGetTask_MarksTaskDegradedWhenEditorWindowIsMissing(t *testing.T) {
	worktree := t.TempDir()
	svc := newTestService()
	svc.taskRepo.getTask = &Task{
		ID: "task-1", Slug: "billing-retry-flow", RepoRoot: "/tmp/repo",
		RepoName: "repo", BranchName: "feat/billing-retry-flow", WorktreePath: worktree,
		TmuxSession: "repo-billing-retry-flow", AgentWindowName: "agent", EditorWindowName: "editor",
		Status: TaskStatusRunning,
	}
	svc.gitRepo.branchExists = true
	svc.tmuxRepo.sessionExists = true
	svc.tmuxRepo.windowExists = map[string]bool{"agent": true, "editor": false}

	task, err := svc.service.GetTask(t.Context(), "billing-retry-flow")
	require.NoError(t, err)
	require.Equal(t, TaskStatusDegraded, task.Status)
	require.True(t, task.AgentWindowExists)
	require.False(t, task.EditorWindowExists)
}
```

Also add tests for:

- missing `agent` window => `broken`
- missing session => `broken`
- `OpenTask` succeeds for `degraded` when `agent` exists
- cleanup clears `AgentWindowExists` and `EditorWindowExists` after killing the session

- [ ] **Step 2: Run the focused core tests to verify red**

Run: `go test ./internal/core -run 'TestService(NewTask|GetTask|OpenTask|DeleteTaskResources)' -v`
Expected: FAIL because `degraded`, repo names, and window checks are not wired into the service yet.

- [ ] **Step 3: Add the new task status**

Update `internal/core/status.go`:

```go
const (
	TaskStatusCreating = "creating"
	TaskStatusReady    = "ready"
	TaskStatusRunning  = "running"
	TaskStatusDegraded = "degraded"
	TaskStatusBroken   = "broken"
	TaskStatusCleaned  = "cleaned"
)
```

- [ ] **Step 4: Update task creation to seed tmux window metadata and target `agent` explicitly**

Update the creation path in `internal/core/service.go`:

```go
task := &Task{
	ID:               fmt.Sprintf("%d", now.UnixNano()),
	Prompt:           input.Prompt,
	DisplayName:      displayName,
	Slug:             taskSlug,
	RepoRoot:         repoCtx.Root,
	RepoName:         repoCtx.Name,
	BaseBranch:       repoCtx.BaseBranch,
	BranchName:       "feat/" + taskSlug,
	WorktreePath:     filepath.Join(filepath.Dir(repoCtx.Root), repoCtx.Name+"-"+taskSlug),
	TmuxSession:      repoCtx.Name + "-" + taskSlug,
	AgentWindowName:  "agent",
	EditorWindowName: "editor",
	Provider:         "codex",
	Status:           TaskStatusCreating,
	CreatedAt:        now,
	UpdatedAt:        now,
}
```

Then create the session and launch Codex like this:

```go
if err := s.tmux.CreateSession(ctx, CreateSessionInput{
	SessionName:      task.TmuxSession,
	WorkingDir:       task.WorktreePath,
	AgentWindowName:  task.AgentWindowName,
	EditorWindowName: task.EditorWindowName,
}); err != nil {
	return s.markBroken(ctx, task, fmt.Errorf("start tmux session: %w", err))
}

task.SessionExists = true
task.AgentWindowExists = true
task.EditorWindowExists = true

if err := s.tmux.SendKeysToWindow(ctx, task.TmuxSession, task.AgentWindowName, command); err != nil {
	return s.markBroken(ctx, task, fmt.Errorf("launch codex: %w", err))
}
```

- [ ] **Step 5: Update reconciliation and open/cleanup behavior**

Adjust `reconcileTask` and related call sites:

```go
sessionExists, err := s.tmux.SessionExists(ctx, reconciled.TmuxSession)
if err != nil {
	return nil, err
}
reconciled.SessionExists = sessionExists

if sessionExists {
	reconciled.AgentWindowExists, err = s.tmux.WindowExists(ctx, reconciled.TmuxSession, reconciled.AgentWindowName)
	if err != nil {
		return nil, err
	}
	reconciled.EditorWindowExists, err = s.tmux.WindowExists(ctx, reconciled.TmuxSession, reconciled.EditorWindowName)
	if err != nil {
		return nil, err
	}
} else {
	reconciled.AgentWindowExists = false
	reconciled.EditorWindowExists = false
}
```

Then classify state like this:

```go
switch {
case task.Status == TaskStatusCleaned && !reconciled.WorktreeExists && !reconciled.SessionExists:
	reconciled.Status = TaskStatusCleaned
case !reconciled.WorktreeExists || !reconciled.BranchExists || !reconciled.SessionExists || !reconciled.AgentWindowExists:
	reconciled.Status = TaskStatusBroken
case !reconciled.EditorWindowExists:
	reconciled.Status = TaskStatusDegraded
default:
	reconciled.Status = TaskStatusRunning
}
```

Update `OpenTask` so it opens `running` and `degraded` tasks when `SessionExists` and `AgentWindowExists` are both true, and update cleanup so killing the session sets:

```go
task.SessionExists = false
task.AgentWindowExists = false
task.EditorWindowExists = false
```

- [ ] **Step 6: Re-run the focused and full core tests**

Run: `go test ./internal/core -run 'TestService(NewTask|GetTask|OpenTask|DeleteTaskResources)' -v`
Expected: PASS

Run: `go test ./internal/core -v`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/core/status.go internal/core/service.go internal/core/fakes_test.go internal/core/service_new_test.go internal/core/service_status_test.go internal/core/service_open_test.go internal/core/service_cleanup_test.go
git commit -m "feat: reconcile tasks against agent and editor windows"
```

## Task 4: Make The TUI The Primary Control Surface

**Files:**
- Modify: `go.mod`
- Modify: `internal/adapters/handler/cli/root.go`
- Modify: `internal/adapters/handler/cli/new.go`
- Modify: `internal/adapters/handler/cli/tui.go`
- Modify: `internal/adapters/handler/cli/tui_model.go`
- Modify: `internal/adapters/handler/cli/new_test.go`
- Modify: `internal/adapters/handler/cli/tui_test.go`
- Modify: `internal/adapters/handler/cli/tui_model_test.go`

- [ ] **Step 1: Add the TUI creation tests and richer list rendering tests**

Extend `internal/adapters/handler/cli/tui_model_test.go` with cases like:

```go
func TestModelUpdate_NEntersPromptEntryMode(t *testing.T) {
	m := newLoadedTUIModel(t, &fakeTUIService{}, tuiTask("task-one"))

	m, _ = updateTUIModel(t, m, keyRunes("n"))

	require.Equal(t, tuiModePromptInput, m.mode)
	require.Contains(t, m.View(), "New task prompt")
}

func TestModelUpdate_CreateFlowSuggestsNameAndCreatesTask(t *testing.T) {
	service := &fakeTUIService{
		suggestedName: "billing retry flow",
		createdTask:   tuiTask("billing-retry-flow"),
	}
	m := newLoadedTUIModel(t, service, tuiTask("task-one"))

	m, _ = updateTUIModel(t, m, keyRunes("n"))
	m.promptInput.SetValue("add billing retry flow")
	m, cmd := updateTUIModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	msg := cmd()
	m, _ = updateTUIModel(t, m, msg)
	require.Equal(t, tuiModeNameConfirm, m.mode)

	m.nameInput.SetValue("billing retry flow")
	m, cmd = updateTUIModel(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	require.NotNil(t, cmd)

	createMsg := cmd()
	require.Equal(t, "add billing retry flow", service.createdInput.Prompt)
	require.Equal(t, "billing retry flow", service.createdInput.ConfirmedDisplayName)
}
```

Also update the main-list rendering test so each row includes:

- repo name
- `agent` window status
- `editor` window status

- [ ] **Step 2: Run the CLI handler tests to verify red**

Run: `go test ./internal/adapters/handler/cli -v`
Expected: FAIL because the TUI has no create state, no `n` key, and the shared service interface does not expose progress-aware creation.

- [ ] **Step 3: Add `bubbles/textinput` and expose progress-aware creation through `TaskService`**

Update `go.mod` to add:

```go
require github.com/charmbracelet/bubbles v0.21.0
```

Update `internal/adapters/handler/cli/root.go`:

```go
type TaskService interface {
	Doctor(ctx context.Context, cwd string) (core.DoctorResult, error)
	SuggestTaskName(ctx context.Context, prompt string) (string, error)
	NewTask(ctx context.Context, input core.NewTaskInput) (*core.Task, error)
	CreateTaskWithProgress(ctx context.Context, input core.NewTaskInput, options core.CreateTaskOptions, progress func(core.TaskProgress)) (*core.Task, error)
	ListTasks(ctx context.Context) ([]*core.Task, error)
	GetTask(ctx context.Context, idOrSlug string) (*core.Task, error)
	OpenTask(ctx context.Context, idOrSlug string) error
	DeleteTaskResources(ctx context.Context, idOrSlug string) (*core.Task, error)
}
```

Then simplify `internal/adapters/handler/cli/new.go` so it uses `deps.Service.CreateTaskWithProgress(...)` directly instead of the local `taskCreationService` type assertion.

- [ ] **Step 4: Add explicit TUI modes for list, cleanup confirmation, prompt input, and name confirmation**

Reshape `internal/adapters/handler/cli/tui_model.go` around a mode enum and Bubble Tea text inputs:

```go
type tuiMode int

const (
	tuiModeList tuiMode = iota
	tuiModeCleanupConfirm
	tuiModePromptInput
	tuiModeNameConfirm
)

type model struct {
	service      TaskService
	tasks        []*core.Task
	selected     int
	loading      bool
	busy         bool
	mode         tuiMode
	err          error
	promptInput  textinput.Model
	nameInput    textinput.Model
	draftPrompt  string
}
```

Add commands/messages for:

- suggesting a name from the entered prompt
- creating the task with the confirmed display name
- refreshing the task list after cleanup
- quitting the TUI after successful open or successful create, since tmux attach/switch will take over the terminal

Use concrete message types:

```go
type suggestedNameMsg struct {
	prompt string
	name   string
	err    error
}

type taskCreatedMsg struct {
	task *core.Task
	err  error
}
```

- [ ] **Step 5: Render the richer control-center list and wire the new keybindings**

Update the list view and key handling in `internal/adapters/handler/cli/tui_model.go`:

```go
b.WriteString("Agent control center\n")
b.WriteString("j/k: move  g/G: jump  enter: open  n: new  x: clean up  r: refresh  q: quit\n\n")

fmt.Fprintf(
	&b,
	"%s %s | repo: %s | status: %s | tmux: %s | worktree: %s | agent: %s | editor: %s | branch: %s\n",
	marker,
	task.DisplayName,
	task.RepoName,
	task.Status,
	yesNo(task.SessionExists),
	yesNo(task.WorktreeExists),
	yesNo(task.AgentWindowExists),
	yesNo(task.EditorWindowExists),
	task.BranchName,
)
```

Handle these keys:

- `n` from list mode enters prompt input
- `enter` from prompt input asks `SuggestTaskName`
- `enter` from name confirmation calls `CreateTaskWithProgress` with `OpenSession: true`
- `esc` from either create mode returns to the main list
- `x` still opens cleanup confirmation from list mode only

- [ ] **Step 6: Update the TUI service fakes**

Extend the fake services in `internal/adapters/handler/cli/tui_model_test.go`, `new_test.go`, `list_test.go`, and `status_test.go` with:

```go
suggestedName string
createdTask   *core.Task
createdInput  core.NewTaskInput
createErr     error

func (f *fakeTUIService) CreateTaskWithProgress(_ context.Context, input core.NewTaskInput, _ core.CreateTaskOptions, _ func(core.TaskProgress)) (*core.Task, error) {
	f.createdInput = input
	return f.createdTask, f.createErr
}
```

- [ ] **Step 7: Re-run the CLI handler tests**

Run: `go test ./internal/adapters/handler/cli -v`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add go.mod internal/adapters/handler/cli/root.go internal/adapters/handler/cli/new.go internal/adapters/handler/cli/tui.go internal/adapters/handler/cli/tui_model.go internal/adapters/handler/cli/new_test.go internal/adapters/handler/cli/tui_test.go internal/adapters/handler/cli/tui_model_test.go
git commit -m "feat: make the TUI the primary agent control center"
```

## Task 5: Refresh CLI Readouts, Docs, And Full Verification

**Files:**
- Modify: `internal/adapters/handler/cli/list.go`
- Modify: `internal/adapters/handler/cli/status.go`
- Modify: `internal/adapters/handler/cli/list_test.go`
- Modify: `internal/adapters/handler/cli/status_test.go`
- Modify: `README.md`

- [ ] **Step 1: Write the failing list and status output tests**

Update `internal/adapters/handler/cli/list_test.go`:

```go
func TestListCommand_PrintsRepoAndWindowHealth(t *testing.T) {
	out := &bytes.Buffer{}
	service := fakeListCLIService{
		tasks: []*core.Task{{
			DisplayName:       "billing retry flow",
			RepoName:          "repo",
			Provider:          "codex",
			Status:            core.TaskStatusDegraded,
			TmuxSession:       "repo-billing-retry-flow",
			BranchName:        "feat/billing-retry-flow",
			AgentWindowExists: true,
			EditorWindowExists: false,
		}},
	}

	cmd := newListCommand(Dependencies{Service: service, Stdout: out, Stderr: out})
	cmd.SetOut(out)
	cmd.SetErr(out)

	require.NoError(t, cmd.Execute())
	require.Contains(t, out.String(), "repo")
	require.Contains(t, out.String(), "degraded")
	require.Contains(t, out.String(), "agent")
	require.Contains(t, out.String(), "editor")
}
```

Update `internal/adapters/handler/cli/status_test.go`:

```go
require.Contains(t, out.String(), "Repo: repo")
require.Contains(t, out.String(), "AgentWindow: agent")
require.Contains(t, out.String(), "AgentWindowExists: true")
require.Contains(t, out.String(), "EditorWindowExists: false")
```

- [ ] **Step 2: Run the focused CLI tests to verify red**

Run: `go test ./internal/adapters/handler/cli -run 'Test(ListCommand|StatusCommand)' -v`
Expected: FAIL because the CLI output does not expose repo or tmux-window health yet.

- [ ] **Step 3: Update the CLI output for richer debugging**

Update `internal/adapters/handler/cli/list.go`:

```go
if _, err = fmt.Fprintln(cmd.OutOrStdout(), "NAME\tREPO\tPROVIDER\tSTATUS\tAGENT\tEDITOR\tSESSION\tBRANCH"); err != nil {
	return err
}

for _, task := range tasks {
	if _, err = fmt.Fprintf(
		cmd.OutOrStdout(),
		"%s\t%s\t%s\t%s\t%t\t%t\t%s\t%s\n",
		task.DisplayName,
		task.RepoName,
		task.Provider,
		task.Status,
		task.AgentWindowExists,
		task.EditorWindowExists,
		task.TmuxSession,
		task.BranchName,
	); err != nil {
		return err
	}
}
```

Update `internal/adapters/handler/cli/status.go`:

```go
_, err = fmt.Fprintf(
	cmd.OutOrStdout(),
	"Name: %s\nSlug: %s\nRepo: %s\nStatus: %s\nSession: %s\nAgentWindow: %s\nEditorWindow: %s\nWorktree: %s\nWorktreeExists: %t\nBranchExists: %t\nSessionExists: %t\nAgentWindowExists: %t\nEditorWindowExists: %t\n",
	task.DisplayName,
	task.Slug,
	task.RepoName,
	task.Status,
	task.TmuxSession,
	task.AgentWindowName,
	task.EditorWindowName,
	task.WorktreePath,
	task.WorktreeExists,
	task.BranchExists,
	task.SessionExists,
	task.AgentWindowExists,
	task.EditorWindowExists,
)
```

- [ ] **Step 4: Update the README to match the real product**

Rewrite the stale sections in `README.md` so they describe:

- TUI-first workflow
- multi-window tmux sessions
- `agent` and `editor` window contract
- `agent tui` as the default daily entry point
- `n` for creation, `enter` for opening, `x` for cleanup

Use wording like:

```md
The first release is centered on `agent tui`. Each task maps to a git worktree and a tmux session with a required `agent` window and a seeded `editor` window. `agent` owns the `agent` window for automation; everything else in the session is user-customizable.
```

- [ ] **Step 5: Run the full test suite**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/adapters/handler/cli/list.go internal/adapters/handler/cli/status.go internal/adapters/handler/cli/list_test.go internal/adapters/handler/cli/status_test.go README.md
git commit -m "docs: refresh control-center CLI and README"
```
