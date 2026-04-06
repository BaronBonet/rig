# Core Boundary Restructure Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restructure the codebase so `internal/core` contains only high-level business logic, application wiring lives in `cmd/agent/main.go`, config loading lives in `internal/infrastructure`, and adapters are split into `repository`, `client`, and `filesystem` with clear responsibilities.

**Architecture:** Keep a single `internal/core` package, but collapse it to `domain.go`, `service.go`, `ports.go`, and `errors.go`. Introduce `internal/infrastructure/config.go` for env loading with `github.com/caarlos0/env/v11`, replace the lazy `runtimeService` composition pattern with explicit wiring in `cmd/agent/main.go`, and raise the `core` port surface so the service speaks in business capabilities rather than tmux command sequences.

**Tech Stack:** Go 1.26, Cobra CLI, Bubble Tea TUI, `github.com/caarlos0/env/v11`, SQLite (`modernc.org/sqlite`), tmux, git

---

## File Structure

### Planned file responsibilities

- `internal/infrastructure/config.go`
  Loads environment variables, applies defaults, and returns a composed application config.
- `internal/infrastructure/config_test.go`
  Verifies config defaults and env overrides.
- `cmd/agent/main.go`
  Acts as the composition root and constructs adapters plus `core.Service`.
- `cmd/agent/main_test.go`
  Verifies the composition root returns a concrete service and no longer uses `runtimeService`.
- `internal/core/domain.go`
  Holds `Task`, statuses, runtime state, progress types, and the slim `core.Config`.
- `internal/core/ports.go`
  Holds business-oriented ports and supporting value types such as `LaunchRequest`, `RepoResources`, and `SessionResources`.
- `internal/core/service.go`
  Holds orchestration only: create, list, get, open, cleanup, reconcile, doctor.
- `internal/core/errors.go`
  Holds exported application errors.
- `internal/core/fakes_test.go`
  Provides fakes for the revised ports.
- `internal/core/service_*_test.go`
  Verifies the service behavior against the higher-level ports.
- `internal/adapters/repository/sqlite/...`
  Owns SQLite persistence and storage availability checks.
- `internal/adapters/repository/agentconfig/...`
  Owns parsing/loading of the repo-local `agent.yaml` document.
- `internal/adapters/client/git/...`
  Owns git-based repo detection and task workspace inspection/creation/removal.
- `internal/adapters/client/tmux/...`
  Owns tmux lifecycle, launch choreography, prompt waiting, and runtime snapshots.
- `internal/adapters/client/codex/...`
  Owns Codex name suggestion, launch request generation, and runtime-state detection.
- `internal/adapters/client/claude/...`
  Owns Claude name suggestion, launch request generation, and runtime-state detection.
- `internal/adapters/filesystem/workspace/...`
  Owns workspace seed validation and copy behavior.

### Expected deletes after consolidation

- `internal/core/config.go`
- `internal/core/progress.go`
- `internal/core/runtime.go`
- `internal/core/status.go`
- `internal/core/task.go`
- `cmd/agent/main.go` support type `runtimeService`
- `cmd/agent/main.go` support type `noopTaskRepository`

### Package moves

- `internal/adapters/repository/git` -> `internal/adapters/client/git`
- `internal/adapters/repository/tmux` -> `internal/adapters/client/tmux`
- `internal/adapters/repository/codex` -> `internal/adapters/client/codex`
- `internal/adapters/repository/claude` -> `internal/adapters/client/claude`
- `internal/adapters/repository/workspace` -> `internal/adapters/filesystem/workspace`

---

### Task 1: Add Infrastructure Config Loading

**Files:**
- Create: `internal/infrastructure/config.go`
- Create: `internal/infrastructure/config_test.go`
- Modify: `go.mod`
- Modify: `go.sum`
- Modify: `internal/core/config.go`
- Modify: `internal/adapters/repository/sqlite/repository.go`
- Modify: `internal/adapters/repository/codex/repository.go`
- Modify: `internal/adapters/repository/claude/repository.go`

- [ ] **Step 1: Write the failing config-loader test**

```go
package infrastructure

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadConfig_DefaultsAndOverrides(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AGENT_PROVIDER", "claude")
	t.Setenv("AGENT_SQLITE_PATH", filepath.Join(home, "custom.db"))

	cfg, err := LoadConfig()
	require.NoError(t, err)

	require.Equal(t, "claude", cfg.Service.Provider)
	require.Equal(t, filepath.Join(home, "custom.db"), cfg.SQLite.Path)
	require.Equal(t, "codex", cfg.Codex.Binary)
	require.Equal(t, "claude", cfg.Claude.Binary)
}
```

