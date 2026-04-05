# TUI Runtime Session State Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Codex-first live runtime state badges to every TUI task row using a persistent tmux control-mode monitor, without changing the meaning of structural task status.

**Architecture:** Keep structural reconciliation in `core.Service` exactly as the source of truth for worktree/session/window health, then enrich tasks with a derived `RuntimeState` supplied by a shared tmux runtime monitor plus provider-specific detectors. `runtimeService` owns the persistent tmux runtime monitor so it survives repeated `newService()` calls, while `core.Service` stays provider-agnostic by consuming injected runtime detectors keyed by provider.

**Tech Stack:** Go, tmux control mode, Bubble Tea, lipgloss, testify

---

## File Structure

| Action | File | Responsibility |
|--------|------|----------------|
| Create | `internal/core/runtime.go` | Define `RuntimeState`, `RuntimeSnapshot`, `RuntimeMonitor`, and `RuntimeStateDetector` |
| Modify | `internal/core/task.go` | Add derived runtime fields to `Task` |
| Modify | `internal/core/service.go` | Inject runtime monitor/detectors and enrich reconciled tasks with live runtime state |
| Modify | `internal/core/fakes_test.go` | Add fake runtime monitor and fake runtime detector support to the service harness |
| Modify | `internal/core/service_list_test.go` | Add runtime enrichment coverage in list reconciliation tests |
| Modify | `internal/core/service_status_test.go` | Add runtime enrichment coverage in get-task reconciliation tests |
| Create | `internal/adapters/repository/tmux/control_pipe.go` | Persistent tmux control-mode connection and `%output` event parsing |
| Create | `internal/adapters/repository/tmux/runtime_monitor.go` | Shared monitor that binds agent panes, tracks recent output, and captures pane snapshots |
| Create | `internal/adapters/repository/tmux/runtime_monitor_test.go` | Tests for pane binding, ambiguity handling, shell return, and recent-output bookkeeping |
| Create | `internal/adapters/repository/codex/runtime_detector.go` | Pure Codex runtime-state detector from a provider-agnostic snapshot |
| Create | `internal/adapters/repository/codex/runtime_detector_test.go` | Busy/prompt/finished precedence tests |
| Modify | `internal/adapters/handler/cli/tui_style.go` | Add runtime badge styling helpers |
| Modify | `internal/adapters/handler/cli/tui_model.go` | Render runtime badges for every task row |
| Modify | `internal/adapters/handler/cli/tui_model_test.go` | Assert row-level runtime badge rendering and no-badge fallback |
| Modify | `cmd/agent/main.go` | Make `runtimeService` own the shared runtime monitor and inject detectors into each `core.Service` |
| Modify | `cmd/agent/main_test.go` | Verify `runtimeService` constructs services with the new runtime dependencies |

---

### Task 1: Add Core Runtime Types And Service Wiring

**Files:**
- Create: `internal/core/runtime.go`
- Modify: `internal/core/task.go`
- Modify: `internal/core/service.go`
- Modify: `internal/core/fakes_test.go`
- Modify: `internal/core/service_list_test.go`
- Modify: `internal/core/service_status_test.go`

- [ ] **Step 1: Write the failing service test for runtime enrichment**

Add this test to `internal/core/service_list_test.go`:

```go
func TestServiceListTasks_EnrichesRuntimeStateForCodexTask(t *testing.T) {
	worktree := t.TempDir()
	svc := newTestService()
	svc.taskRepo.listTasks = []*Task{{
		ID:                "task-1",
		Slug:              "billing-retry-flow",
		RepoRoot:          "/tmp/repo",
		BranchName:        "feat/billing-retry-flow",
		WorktreePath:      worktree,
		TmuxSession:       "repo-billing-retry-flow",
		AgentWindowName:   "agent",
		EditorWindowName:  "editor",
		Provider:          "codex",
		Status:            TaskStatusRunning,
	}}
	svc.gitRepo.branchExists = true
	svc.tmuxRepo.sessionExists = true
	svc.tmuxRepo.windowExists = map[string]map[string]bool{
		"repo-billing-retry-flow": {
			"agent":  true,
			"editor": true,
		},
	}
	svc.runtimeMonitor.snapshot = RuntimeSnapshot{
		PaneID:             "%24",
		ForegroundCommand:  "codex",
		Content:            "› review my changes\n  gpt-5.4 high · 82% left",
		ObservedAt:         time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC),
		LastOutputAt:       time.Date(2026, 4, 5, 9, 59, 55, 0, time.UTC),
	}
	svc.runtimeDetector.state = RuntimeStateNeedsInput

	tasks, err := svc.service.ListTasks(t.Context())
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.Equal(t, RuntimeStateNeedsInput, tasks[0].RuntimeState)
}
```

