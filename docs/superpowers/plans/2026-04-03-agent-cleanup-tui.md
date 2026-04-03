# Agent Cleanup TUI Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a Bubble Tea-powered `agent tui` command that lists tracked tasks and lets the user delete the tmux session and worktree for a selected task while preserving the git branch.

**Architecture:** Extend the existing orchestration core with an explicit cleanup action and a new `cleaned` task status. Keep cleanup semantics in `internal/core`, add the needed destructive operations to the git and tmux adapters, and add a thin Bubble Tea-based TUI adapter in `internal/adapters/handler/cli` that calls the core service and renders a single full-screen list with confirmation state.

**Tech Stack:** Go, Cobra, Bubble Tea, SQLite, tmux, git, `os/exec`, testify

---

## File Structure

Modify or create the following files during implementation.

### CLI Entrypoint And Handlers

- Modify: `cmd/agent/main.go`
- Modify: `internal/adapters/handler/cli/root.go`
- Create: `internal/adapters/handler/cli/tui.go`
- Create: `internal/adapters/handler/cli/tui_model.go`
- Create: `internal/adapters/handler/cli/tui_test.go`
- Create: `internal/adapters/handler/cli/tui_model_test.go`

### Core

- Modify: `internal/core/ports.go`
- Modify: `internal/core/status.go`
- Modify: `internal/core/task.go`
- Modify: `internal/core/service.go`
- Modify: `internal/core/fakes_test.go`
- Create: `internal/core/service_cleanup_test.go`

### Repository Adapters

- Modify: `internal/adapters/repository/git/repository.go`
- Modify: `internal/adapters/repository/git/repository_test.go`
- Modify: `internal/adapters/repository/tmux/repository.go`
- Modify: `internal/adapters/repository/tmux/repository_test.go`

### Docs And Module Metadata

- Modify: `go.mod`
- Modify: `README.md`

## Implementation Notes

- Keep the first TUI cut single-purpose and cleanup-focused.
- Do not add search, filtering, details panes, mouse behavior, or branch deletion.
- A cleaned task stays in SQLite; only its runtime resources are removed.
- Reconciliation must preserve `cleaned` when both session and worktree are absent.
- Partial cleanup is `broken`, but a later successful retry should transition the task to `cleaned` and clear `LastError`.
- Use `git worktree remove <path>` for cleanup rather than deleting directories directly.

### Interfaces To Add

```go
type GitRepository interface {
    IsAvailable(ctx context.Context) error
    DetectRepo(ctx context.Context, cwd string) (RepoContext, error)
    BranchExists(ctx context.Context, repoRoot, branch string) (bool, error)
    CreateWorktree(ctx context.Context, in CreateWorktreeInput) error
    RemoveWorktree(ctx context.Context, repoRoot, worktreePath string) error
}

type TmuxRepository interface {
    IsAvailable(ctx context.Context) error
    SessionExists(ctx context.Context, session string) (bool, error)
    CreateSession(ctx context.Context, in CreateSessionInput) error
    AttachOrSwitch(ctx context.Context, session string) error
    SendKeys(ctx context.Context, session string, command []string) error
    KillSession(ctx context.Context, session string) error
}
```

### Service Method To Add

```go
func (s *Service) DeleteTaskResources(ctx context.Context, idOrSlug string) (*Task, error)
```

The method should:

- load and reconcile the task
- kill the tmux session if present
- remove the worktree if present
- update booleans after each step
- mark partial cleanup as `broken`
- mark fully cleaned resources as `cleaned`
- preserve the git branch
- clear `LastError` on success
- append lifecycle events for cleanup attempts and results

### TUI Commands And Keys

The first version should support:

- `j` / `k` selection movement
- `g` / `G` jump to top or bottom
- `r` refresh
- `x` open confirmation
- `y` confirm cleanup
- `n`, `esc`, or `q` cancel confirmation
- `q` quit from normal list mode

## Task 1: Add Cleanup Semantics To The Core

**Files:**
- Modify: `internal/core/status.go`
- Modify: `internal/core/ports.go`
- Modify: `internal/core/service.go`
- Modify: `internal/core/fakes_test.go`
- Create: `internal/core/service_cleanup_test.go`

- [ ] **Step 1: Write the failing cleanup service tests**