- [ ] **Step 2: Run the new test and verify it fails**

Run: `go test ./internal/infrastructure -run TestLoadConfig_DefaultsAndOverrides -v`

Expected: FAIL because `internal/infrastructure/config.go` and `LoadConfig` do not exist yet.

- [ ] **Step 3: Add the env dependency and plain config structs**

Run:

```bash
go get github.com/caarlos0/env/v11
```

Update the current config types so `core.Config` only keeps app-level values, and concrete adapters own their own config:

```go
// internal/core/config.go
package core

type Config struct {
	Provider string
}
```

```go
// internal/adapters/repository/sqlite/repository.go
type Config struct {
	Path string
}
```

```go
// internal/adapters/repository/codex/repository.go
type Config struct {
	Binary string
}
```

```go
// internal/adapters/repository/claude/repository.go
type Config struct {
	Binary string
}
```

- [ ] **Step 4: Implement the infrastructure loader with defaults**

```go
// internal/infrastructure/config.go
package infrastructure

import (
	"os"
	"path/filepath"

	"github.com/caarlos0/env/v11"

	clauderepo "agent/internal/adapters/repository/claude"
	codexrepo "agent/internal/adapters/repository/codex"
	sqliterepo "agent/internal/adapters/repository/sqlite"
	"agent/internal/core"
)

type Config struct {
	Service core.Config
	SQLite  sqliterepo.Config
	Codex   codexrepo.Config
	Claude  clauderepo.Config
}

type envConfig struct {
	Provider     string `env:"AGENT_PROVIDER" envDefault:"codex"`
	SQLitePath   string `env:"AGENT_SQLITE_PATH"`
	CodexBinary  string `env:"AGENT_CODEX_BINARY" envDefault:"codex"`
	ClaudeBinary string `env:"AGENT_CLAUDE_BINARY" envDefault:"claude"`
}

func LoadConfig() (*Config, error) {
	raw := envConfig{}
	if err := env.Parse(&raw); err != nil {
		return nil, err
	}

	if raw.SQLitePath == "" {
		raw.SQLitePath = defaultSQLitePath()
	}

	return &Config{
		Service: core.Config{
			Provider: raw.Provider,
		},
		SQLite: sqliterepo.Config{
			Path: raw.SQLitePath,
		},
		Codex: codexrepo.Config{
			Binary: raw.CodexBinary,
		},
		Claude: clauderepo.Config{
			Binary: raw.ClaudeBinary,
		},
	}, nil
}

func defaultSQLitePath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".agent/state.db"
	}

	return filepath.Join(home, ".local", "share", "agent", "state.db")
}
```

- [ ] **Step 5: Update adapter constructors to accept config structs**

```go
// internal/adapters/repository/sqlite/repository.go
func NewRepository(cfg Config) (*Repository, error) {
	if err := os.MkdirAll(filepath.Dir(cfg.Path), 0o755); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", cfg.Path)
	if err != nil {
		return nil, err
	}
	// existing initSchema path continues unchanged
}
```

```go
// internal/adapters/repository/codex/repository.go
func NewRepository(runner execx.Runner, cfg Config) *Repository {
	if cfg.Binary == "" {
		cfg.Binary = "codex"
	}
	return &Repository{runner: runner, binary: cfg.Binary}
}
```

```go
// internal/adapters/repository/claude/repository.go
func NewRepository(runner execx.Runner, cfg Config) *Repository {
	if cfg.Binary == "" {
		cfg.Binary = "claude"
	}
	return &Repository{runner: runner, binary: cfg.Binary}
}
```

- [ ] **Step 6: Run the config tests and verify they pass**

Run: `go test ./internal/infrastructure ./internal/adapters/repository/sqlite ./internal/adapters/repository/codex ./internal/adapters/repository/claude -v`

Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum internal/infrastructure/config.go internal/infrastructure/config_test.go internal/core/config.go internal/adapters/repository/sqlite/repository.go internal/adapters/repository/codex/repository.go internal/adapters/repository/claude/repository.go
git commit -m "refactor: add infrastructure config loading"
```

### Task 2: Replace Lazy RuntimeService With Explicit Composition

**Files:**
- Modify: `cmd/agent/main.go`
- Modify: `cmd/agent/main_test.go`

- [ ] **Step 1: Write the failing main wiring test**

```go
package main