- [ ] **Step 2: Run the failing core tests**

Run: `go test ./internal/core/... -run 'TestServiceListTasks_EnrichesRuntimeStateForCodexTask|TestServiceGetTask_ReconcilesLiveFields' -count=1`

Expected: FAIL with compile errors such as `undefined: RuntimeSnapshot`, `Task.RuntimeState undefined`, or missing runtime monitor fields in the test harness.

- [ ] **Step 3: Define the runtime types in core**

Create `internal/core/runtime.go`:

```go
package core

import (
	"context"
	"time"
)

type RuntimeState string

const (
	RuntimeStateNone       RuntimeState = ""
	RuntimeStateRunning    RuntimeState = "running"
	RuntimeStateNeedsInput RuntimeState = "needs_input"
	RuntimeStateFinished   RuntimeState = "finished"
)

type RuntimeSnapshot struct {
	SessionName        string
	WindowName         string
	PaneID             string
	ForegroundCommand  string
	Content            string
	ObservedAt         time.Time
	LastOutputAt       time.Time
}

type RuntimeMonitor interface {
	Snapshot(ctx context.Context, task *Task) (RuntimeSnapshot, error)
	Close() error
}

type RuntimeStateDetector interface {
	Detect(snapshot RuntimeSnapshot) RuntimeState
}
```

- [ ] **Step 4: Add derived runtime fields to `Task`**

Update `internal/core/task.go`:

```go
type Task struct {
	CreatedAt            time.Time    `json:"created_at"`
	UpdatedAt            time.Time    `json:"updated_at"`
	LastReconciledAt     time.Time    `json:"last_reconciled_at"`
	RuntimeStateUpdatedAt time.Time   `json:"runtime_state_updated_at"`
	ID                   string       `json:"id"`
	Prompt               string       `json:"prompt"`
	DisplayName          string       `json:"display_name"`
	Slug                 string       `json:"slug"`
	RepoRoot             string       `json:"repo_root"`
	RepoName             string       `json:"repo_name"`
	BaseBranch           string       `json:"base_branch"`
	BranchName           string       `json:"branch_name"`
	WorktreePath         string       `json:"worktree_path"`
	TmuxSession          string       `json:"tmux_session"`
	AgentWindowName      string       `json:"agent_window_name"`
	EditorWindowName     string       `json:"editor_window_name"`
	Provider             string       `json:"provider"`
	Status               TaskStatus   `json:"status"`
	RuntimeState         RuntimeState `json:"runtime_state"`
	LastError            string       `json:"last_error"`
	WorktreeExists       bool         `json:"worktree_exists"`
	BranchExists         bool         `json:"branch_exists"`
	SessionExists        bool         `json:"session_exists"`
	AgentWindowExists    bool         `json:"agent_window_exists"`
	EditorWindowExists   bool         `json:"editor_window_exists"`
}
```

- [ ] **Step 5: Inject runtime dependencies into `core.Service`**

Update the `Service` struct and constructor in `internal/core/service.go`:

```go
type Service struct {
	tasks            TaskRepository
	git              GitRepository
	tmux             TmuxRepository
	providers        map[string]ProviderRepository
	runtimeMonitor   RuntimeMonitor
	runtimeDetectors map[string]RuntimeStateDetector
	repoConfig       RepoConfigRepository
	workspace        WorkspaceSeeder
	clock            timeutil.Clock
	cfg              Config
}

func NewService(
	tasks TaskRepository,
	git GitRepository,
	tmux TmuxRepository,
	providers map[string]ProviderRepository,
	runtimeMonitor RuntimeMonitor,
	runtimeDetectors map[string]RuntimeStateDetector,
	repoConfig RepoConfigRepository,
	workspace WorkspaceSeeder,
	clock timeutil.Clock,
	cfg Config,
) *Service {
	return &Service{
		tasks:            tasks,
		git:              git,
		tmux:             tmux,
		providers:        providers,
		runtimeMonitor:   runtimeMonitor,
		runtimeDetectors: runtimeDetectors,
		repoConfig:       repoConfig,
		workspace:        workspace,
		clock:            clock,
		cfg:              cfg,
	}
}
```