```go
func TestServiceDeleteTaskResources_MarksTaskCleanedAfterRemovingSessionAndWorktree(t *testing.T) {
    harness := newTestService()
    harness.taskRepo.getTask = &Task{
        ID:            "1",
        Slug:          "billing-retry-flow",
        DisplayName:   "Billing Retry Flow",
        RepoRoot:      "/tmp/repo",
        BranchName:    "feat/billing-retry-flow",
        WorktreePath:  "/tmp/repo-billing-retry-flow",
        TmuxSession:   "repo-billing-retry-flow",
        Status:        TaskStatusRunning,
        WorktreeExists: true,
        SessionExists:  true,
    }
    harness.gitRepo.branchExists = true
    harness.gitRepo.worktreeExists = true
    harness.tmuxRepo.sessionExists = true

    task, err := harness.service.DeleteTaskResources(t.Context(), "billing-retry-flow")

    require.NoError(t, err)
    require.Equal(t, TaskStatusCleaned, task.Status)
    require.False(t, task.WorktreeExists)
    require.False(t, task.SessionExists)
    require.Empty(t, task.LastError)
}
```

Also add focused failing tests for:

- already-missing tmux session still cleaning the worktree successfully
- already-missing worktree still cleaning the tmux session successfully
- tmux success plus worktree failure ending in `broken`
- cleanup appending lifecycle events for attempt and result
- intermediate `UpdateTask` failure returning an error instead of silently succeeding
- partial cleanup preserving the original cleanup failure text rather than overwriting it with a generic drift message
- reconciliation preserving `cleaned` when both resources remain absent
- reconciliation turning a `cleaned` task back into `broken` if a session or worktree reappears

- [ ] **Step 2: Run the focused core tests to verify red**

Run: `go test ./internal/core -run 'TestService(DeleteTaskResources|Reconcile)' -v`
Expected: FAIL because `TaskStatusCleaned`, adapter fields, and `DeleteTaskResources` do not exist yet

- [ ] **Step 3: Add the new core status and port methods**

```go
const (
    TaskStatusCreating TaskStatus = "creating"
    TaskStatusReady    TaskStatus = "ready"
    TaskStatusRunning  TaskStatus = "running"
    TaskStatusCleaned  TaskStatus = "cleaned"
    TaskStatusBroken   TaskStatus = "broken"
)
```

Extend the fake repositories in `internal/core/fakes_test.go` with:

- `RemoveWorktree`
- `KillSession`
- enough state to simulate already-missing resources and partial failures

- [ ] **Step 4: Implement cleanup orchestration in the service**

Implement `DeleteTaskResources` with stepwise state persistence:

```go
func (s *Service) DeleteTaskResources(ctx context.Context, idOrSlug string) (*Task, error) {
    task, err := s.GetTask(ctx, idOrSlug)
    if err != nil {
        return nil, err
    }

    cleanupErrs := make([]string, 0, 2)

    if task.SessionExists {
        if err := s.tmux.KillSession(ctx, task.TmuxSession); err != nil {
            cleanupErrs = append(cleanupErrs, "tmux: "+err.Error())
        } else {
            task.SessionExists = false
        }
        task.UpdatedAt = s.clock.Now().UTC()
        if err := s.tasks.UpdateTask(ctx, task); err != nil {
            return task, err
        }
    }

    if task.WorktreeExists {
        if err := s.git.RemoveWorktree(ctx, task.RepoRoot, task.WorktreePath); err != nil {
            cleanupErrs = append(cleanupErrs, "worktree: "+err.Error())
        } else {
            task.WorktreeExists = false
        }
        task.UpdatedAt = s.clock.Now().UTC()
        if err := s.tasks.UpdateTask(ctx, task); err != nil {
            return task, err
        }
    }

    if len(cleanupErrs) > 0 {
        task.Status = TaskStatusBroken
        task.LastError = strings.Join(cleanupErrs, ", ")
    } else {
        task.Status = TaskStatusCleaned
        task.LastError = ""
    }

    if err := s.tasks.UpdateTask(ctx, task); err != nil {
        return task, err
    }
    _ = s.tasks.AppendEvent(ctx, task.ID, "cleanup_attempted", task.Slug)
    _ = s.tasks.AppendEvent(ctx, task.ID, "cleanup_result", string(task.Status))

    reconciled, err := s.reconcileTask(ctx, task)
    if err != nil {
        return nil, err
    }
    if len(cleanupErrs) > 0 {
        reconciled.LastError = strings.Join(cleanupErrs, ", ")
        if err := s.tasks.UpdateTask(ctx, reconciled); err != nil {
            return reconciled, err
        }
    }
    return reconciled, nil
}
```

Adjust `reconcileTask` so `cleaned` is preserved only when both session and worktree are absent. Keep branch reappearance irrelevant, since the branch is intentionally retained. Do not let generic reconciliation drift messages overwrite a specific cleanup failure reason captured during `DeleteTaskResources`.

