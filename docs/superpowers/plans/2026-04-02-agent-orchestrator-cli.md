# Agent Orchestrator CLI Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the first working version of the `agent` Go CLI that creates Codex-backed task worktrees and tmux sessions, persists them in SQLite, and supports `new`, `ls`, `open`, `status`, and `doctor`.

**Architecture:** The CLI should follow a hexagonal structure. `cmd/agent` stays thin, `internal/adapters/handler/cli` owns Cobra wiring and user-facing flows, `internal/core` owns task orchestration and reconciliation, and adapters under `internal/adapters/repository` wrap SQLite, git, tmux, and Codex. The implementation should prefer deterministic local orchestration with Codex used only for naming and launch.

**Tech Stack:** Go, Cobra, SQLite, `os/exec`, tmux, git, Codex CLI

---

## File Structure

Create or modify the following files during implementation.

### Project Root

- Create: `go.mod`
- Create: `.gitignore`
- Create: `README.md`

### Entrypoint And CLI Handler

- Create: `cmd/agent/main.go`
- Create: `internal/adapters/handler/cli/root.go`
- Create: `internal/adapters/handler/cli/root_test.go`
- Create: `internal/adapters/handler/cli/new.go`
- Create: `internal/adapters/handler/cli/new_test.go`
- Create: `internal/adapters/handler/cli/list.go`
- Create: `internal/adapters/handler/cli/list_test.go`
- Create: `internal/adapters/handler/cli/open.go`
- Create: `internal/adapters/handler/cli/open_test.go`
- Create: `internal/adapters/handler/cli/status.go`
- Create: `internal/adapters/handler/cli/status_test.go`
- Create: `internal/adapters/handler/cli/doctor.go`
- Create: `internal/adapters/handler/cli/doctor_test.go`

### Core

- Create: `internal/core/task.go`
- Create: `internal/core/task_test.go`
- Create: `internal/core/status.go`
- Create: `internal/core/errors.go`
- Create: `internal/core/config.go`
- Create: `internal/core/ports.go`
- Create: `internal/core/service.go`
- Create: `internal/core/service_new_test.go`
- Create: `internal/core/service_list_test.go`
- Create: `internal/core/service_status_test.go`
- Create: `internal/core/service_open_test.go`
- Create: `internal/core/service_doctor_test.go`

### Repository And System Adapters

- Create: `internal/adapters/repository/sqlite/repository.go`
- Create: `internal/adapters/repository/sqlite/repository_test.go`
- Create: `internal/adapters/repository/git/repository.go`
- Create: `internal/adapters/repository/git/repository_test.go`
- Create: `internal/adapters/repository/tmux/repository.go`
- Create: `internal/adapters/repository/tmux/repository_test.go`
- Create: `internal/adapters/repository/codex/repository.go`
- Create: `internal/adapters/repository/codex/repository_test.go`

### Shared Helpers

- Create: `internal/pkg/execx/runner.go`
- Create: `internal/pkg/execx/runner_test.go`
- Create: `internal/pkg/slug/slug.go`
- Create: `internal/pkg/slug/slug_test.go`
- Create: `internal/pkg/timeutil/clock.go`

### Integration-Smoke Layer

- Create: `internal/core/fakes_test.go`

## Implementation Notes

- Keep the first version single-provider: `codex`.
- Do not implement cleanup, merge, hooks, or multi-window tmux layouts.
- Default the SQLite DB to `~/.local/share/agent/state.db`, but allow config override.
- Default the worktree root strategy to sibling-based.
- Default `agent new` to interactive mode and attach into the created session.
- Support a non-interactive path for parent-agent usage before calling the CLI "done".

### Core Types To Introduce Early

```go
type TaskStatus string

const (
    TaskStatusCreating TaskStatus = "creating"
    TaskStatusReady    TaskStatus = "ready"
    TaskStatusRunning  TaskStatus = "running"
    TaskStatusBroken   TaskStatus = "broken"
)

type Task struct {
    ID               string
    Prompt           string
    DisplayName      string
    Slug             string
    RepoRoot         string
    BaseBranch       string
    BranchName       string
    WorktreePath     string
    TmuxSession      string
    Provider         string
    Status           TaskStatus
    WorktreeExists   bool
    BranchExists     bool
    SessionExists    bool
    LastError        string
    CreatedAt        time.Time
    UpdatedAt        time.Time
    LastReconciledAt time.Time
}
```