- [ ] **Step 6: Add runtime enrichment helper in `service.go`**

Add this helper near `reconcileTask` in `internal/core/service.go`:

```go
func (s *Service) enrichRuntimeState(ctx context.Context, task *Task) error {
	task.RuntimeState = RuntimeStateNone

	if task == nil || s.runtimeMonitor == nil {
		return nil
	}
	if !task.SessionExists || !task.AgentWindowExists {
		return nil
	}
	detector, ok := s.runtimeDetectors[task.Provider]
	if !ok || detector == nil {
		return nil
	}

	snapshot, err := s.runtimeMonitor.Snapshot(ctx, task)
	if err != nil {
		return err
	}

	task.RuntimeState = detector.Detect(snapshot)
	task.RuntimeStateUpdatedAt = s.clock.Now().UTC()
	return nil
}
```

Then update `ListTasks` and `GetTask` to call it after structural reconciliation:

```go
if err := s.enrichRuntimeState(ctx, nextTask); err != nil {
	return nil, err
}
```

and

```go
if err := s.enrichRuntimeState(ctx, task); err != nil {
	return nil, err
}
```

- [ ] **Step 7: Add fake runtime dependencies to the test harness**

Update `internal/core/fakes_test.go`:

```go
type testServiceHarness struct {
	service         *Service
	taskRepo        *fakeTaskRepository
	gitRepo         *fakeGitRepository
	tmuxRepo        *fakeTmuxRepository
	providerRepo    *fakeProviderRepository
	runtimeMonitor  *fakeRuntimeMonitor
	runtimeDetector *fakeRuntimeStateDetector
	configRepo      *fakeRepoConfigRepository
	workspaceSeeder *fakeWorkspaceSeeder
}

type fakeRuntimeMonitor struct {
	snapshot RuntimeSnapshot
	err      error
}

func (f *fakeRuntimeMonitor) Snapshot(context.Context, *Task) (RuntimeSnapshot, error) {
	return f.snapshot, f.err
}

func (*fakeRuntimeMonitor) Close() error { return nil }

type fakeRuntimeStateDetector struct {
	state RuntimeState
}

func (f *fakeRuntimeStateDetector) Detect(RuntimeSnapshot) RuntimeState {
	return f.state
}
```

Update `newTestService()` to wire them into `NewService`:

```go
runtimeMonitor := &fakeRuntimeMonitor{}
runtimeDetector := &fakeRuntimeStateDetector{}

service: NewService(
	taskRepo,
	gitRepo,
	tmuxRepo,
	map[string]ProviderRepository{"codex": providerRepo},
	runtimeMonitor,
	map[string]RuntimeStateDetector{"codex": runtimeDetector},
	configRepo,
	workspaceSeeder,
	fakeClock{now: time.Date(2026, 4, 2, 12, 0, 0, 0, time.UTC)},
	Config{DatabasePath: "/tmp/agent/state.db", Provider: "codex"},
),
```

- [ ] **Step 8: Add unsupported-provider coverage**

Add this test to `internal/core/service_status_test.go`:

```go
func TestServiceGetTask_LeavesRuntimeStateEmptyForUnsupportedProvider(t *testing.T) {
	worktree := t.TempDir()
	svc := newTestService()
	svc.taskRepo.getTask = &Task{
		ID:               "task-1",
		Slug:             "billing-retry-flow",
		RepoRoot:         "/tmp/repo",
		BranchName:       "feat/billing-retry-flow",
		WorktreePath:     worktree,
		TmuxSession:      "repo-billing-retry-flow",
		AgentWindowName:  "agent",
		EditorWindowName: "editor",
		Provider:         "claude",
		Status:           TaskStatusRunning,
	}
	svc.gitRepo.branchExists = true
	svc.tmuxRepo.sessionExists = true
	svc.tmuxRepo.windowExists = map[string]map[string]bool{
		"repo-billing-retry-flow": {
			"agent":  true,
			"editor": true,
		},
	}

	task, err := svc.service.GetTask(t.Context(), "billing-retry-flow")
	require.NoError(t, err)
	require.Equal(t, RuntimeStateNone, task.RuntimeState)
}
```

- [ ] **Step 9: Run core tests to verify green**

Run: `go test ./internal/core/... -count=1`

Expected: PASS

- [ ] **Step 10: Commit**