- [ ] **Step 5: Re-run the focused core tests**

Run: `go test ./internal/core -run 'TestService(DeleteTaskResources|Reconcile)' -v`
Expected: PASS

- [ ] **Step 6: Run the full core package tests**

Run: `go test ./internal/core -v`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/core/status.go internal/core/ports.go internal/core/service.go internal/core/fakes_test.go internal/core/service_cleanup_test.go
git commit -m "feat: add task cleanup semantics"
```

## Task 2: Add Cleanup Operations To Git And Tmux Adapters

**Files:**
- Modify: `internal/adapters/repository/git/repository.go`
- Modify: `internal/adapters/repository/git/repository_test.go`
- Modify: `internal/adapters/repository/tmux/repository.go`
- Modify: `internal/adapters/repository/tmux/repository_test.go`
- Modify: `cmd/agent/main.go`

- [ ] **Step 1: Write the failing adapter tests**

Add tests that expect:

- git cleanup to call `git worktree remove <path>`
- tmux cleanup to call `tmux kill-session -t =<session>`
- runtime service wiring in `cmd/agent/main.go` to satisfy the expanded interfaces

Example git test:

```go
func TestRepositoryRemoveWorktree_UsesExpectedGitCommand(t *testing.T) {
    runner := execx.NewFakeRunner([]execx.Result{{}})
    repo := NewRepository(runner)

    err := repo.RemoveWorktree(context.Background(), "/tmp/repo", "/tmp/repo-billing-retry-flow")

    require.NoError(t, err)
    require.Equal(t, []string{
        "worktree",
        "remove",
        "/tmp/repo-billing-retry-flow",
    }, runner.Calls[0].Args)
}
```

- [ ] **Step 2: Run the focused adapter tests to verify red**

Run: `go test ./internal/adapters/repository/git ./internal/adapters/repository/tmux -run 'TestRepository(RemoveWorktree|KillSession)' -v`
Expected: FAIL because the cleanup methods do not exist yet

- [ ] **Step 3: Implement the new adapter methods**

Add:

```go
func (r *Repository) RemoveWorktree(ctx context.Context, repoRoot, worktreePath string) error {
    _, err := r.runner.Run(ctx, repoRoot, "git", "worktree", "remove", worktreePath)
    return err
}
```

```go
func (r *Repository) KillSession(ctx context.Context, session string) error {
    _, err := r.runner.Run(ctx, "", "tmux", "kill-session", "-t", exactSessionTarget(session))
    return err
}
```

Then update `runtimeTmuxRepository` in `cmd/agent/main.go` to implement `KillSession` using the same session-name normalization path it already uses for session existence and send-keys targeting.

- [ ] **Step 4: Re-run the focused adapter tests**

Run: `go test ./internal/adapters/repository/git ./internal/adapters/repository/tmux -run 'TestRepository(RemoveWorktree|KillSession)' -v`
Expected: PASS

- [ ] **Step 5: Run package-level adapter tests**

Run: `go test ./internal/adapters/repository/git ./internal/adapters/repository/tmux -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/adapters/repository/git/repository.go internal/adapters/repository/git/repository_test.go internal/adapters/repository/tmux/repository.go internal/adapters/repository/tmux/repository_test.go cmd/agent/main.go
git commit -m "feat: add cleanup adapter operations"
```

## Task 3: Build The Bubble Tea TUI Command

**Files:**
- Modify: `go.mod`
- Modify: `internal/adapters/handler/cli/root.go`
- Create: `internal/adapters/handler/cli/tui.go`
- Create: `internal/adapters/handler/cli/tui_model.go`
- Create: `internal/adapters/handler/cli/tui_test.go`
- Create: `internal/adapters/handler/cli/tui_model_test.go`

- [ ] **Step 1: Write the failing TUI model tests**

Add model-level tests for:

- `j` and `k` changing the selected row
- `g` and `G` jumping to bounds
- `x` entering confirmation mode
- `n`, `esc`, and `q` canceling confirmation without quitting
- `y` dispatching cleanup for the selected task
- `r` triggering a refresh command
- the main list view rendering display name, status, tmux presence, worktree presence, and branch name
- the confirmation view rendering explicit copy that the tmux session and worktree will be deleted while the branch is kept
- failed cleanup rendering an inline error while leaving the TUI usable

Example:

```go
func TestModelUpdate_XEntersConfirmMode(t *testing.T) {
    model := newModel(fakeTUIService{
        tasks: []*core.Task{{Slug: "billing-retry-flow", DisplayName: "Billing Retry Flow"}},
    })

    next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
    updated := next.(model)

    require.True(t, updated.confirming)
    require.Equal(t, "billing-retry-flow", updated.selectedTask().Slug)
}
```

Also add a command wiring test that `agent --help` includes `tui`.

- [ ] **Step 2: Run the focused CLI tests to verify red**

Run: `go test ./internal/adapters/handler/cli -run 'Test(NewRootCommand_HelpIncludesTUI|ModelUpdate_)' -v`
Expected: FAIL because the new command and model do not exist yet

- [ ] **Step 3: Add the Bubble Tea dependency and the service method to the CLI interface**

Update `go.mod` to include Bubble Tea and extend `TaskService` with:

```go
DeleteTaskResources(ctx context.Context, idOrSlug string) (*core.Task, error)
```

Keep the TUI adapter on top of the existing service abstraction rather than building a second cleanup stack.

- [ ] **Step 4: Implement the model and command**

Create:

- `tui_model.go` for state, message types, and `tea.Model`
- `tui.go` for Cobra wiring and `tea.NewProgram(...)`

Model structure should stay small:

```go
type model struct {
    service     TaskService
    tasks       []*core.Task
    selected    int
    confirming  bool
    err         error
    width       int
    height      int
}
```

Key behavior:

- normal mode `q` returns `tea.Quit`
- confirmation mode `q`, `n`, and `esc` only dismiss the confirmation
- `y` triggers cleanup for the selected task and then refreshes the list
- normal list rendering includes display name, status, tmux/session presence, worktree presence, and branch name for the selected rows
- confirmation rendering includes explicit deletion-scope copy: tmux session and worktree are deleted, branch is preserved
- cleanup failures are rendered inline from the model `err` field and do not crash or exit the TUI

Wire the program as a full-screen TUI:

```go
program := tea.NewProgram(newModel(service), tea.WithAltScreen())
```

The rendered view can be plain text; do not spend time on Lip Gloss polish in this first pass.

- [ ] **Step 5: Re-run the focused CLI tests**

Run: `go test ./internal/adapters/handler/cli -run 'Test(NewRootCommand_HelpIncludesTUI|ModelUpdate_)' -v`
Expected: PASS

- [ ] **Step 6: Run the full CLI handler tests**

Run: `go test ./internal/adapters/handler/cli -v`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add go.mod internal/adapters/handler/cli/root.go internal/adapters/handler/cli/tui.go internal/adapters/handler/cli/tui_model.go internal/adapters/handler/cli/tui_test.go internal/adapters/handler/cli/tui_model_test.go
git commit -m "feat: add cleanup tui command"
```