### Ports To Keep Stable

```go
type TaskRepository interface {
    CreateTask(ctx context.Context, task *Task) error
    UpdateTask(ctx context.Context, task *Task) error
    GetTask(ctx context.Context, idOrSlug string) (*Task, error)
    ListTasks(ctx context.Context) ([]*Task, error)
    AppendEvent(ctx context.Context, taskID, eventType, payload string) error
}

type GitRepository interface {
    IsAvailable(ctx context.Context) error
    DetectRepo(ctx context.Context, cwd string) (RepoContext, error)
    BranchExists(ctx context.Context, repoRoot, branch string) (bool, error)
    CreateWorktree(ctx context.Context, in CreateWorktreeInput) error
}

type TmuxRepository interface {
    IsAvailable(ctx context.Context) error
    SessionExists(ctx context.Context, session string) (bool, error)
    CreateSession(ctx context.Context, in CreateSessionInput) error
    AttachOrSwitch(ctx context.Context, session string) error
    SendKeys(ctx context.Context, session string, command []string) error
}

type CodexRepository interface {
    ProposeTaskName(ctx context.Context, prompt string) (string, error)
    BuildLaunchCommand(task *Task) ([]string, error)
    IsAvailable(ctx context.Context) error
}
```

### Task 1: Bootstrap The Project Skeleton

**Files:**
- Create: `go.mod`
- Create: `.gitignore`
- Create: `README.md`
- Create: `cmd/agent/main.go`
- Create: `internal/adapters/handler/cli/root.go`
- Create: `internal/adapters/handler/cli/new.go`
- Create: `internal/adapters/handler/cli/list.go`
- Create: `internal/adapters/handler/cli/open.go`
- Create: `internal/adapters/handler/cli/status.go`
- Create: `internal/adapters/handler/cli/doctor.go`
- Test: `internal/adapters/handler/cli/root_test.go`

- [ ] **Step 1: Write the failing root command test**

```go
func TestNewRootCommand_HelpIncludesSubcommands(t *testing.T) {
    out := &bytes.Buffer{}
    cmd := NewRootCommand(Dependencies{})
    cmd.SetOut(out)
    cmd.SetArgs([]string{"--help"})

    err := cmd.Execute()
    require.NoError(t, err)

    output := out.String()
    require.Contains(t, output, "new")
    require.Contains(t, output, "doctor")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/handler/cli -run TestNewRootCommand_HelpIncludesSubcommands -v`
Expected: FAIL because `NewRootCommand` does not exist

- [ ] **Step 3: Create the module and minimal Cobra root**

Create `go.mod` using the real repository module path if a remote already exists. If no remote exists yet, use a temporary local module path such as:

```go
module agent

go 1.26.1
```

```go
func NewRootCommand(deps Dependencies) *cobra.Command {
    cmd := &cobra.Command{
        Use: "agent",
    }
    cmd.AddCommand(newNewCommand(deps))
    cmd.AddCommand(newListCommand(deps))
    cmd.AddCommand(newOpenCommand(deps))
    cmd.AddCommand(newStatusCommand(deps))
    cmd.AddCommand(newDoctorCommand(deps))
    return cmd
}
```

Also create placeholder subcommand constructors in `new.go`, `list.go`, `open.go`, `status.go`, and `doctor.go` so the module compiles before the later tasks fill in real behavior.

- [ ] **Step 4: Re-run the focused CLI test**

Run: `go test ./internal/adapters/handler/cli -run TestNewRootCommand_HelpIncludesSubcommands -v`
Expected: PASS

- [ ] **Step 5: Add baseline project files**

```gitignore
bin/
dist/
.DS_Store
*.db
*.db-shm
*.db-wal
```

Document the current command surface and v1 scope in `README.md`.

