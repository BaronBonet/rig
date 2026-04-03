# Agent Workspace Seeding Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add repo-local workspace seeding to `agent new` so configured ignored files and directories are copied from the main repo root into the new worktree before tmux and Codex start.

**Architecture:** Keep seeding as a first-class orchestration step in `internal/core` rather than burying it inside the git adapter. Add a repo-local `agent.yaml` loader, a dedicated workspace seeder port plus filesystem adapter, then thread both through `createTask` and `doctor` so creation and validation share the same rules.

**Tech Stack:** Go, Cobra, SQLite, tmux, git, `os`, `path/filepath`, `io`, `gopkg.in/yaml.v3`, testify

---

## File Structure

Modify or create the following files during implementation.

### Core

- Modify: `internal/core/ports.go`
- Modify: `internal/core/progress.go`
- Modify: `internal/core/service.go`
- Modify: `internal/core/fakes_test.go`
- Modify: `internal/core/service_new_test.go`
- Modify: `internal/core/service_doctor_test.go`

### Repository Adapters

- Create: `internal/adapters/repository/agentconfig/repository.go`
- Create: `internal/adapters/repository/agentconfig/repository_test.go`
- Create: `internal/adapters/repository/workspace/repository.go`
- Create: `internal/adapters/repository/workspace/repository_test.go`

### CLI And Runtime Wiring

- Modify: `cmd/agent/main.go`
- Modify: `cmd/agent/main_test.go`
- Modify: `internal/adapters/handler/cli/doctor.go`
- Modify: `internal/adapters/handler/cli/doctor_test.go`
- Modify: `internal/adapters/handler/cli/new.go`
- Modify: `internal/adapters/handler/cli/new_test.go`

### Docs And Module Metadata

- Modify: `README.md`
- Modify: `go.mod`
- Modify: `go.sum`

## Implementation Notes

- `agent.yaml` is repo-local and optional.
- Initial config shape is:

```yaml
seed:
  copy:
    - .env
    - .lazy.lua
    - local/
```

- `seed.copy` entries are literal repo-relative paths only. No globs.
- Seeding runs after `git worktree add` and before tmux session creation.
- Missing source paths fail the command.
- Existing destination paths fail the command.
- Symlinks should fail explicitly in v1.
- `doctor` should validate repo-local `agent.yaml` when running inside a repo.
- `--json` behavior for `agent new` must remain machine-readable; progress stays on stderr.

### Interfaces To Add

Add a repo-local config port and a workspace seeding port in `internal/core/ports.go`:

```go
type RepoConfig struct {
    Seed SeedConfig
}

type SeedConfig struct {
    Copy []string
}

type RepoConfigRepository interface {
    LoadRepoConfig(ctx context.Context, repoRoot string) (RepoConfig, error)
}

type SeedWorkspaceInput struct {
    RepoRoot      string
    WorktreePath  string
    RelativePaths []string
}

type WorkspaceSeeder interface {
    SeedWorkspace(ctx context.Context, in SeedWorkspaceInput, progress func(string)) error
    ValidateSeedPaths(ctx context.Context, repoRoot string, relativePaths []string) error
}
```

### Progress Events To Add

Extend `internal/core/progress.go` with:

```go
const (
    TaskProgressWorkspaceSeeding TaskProgressStep = "workspace_seeding"
    TaskProgressWorkspaceSeeded  TaskProgressStep = "workspace_seeded"
)
```

Use `TaskProgressWorkspaceSeeding` for the stage start and `TaskProgressWorkspaceSeeded` for each copied path so the CLI can print:

- `Seeding workspace...`
- `Copied .env`
- `Copied local/`

### Doctor Result Extension

The current `DoctorResult` only reports failures. Extend it so doctor can emit repo-local config details without treating them as failures:

```go
type DoctorResult struct {
    Notes    []string
    Failures []string
}
```

Recommended note lines:

- `config: agent.yaml not found`
- `config: loaded agent.yaml`
- `config: seed path ok: .env`
- `config: seed path ok: local/`

The CLI should print notes first, then either `doctor: ok` when there are no failures or the failure lines when there are.

## Task 1: Add Repo-Local Config Loading

**Files:**
- Modify: `internal/core/ports.go`
- Create: `internal/adapters/repository/agentconfig/repository.go`
- Create: `internal/adapters/repository/agentconfig/repository_test.go`
- Modify: `go.mod`
- Modify: `go.sum`

- [ ] **Step 1: Write the failing config loader tests**

Create focused tests for:

- missing `agent.yaml` returning an empty config and no error
- valid YAML returning `seed.copy` entries
- invalid YAML returning an error
- invalid `seed.copy` entry types returning an error
- normalization trimming trailing slashes only for storage or validation, not user-facing messages

