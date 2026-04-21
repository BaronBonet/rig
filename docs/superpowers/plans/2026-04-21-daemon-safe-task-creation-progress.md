# Daemon-Safe Task Creation Progress Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restore live task-creation progress in the daemon-backed `rig` flow so the TUI shows streamed create milestones before the task appears in the list.

**Architecture:** Keep `TaskService.CreateTask` and the existing one-shot `TaskFrontend.CreateTask` intact, but add an internal create-progress sink inside the core create flow and a streamed create path over the taskdaemon socket. The TUI should switch from one-shot create to streamed create events, render progress steps locally, and still refresh from the authoritative task list after success.

**Tech Stack:** Go, Bubble Tea, Lip Gloss, Unix domain sockets, SQLite task repository, tmux/Codex adapters.

---

## File Structure

### Core progress types and create-flow plumbing

- Modify: `internal/core/task_service_ports.go`
  Add create-progress step/event types and extend `TaskFrontend` with a streamed create method.
- Modify: `internal/core/task_service.go`
  Emit progress at the prompt-create orchestration boundaries through a private progress sink.
- Modify: `internal/core/task_service_create_test.go`
  Add tests for progress emission order and no-op behavior when no sink is attached.

### Taskdaemon streamed create protocol

- Modify: `internal/adapters/taskdaemon/protocol.go`
  Add streamed create event payloads and response helpers.
- Modify: `internal/adapters/taskdaemon/frontend.go`
  Implement `CreateTaskStream`, and keep one-shot `CreateTask` as a thin drain-over-stream wrapper.
- Modify: `internal/adapters/taskdaemon/unix_socket_server.go`
  Stream `task_create_progress` followed by terminal `task_created` or `error`.
- Modify: `internal/adapters/taskdaemon/server.go`
  Expose the streamed create path from the daemon-facing frontend/service side.
- Modify: `internal/adapters/taskdaemon/frontend_test.go`
  Add streamed client tests and verify one-shot create still works.
- Modify: `internal/adapters/taskdaemon/server_test.go`
  Add protocol tests for progress message ordering and terminal failure behavior.

### TUI create-progress state and rendering

- Modify: `internal/adapters/handler/tui/commands.go`
  Replace one-shot create command usage with streamed create commands and progress messages.
- Modify: `internal/adapters/handler/tui/model.go`
  Track create-progress steps, active step, and terminal failure retention.
- Modify: `internal/adapters/handler/tui/render.go`
  Render the progress step list in the create view.
- Modify: `internal/adapters/handler/tui/model_test.go`
  Add tests for progress rendering, progress failure retention, and success transition back to browse mode.

### Verification and operator sanity check

- Modify if needed: `cmd/rig/main.go`
  No intended behavior change, but touch only if the streamed frontend contract forces wiring updates.
- Verify with:
  - targeted package tests
  - `go test ./...`
  - manual `rig` relaunch using the repo-built binary if daemon/build-version behavior needs confirmation

## Planning Notes

- Do not reintroduce a public callback parameter on `TaskService.CreateTask`.
- Keep step labels in the TUI. The daemon/core should send stable step IDs only.
- Preserve the existing post-create authoritative refresh behavior in the TUI.
- Keep the one-shot `CreateTask` path available by draining the streamed create path unless removing it is proven safe.
- Do not persist create progress to SQLite in this plan.

## Task 1: Add Create-Progress Types And Internal Core Emission

**Files:**
- Modify: `internal/core/task_service_ports.go`
- Modify: `internal/core/task_service.go`
- Modify: `internal/core/task_service_create_test.go`

- [ ] **Step 1: Write the failing tests**