- [ ] **Step 6: Run the full test suite**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add go.mod .gitignore README.md cmd/agent/main.go internal/adapters/handler/cli/root.go internal/adapters/handler/cli/root_test.go
git commit -m "feat: scaffold agent CLI"
```

### Task 2: Define Core Models, Ports, And Config

**Files:**
- Create: `internal/core/task.go`
- Create: `internal/core/task_test.go`
- Create: `internal/core/status.go`
- Create: `internal/core/errors.go`
- Create: `internal/core/config.go`
- Create: `internal/core/ports.go`
- Test: `internal/pkg/slug/slug_test.go`
- Create: `internal/pkg/slug/slug.go`
- Create: `internal/pkg/timeutil/clock.go`

- [ ] **Step 1: Write the failing slug and naming tests**

```go
func TestSlugFromDisplayName_NormalizesToLowerKebabCase(t *testing.T) {
    got := slug.FromDisplayName("Billing Retry Flow")
    require.Equal(t, "billing-retry-flow", got)
}

func TestSlugEnsureUnique_AppendsNumericSuffix(t *testing.T) {
    got := slug.EnsureUnique("billing-retry-flow", map[string]struct{}{
        "billing-retry-flow":   {},
        "billing-retry-flow-2": {},
    })
    require.Equal(t, "billing-retry-flow-3", got)
}
```

- [ ] **Step 2: Run the focused naming tests**

Run: `go test ./internal/pkg/slug -v`
Expected: FAIL because slug helpers do not exist

- [ ] **Step 3: Implement the minimal core types and slug helpers**

```go
type Config struct {
    BaseBranch    string
    DatabasePath  string
    WorktreeMode  string
    CodexBinary   string
    AttachOnNew   bool
    NonInteractive bool
}
```

Keep `RepoContext`, `CreateWorktreeInput`, and `CreateSessionInput` in `ports.go` so the service can stay adapter-agnostic.

- [ ] **Step 4: Add core errors and status helpers**

```go
var (
    ErrTaskNotFound = errors.New("task not found")
    ErrBrokenTask   = errors.New("task is broken")
)
```

- [ ] **Step 5: Re-run the focused tests**

Run: `go test ./internal/pkg/slug ./internal/core -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/core/task.go internal/core/task_test.go internal/core/status.go internal/core/errors.go internal/core/config.go internal/core/ports.go internal/pkg/slug/slug.go internal/pkg/slug/slug_test.go internal/pkg/timeutil/clock.go
git commit -m "feat: add core task model and naming primitives"
```

### Task 3: Build Command Runner And Codex/Git/Tmux Adapters

**Files:**
- Create: `internal/pkg/execx/runner.go`
- Create: `internal/pkg/execx/runner_test.go`
- Create: `internal/adapters/repository/codex/repository.go`
- Create: `internal/adapters/repository/codex/repository_test.go`
- Create: `internal/adapters/repository/git/repository.go`
- Create: `internal/adapters/repository/git/repository_test.go`
- Create: `internal/adapters/repository/tmux/repository.go`
- Create: `internal/adapters/repository/tmux/repository_test.go`

- [ ] **Step 1: Write the failing adapter tests using a fake runner**

```go
func TestCodexRepositoryBuildLaunchCommand_IncludesPrompt(t *testing.T) {
    repo := codex.NewRepository("codex")
    cmd, err := repo.BuildLaunchCommand(&core.Task{
        Prompt: "add billing retry flow",
    })
    require.NoError(t, err)
    require.Equal(t, "codex", cmd[0])
    require.Equal(t, "add billing retry flow", cmd[len(cmd)-1])
}
```

```go
func TestGitRepositoryDetectRepo_ParsesTopLevelAndBranch(t *testing.T) {
    runner := execx.NewFakeRunner([]execx.Result{
        {Stdout: "/tmp/repo\n", Err: nil},
        {Stdout: "main\n", Err: nil},
        {Stdout: "demo\n", Err: nil},
    })
    repo := gitrepo.NewRepository(runner)
    ctx, err := repo.DetectRepo(context.Background(), "/tmp/repo")
    require.NoError(t, err)
    require.Equal(t, "/tmp/repo", ctx.Root)
    require.Equal(t, "main", ctx.BaseBranch)
    require.Equal(t, "demo", ctx.Name)
}
```

- [ ] **Step 2: Run the focused adapter tests**

Run: `go test ./internal/pkg/execx ./internal/adapters/repository/... -v`
Expected: FAIL because the runner and adapters do not exist

- [ ] **Step 3: Implement the reusable command runner**

```go
type Runner interface {
    Run(ctx context.Context, cwd string, name string, args ...string) (Result, error)
}
```

Include a fake implementation for adapter tests so the tests never shell out to real binaries.

- [ ] **Step 4: Implement the Codex adapter**

Use one method to propose a short title and one to build the final launch command. Keep the title prompt deterministic and short so the adapter returns a plain short title.

- [ ] **Step 5: Implement the Git and tmux adapters**

Git adapter must cover:
- repo detection
- branch existence checks
- worktree creation

Tmux adapter must cover:
- session existence
- detached session creation in a target directory
- attach-or-switch behavior
- send-keys command injection

- [ ] **Step 6: Re-run the adapter tests**

Run: `go test ./internal/pkg/execx ./internal/adapters/repository/... -v`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/pkg/execx/runner.go internal/pkg/execx/runner_test.go internal/adapters/repository/codex/repository.go internal/adapters/repository/codex/repository_test.go internal/adapters/repository/git/repository.go internal/adapters/repository/git/repository_test.go internal/adapters/repository/tmux/repository.go internal/adapters/repository/tmux/repository_test.go
git commit -m "feat: add external system adapters"
```

