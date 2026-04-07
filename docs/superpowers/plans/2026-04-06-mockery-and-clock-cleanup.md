# Mockery And Clock Cleanup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace handwritten interface-based test doubles across the repo with `mockery`-generated mocks, remove the custom clock abstraction from `core`, and standardize the workflow around `make dependencies install`, `make generate`, and `go test ./...`.

**Architecture:** Add explicit `mockery` configuration at the repo root, wire generation through `go tool mockery`, and keep generated files package-local but ignored from git. In production, remove `internal/pkg/timeutil` entirely and use `time.Now().UTC()` directly, while rewriting tests to use generated mocks plus time-window assertions instead of frozen timestamps.

**Tech Stack:** Go 1.26, `github.com/vektra/mockery/v2`, testify, Bubble Tea, Cobra, tmux, SQLite

---

## File Structure

### Planned file responsibilities

- `.mockery.yaml`
  Defines the exact packages and interfaces that `go tool mockery` should generate.
- `.gitignore`
  Ignores generated `mock_*.go` files so mocks stay out of version control.
- `Makefile`
  Adds the user-facing `make dependencies install` surface and a `make generate` target that runs `go tool mockery`.
- `go.mod`
  Adds `github.com/vektra/mockery/v2` as a Go tool dependency so `go tool mockery` works after dependency installation.
- `scripts/dependencies/install.sh`
  Keeps dependency installation flowing through the existing script. Only adjust messaging if needed to reflect tool dependencies.
- `internal/pkg/execx/runner.go`
  Deletes `FakeRunner` and keeps only the production runner plus command error types.
- `internal/core/service.go`
  Removes the clock dependency and uses `time.Now().UTC()` directly.
- `internal/core/test_helpers_test.go`
  Replaces `fakes_test.go` with a test harness built from generated mocks and default expectations.
- `internal/adapters/handler/cli/*_test.go`
  Uses generated `TaskService` mocks instead of handwritten fake services.
- `internal/adapters/client/*/*_test.go`
  Uses generated `Runner`, `RuntimeMonitor`, `controlPipe`, and `controlPipeFactory` mocks where those interfaces already exist.

### Expected deletes

- `internal/pkg/timeutil/clock.go`
- `internal/core/fakes_test.go`

### Generated files expected after `make generate`

- `internal/core/mock_task_repository.go`
- `internal/core/mock_repo_config_repository.go`
- `internal/core/mock_workspace_seeder.go`
- `internal/core/mock_repo_client.go`
- `internal/core/mock_session_client.go`
- `internal/core/mock_runtime_monitor.go`
- `internal/core/mock_provider_client.go`
- `internal/adapters/handler/cli/mock_task_service.go`
- `internal/pkg/execx/mock_runner.go`
- `internal/adapters/client/tmux/mock_control_pipe.go`
- `internal/adapters/client/tmux/mock_control_pipe_factory.go`

---

### Task 1: Add Mockery Tooling And Generation Workflow

**Files:**
- Create: `.mockery.yaml`
- Modify: `.gitignore`
- Modify: `Makefile`
- Modify: `go.mod`
- Modify: `scripts/dependencies/install.sh`

- [ ] **Step 1: Add the failing generation workflow test by checking the current surface**

Run: `make generate`

Expected: FAIL with `No rule to make target 'generate'` because the repo does not yet expose the generation workflow.

- [ ] **Step 2: Add the mockery tool dependency and root config**

In `go.mod`, add a tool block in the same style as `fws-facade`:

```go
tool (
	github.com/vektra/mockery/v2
)
```

Create `.mockery.yaml` with explicit packages and interfaces:

```yaml
log-level: warn

filename: "mock_{{ snakecase .InterfaceName }}.go"
mockname: "Mock{{.InterfaceName}}"
inpackage: true
resolve-type-alias: false
disable-version-string: true
issue-845-fix: true
with-expecter: true
all: false
recursive: false
dir: "{{ .InterfaceDirRelative }}"

packages:
  agent/internal/core:
    interfaces:
      TaskRepository:
      RepoConfigRepository:
      WorkspaceSeeder:
      RepoClient:
      SessionClient:
      RuntimeMonitor:
      ProviderClient:
  agent/internal/adapters/handler/cli:
    interfaces:
      TaskService:
  agent/internal/pkg/execx:
    interfaces:
      Runner:
  agent/internal/adapters/client/tmux:
    interfaces:
      controlPipe:
      controlPipeFactory:
```

