# Observer Daemon Hybrid Status Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a long-running local observer daemon that owns hook ingestion and tmux activity monitoring, persists hybrid task status into SQLite, and streams live updates to the TUI without losing the existing `needs_input` signal.

**Architecture:** Keep the TUI as the only normal user entrypoint, but move live observability out of the TUI into a background observer process. The observer writes durable state to SQLite and publishes lightweight local updates so the TUI can render from snapshots first and stream live changes second. The visible status model becomes `finished`, `needs_input`, `working`, `working · command`, and `disconnected`.

**Tech Stack:** Go, Cobra, Bubble Tea, SQLite, tmux control-mode monitoring, local HTTP for hook ingestion, Unix domain sockets for local streaming/control

---

## File Map

- Modify: `cmd/agent/main.go`
  - Stop embedding live hook server ownership in the TUI path.
  - Add observer bootstrap wiring and self-exec support.
- Modify: `internal/infrastructure/config.go`
  - Add observer socket/path config and any health-check defaults.
- Modify: `internal/core/domain.go`
  - Replace hook-phase-centric UI fields with hybrid observer-facing status and activity detail types.
- Modify: `internal/core/ports.go`
  - Add observer repository/service interfaces for runtime snapshots and subscriptions.
- Modify: `internal/core/service.go`
  - Load persisted observer summaries into `TaskView`.
  - Expose stream subscription methods for the TUI.
- Modify: `internal/adapters/handler/cli/root.go`
  - Ensure the observer is running before the TUI starts.
  - Remove TUI-owned hook HTTP server startup.
- Modify: `internal/adapters/handler/cli/tui_model.go`
  - Render hybrid status precedence and observer-backed metadata.
- Modify: `internal/adapters/handler/cli/tui_style.go`
  - Add styles for `working`, `command`, and `disconnected`.
- Modify: `internal/adapters/client/tmux/runtime_monitor.go`
  - Reuse existing snapshot logic from the observer path.
- Modify: `internal/adapters/client/codex/runtime_detector.go`
  - Keep `needs_input` / `finished` derivation authoritative for Codex tasks.
- Modify: `internal/adapters/repository/sqlite/repository.go`
  - Add tables or columns needed for observer runtime snapshots and stream subscribers.
- Modify: `internal/adapters/repository/sqlite/hook_observability.go`
  - Stop leaking raw hook phases into user-facing state.
  - Contribute only activity refinement and metadata.
- Create: `internal/adapters/observability/observer/server.go`
  - Background daemon entrypoint, lifecycle, health, and shutdown coordination.
- Create: `internal/adapters/observability/observer/server_test.go`
  - Startup, singleton, and shutdown tests.
- Create: `internal/adapters/observability/observer/hub.go`
  - Local subscriber fanout for task updates.
- Create: `internal/adapters/observability/observer/hub_test.go`
  - Publish/subscribe tests.
- Create: `internal/adapters/observability/observer/tmuxwatcher.go`
  - Tmux-triggered refresh loop for managed tasks.
- Create: `internal/adapters/observability/observer/tmuxwatcher_test.go`
  - Runtime refresh and reconnect tests.
- Create: `internal/adapters/observability/observer/status.go`
  - Hybrid precedence logic: `finished`, `needs_input`, `working`, `working · command`, `disconnected`.
- Create: `internal/adapters/observability/observer/status_test.go`
  - Precedence and activity-detail tests.
- Create: `internal/adapters/observability/observer/socket.go`
  - Unix socket listener for TUI subscriptions and control traffic.
- Create: `internal/adapters/observability/observer/socket_test.go`
  - Socket protocol tests.
- Create: `internal/adapters/observability/observer/process.go`
  - Background process start, health-check, PID/socket cleanup, and self-exec helpers.
- Create: `internal/adapters/observability/observer/process_test.go`
  - Auto-start and stale-process cleanup tests.
- Create: `internal/core/mock_observer_runtime_repository_test.go`
  - Mock support for new observer persistence interfaces.

## Task 1: Establish The Observer Process Boundary