### Task 4: Add SQLite Task Storage

**Files:**
- Create: `internal/adapters/repository/sqlite/repository.go`
- Create: `internal/adapters/repository/sqlite/repository_test.go`
- Modify: `internal/core/ports.go`

- [ ] **Step 1: Write the failing SQLite repository tests**

```go
func TestRepositoryCreateAndGetTask(t *testing.T) {
    repo := newTestRepository(t)
    task := &core.Task{
        ID:          "task-1",
        DisplayName: "billing retry flow",
        Slug:        "billing-retry-flow",
        Provider:    "codex",
        Status:      core.TaskStatusCreating,
    }

    require.NoError(t, repo.CreateTask(context.Background(), task))

    got, err := repo.GetTask(context.Background(), "billing-retry-flow")
    require.NoError(t, err)
    require.Equal(t, task.DisplayName, got.DisplayName)
}
```

- [ ] **Step 2: Run the focused SQLite tests**

Run: `go test ./internal/adapters/repository/sqlite -v`
Expected: FAIL because the repository does not exist

- [ ] **Step 3: Implement schema creation and repository methods**

Create the schema on startup in the repository constructor for now. Keep it simple:

```sql
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
```

- [ ] **Step 4: Add event appends and list ordering**

Order `ListTasks` by `updated_at desc` so the freshest task appears first.

- [ ] **Step 5: Re-run the SQLite tests**

Run: `go test ./internal/adapters/repository/sqlite -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/adapters/repository/sqlite/repository.go internal/adapters/repository/sqlite/repository_test.go internal/core/ports.go
git commit -m "feat: add sqlite task repository"
```

### Task 5: Implement The Core Service And `doctor`

**Files:**
- Create: `internal/core/service.go`
- Create: `internal/core/service_doctor_test.go`
- Create: `internal/adapters/handler/cli/doctor.go`
- Create: `internal/adapters/handler/cli/doctor_test.go`
- Create: `internal/core/fakes_test.go`

- [ ] **Step 1: Write the failing doctor service test**

```go
func TestServiceDoctor_ReturnsMissingBinaryFailures(t *testing.T) {
    svc := newTestService()
    svc.codexRepo.isAvailableErr = errors.New("missing codex")

    result, err := svc.Doctor(context.Background(), "/tmp/repo")
    require.NoError(t, err)
    require.Contains(t, result.Failures, "missing codex")
}
```

- [ ] **Step 2: Run the focused doctor tests**

Run: `go test ./internal/core -run TestServiceDoctor_ReturnsMissingBinaryFailures -v`
Expected: FAIL because `Doctor` does not exist

- [ ] **Step 3: Implement the core service container**

Start with constructor injection:

```go
type Service struct {
    tasks TaskRepository
    git   GitRepository
    tmux  TmuxRepository
    codex CodexRepository
    clock timeutil.Clock
    cfg   Config
}
```

- [ ] **Step 4: Implement `Doctor` and wire the CLI command**

Doctor must check:
- git binary availability
- tmux binary availability
- Codex availability
- SQLite path creatability
- git repository usability when a cwd is supplied

Print a readable human summary in the CLI handler.