- [ ] **Step 3: Expose the new Makefile surface and ignore generated mocks**

Update `Makefile` so the user-facing workflow becomes `make dependencies install` and `make generate`:

```make
.PHONY: dependencies
dependencies:

.PHONY: install
install:
	@./scripts/dependencies/install.sh

.PHONY: dependencies-install
dependencies-install: install

.PHONY: generate
generate:
	@go tool mockery
```

Extend `.gitignore` with explicit generated mock patterns:

```gitignore
internal/core/mock_*.go
internal/adapters/handler/cli/mock_*.go
internal/pkg/execx/mock_*.go
internal/adapters/client/tmux/mock_*.go
```

If `scripts/dependencies/install.sh` still says only module dependencies, update the message to acknowledge tool dependencies while keeping `go mod download` as the mechanism:

```bash
echo "Downloading Go module and tool dependencies..."
go mod download
```

- [ ] **Step 4: Run the dependency and generation workflow**

Run: `make dependencies install && make generate`

Expected: PASS. `go tool mockery` should generate the expected `mock_*.go` files in the configured packages.

- [ ] **Step 5: Commit Task 1**

```bash
git add .mockery.yaml .gitignore Makefile go.mod go.sum scripts/dependencies/install.sh
git commit -m "build: add mockery generation workflow"
```

### Task 2: Remove `FakeRunner` And Convert Runner-Based Adapter Tests

**Files:**
- Modify: `internal/pkg/execx/runner.go`
- Modify: `internal/pkg/execx/runner_test.go`
- Modify: `internal/adapters/client/git/repository_test.go`
- Modify: `internal/adapters/client/codex/repository_test.go`
- Modify: `internal/adapters/client/claude/repository_test.go`

- [ ] **Step 1: Rewrite one runner-based adapter test to use the generated mock**

Update `internal/adapters/client/git/repository_test.go` to use `MockRunner` instead of `NewFakeRunner(...)`:

```go
func TestRepositoryDetectRepo_ReturnsRepoContext(t *testing.T) {
	runner := &execx.MockRunner{}
	runner.EXPECT().
		Run(mock.Anything, "/tmp/repo", "git", "rev-parse", "--show-toplevel").
		Return(execx.Result{Stdout: "/tmp/repo\n"}, nil).
		Once()
	runner.EXPECT().
		Run(mock.Anything, "/tmp/repo", "git", "rev-parse", "--abbrev-ref", "HEAD").
		Return(execx.Result{Stdout: "main\n"}, nil).
		Once()

	repo := NewRepository(runner)

	got, err := repo.DetectRepo(context.Background(), "/tmp/repo")
	require.NoError(t, err)
	require.Equal(t, "/tmp/repo", got.Root)
	require.Equal(t, "repo", got.Name)
	require.Equal(t, "main", got.BaseBranch)
}
```

Use the same pattern in the Codex and Claude repository tests, including `RunWithStdin(...)` expectations where launch commands use stdin.

- [ ] **Step 2: Run the focused adapter tests and verify they fail before removing `FakeRunner`**

Run: `go test ./internal/adapters/client/git ./internal/adapters/client/codex ./internal/adapters/client/claude -v`

Expected: FAIL while the old tests and the new mock usage are mixed or the generated mock imports are not yet wired into all files.

- [ ] **Step 3: Delete `FakeRunner` from production code and convert its direct unit test**

Remove this block from `internal/pkg/execx/runner.go`:

```go
type FakeRunner struct {
	Results []Result
	Errors  []error
	Calls   []Call
}

func NewFakeRunner(results []Result) *FakeRunner {
	return &FakeRunner{Results: results}
}
```

Delete the remaining `FakeRunner` methods as well, and replace `internal/pkg/execx/runner_test.go` with command-error coverage only:

```go
func TestCommandError_ErrorIncludesCommandAndStderr(t *testing.T) {
	err := CommandError{
		Cwd:    "/tmp/repo",
		Name:   "git",
		Args:   []string{"worktree", "add"},
		Stdout: "",
		Stderr: "fatal: branch already exists",
		Err:    errors.New("exit status 1"),
	}

	require.Contains(t, err.Error(), "git worktree add")
	require.Contains(t, err.Error(), "fatal: branch already exists")
}
```

- [ ] **Step 4: Re-run the runner and adapter packages**

