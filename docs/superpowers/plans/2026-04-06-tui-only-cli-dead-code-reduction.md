# TUI-Only CLI Dead Code Reduction Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reduce the runtime surface to `agent` for the TUI and `agent doctor` for diagnostics, while deleting dead CLI code, dead tests, and dead core wrappers that no longer serve those paths.

**Architecture:** Keep the current TUI behavior and `doctor` flow, but collapse the Cobra shell so the root command launches Bubble Tea directly and only `doctor` remains as a subcommand. After the CLI surface is reduced, remove compatibility-only methods such as `Service.NewTask` and inline helpers that exist only to support deleted paths or test-only wrappers.

**Tech Stack:** Go 1.26, Cobra CLI, Bubble Tea TUI, testify, SQLite (`modernc.org/sqlite`), tmux

---

## File Structure

### Planned file responsibilities

- `cmd/agent/main.go`
  Keeps constructing dependencies and executing the root command, but now `agent` itself launches the TUI.
- `internal/adapters/handler/cli/root.go`
  Becomes the single CLI entrypoint with TUI-by-default behavior and a retained `doctor` subcommand.
- `internal/adapters/handler/cli/doctor.go`
  Remains the one supported operational subcommand.
- `internal/adapters/handler/cli/tui_model.go`
  Continues to drive task list, task creation, open, and cleanup flows without behavior changes.
- `internal/adapters/handler/cli/root_test.go`
  Verifies the reduced command surface and that the root command launches the TUI.
- `internal/adapters/handler/cli/tui_test.go`
  Covers the surviving root/TUI execution behavior.
- `internal/adapters/handler/cli/doctor_test.go`
  Verifies the retained `doctor` command output.
- `internal/core/service.go`
  Removes dead exported wrappers and keeps only the runtime creation path used by the TUI.
- `internal/core/service_new_test.go`
  Moves task-creation assertions from `NewTask(...)` to `CreateTaskWithProgress(...)`.

### Expected deletes

- `internal/adapters/handler/cli/tui.go`
- `internal/adapters/handler/cli/new.go`
- `internal/adapters/handler/cli/list.go`
- `internal/adapters/handler/cli/open.go`
- `internal/adapters/handler/cli/status.go`
- `internal/adapters/handler/cli/new_test.go`
- `internal/adapters/handler/cli/list_test.go`
- `internal/adapters/handler/cli/open_test.go`
- `internal/adapters/handler/cli/status_test.go`

### Expected interface reduction

- `internal/adapters/handler/cli/root.go`
  Remove `NewTask` and `GetTask` from `TaskService`.
- `internal/core/service.go`
  Remove `NewTask(...)`.
  Inline `createTask(...)` into `CreateTaskWithProgress(...)` if that helper no longer earns its keep.

---

### Task 1: Collapse The Root Command To TUI-By-Default

**Files:**
- Modify: `internal/adapters/handler/cli/root.go`
- Modify: `internal/adapters/handler/cli/root_test.go`
- Modify: `internal/adapters/handler/cli/tui_test.go`
- Delete: `internal/adapters/handler/cli/tui.go`

- [ ] **Step 1: Write the failing root-command tests for the new surface**

Replace the current help/subcommand expectations with tests that match the approved product surface:

```go
package cli

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewRootCommand_HelpOnlyIncludesDoctorSubcommand(t *testing.T) {
	out := &bytes.Buffer{}

	cmd := NewRootCommand(Dependencies{})
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"--help"})

	err := cmd.Execute()
	require.NoError(t, err)

	output := out.String()
	require.Contains(t, output, "doctor")
	require.NotContains(t, output, "new")
	require.NotContains(t, output, "ls")
	require.NotContains(t, output, "open")
	require.NotContains(t, output, "status")
	require.NotContains(t, output, "tui")
}
```

```go
func TestNewRootCommand_RunsTUIWhenNoArgsProvided(t *testing.T) {
	out := &bytes.Buffer{}
	service := &fakeTUIService{}

	cmd := NewRootCommand(Dependencies{Service: service, Stdout: out, Stderr: out})
	cmd.SetIn(bytes.NewBufferString("q"))
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs(nil)

	err := cmd.Execute()
	require.NoError(t, err)
	require.Equal(t, 1, service.listCalls)
}
```

- [ ] **Step 2: Run the focused CLI tests and verify they fail**

Run: `go test ./internal/adapters/handler/cli -run 'TestNewRootCommand_(HelpOnlyIncludesDoctorSubcommand|RunsTUIWhenNoArgsProvided)' -v`

Expected: FAIL because `root.go` still advertises multiple commands and does not launch the TUI on bare `agent`.

- [ ] **Step 3: Rewrite the root command so `agent` launches the TUI and only `doctor` remains**

Inline the old `newTUICommand` behavior into `NewRootCommand` and delete the separate constructor:

```go
package cli

import (
	"context"
	"fmt"
	"io"

	"agent/internal/core"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"
)

type TaskService interface {
	Doctor(ctx context.Context, cwd string) (core.DoctorResult, error)
	SuggestTaskName(ctx context.Context, prompt string, provider string) (string, error)
	CreateTaskWithProgress(
		ctx context.Context,
		input core.NewTaskInput,
		options core.CreateTaskOptions,
		progress func(core.TaskProgress),
	) (*core.Task, error)
	ListTasks(ctx context.Context) ([]*core.Task, error)
	OpenTask(ctx context.Context, idOrSlug string) error
	DeleteTaskResources(ctx context.Context, idOrSlug string) (*core.Task, error)
}

func NewRootCommand(deps Dependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Manage task worktrees and tmux sessions for agent-driven work",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if deps.Service == nil {
				return fmt.Errorf("service not configured")
			}

			program := tea.NewProgram(
				newTUIModel(deps.Service, deps.Cwd),
				tea.WithInput(cmd.InOrStdin()),
				tea.WithOutput(cmd.OutOrStdout()),
			)

			_, err := program.Run()
			return err
		},
	}

	if deps.Stdout != nil {
		cmd.SetOut(deps.Stdout)
	}
	if deps.Stderr != nil {
		cmd.SetErr(deps.Stderr)
	}

	cmd.AddCommand(newDoctorCommand(deps))
	return cmd
}
```

- [ ] **Step 4: Remove the obsolete `newTUICommand` tests and replace them with root-command coverage**

Delete the old `TestNewTUICommand_*` cases from `internal/adapters/handler/cli/tui_test.go` and keep only behavior that still exists:

```go
func TestNewRootCommand_ReturnsErrorWhenServiceNotConfigured(t *testing.T) {
	cmd := NewRootCommand(Dependencies{})
	cmd.SetIn(bytes.NewBufferString("q"))
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err := cmd.Execute()
	require.EqualError(t, err, "service not configured")
}
```

- [ ] **Step 5: Run the CLI package tests and verify the reduced root surface passes**

Run: `go test ./internal/adapters/handler/cli -run 'TestNewRootCommand|TestDoctorCommand' -v`

Expected: PASS, with root-command tests covering TUI startup and `doctor` still available.

- [ ] **Step 6: Commit Task 1**

```bash
git add internal/adapters/handler/cli/root.go internal/adapters/handler/cli/root_test.go internal/adapters/handler/cli/tui_test.go internal/adapters/handler/cli/doctor_test.go
git rm internal/adapters/handler/cli/tui.go
git commit -m "refactor: make root command launch the tui"
```

### Task 2: Delete Legacy CLI Commands And Narrow The CLI Interface

**Files:**
- Delete: `internal/adapters/handler/cli/new.go`
- Delete: `internal/adapters/handler/cli/list.go`
- Delete: `internal/adapters/handler/cli/open.go`
- Delete: `internal/adapters/handler/cli/status.go`
- Delete: `internal/adapters/handler/cli/new_test.go`
- Delete: `internal/adapters/handler/cli/list_test.go`
- Delete: `internal/adapters/handler/cli/open_test.go`
- Delete: `internal/adapters/handler/cli/status_test.go`
- Modify: `internal/adapters/handler/cli/doctor_test.go`
- Modify: `internal/adapters/handler/cli/tui_test.go`

- [ ] **Step 1: Remove the obsolete command files and command-specific tests**

Delete the legacy command implementations and their tests because they no longer serve a runtime path:

```text
internal/adapters/handler/cli/new.go
internal/adapters/handler/cli/list.go
internal/adapters/handler/cli/open.go
internal/adapters/handler/cli/status.go
internal/adapters/handler/cli/new_test.go
internal/adapters/handler/cli/list_test.go
internal/adapters/handler/cli/open_test.go
internal/adapters/handler/cli/status_test.go
```

- [ ] **Step 2: Narrow the remaining CLI fake services to the surviving interface**

Replace the broad fake service methods in `doctor_test.go` with only the methods still required by `TaskService`:

```go
type fakeCLIService struct {
	doctorErr    error
	doctorResult core.DoctorResult
}

func (f fakeCLIService) Doctor(context.Context, string) (core.DoctorResult, error) {
	return f.doctorResult, f.doctorErr
}

func (fakeCLIService) SuggestTaskName(context.Context, string, string) (string, error) {
	return "", nil
}

func (fakeCLIService) CreateTaskWithProgress(
	context.Context,
	core.NewTaskInput,
	core.CreateTaskOptions,
	func(core.TaskProgress),
) (*core.Task, error) {
	return nil, nil
}

func (fakeCLIService) ListTasks(context.Context) ([]*core.Task, error) { return nil, nil }
func (fakeCLIService) OpenTask(context.Context, string) error          { return nil }
func (fakeCLIService) DeleteTaskResources(context.Context, string) (*core.Task, error) {
	return nil, nil
}
```