- [ ] **Step 5: Re-run the core and CLI doctor tests**

Run: `go test ./internal/core ./internal/adapters/handler/cli -run Doctor -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/core/service.go internal/core/service_doctor_test.go internal/core/fakes_test.go internal/adapters/handler/cli/doctor.go internal/adapters/handler/cli/doctor_test.go
git commit -m "feat: add doctor command and core service wiring"
```

### Task 6: Implement `agent new`

**Files:**
- Create: `internal/core/service_new_test.go`
- Create: `internal/adapters/handler/cli/new.go`
- Create: `internal/adapters/handler/cli/new_test.go`
- Modify: `internal/core/service.go`

- [ ] **Step 1: Write the failing core `new` workflow test**

```go
func TestServiceNewTask_CreatesWorktreeSessionAndPersistsTask(t *testing.T) {
    svc := newTestService()
    svc.git.repoContext = core.RepoContext{
        Root:       "/tmp/repo",
        Name:       "repo",
        BaseBranch: "main",
    }
    svc.codex.proposedName = "billing retry flow"

    task, err := svc.NewTask(context.Background(), core.NewTaskInput{
        Prompt: "add billing retry flow",
        ConfirmedDisplayName: "billing retry flow",
    })

    require.NoError(t, err)
    require.Equal(t, "feat/billing-retry-flow", task.BranchName)
    require.Equal(t, "/tmp/repo-billing-retry-flow", task.WorktreePath)
    require.Equal(t, "repo:billing-retry-flow", task.TmuxSession)
    require.Equal(t, core.TaskStatusRunning, task.Status)
}
```

- [ ] **Step 2: Run the focused `new` tests**

Run: `go test ./internal/core -run TestServiceNewTask_CreatesWorktreeSessionAndPersistsTask -v`
Expected: FAIL because `NewTask` does not exist

- [ ] **Step 3: Implement the service workflow**

Workflow details:
- detect repo
- propose name
- accept confirmed or override name from caller
- normalize slug
- check for collisions against existing tasks
- create branch and sibling worktree
- create detached tmux session
- build Codex launch command
- send launch command into tmux
- persist task and append events

- [ ] **Step 4: Implement the CLI handler**

Support both modes:
- interactive mode prompts to confirm or edit the proposed name
- non-interactive mode accepts the proposed name automatically

Also add a machine-readable output mode for parent-agent usage. JSON is sufficient.

- [ ] **Step 5: Add failure-path tests**

Cover:
- Codex title proposal failure falling back to local title
- tmux creation failure persisting a broken task
- Codex launch failure persisting a broken task

- [ ] **Step 6: Re-run focused tests**