```go
func TestTaskServiceCreateTask_EmitsProgressStepsInOrder(t *testing.T) {
	svc := newTestTaskService(t)
	var got []TaskCreateProgressStep
	ctx := contextWithCreateProgressSink(t.Context(), func(step TaskCreateProgressStep) {
		got = append(got, step)
	})

	_, err := svc.service.CreateTask(ctx, CreateTaskInput{
		Cwd:      "/tmp/repo",
		Prompt:   "testing creating a new task",
		Provider: ProviderCodex,
	})
	require.NoError(t, err)
	require.Equal(t, []TaskCreateProgressStep{
		TaskCreateProgressSuggestingName,
		TaskCreateProgressCreatingWorktree,
		TaskCreateProgressPreparingWorkspace,
		TaskCreateProgressStartingSession,
	}, got)
}

func TestTaskServiceCreateTask_AllowsMissingProgressSink(t *testing.T) {
	svc := newTestTaskService(t)
	_, err := svc.service.CreateTask(t.Context(), CreateTaskInput{
		Cwd:      "/tmp/repo",
		Prompt:   "testing creating a new task",
		Provider: ProviderCodex,
	})
	require.NoError(t, err)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/core -run 'TestTaskServiceCreateTask_EmitsProgressStepsInOrder|TestTaskServiceCreateTask_AllowsMissingProgressSink'`

Expected: FAIL because the progress types and sink plumbing do not exist yet.

- [ ] **Step 3: Write the minimal implementation**

```go
type TaskCreateProgressStep string

const (
	TaskCreateProgressSuggestingName    TaskCreateProgressStep = "suggesting_name"
	TaskCreateProgressCreatingWorktree  TaskCreateProgressStep = "creating_worktree"
	TaskCreateProgressPreparingWorkspace TaskCreateProgressStep = "preparing_workspace"
	TaskCreateProgressStartingSession   TaskCreateProgressStep = "starting_session"
)

type taskCreateProgressSink interface {
	Emit(TaskCreateProgressStep)
}

func emitTaskCreateProgress(ctx context.Context, step TaskCreateProgressStep) {
	sink := taskCreateProgressSinkFromContext(ctx)
	if sink != nil {
		sink.Emit(step)
	}
}
```

Emit the steps in `createTaskFromPrompt` before:

- `suggestTaskName`
- `CreateTaskWorkspace`
- `prepareTaskWorkspace`
- `startTaskRuntime`

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/core -run 'TestTaskServiceCreateTask_EmitsProgressStepsInOrder|TestTaskServiceCreateTask_AllowsMissingProgressSink'`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/core/task_service_ports.go internal/core/task_service.go internal/core/task_service_create_test.go
git commit -m "feat: emit internal task creation progress steps"
```

## Task 2: Add Streamed Create To TaskFrontend And Taskdaemon

**Files:**
- Modify: `internal/core/task_service_ports.go`
- Modify: `internal/adapters/taskdaemon/protocol.go`
- Modify: `internal/adapters/taskdaemon/frontend.go`
- Modify: `internal/adapters/taskdaemon/server.go`
- Modify: `internal/adapters/taskdaemon/unix_socket_server.go`
- Modify: `internal/adapters/taskdaemon/frontend_test.go`
- Modify: `internal/adapters/taskdaemon/server_test.go`

- [ ] **Step 1: Write the failing tests**

```go
func TestFrontend_CreateTaskStreamYieldsProgressThenTask(t *testing.T) {
	socketPath := frontendTestSocketPath(t)
	requestCh := make(chan socketRequest, 1)
	serverErrCh := serveStreamingFrontendSocket(t, socketPath, func(req socketRequest, encoder *json.Encoder) error {
		requestCh <- req
		if err := encoder.Encode(socketEnvelope{
			Type: "task_create_progress",
			OK:   true,
			CreateProgress: &core.TaskCreateProgressEvent{Step: core.TaskCreateProgressSuggestingName},
		}); err != nil {
			return err
		}
		return encoder.Encode(socketEnvelope{
			Type: "task_created",
			OK:   true,
			Task: &core.Task{ID: "task-123", DisplayName: "ship it"},
		})
	})

	frontend := New(Config{SocketPath: socketPath}).Frontend()
	events, err := frontend.CreateTaskStream(t.Context(), core.CreateTaskInput{Prompt: "ship it"})
	require.NoError(t, err)
	require.Equal(t, socketRequest{Command: "create_task", Input: &core.CreateTaskInput{Prompt: "ship it"}}, <-requestCh)

	first := <-events
	require.Equal(t, core.TaskCreateProgressSuggestingName, first.Progress.Step)
	second := <-events
	require.Equal(t, "task-123", second.Task.ID)
	require.NoError(t, <-serverErrCh)
}
```