Use a table-driven structure like:

```go
func TestRepositoryLoadRepoConfig(t *testing.T) {
    repoRoot := t.TempDir()
    path := filepath.Join(repoRoot, "agent.yaml")
    require.NoError(t, os.WriteFile(path, []byte("seed:\n  copy:\n    - .env\n"), 0o644))

    repo, err := NewRepository()
    require.NoError(t, err)

    cfg, err := repo.LoadRepoConfig(t.Context(), repoRoot)
    require.NoError(t, err)
    require.Equal(t, []string{".env"}, cfg.Seed.Copy)
}
```

- [ ] **Step 2: Run the config loader tests to verify red**

Run: `go test ./internal/adapters/repository/agentconfig -v`
Expected: FAIL because the package and loader do not exist yet

- [ ] **Step 3: Add the YAML dependency and the repo config port**

Update `go.mod` with:

```go
require gopkg.in/yaml.v3 v3.0.1
```

Add `RepoConfig`, `SeedConfig`, and `RepoConfigRepository` in `internal/core/ports.go`.

- [ ] **Step 4: Implement the repo-local config repository**

Implement `LoadRepoConfig` in `internal/adapters/repository/agentconfig/repository.go`:

```go
func (r *Repository) LoadRepoConfig(_ context.Context, repoRoot string) (core.RepoConfig, error) {
    path := filepath.Join(repoRoot, "agent.yaml")
    raw, err := os.ReadFile(path)
    if err != nil {
        if errors.Is(err, os.ErrNotExist) {
            return core.RepoConfig{}, nil
        }
        return core.RepoConfig{}, err
    }

    var cfg fileConfig
    if err := yaml.Unmarshal(raw, &cfg); err != nil {
        return core.RepoConfig{}, fmt.Errorf("parse agent.yaml: %w", err)
    }

    return core.RepoConfig{
        Seed: core.SeedConfig{
            Copy: normalizeSeedPaths(cfg.Seed.Copy),
        },
    }, nil
}
```

Validate that each entry is a non-empty string. Reject unsupported YAML shapes instead of coercing them silently.

- [ ] **Step 5: Re-run the config loader tests**

Run: `go test ./internal/adapters/repository/agentconfig -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/core/ports.go internal/adapters/repository/agentconfig/repository.go internal/adapters/repository/agentconfig/repository_test.go
git commit -m "Add repo-local agent config loader"
```

## Task 2: Add the Workspace Seeder Adapter

**Files:**
- Modify: `internal/core/ports.go`
- Create: `internal/adapters/repository/workspace/repository.go`
- Create: `internal/adapters/repository/workspace/repository_test.go`

- [ ] **Step 1: Write the failing workspace seeder tests**

Add focused tests for:

- copying one file into a new worktree path
- copying a nested directory recursively
- missing source path returning an error
- destination conflict returning an error
- symlink source returning an error
- validation-only mode returning nil for good paths and errors for bad paths

Example:

```go
func TestRepositorySeedWorkspace_CopiesFile(t *testing.T) {
    repoRoot := t.TempDir()
    worktree := t.TempDir()
    require.NoError(t, os.WriteFile(filepath.Join(repoRoot, ".env"), []byte("A=1\n"), 0o600))

    repo := NewRepository()
    var copied []string
    err := repo.SeedWorkspace(t.Context(), core.SeedWorkspaceInput{
        RepoRoot:      repoRoot,
        WorktreePath:  worktree,
        RelativePaths: []string{".env"},
    }, func(path string) { copied = append(copied, path) })

    require.NoError(t, err)
    body, err := os.ReadFile(filepath.Join(worktree, ".env"))
    require.NoError(t, err)
    require.Equal(t, "A=1\n", string(body))
    require.Equal(t, []string{".env"}, copied)
}
```

- [ ] **Step 2: Run the workspace seeder tests to verify red**

Run: `go test ./internal/adapters/repository/workspace -v`
Expected: FAIL because the package and seeder do not exist yet

- [ ] **Step 3: Add the workspace seeder port**

Add `SeedWorkspaceInput` and `WorkspaceSeeder` to `internal/core/ports.go`.

- [ ] **Step 4: Implement strict filesystem seeding**

Implement `SeedWorkspace` and `ValidateSeedPaths` in `internal/adapters/repository/workspace/repository.go`.

Key rules:

- resolve every configured path under `RepoRoot`
- reject paths that escape the repo root
- reject symlinks via `os.Lstat`
- fail if source is missing
- fail if destination already exists
- copy files with content and mode
- create directories as needed and recurse through directory entries
- call the progress callback after each successful top-level path copy