**Files:**
- Modify: `cmd/agent/main.go`
- Modify: `internal/infrastructure/config.go`
- Modify: `internal/adapters/handler/cli/root.go`
- Create: `internal/adapters/observability/observer/process.go`
- Create: `internal/adapters/observability/observer/process_test.go`
- Create: `internal/adapters/observability/observer/server.go`
- Create: `internal/adapters/observability/observer/server_test.go`

- [ ] **Step 1: Write the failing process-boundary tests**

Add tests that express the new ownership model:

- `TestNewRootCommand_StartsObserverBeforeLaunchingTUI`
- `TestEnsureObserverRunning_ReusesHealthyObserver`
- `TestEnsureObserverRunning_SpawnsObserverWhenUnavailable`
- `TestEnsureObserverRunning_CleansStaleSocketBeforeRespawn`

Test shape:

```go
func TestEnsureObserverRunning_ReusesHealthyObserver(t *testing.T) {
	manager := observer.NewProcessManager(observer.ProcessConfig{
		SocketPath: "/tmp/agent-observer-test.sock",
		Exec:       stubExec,
		Dial:       stubHealthyDial,
	})

	err := manager.EnsureRunning(context.Background())

	require.NoError(t, err)
	require.Empty(t, stubExec.Calls())
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/adapters/observability/observer ./internal/adapters/handler/cli -count=1`

Expected: FAIL with missing observer process types and root command wiring.

- [ ] **Step 3: Implement the observer process manager and hidden server entrypoint**

Implement:

- `observer.ProcessConfig`
- `observer.ProcessManager`
- `EnsureRunning(ctx context.Context) error`
- `Serve(ctx context.Context) error`

Implementation constraints:

- the normal user entrypoint stays `agent`
- the actual daemon process can be started by self-exec through a hidden/internal command
- the observer exposes a health check over the Unix socket
- stale socket cleanup happens before respawn

Core shape:

```go
type ProcessManager struct {
	socketPath string
	execPath   string
	spawn      func(context.Context, []string) error
	dial       func(context.Context, string) error
}

func (m *ProcessManager) EnsureRunning(ctx context.Context) error {
	if err := m.dial(ctx, m.socketPath); err == nil {
		return nil
	}
	_ = os.Remove(m.socketPath)
	if err := m.spawn(ctx, []string{"observer", "serve"}); err != nil {
		return err
	}
	return waitForHealthyObserver(ctx, m.dial, m.socketPath)
}
```

- [ ] **Step 4: Wire observer startup into the TUI path**

Before Bubble Tea starts in `internal/adapters/handler/cli/root.go`, call `EnsureRunning`. Remove the current TUI-owned `StartHookServer` startup path from the default `agent` run path.

Expected root flow:

```go
if deps.ObserverProcess != nil {
	if err := deps.ObserverProcess.EnsureRunning(cmd.Context()); err != nil {
		return err
	}
}
```

- [ ] **Step 5: Run the focused tests**