```go
func TestUnixSocketServer_CreateTaskStreamsProgressBeforeTerminalResult(t *testing.T) {
	// service emits one progress step, then returns a task
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/adapters/taskdaemon -run 'TestFrontend_CreateTaskStreamYieldsProgressThenTask|TestUnixSocketServer_CreateTaskStreamsProgressBeforeTerminalResult'`

Expected: FAIL because `CreateTaskStream` and the `task_create_progress` envelope do not exist yet.

- [ ] **Step 3: Write the minimal implementation**

```go
type TaskCreateEvent struct {
	Progress *TaskCreateProgressEvent
	Task     *Task
	Err      error
}

type TaskFrontend interface {
	OpenTaskSession(context.Context, *Task) error
	CreateTask(context.Context, CreateTaskInput) (*Task, error)
	CreateTaskStream(context.Context, CreateTaskInput) (<-chan TaskCreateEvent, error)
	// ...
}
```

Implement:

- frontend stream reader in `internal/adapters/taskdaemon/frontend.go`
- one-shot `CreateTask` by draining `CreateTaskStream`
- server-side create progress sink in `unix_socket_server.go`
- `socketEnvelope` fields for create-progress messages

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/adapters/taskdaemon -run 'TestFrontend_CreateTaskStreamYieldsProgressThenTask|TestUnixSocketServer_CreateTaskStreamsProgressBeforeTerminalResult'`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/core/task_service_ports.go internal/adapters/taskdaemon/protocol.go internal/adapters/taskdaemon/frontend.go internal/adapters/taskdaemon/server.go internal/adapters/taskdaemon/unix_socket_server.go internal/adapters/taskdaemon/frontend_test.go internal/adapters/taskdaemon/server_test.go
git commit -m "feat: stream task creation progress over taskdaemon"
```

## Task 3: Add Terminal Error Coverage And One-Shot Compatibility

**Files:**
- Modify: `internal/adapters/taskdaemon/frontend_test.go`
- Modify: `internal/adapters/taskdaemon/server_test.go`
- Modify: `internal/adapters/taskdaemon/frontend.go`
- Modify: `internal/adapters/taskdaemon/unix_socket_server.go`

- [ ] **Step 1: Write the failing tests**

```go
func TestFrontend_CreateTaskStreamYieldsTerminalError(t *testing.T) {
	// progress event first, then terminal error envelope
}

func TestFrontend_CreateTaskDrainsStreamAndReturnsFinalTask(t *testing.T) {
	// create one-shot still works over streamed transport
}

func TestUnixSocketServer_CreateTaskStreamsTerminalErrorOnFailure(t *testing.T) {
	// service emits progress, then returns an error
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/adapters/taskdaemon -run 'TestFrontend_CreateTaskStreamYieldsTerminalError|TestFrontend_CreateTaskDrainsStreamAndReturnsFinalTask|TestUnixSocketServer_CreateTaskStreamsTerminalErrorOnFailure'`

Expected: FAIL because terminal stream error behavior and one-shot draining are incomplete or missing.

- [ ] **Step 3: Write the minimal implementation**

Make sure:

- the server encodes `error` as the terminal message and stops streaming
- the frontend emits a final event with `Err`
- one-shot `CreateTask` ignores progress events and returns the final task or error

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/adapters/taskdaemon -run 'TestFrontend_CreateTaskStreamYieldsTerminalError|TestFrontend_CreateTaskDrainsStreamAndReturnsFinalTask|TestUnixSocketServer_CreateTaskStreamsTerminalErrorOnFailure'`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/taskdaemon/frontend.go internal/adapters/taskdaemon/frontend_test.go internal/adapters/taskdaemon/unix_socket_server.go internal/adapters/taskdaemon/server_test.go
git commit -m "fix: finalize streamed task creation behavior"
```

## Task 4: Switch The TUI Create Flow To Streamed Progress Events

**Files:**
- Modify: `internal/adapters/handler/tui/commands.go`
- Modify: `internal/adapters/handler/tui/model.go`
- Modify: `internal/adapters/handler/tui/render.go`
- Modify: `internal/adapters/handler/tui/model_test.go`

- [ ] **Step 1: Write the failing tests**

```go
func TestModel_CreateTaskStreamRendersProgressSteps(t *testing.T) {
	frontend := newStubFrontend()
	frontend.createTaskStream = []core.TaskCreateEvent{
		{Progress: &core.TaskCreateProgressEvent{Step: core.TaskCreateProgressSuggestingName}},
		{Progress: &core.TaskCreateProgressEvent{Step: core.TaskCreateProgressCreatingWorktree}},
		{Task: &core.Task{ID: "task-3", DisplayName: "ship it", RepoName: "rig", Provider: core.ProviderCodex}},
	}

	m := newLoadedModel(frontend)
	m.mode = modePromptInput
	m.prompt = "ship it"

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)

	// advance first progress event and assert view contains "Suggesting name"
	// advance second progress event and assert view contains "Creating worktree"
}

func TestModel_CreateTaskStreamFailurePreservesPromptAndSteps(t *testing.T) {
	// progress event, then terminal error event
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/adapters/handler/tui -run 'TestModel_CreateTaskStreamRendersProgressSteps|TestModel_CreateTaskStreamFailurePreservesPromptAndSteps'`

Expected: FAIL because the model still assumes one-shot create completion.

- [ ] **Step 3: Write the minimal implementation**

Add:

- a Tea command that starts `CreateTaskStream`
- a Tea command that waits for the next create-stream event
- model state for:
  - completed create steps
  - current active create step
  - terminal create error
- render helpers in `render.go` for:
  - completed step
  - active shimmer step
  - dim future steps

Keep:

- prompt preservation on failure
- browse-mode transition on success
- post-success authoritative `loadTasksCmd(...)`

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/adapters/handler/tui -run 'TestModel_CreateTaskStreamRendersProgressSteps|TestModel_CreateTaskStreamFailurePreservesPromptAndSteps'`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/handler/tui/commands.go internal/adapters/handler/tui/model.go internal/adapters/handler/tui/render.go internal/adapters/handler/tui/model_test.go
git commit -m "feat: show streamed task creation progress in tui"
```

## Task 5: Full Verification And Runtime Sanity Check

**Files:**
- Modify only if needed after verification fallout.

- [ ] **Step 1: Run focused package tests**

Run:

```bash
go test ./internal/core ./internal/adapters/taskdaemon ./internal/adapters/handler/tui
```

Expected: PASS

- [ ] **Step 2: Run the full test suite**

Run:

```bash
go test ./...
```

Expected: PASS

- [ ] **Step 3: Rebuild the binary used by the shell**

Run:

```bash
make build
install -m 755 ./local/bin/rig /Users/ebon/.local/bin/rig
```

Expected: fresh `rig` binary built and installed with the current repo version.

- [ ] **Step 4: Manual runtime verification**

Run:

```bash
rig
```

Expected:

- create a new prompt-backed task
- progress steps appear in order:
  - `Suggesting name`
  - `Creating worktree`
  - `Preparing workspace`
  - `Starting session`
- success returns to browse mode with the generated task name selected

- [ ] **Step 5: Final commit**

```bash
git add internal/core internal/adapters/taskdaemon internal/adapters/handler/tui
git commit -m "feat: restore daemon-safe task creation progress"
```