Helpful helpers:

```go
func resolveRelativePath(root, rel string) (string, error)
func ensureMissing(path string) error
func copyFile(src, dst string, mode fs.FileMode) error
func copyDir(src, dst string) error
func isSymlink(info fs.FileInfo) bool
```

- [ ] **Step 5: Re-run the workspace seeder tests**

Run: `go test ./internal/adapters/repository/workspace -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/core/ports.go internal/adapters/repository/workspace/repository.go internal/adapters/repository/workspace/repository_test.go
git commit -m "Add workspace seeding adapter"
```

## Task 3: Integrate Seeding Into Core Task Creation And Doctor

**Files:**
- Modify: `internal/core/progress.go`
- Modify: `internal/core/service.go`
- Modify: `internal/core/fakes_test.go`
- Modify: `internal/core/service_new_test.go`
- Modify: `internal/core/service_doctor_test.go`

- [ ] **Step 1: Write the failing core tests**

Add focused tests for:

- `CreateTaskWithProgress` seeding configured files after worktree creation and before tmux creation
- seeding failure marking the task broken with a `seed workspace:` error
- progress events including `TaskProgressWorkspaceSeeding` and per-path copied messages
- `Doctor` returning config notes for missing config, valid config, and invalid config
- `Doctor` surfacing missing configured seed paths as failures

Example:

```go
func TestServiceCreateTaskWithProgress_SeedsWorkspaceBeforeTmux(t *testing.T) {
    svc := newTestService()
    svc.configRepo.repoConfig = RepoConfig{
        Seed: SeedConfig{Copy: []string{".env", "local/"}},
    }

    var events []TaskProgress
    _, err := svc.service.CreateTaskWithProgress(t.Context(), NewTaskInput{
        Cwd:                  "/tmp/repo",
        Prompt:               "seed workspace",
        ConfirmedDisplayName: "Seed Workspace",
    }, CreateTaskOptions{}, func(event TaskProgress) {
        events = append(events, event)
    })

    require.NoError(t, err)
    require.Equal(t, []string{".env", "local/"}, svc.workspaceSeeder.seededPaths)
    require.True(t, svc.workspaceSeeder.seededBeforeTmux)
    require.Contains(t, progressSteps(events), TaskProgressWorkspaceSeeding)
}
```

- [ ] **Step 2: Run the focused core tests to verify red**

Run: `go test ./internal/core -run 'TestService(CreateTaskWithProgress|Doctor)' -v`
Expected: FAIL because config loading and workspace seeding are not wired into the service yet

- [ ] **Step 3: Extend the service dependencies and fakes**

Update `Service` and `NewService` to accept:

- `RepoConfigRepository`
- `WorkspaceSeeder`

Extend `newTestService()` and `internal/core/fakes_test.go` with:

- `fakeRepoConfigRepository`
- `fakeWorkspaceSeeder`

The fake seeder should record:

- `seedInput`
- `seededPaths`
- `seedErr`
- whether seeding happened before tmux session creation

- [ ] **Step 4: Integrate config loading, seeding, and doctor notes**

In `createTask`:

1. load repo config right after repo detection
2. create the worktree
3. if `len(repoConfig.Seed.Copy) > 0`, emit `Seeding workspace...`
4. call `SeedWorkspace`
5. emit one `TaskProgressWorkspaceSeeded` event per copied path
6. only then create tmux and launch Codex

Sketch:

```go
repoConfig, err := s.repoConfig.LoadRepoConfig(ctx, repoCtx.Root)
if err != nil {
    return nil, err
}

if len(repoConfig.Seed.Copy) > 0 {
    emitTaskProgress(progress, TaskProgress{
        Step:    TaskProgressWorkspaceSeeding,
        Message: "Seeding workspace...",
        Task:    cloneTask(task),
    })

    err := s.workspace.SeedWorkspace(ctx, SeedWorkspaceInput{
        RepoRoot:      task.RepoRoot,
        WorktreePath:  task.WorktreePath,
        RelativePaths: repoConfig.Seed.Copy,
    }, func(path string) {
        emitTaskProgress(progress, TaskProgress{
            Step:    TaskProgressWorkspaceSeeded,
            Message: fmt.Sprintf("Copied %s", path),
            Task:    cloneTask(task),
        })
    })
    if err != nil {
        return s.markBroken(ctx, task, fmt.Errorf("seed workspace: %w", err))
    }
}
```

In `Doctor`:

- load repo config when repo detection succeeds
- append `Notes` describing config presence and valid seed paths
- append a `Failures` entry for invalid config or invalid seed paths

- [ ] **Step 5: Re-run the focused core tests**