Run: `go test ./internal/pkg/execx ./internal/adapters/client/git ./internal/adapters/client/codex ./internal/adapters/client/claude -v`

Expected: PASS, with no `FakeRunner` left in production code or tests.

- [ ] **Step 5: Commit Task 2**

```bash
git add internal/pkg/execx/runner.go internal/pkg/execx/runner_test.go internal/adapters/client/git/repository_test.go internal/adapters/client/codex/repository_test.go internal/adapters/client/claude/repository_test.go
git commit -m "test: replace runner fakes with mockery mocks"
```

### Task 3: Convert Tmux Tests To Generated Interface Mocks

**Files:**
- Modify: `internal/adapters/client/tmux/repository_test.go`
- Modify: `internal/adapters/client/tmux/runtime_monitor_test.go`

- [ ] **Step 1: Rewrite the runtime-monitor tests around generated in-package mocks**

In `internal/adapters/client/tmux/runtime_monitor_test.go`, replace `fakeControlPipe` and `fakeControlPipeFactory` with generated mocks:

```go
func TestRuntimeMonitorSnapshot_BindsOnlyCodexPaneInSplitAgentWindow(t *testing.T) {
	pipe := &MockcontrolPipe{}
	pipe.EXPECT().
		SendCommand(paneListCommand("repo-billing-retry-flow", "agent")).
		Return("%24\tcodex\t1\n%31\tzsh\t0", nil).
		Once()
	pipe.EXPECT().
		SendCommand("capture-pane -t %24 -p -e").
		Return("› review my changes\nWorking (26s • esc to interrupt)\n", nil).
		Once()
	pipe.EXPECT().
		LastOutputAt().
		Return(time.Date(2026, 4, 5, 9, 59, 55, 0, time.UTC)).
		Once()

	factory := &MockcontrolPipeFactory{}
	factory.EXPECT().
		Attach("repo-billing-retry-flow").
		Return(pipe, nil).
		Once()

	monitor := NewRuntimeMonitorWithFactory(factory, func() time.Time {
		return time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC)
	})
```

Apply the same conversion to the dead-pipe eviction, close, and repeated-observation tests.

- [ ] **Step 2: Rewrite the repository test that used handwritten tmux fakes**

In `internal/adapters/client/tmux/repository_test.go`, replace `fakeRuntimeMonitor` and the custom `fakeRunner` with generated mocks:

```go
func TestRepositorySnapshotTaskSession_UsesRuntimeMonitor(t *testing.T) {
	repo := NewRepository(&execx.MockRunner{})
	monitor := &core.MockRuntimeMonitor{}
	monitor.EXPECT().
		Snapshot(mock.Anything, mock.MatchedBy(func(task *core.Task) bool {
			return task.TmuxSession == "repo-billing-retry-flow"
		})).
		Return(core.RuntimeSnapshot{
			SessionName: "repo-billing-retry-flow",
			PaneID:      "%24",
		}, nil).
		Once()

	repo.runtimeMonitor = monitor

	snapshot, err := repo.SnapshotTaskSession(context.Background(), &core.Task{TmuxSession: "repo-billing-retry-flow"})
	require.NoError(t, err)
	require.Equal(t, "repo-billing-retry-flow", snapshot.SessionName)
	require.Equal(t, "%24", snapshot.PaneID)
}
```

Convert the remaining repository tests to `execx.MockRunner` expectations and delete all local fake struct definitions at the bottom of the file.

- [ ] **Step 3: Run the focused tmux tests and verify they pass**

Run: `go test ./internal/adapters/client/tmux -run 'TestRepository|TestRuntimeMonitor|TestPaneListCommand' -v`

Expected: PASS, with no handwritten tmux fake implementations left in the package tests.

- [ ] **Step 4: Commit Task 3**

```bash
git add internal/adapters/client/tmux/repository_test.go internal/adapters/client/tmux/runtime_monitor_test.go
git commit -m "test: convert tmux tests to generated mocks"
```

### Task 4: Convert CLI Tests To `TaskService` Mocks

**Files:**
- Modify: `internal/adapters/handler/cli/doctor_test.go`
- Modify: `internal/adapters/handler/cli/root_test.go`
- Modify: `internal/adapters/handler/cli/tui_model_test.go`
- Modify: `internal/adapters/handler/cli/tui_test.go`