```bash
git add internal/core/runtime.go internal/core/task.go internal/core/service.go internal/core/fakes_test.go internal/core/service_list_test.go internal/core/service_status_test.go
git commit -m "feat: add core runtime state wiring"
```

---

### Task 2: Implement The Shared Tmux Runtime Monitor

**Files:**
- Create: `internal/adapters/repository/tmux/control_pipe.go`
- Create: `internal/adapters/repository/tmux/runtime_monitor.go`
- Create: `internal/adapters/repository/tmux/runtime_monitor_test.go`

- [ ] **Step 1: Write the failing pane-binding test**

Create `internal/adapters/repository/tmux/runtime_monitor_test.go` with this first test:

```go
func TestRuntimeMonitorSnapshot_BindsOnlyCodexPaneInSplitAgentWindow(t *testing.T) {
	pipe := &fakeControlPipe{
		output: map[string]string{
			"list-panes -t =repo-billing-retry-flow:agent -F #{pane_id}\t#{pane_current_command}": "%24\tcodex\n%31\tzsh",
			"capture-pane -t %24 -p -e": "› review current changes\n  gpt-5.4 high · 82% left",
		},
	}
	monitor := NewRuntimeMonitorWithFactory(&fakeControlPipeFactory{
		pipes: map[string]controlPipe{"repo-billing-retry-flow": pipe},
	}, func() time.Time {
		return time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC)
	})

	snapshot, err := monitor.Snapshot(context.Background(), &core.Task{
		TmuxSession:     "repo-billing-retry-flow",
		AgentWindowName: "agent",
	})
	require.NoError(t, err)
	require.Equal(t, "%24", snapshot.PaneID)
	require.Equal(t, "codex", snapshot.ForegroundCommand)
}
```

- [ ] **Step 2: Run the failing tmux monitor test**

Run: `go test ./internal/adapters/repository/tmux/... -run TestRuntimeMonitorSnapshot_BindsOnlyCodexPaneInSplitAgentWindow -count=1`

Expected: FAIL with compile errors because `RuntimeMonitor`, `controlPipe`, or `NewRuntimeMonitorWithFactory` do not exist yet.

- [ ] **Step 3: Implement the control pipe abstraction**

Create `internal/adapters/repository/tmux/control_pipe.go`:

```go
package tmux

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

type controlPipe interface {
	SendCommand(command string) (string, error)
	LastOutputAt() time.Time
	Close() error
}

type execControlPipe struct {
	sessionName  string
	cmd          *exec.Cmd
	stdin        io.WriteCloser
	stdout       io.ReadCloser
	responseCh   chan string
	errCh        chan error
	mu           sync.RWMutex
	lastOutputAt time.Time
}
```

Add these two key methods in the same file:

```go
func (p *execControlPipe) SendCommand(command string) (string, error) {
	if _, err := fmt.Fprintln(p.stdin, command); err != nil {
		return "", err
	}

	select {
	case output := <-p.responseCh:
		return output, nil
	case err := <-p.errCh:
		return "", err
	case <-time.After(5 * time.Second):
		return "", fmt.Errorf("tmux control command timed out: %s", command)
	}
}

func (p *execControlPipe) LastOutputAt() time.Time {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.lastOutputAt
}
```

Use a scanner goroutine that:

- updates `lastOutputAt` on `%output`
- collects `%begin` / `%end` command responses
- ignores unrelated control lines

- [ ] **Step 4: Implement the runtime monitor**

Create `internal/adapters/repository/tmux/runtime_monitor.go`:

```go
package tmux

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"agent/internal/core"
)

type RuntimeMonitor struct {
	factory    controlPipeFactory
	now        func() time.Time
	mu         sync.Mutex
	pipes      map[string]controlPipe
	boundPanes map[string]string
}

type controlPipeFactory interface {
	Attach(session string) (controlPipe, error)
}

func NewRuntimeMonitor() *RuntimeMonitor {
	return NewRuntimeMonitorWithFactory(execControlPipeFactory{}, time.Now)
}

func NewRuntimeMonitorWithFactory(factory controlPipeFactory, now func() time.Time) *RuntimeMonitor {
	return &RuntimeMonitor{
		factory:    factory,
		now:        now,
		pipes:      make(map[string]controlPipe),
		boundPanes: make(map[string]string),
	}
}
```

Implement `Snapshot` like this:

```go
func (m *RuntimeMonitor) Snapshot(ctx context.Context, task *core.Task) (core.RuntimeSnapshot, error) {
	if task == nil {
		return core.RuntimeSnapshot{}, nil
	}

	pipe, err := m.pipeForSession(task.TmuxSession)
	if err != nil {
		return core.RuntimeSnapshot{}, err
	}

	paneID, command, err := m.resolvePaneBinding(task, pipe)
	if err != nil || paneID == "" {
		return core.RuntimeSnapshot{}, err
	}

	content, err := pipe.SendCommand(fmt.Sprintf("capture-pane -t %s -p -e", paneID))
	if err != nil {
		return core.RuntimeSnapshot{}, err
	}

	return core.RuntimeSnapshot{
		SessionName:       task.TmuxSession,
		WindowName:        task.AgentWindowName,
		PaneID:            paneID,
		ForegroundCommand: command,
		Content:           content,
		ObservedAt:        m.now().UTC(),
		LastOutputAt:      pipe.LastOutputAt().UTC(),
	}, nil
}
```

- [ ] **Step 5: Implement pane binding logic**

Add this helper in `runtime_monitor.go`:

```go
func (m *RuntimeMonitor) resolvePaneBinding(task *core.Task, pipe controlPipe) (string, string, error) {
	sessionKey := task.TmuxSession
	if bound := m.boundPanes[sessionKey]; bound != "" {
		commandOutput, err := pipe.SendCommand(
			fmt.Sprintf("list-panes -t %s:%s -F #{pane_id}\t#{pane_current_command}", exactSessionTarget(task.TmuxSession), windowOrDefault(task.AgentWindowName, "agent")),
		)
		if err != nil {
			return "", "", err
		}
		for _, line := range strings.Split(strings.TrimSpace(commandOutput), "\n") {
			parts := strings.Split(line, "\t")
			if len(parts) == 2 && parts[0] == bound {
				return parts[0], normalizeForegroundCommand(parts[1]), nil
			}
		}
		delete(m.boundPanes, sessionKey)
	}

	output, err := pipe.SendCommand(
		fmt.Sprintf("list-panes -t %s:%s -F #{pane_id}\t#{pane_current_command}", exactSessionTarget(task.TmuxSession), windowOrDefault(task.AgentWindowName, "agent")),
	)
	if err != nil {
		return "", "", err
	}

	type paneInfo struct{ id, command string }
	var panes []paneInfo
	var codexPanes []paneInfo
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		parts := strings.Split(line, "\t")
		if len(parts) != 2 {
			continue
		}
		info := paneInfo{id: parts[0], command: normalizeForegroundCommand(parts[1])}
		panes = append(panes, info)
		if info.command == "codex" {
			codexPanes = append(codexPanes, info)
		}
	}

	switch {
	case len(codexPanes) == 1:
		m.boundPanes[sessionKey] = codexPanes[0].id
		return codexPanes[0].id, codexPanes[0].command, nil
	case len(panes) == 1:
		m.boundPanes[sessionKey] = panes[0].id
		return panes[0].id, panes[0].command, nil
	default:
		return "", "", nil
	}
}
```

- [ ] **Step 6: Add monitor close behavior**

Add this method to `runtime_monitor.go`:

```go
func (m *RuntimeMonitor) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for session, pipe := range m.pipes {
		_ = pipe.Close()
		delete(m.pipes, session)
		delete(m.boundPanes, session)
	}

	return nil
}
```

- [ ] **Step 7: Add the ambiguity and shell tests**

Add these tests to `internal/adapters/repository/tmux/runtime_monitor_test.go`:

```go
func TestRuntimeMonitorSnapshot_ReturnsEmptyWhenMultipleCodexPanesExist(t *testing.T) {
	pipe := &fakeControlPipe{
		output: map[string]string{
			"list-panes -t =repo-billing-retry-flow:agent -F #{pane_id}\t#{pane_current_command}": "%24\tcodex\n%31\tcodex",
		},
	}
	monitor := NewRuntimeMonitorWithFactory(&fakeControlPipeFactory{
		pipes: map[string]controlPipe{"repo-billing-retry-flow": pipe},
	}, time.Now)

	snapshot, err := monitor.Snapshot(context.Background(), &core.Task{
		TmuxSession:     "repo-billing-retry-flow",
		AgentWindowName: "agent",
	})
	require.NoError(t, err)
	require.Equal(t, "", snapshot.PaneID)
}

func TestRuntimeMonitorSnapshot_ReusesBoundPaneAfterCodexReturnsToShell(t *testing.T) {
	pipe := &fakeControlPipe{
		output: map[string]string{
			"list-panes -t =repo-billing-retry-flow:agent -F #{pane_id}\t#{pane_current_command}": "%24\tcodex",
			"capture-pane -t %24 -p -e": "done\n",
		},
	}
	monitor := NewRuntimeMonitorWithFactory(&fakeControlPipeFactory{
		pipes: map[string]controlPipe{"repo-billing-retry-flow": pipe},
	}, time.Now)

	_, err := monitor.Snapshot(context.Background(), &core.Task{TmuxSession: "repo-billing-retry-flow", AgentWindowName: "agent"})
	require.NoError(t, err)

	pipe.output["list-panes -t =repo-billing-retry-flow:agent -F #{pane_id}\t#{pane_current_command}"] = "%24\tzsh"
	snapshot, err := monitor.Snapshot(context.Background(), &core.Task{TmuxSession: "repo-billing-retry-flow", AgentWindowName: "agent"})
	require.NoError(t, err)
	require.Equal(t, "%24", snapshot.PaneID)
	require.Equal(t, "zsh", snapshot.ForegroundCommand)
}
```

- [ ] **Step 8: Run tmux repository tests**

Run: `go test ./internal/adapters/repository/tmux/... -count=1`

Expected: PASS

- [ ] **Step 9: Commit**

```bash
git add internal/adapters/repository/tmux/control_pipe.go internal/adapters/repository/tmux/runtime_monitor.go internal/adapters/repository/tmux/runtime_monitor_test.go
git commit -m "feat: add tmux runtime monitor"
```

---

### Task 3: Add The Codex Runtime Detector And RuntimeService Ownership

**Files:**
- Create: `internal/adapters/repository/codex/runtime_detector.go`
- Create: `internal/adapters/repository/codex/runtime_detector_test.go`
- Modify: `cmd/agent/main.go`
- Modify: `cmd/agent/main_test.go`

- [ ] **Step 1: Write the failing Codex detector test**

Create `internal/adapters/repository/codex/runtime_detector_test.go` with:

```go
func TestRuntimeDetector_Detect_PrefersBusyMarkerOverPromptMarker(t *testing.T) {
	detector := NewRuntimeDetector(2 * time.Second)

	state := detector.Detect(core.RuntimeSnapshot{
		ForegroundCommand: "codex",
		Content: "› review current changes\nWorking (26s • esc to interrupt)",
		ObservedAt: time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC),
		LastOutputAt: time.Date(2026, 4, 5, 9, 59, 59, 0, time.UTC),
	})

	require.Equal(t, core.RuntimeStateRunning, state)
}
```

- [ ] **Step 2: Run the failing Codex detector test**

Run: `go test ./internal/adapters/repository/codex/... -run TestRuntimeDetector_Detect_PrefersBusyMarkerOverPromptMarker -count=1`

Expected: FAIL because `NewRuntimeDetector` does not exist.

- [ ] **Step 3: Implement the pure Codex detector**

Create `internal/adapters/repository/codex/runtime_detector.go`:

```go
package codex

import (
	"strings"
	"time"

	"agent/internal/core"
)

type RuntimeDetector struct {
	activityWindow time.Duration
}

func NewRuntimeDetector(activityWindow time.Duration) *RuntimeDetector {
	return &RuntimeDetector{activityWindow: activityWindow}
}

func (d *RuntimeDetector) Detect(snapshot core.RuntimeSnapshot) core.RuntimeState {
	command := normalizeForegroundCommand(snapshot.ForegroundCommand)
	content := strings.ToLower(snapshot.Content)

	if command == "zsh" || command == "bash" || command == "fish" {
		if strings.TrimSpace(snapshot.PaneID) != "" {
			return core.RuntimeStateFinished
		}
		return core.RuntimeStateNone
	}

	if command != "codex" {
		return core.RuntimeStateNone
	}

	if hasCodexBusyMarker(content) || snapshot.ObservedAt.Sub(snapshot.LastOutputAt) <= d.activityWindow {
		return core.RuntimeStateRunning
	}
	if hasCodexPromptMarker(snapshot.Content) {
		return core.RuntimeStateNeedsInput
	}

	return core.RuntimeStateNone
}
```

Add these helpers in the same file:

```go
func hasCodexBusyMarker(content string) bool {
	return strings.Contains(content, "esc to interrupt") ||
		strings.Contains(content, "ctrl+c to interrupt") ||
		strings.Contains(content, "working (")
}

func hasCodexPromptMarker(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "›" || strings.HasPrefix(trimmed, "› ") {
			return true
		}
		if strings.Contains(trimmed, "Continue?") {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Add detector coverage for prompt and shell return**

Add these tests to `internal/adapters/repository/codex/runtime_detector_test.go`:

```go
func TestRuntimeDetector_Detect_ReturnsNeedsInputForPromptWithoutRecentOutput(t *testing.T) {
	detector := NewRuntimeDetector(2 * time.Second)

	state := detector.Detect(core.RuntimeSnapshot{
		PaneID:            "%24",
		ForegroundCommand: "codex",
		Content:           "› review current changes\n  gpt-5.4 high · 82% left",
		ObservedAt:        time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC),
		LastOutputAt:      time.Date(2026, 4, 5, 9, 59, 50, 0, time.UTC),
	})

	require.Equal(t, core.RuntimeStateNeedsInput, state)
}

func TestRuntimeDetector_Detect_ReturnsFinishedWhenPaneReturnsToShell(t *testing.T) {
	detector := NewRuntimeDetector(2 * time.Second)

	state := detector.Detect(core.RuntimeSnapshot{
		PaneID:            "%24",
		ForegroundCommand: "zsh",
		Content:           "git status\n",
	})

	require.Equal(t, core.RuntimeStateFinished, state)
}
```

- [ ] **Step 5: Make `runtimeService` own the shared monitor**

Update `cmd/agent/main.go`:

```go
type runtimeService struct {
	runner         execx.ExecRunner
	cfg            core.Config
	runtimeMonitor core.RuntimeMonitor
}
```

In `buildDependencies()`:

```go
service := &runtimeService{
	cfg:            cfg,
	runner:         execx.ExecRunner{},
	runtimeMonitor: tmuxrepo.NewRuntimeMonitor(),
}
```

In `newService()`:

```go
return core.NewService(
	taskRepo,
	gitrepo.NewRepository(r.runner),
	tmuxrepo.NewRepository(r.runner),
	providers,
	r.runtimeMonitor,
	map[string]core.RuntimeStateDetector{
		"codex": codexrepo.NewRuntimeDetector(2 * time.Second),
	},
	agentconfigrepo.NewRepository(),
	workspacerepo.NewRepository(),
	timeutil.RealClock{},
	r.cfg,
), nil
```

- [ ] **Step 6: Update the main service construction test**

Update `cmd/agent/main_test.go`:

```go
func TestRuntimeService_NewServiceConstructs(t *testing.T) {
	svc := &runtimeService{
		cfg:            core.DefaultConfig(),
		runner:         execx.ExecRunner{},
		runtimeMonitor: tmuxrepo.NewRuntimeMonitor(),
	}

	service, err := svc.newService(false)
	require.NoError(t, err)
	require.NotNil(t, service)
}
```

- [ ] **Step 7: Run Codex and main-package tests**

Run: `go test ./internal/adapters/repository/codex/... ./cmd/agent/... -count=1`

Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add internal/adapters/repository/codex/runtime_detector.go internal/adapters/repository/codex/runtime_detector_test.go cmd/agent/main.go cmd/agent/main_test.go
git commit -m "feat: add codex runtime detection"
```

---

### Task 4: Render Runtime Badges In The TUI

**Files:**
- Modify: `internal/adapters/handler/cli/tui_style.go`
- Modify: `internal/adapters/handler/cli/tui_model.go`
- Modify: `internal/adapters/handler/cli/tui_model_test.go`

- [ ] **Step 1: Write the failing TUI row-rendering test**

Add this test to `internal/adapters/handler/cli/tui_model_test.go`:

```go
func TestModelView_ListShowsRuntimeBadgePerTaskRow(t *testing.T) {
	running := tuiTask("running-task")
	running.RuntimeState = core.RuntimeStateRunning

	waiting := tuiTask("waiting-task")
	waiting.RuntimeState = core.RuntimeStateNeedsInput

	finished := tuiTask("finished-task")
	finished.RuntimeState = core.RuntimeStateFinished

	m := newLoadedTUIModel(t, &fakeTUIService{}, running, waiting, finished)

	view := stripANSI(m.View())
	require.Contains(t, view, "running-task")
	require.Contains(t, view, "● running")
	require.Contains(t, view, "waiting-task")
	require.Contains(t, view, "◐ needs input")
	require.Contains(t, view, "finished-task")
	require.Contains(t, view, "○ finished")
}
```