import (
	"testing"

	"agent/internal/core"

	"github.com/stretchr/testify/require"
)

func TestBuildDependencies_ReturnsConcreteService(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	deps, err := buildDependencies()
	require.NoError(t, err)
	require.IsType(t, &core.Service{}, deps.Service)
}
```

- [ ] **Step 2: Run the wiring test and verify it fails**

Run: `go test ./cmd/agent -run TestBuildDependencies_ReturnsConcreteService -v`

Expected: FAIL because `buildDependencies()` still returns a `runtimeService`.

- [ ] **Step 3: Rewrite `buildDependencies()` as the composition root**

```go
func buildDependencies() (cli.Dependencies, error) {
	cfg, err := infrastructure.LoadConfig()
	if err != nil {
		return cli.Dependencies{}, err
	}

	runner := execx.ExecRunner{}
	runtimeMonitor := tmuxrepo.NewRuntimeMonitor()

	taskRepo, err := sqliterepo.NewRepository(cfg.SQLite)
	if err != nil {
		return cli.Dependencies{}, err
	}

	service := core.NewService(
		taskRepo,
		gitrepo.NewRepository(runner),
		tmuxrepo.NewRepository(runner),
		map[string]core.ProviderRepository{
			"codex":  codexrepo.NewRepository(runner, cfg.Codex),
			"claude": clauderepo.NewRepository(runner, cfg.Claude),
		},
		runtimeMonitor,
		map[string]core.RuntimeStateDetector{
			"codex":  codexrepo.NewRuntimeDetector(2 * time.Second),
			"claude": clauderepo.NewRuntimeDetector(2 * time.Second),
		},
		agentconfigrepo.NewRepository(),
		workspacerepo.NewRepository(),
		timeutil.RealClock{},
		cfg.Service,
	)

	return cli.Dependencies{
		Service: service,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
	}, nil
}
```

- [ ] **Step 4: Delete the lazy composition types**

Remove `runtimeService`, its forwarding methods, `newService`, and `noopTaskRepository` from `cmd/agent/main.go`.

The file should keep only:

```go
func main() {
	deps, err := buildDependencies()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if err := cli.NewRootCommand(deps).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
```

- [ ] **Step 5: Run the main package tests**

Run: `go test ./cmd/agent -v`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add cmd/agent/main.go cmd/agent/main_test.go
git commit -m "refactor: make main the composition root"
```

### Task 3: Raise Core Ports To Business-Level Capabilities

**Files:**
- Modify: `internal/core/ports.go`
- Modify: `internal/core/service.go`
- Modify: `internal/core/fakes_test.go`
- Modify: `internal/core/service_new_test.go`
- Modify: `internal/core/service_list_test.go`
- Modify: `internal/core/service_open_test.go`
- Modify: `internal/core/service_cleanup_test.go`
- Modify: `internal/core/service_doctor_test.go`
- Modify: `internal/core/service_status_test.go`

- [ ] **Step 1: Add a failing service test for higher-level runtime orchestration**

```go
func TestCreateTask_UsesSessionClientLaunchRequest(t *testing.T) {
	h := newTestService()
	h.providerRepo.launchRequest = LaunchRequest{
		Command:      []string{"codex"},
		Prompt:       "›",
		InitialInput: []string{"ship it"},
	}

	_, err := h.service.NewTask(context.Background(), NewTaskInput{
		Cwd:    "/tmp/repo",
		Prompt: "ship it",
	})
	require.NoError(t, err)
	require.Equal(t, h.providerRepo.launchRequest, h.sessionClient.startedLaunch)
}
```

- [ ] **Step 2: Run the targeted service test and verify it fails**

Run: `go test ./internal/core -run TestCreateTask_UsesSessionClientLaunchRequest -v`

Expected: FAIL because `LaunchRequest` and `sessionClient` do not exist in `core` yet.

- [ ] **Step 3: Replace low-level ports with business-level ones**

Update `internal/core/ports.go` to define supporting types and interfaces like:

```go
type LaunchRequest struct {
	Command      []string
	Prompt       string
	InitialInput []string
}

type RepoResources struct {
	WorktreeExists bool
	BranchExists   bool
}

type SessionResources struct {
	SessionExists      bool
	AgentWindowExists  bool
	EditorWindowExists bool
}

type TaskRepository interface {
	IsAvailable(ctx context.Context) error
	CreateTask(ctx context.Context, task *Task) error
	UpdateTask(ctx context.Context, task *Task) error
	GetTask(ctx context.Context, idOrSlug string) (*Task, error)
	ListTasks(ctx context.Context) ([]*Task, error)
	AppendEvent(ctx context.Context, taskID, eventType, payload string) error
}

type RepoClient interface {
	IsAvailable(ctx context.Context) error
	DetectRepo(ctx context.Context, cwd string) (RepoContext, error)
	CreateTaskWorkspace(ctx context.Context, task *Task) error
	RemoveTaskWorkspace(ctx context.Context, task *Task) error
	InspectTaskWorkspace(ctx context.Context, task *Task) (RepoResources, error)
}

type SessionClient interface {
	IsAvailable(ctx context.Context) error
	StartTaskSession(ctx context.Context, task *Task, launch LaunchRequest) error
	OpenTaskSession(ctx context.Context, task *Task) error
	DeleteTaskSession(ctx context.Context, task *Task) error
	InspectTaskSession(ctx context.Context, task *Task) (SessionResources, error)
	SnapshotTaskSession(ctx context.Context, task *Task) (RuntimeSnapshot, error)
}

type ProviderClient interface {
	IsAvailable(ctx context.Context) error
	SuggestTaskName(ctx context.Context, prompt string) (string, error)
	LaunchRequest(task *Task) (LaunchRequest, error)
	DetectRuntimeState(snapshot RuntimeSnapshot) RuntimeState
}
```

- [ ] **Step 4: Refactor `Service` to use the new ports**

Reshape the service fields:

```go
type Service struct {
	tasks      TaskRepository
	repo       RepoClient
	session    SessionClient
	providers  map[string]ProviderClient
	repoConfig RepoConfigRepository
	workspace  WorkspaceSeeder
	clock      timeutil.Clock
	cfg        Config
}
```

Update the create path so the service asks the provider for a launch request and asks the session client to start the session:

```go
launch, err := provider.LaunchRequest(task)
if err != nil {
	return s.markBroken(ctx, task, fmt.Errorf("build launch request: %w", err))
}

if err := s.session.StartTaskSession(ctx, task, launch); err != nil {
	return s.markBroken(ctx, task, fmt.Errorf("start task session: %w", err))
}
```

Update the list/get/reconcile paths so they use `InspectTaskWorkspace`, `InspectTaskSession`, and `SnapshotTaskSession` rather than `BranchExists`, `SessionExists`, `WindowExists`, and `CapturePaneContent`.

- [ ] **Step 5: Update the fakes and focused service tests**

Add the new fake types and fields:

```go
type fakeSessionClient struct {
	isAvailableErr   error
	startErr         error
	deleteErr        error
	openErr          error
	startedLaunch    LaunchRequest
	sessionResources SessionResources
	snapshot         RuntimeSnapshot
}

func (f *fakeSessionClient) StartTaskSession(_ context.Context, _ *Task, launch LaunchRequest) error {
	f.startedLaunch = launch
	return f.startErr
}
```

```go
type fakeProviderClient struct {
	suggestedName string
	launchRequest LaunchRequest
	runtimeState  RuntimeState
}

func (f *fakeProviderClient) LaunchRequest(*Task) (LaunchRequest, error) {
	return f.launchRequest, nil
}
```

Ensure the existing service tests still cover:

- provider fallback
- task creation progress
- reconciliation to `broken`, `degraded`, and `running`
- cleanup failure handling
- doctor output

- [ ] **Step 6: Run the core tests**

Run: `go test ./internal/core -v`

Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/core/ports.go internal/core/service.go internal/core/fakes_test.go internal/core/service_new_test.go internal/core/service_list_test.go internal/core/service_open_test.go internal/core/service_cleanup_test.go internal/core/service_doctor_test.go internal/core/service_status_test.go
git commit -m "refactor: raise core ports to business capabilities"
```

### Task 4: Move Storage Health And Doctor Checks To Ports

**Files:**
- Modify: `internal/core/service.go`
- Modify: `internal/core/service_doctor_test.go`
- Modify: `internal/core/fakes_test.go`
- Modify: `internal/adapters/repository/sqlite/repository.go`
- Modify: `internal/adapters/repository/sqlite/repository_test.go`

- [ ] **Step 1: Write the failing doctor test**

```go
func TestDoctor_ReportsTaskRepositoryAvailabilityFailure(t *testing.T) {
	h := newTestService()
	h.taskRepo.isAvailableErr = errors.New("sqlite unavailable")

	result, err := h.service.Doctor(context.Background(), "/tmp/repo")
	require.NoError(t, err)
	require.Contains(t, result.Failures, "storage: sqlite unavailable")
}
```

- [ ] **Step 2: Run the targeted doctor test and verify it fails**

Run: `go test ./internal/core -run TestDoctor_ReportsTaskRepositoryAvailabilityFailure -v`

Expected: FAIL because `TaskRepository` does not yet expose `IsAvailable`, and `Doctor` still calls `ensureDatabasePath`.

- [ ] **Step 3: Update `Doctor` to use port health instead of bootstrap logic**

Replace:

```go
if err := ensureDatabasePath(s.cfg.DatabasePath); err != nil {
	result.Failures = append(result.Failures, "database: "+err.Error())
}
```

With:

```go
if err := s.tasks.IsAvailable(ctx); err != nil {
	result.Failures = append(result.Failures, "storage: "+err.Error())
}
```

Delete `ensureDatabasePath` from `internal/core/service.go`.

- [ ] **Step 4: Implement the storage health check in SQLite**

```go
func (r *Repository) IsAvailable(ctx context.Context) error {
	return r.db.PingContext(ctx)
}
```

And add a simple repository test:

```go
func TestRepository_IsAvailable(t *testing.T) {
	repo, err := NewRepository(Config{Path: filepath.Join(t.TempDir(), "state.db")})
	require.NoError(t, err)
	require.NoError(t, repo.IsAvailable(context.Background()))
}
```

- [ ] **Step 5: Run the doctor and sqlite tests**

Run: `go test ./internal/core ./internal/adapters/repository/sqlite -v`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/core/service.go internal/core/service_doctor_test.go internal/core/fakes_test.go internal/adapters/repository/sqlite/repository.go internal/adapters/repository/sqlite/repository_test.go
git commit -m "refactor: move storage health behind task repository"
```

### Task 5: Rename Adapter Packages By Responsibility

**Files:**
- Move: `internal/adapters/repository/git` -> `internal/adapters/client/git`
- Move: `internal/adapters/repository/tmux` -> `internal/adapters/client/tmux`
- Move: `internal/adapters/repository/codex` -> `internal/adapters/client/codex`
- Move: `internal/adapters/repository/claude` -> `internal/adapters/client/claude`
- Move: `internal/adapters/repository/workspace` -> `internal/adapters/filesystem/workspace`
- Modify: `cmd/agent/main.go`
- Modify: all affected `_test.go` import paths

- [ ] **Step 1: Move the packages with git-aware renames**

Run:

```bash
mkdir -p internal/adapters/client internal/adapters/filesystem
git mv internal/adapters/repository/git internal/adapters/client/git
git mv internal/adapters/repository/tmux internal/adapters/client/tmux
git mv internal/adapters/repository/codex internal/adapters/client/codex
git mv internal/adapters/repository/claude internal/adapters/client/claude
git mv internal/adapters/repository/workspace internal/adapters/filesystem/workspace
```

- [ ] **Step 2: Update imports in the composition root**

```go
import (
	agentconfigrepo "agent/internal/adapters/repository/agentconfig"
	claudeclient "agent/internal/adapters/client/claude"
	codexclient "agent/internal/adapters/client/codex"
	gitclient "agent/internal/adapters/client/git"
	sqliterepo "agent/internal/adapters/repository/sqlite"
	tmuxclient "agent/internal/adapters/client/tmux"
	workspacefs "agent/internal/adapters/filesystem/workspace"
)
```

- [ ] **Step 3: Update package declarations and internal imports**

Each moved package keeps its last path element as the package name:

```go
package git
package tmux
package codex
package claude
package workspace
```

Update imports in tests and callers to point at the new directory paths.

- [ ] **Step 4: Run the adapter and main-package tests**

Run: `go test ./cmd/agent ./internal/adapters/... -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/agent/main.go cmd/agent/main_test.go internal/adapters/client internal/adapters/filesystem internal/adapters/repository/agentconfig internal/adapters/repository/sqlite
git commit -m "refactor: split adapters by responsibility"
```

### Task 6: Push Tool Mechanics Down Into Adapters

**Files:**
- Modify: `internal/adapters/client/git/repository.go`
- Modify: `internal/adapters/client/git/repository_test.go`
- Modify: `internal/adapters/client/tmux/repository.go`
- Modify: `internal/adapters/client/tmux/repository_test.go`
- Modify: `internal/adapters/client/tmux/runtime_monitor.go`
- Modify: `internal/adapters/client/tmux/runtime_monitor_test.go`
- Modify: `internal/adapters/client/codex/repository.go`
- Modify: `internal/adapters/client/codex/repository_test.go`
- Modify: `internal/adapters/client/codex/runtime_detector.go`
- Modify: `internal/adapters/client/claude/repository.go`
- Modify: `internal/adapters/client/claude/repository_test.go`
- Modify: `internal/adapters/client/claude/runtime_detector.go`
- Modify: `internal/core/service.go`

- [ ] **Step 1: Write the failing tmux launch test**

```go
func TestRepository_StartTaskSession_LaunchesCommandAndTypesInitialInput(t *testing.T) {
	runner := &fakeRunner{}
	repo := NewRepository(runner)

	err := repo.StartTaskSession(context.Background(), &core.Task{
		TmuxSession:      "repo_task",
		WorktreePath:     "/tmp/repo-task",
		AgentWindowName:  "agent",
		EditorWindowName: "editor",
	}, core.LaunchRequest{
		Command:      []string{"codex"},
		Prompt:       "›",
		InitialInput: []string{"fix billing retry flow"},
	})

	require.NoError(t, err)
	require.Equal(t, []string{"codex"}, runner.sentCommand)
	require.Equal(t, []string{"fix billing retry flow"}, runner.typedCommand)
}
```

- [ ] **Step 2: Run the targeted tmux test and verify it fails**

Run: `go test ./internal/adapters/client/tmux -run TestRepository_StartTaskSession_LaunchesCommandAndTypesInitialInput -v`

Expected: FAIL because `StartTaskSession` does not exist yet.

- [ ] **Step 3: Move repo and session inspection logic out of `core`**

Implement repo inspection in git:

```go
func (r *Repository) CreateTaskWorkspace(ctx context.Context, task *core.Task) error {
	return r.CreateWorktree(ctx, core.CreateWorktreeInput{
		RepoRoot:     task.RepoRoot,
		BaseBranch:   task.BaseBranch,
		BranchName:   task.BranchName,
		WorktreePath: task.WorktreePath,
	})
}

func (r *Repository) InspectTaskWorkspace(ctx context.Context, task *core.Task) (core.RepoResources, error) {
	worktreeExists, err := worktreePresence(task.WorktreePath)
	if err != nil {
		return core.RepoResources{}, err
	}
	branchExists, err := r.BranchExists(ctx, task.RepoRoot, task.BranchName)
	if err != nil {
		return core.RepoResources{}, err
	}
	return core.RepoResources{
		WorktreeExists: worktreeExists,
		BranchExists:   branchExists,
	}, nil
}
```

- [ ] **Step 4: Move launch choreography into tmux and merge runtime detection into providers**

In tmux:

```go
func (r *Repository) StartTaskSession(ctx context.Context, task *core.Task, launch core.LaunchRequest) error {
	if err := r.CreateSession(ctx, core.CreateSessionInput{
		SessionName:      task.TmuxSession,
		WorkingDir:       task.WorktreePath,
		AgentWindowName:  task.AgentWindowName,
		EditorWindowName: task.EditorWindowName,
	}); err != nil {
		return err
	}

	if err := r.SendKeysToWindow(ctx, task.TmuxSession, task.AgentWindowName, launch.Command); err != nil {
		return err
	}

	if len(launch.InitialInput) > 0 {
		if err := r.waitForPrompt(ctx, task.TmuxSession, task.AgentWindowName, launch.Prompt); err != nil {
			return err
		}
		if err := r.TypeInWindow(ctx, task.TmuxSession, task.AgentWindowName, launch.InitialInput); err != nil {
			return err
		}
	}

	return nil
}
```

In providers:

```go
func (r *Repository) LaunchRequest(task *core.Task) (core.LaunchRequest, error) {
	return core.LaunchRequest{
		Command:      []string{r.binary},
		Prompt:       "›",
		InitialInput: []string{task.Prompt},
	}, nil
}

func (r *Repository) DetectRuntimeState(snapshot core.RuntimeSnapshot) core.RuntimeState {
	return detectRuntimeState(snapshot)
}
```

And in `core/service.go`, delete `waitForPrompt`, `worktreePresence`, and the separate `runtimeDetectors` map usage.

- [ ] **Step 5: Run focused adapter and core tests**

Run: `go test ./internal/adapters/client/git ./internal/adapters/client/tmux ./internal/adapters/client/codex ./internal/adapters/client/claude ./internal/core -v`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/adapters/client/git/repository.go internal/adapters/client/git/repository_test.go internal/adapters/client/tmux/repository.go internal/adapters/client/tmux/repository_test.go internal/adapters/client/tmux/runtime_monitor.go internal/adapters/client/tmux/runtime_monitor_test.go internal/adapters/client/codex/repository.go internal/adapters/client/codex/repository_test.go internal/adapters/client/codex/runtime_detector.go internal/adapters/client/claude/repository.go internal/adapters/client/claude/repository_test.go internal/adapters/client/claude/runtime_detector.go internal/core/service.go
git commit -m "refactor: push tool mechanics into adapters"
```

### Task 7: Collapse Core Into Four Production Files

**Files:**
- Create: `internal/core/domain.go`
- Modify: `internal/core/ports.go`
- Modify: `internal/core/service.go`
- Modify: `internal/core/errors.go`
- Modify: `internal/core/fakes_test.go`
- Delete: `internal/core/config.go`
- Delete: `internal/core/progress.go`
- Delete: `internal/core/runtime.go`
- Delete: `internal/core/status.go`
- Delete: `internal/core/task.go`
- Modify: any imports referring to moved definitions

- [ ] **Step 1: Write a compile-focused regression test around core public types**

```go
func TestCorePublicTypesRemainUsable(t *testing.T) {
	task := Task{
		DisplayName: "billing retry flow",
		Status:      TaskStatusRunning,
		Provider:    "codex",
	}

	require.Equal(t, "billing retry flow", task.DisplayName)
	require.False(t, task.Status.IsTerminal())
}
```

- [ ] **Step 2: Run the targeted core test and verify the current baseline passes**

Run: `go test ./internal/core -run TestCorePublicTypesRemainUsable -v`

Expected: PASS

- [ ] **Step 3: Create `domain.go` and move the domain types into it**

Create:

```go
package core

import "time"

type Config struct {
	Provider string
}

type TaskStatus string

const (
	TaskStatusCreating TaskStatus = "creating"
	TaskStatusReady    TaskStatus = "ready"
	TaskStatusRunning  TaskStatus = "running"
	TaskStatusDegraded TaskStatus = "degraded"
	TaskStatusBroken   TaskStatus = "broken"
	TaskStatusCleaned  TaskStatus = "cleaned"
)

func (s TaskStatus) IsTerminal() bool {
	return s == TaskStatusBroken || s == TaskStatusCleaned
}

type RuntimeState string

const (
	RuntimeStateNone       RuntimeState = ""
	RuntimeStateRunning    RuntimeState = "running"
	RuntimeStateNeedsInput RuntimeState = "needs_input"
	RuntimeStateFinished   RuntimeState = "finished"
)

type Task struct {
	CreatedAt             time.Time
	UpdatedAt             time.Time
	LastReconciledAt      time.Time
	RuntimeStateUpdatedAt time.Time
	ID                    string
	Prompt                string
	DisplayName           string
	Slug                  string
	RepoRoot              string
	RepoName              string
	BaseBranch            string
	BranchName            string
	WorktreePath          string
	TmuxSession           string
	AgentWindowName       string
	EditorWindowName      string
	Provider              string
	Status                TaskStatus
	RuntimeState          RuntimeState
	LastError             string
	WorktreeExists        bool
	BranchExists          bool
	SessionExists         bool
	AgentWindowExists     bool
	EditorWindowExists    bool
}

type TaskProgressStep string

const (
	TaskProgressNaming           TaskProgressStep = "naming"
	TaskProgressNameSelected     TaskProgressStep = "name_selected"
	TaskProgressWorktreeCreating TaskProgressStep = "worktree_creating"
	TaskProgressWorkspaceSeeding TaskProgressStep = "workspace_seeding"
	TaskProgressWorkspaceSeeded  TaskProgressStep = "workspace_seeded"
	TaskProgressTmuxStarting     TaskProgressStep = "tmux_starting"
	TaskProgressAgentLaunching   TaskProgressStep = "agent_launching"
	TaskProgressTaskCreated      TaskProgressStep = "task_created"
	TaskProgressSessionOpening   TaskProgressStep = "session_opening"
)

type TaskProgress struct {
	Task    *Task
	Step    TaskProgressStep
	Message string
}

type RuntimeSnapshot struct {
	SessionName       string
	WindowName        string
	PaneID            string
	HadAgentBinding   bool
	ForegroundCommand string
	Content           string
	ObservedAt        time.Time
	LastOutputAt      time.Time
}
```

- [ ] **Step 4: Delete the superseded files and fix imports**

Remove:

```text
internal/core/config.go
internal/core/progress.go
internal/core/runtime.go
internal/core/status.go
internal/core/task.go
```

Then run `go test ./internal/core` and fix any missing imports or duplicate definitions.

- [ ] **Step 5: Run the focused core, CLI, and adapter tests**

Run: `go test ./internal/core ./internal/adapters/... ./cmd/agent -v`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/core/domain.go internal/core/ports.go internal/core/service.go internal/core/errors.go internal/core/fakes_test.go internal/core/service_new_test.go internal/core/service_list_test.go internal/core/service_open_test.go internal/core/service_cleanup_test.go internal/core/service_doctor_test.go internal/core/service_status_test.go cmd/agent/main.go cmd/agent/main_test.go internal/adapters
git rm internal/core/config.go internal/core/progress.go internal/core/runtime.go internal/core/status.go internal/core/task.go
git commit -m "refactor: collapse core into four files"
```

### Task 8: Full Verification

**Files:**
- Modify: none unless a verification failure uncovers a missed import or stale test

- [ ] **Step 1: Run the full test suite**

Run: `go test ./...`

Expected: PASS

- [ ] **Step 2: Run formatting if any file drifted**

Run: `gofmt -w cmd/agent/main.go cmd/agent/main_test.go $(find internal -name '*.go')`

Expected: no diff after re-running tests

- [ ] **Step 3: Re-run the full test suite after formatting**

Run: `go test ./...`

Expected: PASS

- [ ] **Step 4: Review the final tree against the approved spec**

Check manually that these paths exist and match the design:

```text
internal/core/domain.go
internal/core/service.go
internal/core/ports.go
internal/core/errors.go
internal/infrastructure/config.go
internal/adapters/repository/sqlite
internal/adapters/repository/agentconfig
internal/adapters/client/git
internal/adapters/client/tmux
internal/adapters/client/codex
internal/adapters/client/claude
internal/adapters/filesystem/workspace
```

- [ ] **Step 5: Commit**

```bash
git add cmd/agent/main.go cmd/agent/main_test.go internal docs/superpowers/plans/2026-04-06-core-boundary-restructure.md
git commit -m "refactor: finish core boundary restructure"
```

## Self-Review

### Spec coverage

- Core reduced to `domain.go`, `service.go`, `ports.go`, `errors.go`: covered by Task 7.
- Core contains business logic only: covered by Tasks 3, 4, and 6.
- Composition moved to `cmd/agent/main.go`: covered by Task 2.
- Env loading moved to `internal/infrastructure/config.go` with `caarlos0/env/v11`: covered by Task 1.
- Adapter taxonomy split into `repository`, `client`, `filesystem`: covered by Task 5.
- Storage and tool mechanics moved behind ports/adapters: covered by Tasks 4 and 6.

### Placeholder scan

- No `TODO`, `TBD`, or “implement later” markers remain in the plan.
- All tasks contain exact file paths.
- All code-changing tasks include concrete code snippets.
- All verification steps include exact commands and expected outcomes.

### Type consistency

- `core.Config` is slimmed to `Provider string` in Task 1 and moved into `domain.go` in Task 7.
- `LaunchRequest`, `RepoResources`, and `SessionResources` are introduced in Task 3 and reused consistently later.
- `TaskRepository.IsAvailable`, `RepoClient`, `SessionClient`, and `ProviderClient` are introduced in Task 3 and implemented in Tasks 4 and 6.
- Adapter directory names in Task 5 match the final tree checked in Task 8.