Run: `go test ./internal/core ./internal/adapters/handler/cli -run New -v`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/core/service.go internal/core/service_new_test.go internal/adapters/handler/cli/new.go internal/adapters/handler/cli/new_test.go
git commit -m "feat: add task creation workflow"
```

### Task 7: Implement `agent ls`, `agent status`, And Reconciliation

**Files:**
- Create: `internal/core/service_list_test.go`
- Create: `internal/core/service_status_test.go`
- Create: `internal/adapters/handler/cli/list.go`
- Create: `internal/adapters/handler/cli/list_test.go`
- Create: `internal/adapters/handler/cli/status.go`
- Create: `internal/adapters/handler/cli/status_test.go`
- Modify: `internal/core/service.go`

- [ ] **Step 1: Write the failing reconciliation tests**

```go
func TestServiceListTasks_MarksMissingTmuxSessionAsBroken(t *testing.T) {
    svc := newTestServiceWithTask(core.Task{
        ID:          "task-1",
        Slug:        "billing-retry-flow",
        TmuxSession: "repo:billing-retry-flow",
        Status:      core.TaskStatusRunning,
    })
    svc.tmux.sessionExists = false

    tasks, err := svc.ListTasks(context.Background())
    require.NoError(t, err)
    require.Equal(t, core.TaskStatusBroken, tasks[0].Status)
    require.Contains(t, tasks[0].LastError, "missing tmux session")
}
```

- [ ] **Step 2: Run the focused reconciliation tests**

Run: `go test ./internal/core -run 'TestService(ListTasks|Status)' -v`
Expected: FAIL because list/status behavior does not exist

- [ ] **Step 3: Implement shared reconciliation helpers**

Create one internal reconciliation helper in `service.go` so `ListTasks` and `GetTaskStatus` do not duplicate:
- worktree existence check
- branch existence check
- tmux session existence check
- status transition to `broken`
- `updated_at` and `last_reconciled_at` refresh

- [ ] **Step 4: Implement `ls` and `status` CLI rendering**

`ls` should print a stable table with:
- NAME
- PROVIDER
- STATUS
- SESSION
- BRANCH

`status` should print the full task details and the reconciled booleans.

- [ ] **Step 5: Re-run focused tests**

Run: `go test ./internal/core ./internal/adapters/handler/cli -run 'List|Status' -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/core/service.go internal/core/service_list_test.go internal/core/service_status_test.go internal/adapters/handler/cli/list.go internal/adapters/handler/cli/list_test.go internal/adapters/handler/cli/status.go internal/adapters/handler/cli/status_test.go
git commit -m "feat: add task listing and status reconciliation"
```

### Task 8: Implement `agent open`

**Files:**
- Create: `internal/core/service_open_test.go`
- Create: `internal/adapters/handler/cli/open.go`
- Create: `internal/adapters/handler/cli/open_test.go`
- Modify: `internal/core/service.go`

- [ ] **Step 1: Write the failing `open` test**

```go
func TestServiceOpenTask_AttachesWhenSessionExists(t *testing.T) {
    svc := newTestServiceWithTask(core.Task{
        ID:          "task-1",
        Slug:        "billing-retry-flow",
        TmuxSession: "repo:billing-retry-flow",
    })
    svc.tmux.sessionExists = true

    err := svc.OpenTask(context.Background(), "billing-retry-flow")
    require.NoError(t, err)
    require.Equal(t, "repo:billing-retry-flow", svc.tmux.attachedSession)
}
```

- [ ] **Step 2: Run the focused `open` tests**

Run: `go test ./internal/core ./internal/adapters/handler/cli -run Open -v`
Expected: FAIL because `OpenTask` does not exist

- [ ] **Step 3: Implement `OpenTask`**

Behavior:
- resolve by ID or slug
- reconcile session existence
- fail clearly if the task is broken due to a missing session
- attach or switch using the tmux adapter

- [ ] **Step 4: Re-run focused tests**

Run: `go test ./internal/core ./internal/adapters/handler/cli -run Open -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/core/service.go internal/core/service_open_test.go internal/adapters/handler/cli/open.go internal/adapters/handler/cli/open_test.go
git commit -m "feat: add task open command"
```

### Task 9: Final Verification And Documentation Pass

**Files:**
- Modify: `README.md`
- Modify: `docs/superpowers/specs/2026-04-02-agent-orchestrator-cli-design.md` only if implementation-driven clarifications are required

- [ ] **Step 1: Add a focused README usage section**

Document:
- required binaries
- config file path
- default SQLite location
- default sibling worktree behavior
- example commands for `new`, `ls`, `open`, `status`, `doctor`
- interactive vs non-interactive `new`

- [ ] **Step 2: Run the full test suite**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 3: Run a manual smoke checklist**

Run these commands in a real repo with tmux and Codex installed:

```bash
agent doctor
agent new "add billing retry flow"
agent ls
agent status billing-retry-flow
agent open billing-retry-flow
```

Expected:
- `doctor` reports healthy dependencies
- `new` proposes a name, creates resources, and attaches into tmux
- `ls` shows the task as `running`
- `status` shows reconciled live fields
- `open` re-attaches or switches successfully

- [ ] **Step 4: Commit**

```bash
git add README.md
git commit -m "docs: finalize agent CLI usage"
```

## Review Checklist

Before implementation begins, verify that the plan still matches:

- [ ] [docs/superpowers/specs/2026-04-02-agent-orchestrator-cli-design.md](/Users/ebon/personal_software/tmux-llm-session/docs/superpowers/specs/2026-04-02-agent-orchestrator-cli-design.md)
- [ ] This plan keeps v1 single-provider
- [ ] This plan keeps v1 single-window tmux sessions
- [ ] This plan keeps merge and cleanup out of scope
- [ ] This plan includes non-interactive support for parent-agent callers
- [ ] This plan includes TDD and commit checkpoints