- [ ] **Step 1: Replace handwritten CLI service fakes with `MockTaskService`**

Update `internal/adapters/handler/cli/doctor_test.go` to configure behavior through expectations:

```go
func TestDoctorCommand_PrintsNotesBeforeOk(t *testing.T) {
	service := &MockTaskService{}
	service.EXPECT().
		Doctor(context.Background(), "").
		Return(core.DoctorResult{
			Notes: []string{"config: loaded agent.yaml"},
		}, nil).
		Once()

	out := &bytes.Buffer{}
	cmd := newDoctorCommand(Dependencies{Service: service})
	cmd.SetOut(out)
	cmd.SetErr(out)

	err := cmd.Execute()
	require.NoError(t, err)
	require.Contains(t, out.String(), "config: loaded agent.yaml")
	require.Contains(t, out.String(), "doctor: ok")
}
```

Use the same `MockTaskService` type in `root_test.go` and `tui_model_test.go` for:

- `ListTasks(...)`
- `SuggestTaskName(...)`
- `CreateTaskWithProgress(...)`
- `OpenTask(...)`
- `DeleteTaskResources(...)`

- [ ] **Step 2: Remove the local fake service implementations**

Delete:

```go
type fakeCLIService struct { ... }
type fakeTUIService struct { ... }
```

and any helper methods tied to those structs. Keep helper functions that build task fixtures or Bubble Tea models, but have them accept `*MockTaskService` instead of a handwritten fake type.

- [ ] **Step 3: Run the CLI test package**

Run: `go test ./internal/adapters/handler/cli -v`

Expected: PASS, with all surviving CLI tests driven by generated `TaskService` mocks.

- [ ] **Step 4: Commit Task 4**

```bash
git add internal/adapters/handler/cli/doctor_test.go internal/adapters/handler/cli/root_test.go internal/adapters/handler/cli/tui_model_test.go internal/adapters/handler/cli/tui_test.go
git commit -m "test: replace cli service fakes with mockery mocks"
```

### Task 5: Remove `timeutil.Clock` And Replace Core Handwritten Fakes

**Files:**
- Modify: `internal/core/service.go`
- Modify: `internal/core/service_new_test.go`
- Modify: `internal/core/service_list_test.go`
- Modify: `internal/core/service_open_test.go`
- Modify: `internal/core/service_cleanup_test.go`
- Modify: `internal/core/service_doctor_test.go`
- Modify: `internal/core/service_status_test.go`
- Modify: `internal/core/task_test.go`
- Modify: `internal/core/test_helpers_test.go`
- Modify: `cmd/agent/main.go`
- Modify: `cmd/agent/main_test.go`
- Delete: `internal/core/fakes_test.go`
- Delete: `internal/pkg/timeutil/clock.go`

- [ ] **Step 1: Add a generated-mock-based core test harness**

Create `internal/core/test_helpers_test.go` with a harness that wraps generated mocks and shared defaults:

```go
type testServiceHarness struct {
	service    *Service
	taskRepo   *MockTaskRepository
	repoClient *MockRepoClient
	session    *MockSessionClient
	provider   *MockProviderClient
	configRepo *MockRepoConfigRepository
	workspace  *MockWorkspaceSeeder
}

func newTestService(t *testing.T) *testServiceHarness {
	t.Helper()

	taskRepo := &MockTaskRepository{}
	repoClient := &MockRepoClient{}
	session := &MockSessionClient{}
	provider := &MockProviderClient{}
	configRepo := &MockRepoConfigRepository{}
	workspace := &MockWorkspaceSeeder{}

	repoClient.EXPECT().
		DetectRepo(mock.Anything, mock.Anything).
		Return(RepoContext{
			Root:       "/tmp/repo",
			Name:       "repo",
			BaseBranch: "main",
		}, nil).
		Maybe()

	configRepo.EXPECT().
		LoadRepoConfig(mock.Anything, "/tmp/repo").
		Return(RepoConfig{}, nil).
		Maybe()

	return &testServiceHarness{
		service: NewService(
			taskRepo,
			repoClient,
			session,
			map[string]ProviderClient{"codex": provider},
			configRepo,
			workspace,
			Config{Provider: "codex"},
		),
		taskRepo:   taskRepo,
		repoClient: repoClient,
		session:    session,
		provider:   provider,
		configRepo: configRepo,
		workspace:  workspace,
	}
}
```