Run: `go test ./internal/core -run 'TestService(CreateTaskWithProgress|Doctor)' -v`
Expected: PASS

- [ ] **Step 6: Run the full core package tests**

Run: `go test ./internal/core -v`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/core/progress.go internal/core/service.go internal/core/fakes_test.go internal/core/service_new_test.go internal/core/service_doctor_test.go
git commit -m "Integrate workspace seeding into task creation"
```

## Task 4: Wire Repositories Into Runtime And Update CLI Output

**Files:**
- Modify: `cmd/agent/main.go`
- Modify: `cmd/agent/main_test.go`
- Modify: `internal/adapters/handler/cli/doctor.go`
- Modify: `internal/adapters/handler/cli/doctor_test.go`
- Modify: `internal/adapters/handler/cli/new.go`
- Modify: `internal/adapters/handler/cli/new_test.go`
- Modify: `README.md`

- [ ] **Step 1: Write the failing adapter and CLI tests**

Add or extend tests for:

- runtime service constructing the core service with the new config and workspace repositories
- `doctor` printing notes and then `doctor: ok` when there are no failures
- `doctor` printing notes plus failure lines when config validation fails
- `new` rendering the seeding progress messages to stderr when progress events arrive

Example:

```go
func TestDoctorCommand_PrintsNotesAndOk(t *testing.T) {
    out := &bytes.Buffer{}
    cmd := newDoctorCommand(Dependencies{
        Service: fakeCLIService{
            doctorResult: core.DoctorResult{
                Notes: []string{"config: loaded agent.yaml"},
            },
        },
        Stdout: out,
        Stderr: out,
        Cwd:    "/tmp/repo",
    })
    cmd.SetOut(out)
    cmd.SetErr(out)

    err := cmd.Execute()
    require.NoError(t, err)
    require.Contains(t, out.String(), "config: loaded agent.yaml")
    require.Contains(t, out.String(), "doctor: ok")
}
```

- [ ] **Step 2: Run the focused CLI/runtime tests to verify red**

Run: `go test ./cmd/agent ./internal/adapters/handler/cli -run 'Test(DoctorCommand|NewCommand|RuntimeTmuxRepository|BuildDependencies)' -v`
Expected: FAIL because the new notes output and repository wiring are not implemented yet

- [ ] **Step 3: Wire the new repositories into runtime service construction**

Update `cmd/agent/main.go` so `newService` constructs:

- `agentconfig.NewRepository()`
- `workspace.NewRepository()`

and passes them into `core.NewService(...)`.

If needed, add small constructor tests that assert `Doctor` and `NewTask` still build successfully through `runtimeService`.

- [ ] **Step 4: Update CLI output and docs**

In `internal/adapters/handler/cli/doctor.go`:

- print `DoctorResult.Notes` first
- preserve existing failure printing behavior
- print `doctor: ok` only when there are no failures

In `README.md`, add:

- `agent.yaml` example
- explanation of strict missing-path and conflict behavior
- example of seeding ignored files like `.env`, `.lazy.lua`, and `local/`

- [ ] **Step 5: Re-run focused CLI/runtime tests**

Run: `go test ./cmd/agent ./internal/adapters/handler/cli -v`
Expected: PASS

- [ ] **Step 6: Run full verification**

Run: `go test ./...`
Expected: PASS

Run: `go run ./cmd/agent doctor`
Expected: `doctor: ok` in a healthy local environment, or config notes plus actionable failure lines if the current repo config is invalid

- [ ] **Step 7: Commit**

```bash
git add cmd/agent/main.go cmd/agent/main_test.go internal/adapters/handler/cli/doctor.go internal/adapters/handler/cli/doctor_test.go internal/adapters/handler/cli/new.go internal/adapters/handler/cli/new_test.go README.md
git commit -m "Wire workspace seeding through CLI and runtime"
```

## Final Review

- [ ] **Step 1: Read the full diff**

Run: `git diff --stat HEAD~4..HEAD`
Expected: touched files match the planned seeding surface, without unrelated refactors

- [ ] **Step 2: Re-run full verification before handoff**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 3: Sanity-check in a real repo**

Run from a repo that contains `agent.yaml`:

```bash
go run ./cmd/agent doctor
go run ./cmd/agent new --non-interactive "seed workspace smoke test"
```

Expected:

- `doctor` reports config notes and no failures
- `new` prints `Seeding workspace...` plus `Copied ...` lines for configured paths
- the resulting worktree contains the copied ignored files and directories before Codex starts

- [ ] **Step 4: Commit any final plan-following fixes**

```bash
git add .
git commit -m "Finalize workspace seeding"
```