Run: `go test ./internal/adapters/observability/observer ./internal/adapters/handler/cli -count=1`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add cmd/agent/main.go internal/infrastructure/config.go internal/adapters/handler/cli/root.go internal/adapters/observability/observer
git commit -m "feat: add observer daemon process boundary"
```

## Task 2: Persist Observer Runtime State And Hybrid Status

**Files:**
- Modify: `internal/core/domain.go`
- Modify: `internal/core/ports.go`
- Modify: `internal/adapters/repository/sqlite/repository.go`
- Modify: `internal/adapters/repository/sqlite/hook_observability.go`
- Create: `internal/adapters/observability/observer/status.go`
- Create: `internal/adapters/observability/observer/status_test.go`
- Create: `internal/core/mock_observer_runtime_repository_test.go`

- [ ] **Step 1: Write the failing status-derivation tests**

Add tests for the exact precedence agreed in the spec:

- `TestDeriveDisplayStatus_PrefersFinished`
- `TestDeriveDisplayStatus_PrefersNeedsInputOverHookCommandActivity`
- `TestDeriveDisplayStatus_AddsCommandDetailOnlyWhenWorking`
- `TestDeriveDisplayStatus_ReturnsDisconnectedWhenProcessMissing`

Core examples:

```go
func TestDeriveDisplayStatus_PrefersNeedsInputOverHookCommandActivity(t *testing.T) {
	status := observer.DeriveDisplayStatus(observer.StatusInput{
		RuntimeState: core.RuntimeStateNeedsInput,
		ProcessAlive: true,
		ActiveCommand: true,
	})

	require.Equal(t, core.DisplayStatusNeedsInput, status.Primary)
	require.Empty(t, status.Activity)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/adapters/observability/observer ./internal/adapters/repository/sqlite -count=1`

Expected: FAIL with missing hybrid status types and persistence fields.

- [ ] **Step 3: Add the new observer-facing domain types**

Replace hook-phase-centric UI dependence with explicit display types in `internal/core/domain.go`:

```go
type DisplayStatus string

const (
	DisplayStatusFinished     DisplayStatus = "finished"
	DisplayStatusNeedsInput   DisplayStatus = "needs_input"
	DisplayStatusWorking      DisplayStatus = "working"
	DisplayStatusDisconnected DisplayStatus = "disconnected"
)

type DisplayActivity string

const (
	DisplayActivityNone    DisplayActivity = ""
	DisplayActivityCommand DisplayActivity = "command"
)
```

Extend the persisted summary model with fields the TUI can consume directly:

- `DisplayStatus`
- `DisplayActivity`
- `ProcessAlive`
- `LastRuntimeObservedAt`

- [ ] **Step 4: Implement hybrid derivation and SQLite storage**

Add one persistence path for the observer’s current per-task runtime summary. Keep hook tables for metadata/history, but stop treating `HookRuntimePhase` as the primary UI state.

Implementation shape:

```go
type StatusInput struct {
	TaskStatus     core.TaskStatus
	RuntimeState   core.RuntimeState
	ProcessAlive   bool
	ActiveCommand  bool
}

func DeriveDisplayStatus(in StatusInput) core.DisplayState {
	switch {
	case in.TaskStatus.IsTerminal():
		return core.DisplayState{Primary: core.DisplayStatusFinished}
	case in.RuntimeState == core.RuntimeStateNeedsInput:
		return core.DisplayState{Primary: core.DisplayStatusNeedsInput}
	case in.ProcessAlive && in.ActiveCommand:
		return core.DisplayState{Primary: core.DisplayStatusWorking, Activity: core.DisplayActivityCommand}
	case in.ProcessAlive:
		return core.DisplayState{Primary: core.DisplayStatusWorking}
	default:
		return core.DisplayState{Primary: core.DisplayStatusDisconnected}
	}
}
```

- [ ] **Step 5: Run the focused tests**

Run: `go test ./internal/adapters/observability/observer ./internal/adapters/repository/sqlite ./internal/core -count=1`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/core/domain.go internal/core/ports.go internal/adapters/repository/sqlite/repository.go internal/adapters/repository/sqlite/hook_observability.go internal/adapters/observability/observer
git commit -m "feat: persist hybrid observer status"
```

## Task 3: Move Hook Ingestion Behind The Observer

**Files:**
- Modify: `cmd/agent/main.go`
- Modify: `internal/adapters/handler/cli/root.go`
- Modify: `internal/adapters/observability/codexhooks/http.go`
- Create: `internal/adapters/observability/observer/hub.go`
- Create: `internal/adapters/observability/observer/hub_test.go`
- Modify: `internal/adapters/filesystem/codexhooks/bootstrapper.go`

- [ ] **Step 1: Write the failing hook-observer tests**

Add tests for:

- `TestObserverHookEndpoint_PersistsEventAndPublishesTaskUpdate`
- `TestObserverDropsUnmanagedHookEventsWithoutBroadcast`
- `TestGeneratedForwarderTargetsObserverEndpoint`

Core test example:

```go
func TestObserverHookEndpoint_PersistsEventAndPublishesTaskUpdate(t *testing.T) {
	repo := newHookRepoHarness(t)
	hub := observer.NewHub()
	server := observer.NewServer(repo, hub, ...)

	updateCh, cleanup := hub.Subscribe()
	defer cleanup()

	postHook(t, server, `{"cwd":"/tmp/worktree","session_id":"sess-1","hook_event_name":"PreToolUse"}`)

	select {
	case update := <-updateCh:
		require.Equal(t, taskID, update.TaskID)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for observer update")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/adapters/observability/observer ./internal/adapters/filesystem/codexhooks -count=1`

Expected: FAIL with missing observer hub and endpoint wiring.

- [ ] **Step 3: Implement observer-owned hook ingestion**

Move the hook HTTP server from the TUI path into the observer server. The generated Codex hook forwarder should point only at the observer endpoint; the observer then writes SQLite and emits a task update through the hub.

Key rule:

- no live hook server should be owned by the TUI process anymore

- [ ] **Step 4: Keep direct ingestion as a narrow fallback only if needed for portability**

If the forwarder keeps a fallback path, it should still target the observer process, not the foreground TUI. The observer remains the single backend owner.

Expected generated snippet:

```sh
collector_url="http://127.0.0.1:${AGENT_OBSERVER_HOOK_PORT}/hook"
if curl -fsS -X POST -H "Content-Type: application/json" --data-binary @- "$collector_url"; then
  exit 0
fi
exec "$agent_exec" observer ingest "$event_name"
```

- [ ] **Step 5: Run the focused tests**

Run: `go test ./internal/adapters/observability/observer ./internal/adapters/filesystem/codexhooks -count=1`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add cmd/agent/main.go internal/adapters/handler/cli/root.go internal/adapters/observability/codexhooks/http.go internal/adapters/observability/observer internal/adapters/filesystem/codexhooks/bootstrapper.go
git commit -m "feat: move hook ingestion into observer"
```

## Task 4: Add tmux-Triggered Runtime Refresh And Live Streaming

**Files:**
- Create: `internal/adapters/observability/observer/tmuxwatcher.go`
- Create: `internal/adapters/observability/observer/tmuxwatcher_test.go`
- Create: `internal/adapters/observability/observer/socket.go`
- Create: `internal/adapters/observability/observer/socket_test.go`
- Modify: `internal/adapters/client/tmux/runtime_monitor.go`
- Modify: `internal/core/service.go`
- Modify: `internal/core/ports.go`

- [ ] **Step 1: Write the failing tmux watcher and stream tests**

Add tests for:

- `TestTMuxWatcher_RefreshesAffectedTaskOnPaneActivity`
- `TestSocketServer_BroadcastsObserverTaskUpdate`
- `TestServiceSubscribeTaskUpdates_ReceivesObserverEvents`

Example:

```go
func TestTMuxWatcher_RefreshesAffectedTaskOnPaneActivity(t *testing.T) {
	monitor := newRuntimeMonitorStub(...)
	watcher := observer.NewTMuxWatcher(monitor, repo, hub, ...)

	err := watcher.HandleSessionActivity(context.Background(), "tmux-llm_test-task")

	require.NoError(t, err)
	require.Equal(t, core.RuntimeStateNeedsInput, repo.LastRuntimeState(taskID))
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/adapters/observability/observer ./internal/core -count=1`

Expected: FAIL with missing watcher/socket/service subscription support.

- [ ] **Step 3: Implement tmux-triggered refresh**

Build a watcher that listens for activity on managed sessions and then reuses `RuntimeMonitor.Snapshot` plus provider runtime detection to update persisted runtime state. Do not stream raw pane text into the TUI.

Implementation constraints:

- targeted task refresh only
- reuse existing runtime monitor and detector logic
- publish only derived task updates

- [ ] **Step 4: Implement Unix-socket streaming**

Create a small local protocol for:

- health check
- subscribe
- optional stop command later

Payload shape:

```json
{
  "task_id": "task-123",
  "display_status": "working",
  "display_activity": "command",
  "last_activity_at": "2026-04-09T12:00:00Z"
}
```

- [ ] **Step 5: Run the focused tests**

Run: `go test ./internal/adapters/observability/observer ./internal/core -count=1`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/adapters/observability/observer internal/adapters/client/tmux/runtime_monitor.go internal/core/service.go internal/core/ports.go
git commit -m "feat: stream tmux-backed observer updates"
```

## Task 5: Update The TUI To Consume Hybrid Observer State

**Files:**
- Modify: `internal/adapters/handler/cli/tui_model.go`
- Modify: `internal/adapters/handler/cli/tui_style.go`
- Modify: `internal/core/service.go`
- Modify: `internal/core/domain.go`
- Test: `internal/adapters/handler/cli/tui_model_test.go`

- [ ] **Step 1: Write the failing TUI behavior tests**

Add tests for:

- `TestTaskStateText_PrefersNeedsInputOverHookActivity`
- `TestTaskStateText_ShowsWorkingCommandForActiveCommand`
- `TestTaskStateText_ShowsDisconnectedWhenProcessMissing`
- `TestSelectedTaskView_HidesRawHookEventAsPrimaryStatus`

Core example:

```go
func TestTaskStateText_ShowsWorkingCommandForActiveCommand(t *testing.T) {
	view := &core.TaskView{
		Task: &core.Task{RuntimeState: core.RuntimeStateRunning},
		Observer: &core.ObserverSummary{
			DisplayStatus:   core.DisplayStatusWorking,
			DisplayActivity: core.DisplayActivityCommand,
			LastCommandText: "go test ./...",
		},
	}

	text, _ := taskStateText(view)

	require.Equal(t, "● working · command", text)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/adapters/handler/cli -count=1`

Expected: FAIL with missing observer summary fields and outdated hook-phase rendering.

- [ ] **Step 3: Render observer-backed status and detail pane data**

Update the list row and selected-task pane so that:

- primary status comes from the hybrid observer model
- `needs_input` remains visible
- `working · command` is shown only when command activity exists
- raw hook event names remain in the timeline only, not the primary status area

Preview priority:

```go
return firstNonEmpty(
	view.Observer.LastCommandText,
	view.Observer.LastAssistantMessage,
	view.Observer.LastPromptText,
)
```

- [ ] **Step 4: Keep graceful fallback behavior**

If observer data is absent, keep rendering the current persisted task/runtime information rather than blanking the row. The TUI should degrade cleanly when the observer stream is disconnected.

- [ ] **Step 5: Run the focused tests**

Run: `go test ./internal/adapters/handler/cli -count=1`

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/adapters/handler/cli/tui_model.go internal/adapters/handler/cli/tui_style.go internal/adapters/handler/cli/tui_model_test.go internal/core/service.go internal/core/domain.go
git commit -m "feat: render hybrid observer status in tui"
```

## Task 6: End-To-End Verification And Cleanup

**Files:**
- Modify: any touched files required for final polish
- Test: `cmd/agent/main_test.go`
- Test: `internal/adapters/repository/sqlite/repository_test.go`
- Test: `internal/core/service_hook_observability_test.go`

- [ ] **Step 1: Add end-to-end regression tests for the final boundaries**

Cover:

- TUI startup auto-starts observer
- hook ingestion no longer depends on the foreground TUI process
- hybrid precedence preserves `needs_input`
- closing the TUI does not clear persisted observer state

Example:

```go
func TestBuildDependencies_ConfiguresObserverProcessAndRepository(t *testing.T) {
	deps, err := buildDependencies()

	require.NoError(t, err)
	require.NotNil(t, deps.ObserverProcess)
	require.NotNil(t, deps.Service)
}
```

- [ ] **Step 2: Run the full test suite**

Run: `go test ./...`

Expected: PASS

- [ ] **Step 3: Manual smoke test**

Run:

```bash
go run ./cmd/agent
```

Manual verification:

- TUI opens without manual observer startup
- creating a fresh Codex task causes observer-backed hook activity to appear
- `needs_input` still appears when Codex is waiting on the user
- an active Bash tool run shows `working · command`
- closing the TUI and reopening preserves task/session knowledge

- [ ] **Step 4: Commit**

```bash
git add cmd/agent/main_test.go internal/adapters/repository/sqlite/repository_test.go internal/core/service_hook_observability_test.go
git commit -m "test: cover observer daemon hybrid status flow"
```