- [ ] **Step 2: Remove the production clock abstraction**

Delete `internal/pkg/timeutil/clock.go` and update `internal/core/service.go`:

```go
type Service struct {
	tasks      TaskRepository
	repo       RepoClient
	session    SessionClient
	providers  map[string]ProviderClient
	repoConfig RepoConfigRepository
	workspace  WorkspaceSeeder
	cfg        Config
}
```

Replace clock calls with direct time usage:

```go
now := time.Now().UTC()
task.UpdatedAt = time.Now().UTC()
reconciled.LastReconciledAt = time.Now().UTC()
```

Update the constructor signature:

```go
func NewService(
	tasks TaskRepository,
	repo RepoClient,
	session SessionClient,
	providers map[string]ProviderClient,
	repoConfig RepoConfigRepository,
	workspace WorkspaceSeeder,
	cfg Config,
) *Service
```

Update `cmd/agent/main.go` to stop passing `timeutil.RealClock{}`.

- [ ] **Step 3: Rewrite the core tests to use real-time assertions**

For tests that previously depended on `fakeClock`, capture a time window:

```go
before := time.Now().UTC()

task, err := svc.service.CreateTaskWithProgress(
	t.Context(),
	NewTaskInput{
		Cwd:                  "/tmp/repo",
		Prompt:               "add billing retry flow",
		ConfirmedDisplayName: "billing retry flow",
	},
	CreateTaskOptions{},
	nil,
)

after := time.Now().UTC()

require.NoError(t, err)
require.False(t, task.CreatedAt.Before(before))
require.False(t, task.CreatedAt.After(after))
require.False(t, task.UpdatedAt.Before(before))
require.False(t, task.UpdatedAt.After(after))
```

Use the same pattern for cleanup, reconcile, and runtime-state updates. When exact equality is not important, assert simpler invariants such as `UpdatedAt` changing or `UpdatedAt` being after `CreatedAt`.

- [ ] **Step 4: Remove `internal/core/fakes_test.go` and re-run the core-focused tests**

Run: `go test ./internal/core ./cmd/agent -v`

Expected: PASS, with all core tests driven by generated mocks and no remaining `timeutil` dependency.

- [ ] **Step 5: Commit Task 5**

```bash
git add internal/core/service.go internal/core/service_new_test.go internal/core/service_list_test.go internal/core/service_open_test.go internal/core/service_cleanup_test.go internal/core/service_doctor_test.go internal/core/service_status_test.go internal/core/task_test.go internal/core/test_helpers_test.go cmd/agent/main.go cmd/agent/main_test.go
git rm internal/core/fakes_test.go internal/pkg/timeutil/clock.go
git commit -m "refactor: remove clock abstraction and handwritten core fakes"
```

### Task 6: Final Verification And Cleanup

**Files:**
- Modify: any Go files touched by `gofmt`

- [ ] **Step 1: Regenerate mocks after the full test conversion**

Run: `make generate`

Expected: PASS, with `mock_*.go` files refreshed to match the final interface set.

- [ ] **Step 2: Format the touched Go files**

Run: `gofmt -w cmd internal`

Expected: PASS, with import cleanup and formatting applied across touched files.

- [ ] **Step 3: Run the full test suite**

Run: `go test ./...`

Expected: PASS for the full repository after mock generation.

- [ ] **Step 4: Inspect the worktree**

Run: `git status --short`

Expected: only the intended tracked code changes remain. Generated `mock_*.go` files should be untracked or ignored, not staged for commit.

- [ ] **Step 5: Commit the final cleanup pass**

```bash
git add Makefile scripts/dependencies/install.sh go.mod go.sum .gitignore .mockery.yaml cmd internal docs/superpowers/plans/2026-04-06-mockery-and-clock-cleanup.md
git commit -m "test: standardize mockery and remove test clock helpers"
```

- [ ] **Step 6: Record completion evidence**

Capture the exact verification commands and outcomes in the handoff:

```text
Verified with:
- make dependencies install
- make generate
- go test ./internal/pkg/execx ./internal/adapters/client/git ./internal/adapters/client/codex ./internal/adapters/client/claude -v
- go test ./internal/adapters/client/tmux -run 'TestRepository|TestRuntimeMonitor|TestPaneListCommand' -v
- go test ./internal/adapters/handler/cli -v
- go test ./internal/core ./cmd/agent -v
- go test ./...
- git status --short
```