Keep `fakeTUIService` in `tui_model_test.go` as the authoritative UI fake rather than preserving dead command-specific fakes.

- [ ] **Step 3: Run the CLI package tests and verify only the supported command surface remains**

Run: `go test ./internal/adapters/handler/cli -v`

Expected: PASS, and the package should no longer compile any deleted command handlers or their test fakes.

- [ ] **Step 4: Commit Task 2**

```bash
git add internal/adapters/handler/cli/root.go internal/adapters/handler/cli/doctor_test.go internal/adapters/handler/cli/tui_test.go
git rm internal/adapters/handler/cli/new.go internal/adapters/handler/cli/list.go internal/adapters/handler/cli/open.go internal/adapters/handler/cli/status.go internal/adapters/handler/cli/new_test.go internal/adapters/handler/cli/list_test.go internal/adapters/handler/cli/open_test.go internal/adapters/handler/cli/status_test.go
git commit -m "refactor: remove legacy cli commands"
```

### Task 3: Remove Dead Core Task-Creation Wrappers

**Files:**
- Modify: `internal/core/service.go`
- Modify: `internal/core/service_new_test.go`

- [ ] **Step 1: Rewrite the creation tests to follow the surviving runtime path**

Replace `NewTask(...)` calls in `internal/core/service_new_test.go` with `CreateTaskWithProgress(...)` using zero-value options and nil progress where progress is not under test:

```go
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
```

Apply the same rewrite to the fallback, launch-request, tmux-failure, and workspace-validation tests. Keep the existing dedicated progress test, since it already exercises the canonical runtime path.

- [ ] **Step 2: Run the focused core creation tests and verify they fail**

Run: `go test ./internal/core -run 'TestService(NewTask|CreateTask)' -v`

Expected: FAIL after the test rewrites because `Service.NewTask(...)` is still referenced by production code and the package surface has not been reduced yet.

- [ ] **Step 3: Remove `Service.NewTask(...)` and inline the remaining helper into `CreateTaskWithProgress(...)`**

Delete the compatibility-only wrapper and move the body of `createTask(...)` directly into `CreateTaskWithProgress(...)` if that helper becomes single-use:

```go
func (s *Service) CreateTaskWithProgress(
	ctx context.Context,
	input NewTaskInput,
	options CreateTaskOptions,
	progress func(TaskProgress),
) (*Task, error) {
	repoCtx, err := s.repo.DetectRepo(ctx, input.Cwd)
	if err != nil {
		return nil, err
	}

	// keep the existing task-creation logic here unchanged
	// so the runtime path remains CreateTaskWithProgress(...)
}
```

Remove these definitions entirely:

```go
func (s *Service) NewTask(ctx context.Context, input NewTaskInput) (*Task, error) {
	return s.createTask(ctx, input, CreateTaskOptions{}, nil)
}
```

```go
func (s *Service) createTask(
	ctx context.Context,
	input NewTaskInput,
	options CreateTaskOptions,
	progress func(TaskProgress),
) (*Task, error) {
	// delete this helper after its body moves to CreateTaskWithProgress
}
```

- [ ] **Step 4: Run the core task-creation tests and verify the reduced surface still passes**

Run: `go test ./internal/core -run 'TestService(NewTask|CreateTask)' -v`

Expected: PASS, with all creation coverage now flowing through `CreateTaskWithProgress(...)`.

- [ ] **Step 5: Commit Task 3**

```bash
git add internal/core/service.go internal/core/service_new_test.go
git commit -m "refactor: remove dead task creation wrappers"
```

### Task 4: Final Verification And Cleanup

**Files:**
- Modify: any files touched by `gofmt`

- [ ] **Step 1: Format the touched Go files**

Run: `gofmt -w cmd/agent/main.go internal/adapters/handler/cli/*.go internal/core/*.go`

Expected: the formatter may rewrite imports and alignment in the surviving CLI and core files only.

- [ ] **Step 2: Run the full test suite**

Run: `go test ./...`

Expected: PASS for the full repository, including the retained TUI and `doctor` paths.

- [ ] **Step 3: Inspect the worktree for unintended leftovers**

Run: `git status --short`

Expected: only the intended code and test changes remain, with the deleted CLI files staged or visible as removals.

- [ ] **Step 4: Commit the final cleanup pass**

```bash
git add cmd/agent/main.go internal/adapters/handler/cli internal/core
git commit -m "refactor: trim cli surface to tui and doctor"
```

- [ ] **Step 5: Record completion evidence**

Capture the exact verification commands and outcomes in the implementation notes or handoff message:

```text
Verified with:
- go test ./...
- gofmt -w cmd/agent/main.go internal/adapters/handler/cli/*.go internal/core/*.go
- git status --short
```