- [ ] **Step 2: Run the failing TUI test**

Run: `go test ./internal/adapters/handler/cli/... -run TestModelView_ListShowsRuntimeBadgePerTaskRow -count=1`

Expected: FAIL because the view does not yet render runtime badges.

- [ ] **Step 3: Add runtime badge styling helper**

Update `internal/adapters/handler/cli/tui_style.go`:

```go
func runtimeStateStyle(state core.RuntimeState) (string, lipgloss.Style, string) {
	switch state {
	case core.RuntimeStateRunning:
		return iconStatusActive, healthyStyle, "running"
	case core.RuntimeStateNeedsInput:
		return iconStatusProgress, warningStyle, "needs input"
	case core.RuntimeStateFinished:
		return iconStatusIdle, dimStyle, "finished"
	default:
		return "", dimStyle, ""
	}
}
```

Add the import:

```go
import (
	"agent/internal/core"
	"github.com/charmbracelet/lipgloss"
)
```

- [ ] **Step 4: Render the runtime badge in each list row**

Update the task-row loop in `internal/adapters/handler/cli/tui_model.go`:

```go
for i, task := range m.tasks {
	icon, style := statusStyle(string(task.Status))
	status := style.Render(icon + " " + string(task.Status))

	runtimeIcon, runtimeStyle, runtimeLabel := runtimeStateStyle(task.RuntimeState)
	runtime := ""
	if runtimeLabel != "" {
		runtime = runtimeStyle.Render(runtimeIcon + " " + runtimeLabel)
	}

	namePrefix := "  "
	if i == m.selected {
		namePrefix = iconSelected + " "
	}

	name := namePrefix + task.DisplayName
	row := fmt.Sprintf("%-28s %-18s %s", name, status, runtime)
	if i == m.selected {
		b.WriteString(selectedRowStyle.Render(row) + "\n")
	} else {
		b.WriteString(normalRowStyle.Render(row) + "\n")
	}
}
```

- [ ] **Step 5: Add a no-badge regression test**

Add this test to `internal/adapters/handler/cli/tui_model_test.go`:

```go
func TestModelView_ListOmitsRuntimeBadgeWhenRuntimeStateEmpty(t *testing.T) {
	task := tuiTask("plain-task")
	task.RuntimeState = core.RuntimeStateNone

	m := newLoadedTUIModel(t, &fakeTUIService{}, task)

	view := stripANSI(m.View())
	require.Contains(t, view, "plain-task")
	require.NotContains(t, view, "needs input")
	require.NotContains(t, view, "finished")
}
```

- [ ] **Step 6: Run the CLI handler tests**

Run: `go test ./internal/adapters/handler/cli/... -count=1`

Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/adapters/handler/cli/tui_style.go internal/adapters/handler/cli/tui_model.go internal/adapters/handler/cli/tui_model_test.go
git commit -m "feat: show runtime state badges in tui"
```

---

### Task 5: Final Verification

**Files:**
- Modify: none

- [ ] **Step 1: Run the focused package test suite**

Run: `go test ./internal/core/... ./internal/adapters/repository/tmux/... ./internal/adapters/repository/codex/... ./internal/adapters/handler/cli/... ./cmd/agent/... -count=1`

Expected: PASS

- [ ] **Step 2: Run the full test suite**

Run: `go test ./... -count=1`

Expected: PASS

- [ ] **Step 3: Review git diff**

Run: `git diff --stat && git status --short`

Expected: only the planned runtime-state files appear as modified; no unrelated files are touched.

- [ ] **Step 4: Commit any final verification-only fixes if step 1 or 2 required code changes**

```bash
git add internal/core internal/adapters/repository/tmux internal/adapters/repository/codex internal/adapters/handler/cli cmd/agent
git commit -m "test: finalize runtime session state verification"
```

- [ ] **Step 5: Confirm the worktree is clean**

Run: `git status --short`

Expected: no output

---

## Self-Review

- Spec coverage: covered core/runtime separation, persistent tmux monitor ownership, stable pane binding, Codex-first detection, empty badge for unsupported providers, TUI row badges, and tests for structural/runtime separation.
- Placeholder scan: no `TODO`, `TBD`, or “handle later” language remains in the task steps.
- Type consistency: the plan uses `RuntimeState`, `RuntimeSnapshot`, `RuntimeMonitor`, and `RuntimeStateDetector` consistently across core, tmux, Codex, and CLI layers.