## Task 4: Wire Runtime Cleanup, Update Docs, And Verify End To End

**Files:**
- Modify: `cmd/agent/main.go`
- Modify: `README.md`

- [ ] **Step 1: Add the runtime service cleanup wiring**

Extend `runtimeService` to expose:

```go
func (r *runtimeService) DeleteTaskResources(ctx context.Context, idOrSlug string) (*core.Task, error)
```

This should call `newService(true)` and delegate to the core service, matching the existing `ListTasks`, `GetTask`, and `OpenTask` pattern.

- [ ] **Step 2: Update the README**

Document:

- the new `agent tui` command
- the cleanup-only scope of the first TUI
- the keybindings
- that cleanup removes the tmux session and worktree but preserves the branch

- [ ] **Step 3: Run the full automated suite**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 4: Run targeted CLI verification**

Run: `go run ./cmd/agent --help`
Expected: output includes `tui`

Run: `go run ./cmd/agent doctor`
Expected: `doctor: ok` in a healthy local environment

- [ ] **Step 5: Perform a manual smoke test in a disposable task**

From the repo root:

```bash
go run ./cmd/agent new --non-interactive "temporary cleanup tui smoke"
go run ./cmd/agent tui
```

In the TUI:

- move to the created task with `j` / `k` if needed
- press `x`
- press `y`
- verify the row refreshes to `cleaned`

After exiting:

Run: `tmux ls | rg 'tmux-llm-session-temporary-cleanup-tui-smoke'`
Expected: no matches

Run: `git worktree list | rg 'tmux-llm-session-temporary-cleanup-tui-smoke'`
Expected: no matches

Run: `git branch --format='%(refname:short)' | rg 'feat/temporary-cleanup-tui-smoke'`
Expected: branch still exists

- [ ] **Step 6: Commit**

```bash
git add cmd/agent/main.go README.md
git commit -m "docs: add cleanup tui usage"
```
