# Control Center TUI Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Redesign the control center TUI to reduce information overload — cleaner task list rows, two-column detail panel with Git and Session sections, PR status via GitHub API, Nerd Font icons with Unicode fallbacks.

**Architecture:** The TUI view layer (`tui_model.go`, `tui_style.go`) is rewritten. A new `PRStatusChecker` port and GitHub adapter provide PR status with 1-minute TTL caching. The `TaskView` domain type gains a `PRStatus` field. Hook event display is removed from the UI but ingestion continues unchanged.

**Tech Stack:** Go, Bubble Tea v2, Lipgloss v2, `gh` CLI for GitHub API, mockery for mock generation.

---

### Task 1: Add Nerd Font Icon System

**Files:**
- Modify: `internal/adapters/handler/cli/tui_style.go`
- Test: `internal/adapters/handler/cli/tui_style_test.go` (new)

- [ ] **Step 1: Write the failing test**

Create `internal/adapters/handler/cli/tui_style_test.go`:

```go
package cli

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIconSet_NerdFontReturnsNerdGlyphs(t *testing.T) {
	icons := nerdFontIcons()
	require.Equal(t, "\uE725", icons.Branch)
	require.Equal(t, "\uF401", icons.Repo)
	require.Equal(t, "\uE726", icons.PROpen)
	require.Equal(t, "\uE727", icons.PRMerged)
	require.Equal(t, "\uF017", icons.Time)
	require.Equal(t, "\uF1E6", icons.Process)
	require.Equal(t, "\uF007", icons.Prompt)
	require.Equal(t, "\U000F06A9", icons.LLMOutput)
}

func TestIconSet_UnicodeFallbackReturnsEmoji(t *testing.T) {
	icons := unicodeFallbackIcons()
	require.Equal(t, "🌿", icons.Branch)
	require.Equal(t, "📁", icons.Repo)
	require.Equal(t, "◉", icons.PROpen)
	require.Equal(t, "✔", icons.PRMerged)
	require.Equal(t, "🕐", icons.Time)
	require.Equal(t, "🔌", icons.Process)
	require.Equal(t, "👤", icons.Prompt)
	require.Equal(t, "🤖", icons.LLMOutput)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/ericbonet/software/tmux-llm-v1-refactor-brainstorm && go test ./internal/adapters/handler/cli/ -run TestIconSet -v`
Expected: FAIL — `nerdFontIcons` and `unicodeFallbackIcons` not defined.

- [ ] **Step 3: Write minimal implementation**

Add to `internal/adapters/handler/cli/tui_style.go`, replacing the existing icon constants block:

```go
// IconSet holds all icons used in the TUI. Two sets are available:
// Nerd Font (primary) and Unicode fallback.
type IconSet struct {
	Branch   string
	Repo     string
	PROpen   string
	PRMerged string
	Time     string
	Process  string
	Prompt   string
	LLMOutput string
}

func nerdFontIcons() IconSet {
	return IconSet{
		Branch:    "\uE725", // nf-dev-git_branch
		Repo:      "\uF401", // nf-oct-repo
		PROpen:    "\uE726", // nf-dev-git_pull_request
		PRMerged:  "\uE727", // nf-dev-git_merge
		Time:      "\uF017", // nf-fa-clock_o
		Process:   "\uF1E6", // nf-fa-plug
		Prompt:    "\uF007", // nf-fa-user
		LLMOutput: "\U000F06A9", // nf-md-robot
	}
}

func unicodeFallbackIcons() IconSet {
	return IconSet{
		Branch:    "🌿",
		Repo:      "📁",
		PROpen:    "◉",
		PRMerged:  "✔",
		Time:      "🕐",
		Process:   "🔌",
		Prompt:    "👤",
		LLMOutput: "🤖",
	}
}

// activeIcons returns the icon set to use. Defaults to Nerd Font.
// Call with useNerdFont=false to get Unicode fallback.
func activeIcons(useNerdFont bool) IconSet {
	if useNerdFont {
		return nerdFontIcons()
	}
	return unicodeFallbackIcons()
}
```

Keep the existing icon constants (`iconStatusActive`, `iconStatusIdle`, `iconStatusProgress`, `iconSelected`, `iconHeaderList`, `iconHeaderCreate`, `iconHeaderCleanup`, `iconProviderCodex`, `iconProviderClaude`) — they are still used.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/ericbonet/software/tmux-llm-v1-refactor-brainstorm && go test ./internal/adapters/handler/cli/ -run TestIconSet -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/handler/cli/tui_style.go internal/adapters/handler/cli/tui_style_test.go
git commit -m "feat: add Nerd Font icon system with Unicode fallback"
```

---

### Task 2: Add PR Status Domain Types and Port

**Files:**
- Modify: `internal/core/domain.go`
- Modify: `internal/core/ports.go`
- Modify: `.mockery.yaml`

- [ ] **Step 1: Add PRStatus type to domain.go**

Add to `internal/core/domain.go` after the `DisplayActivity` constants:

```go
type PRState string

const (
	PRStateNone   PRState = ""
	PRStateOpen   PRState = "open"
	PRStateMerged PRState = "merged"
)

type PRStatus struct {
	State  PRState
	Number int
}
```

- [ ] **Step 2: Add PRStatus field to TaskView**

In `internal/core/domain.go`, modify the `TaskView` struct:

```go
type TaskView struct {
	Task        *Task
	HookSession *HookSessionSummary
	Observer    *ObserverSummary
	PR          *PRStatus
}
```

- [ ] **Step 3: Add PRStatusChecker port**

Add to `internal/core/ports.go`:

```go
type PRStatusChecker interface {
	CheckPRStatus(ctx context.Context, repoRoot string, branchName string) (*PRStatus, error)
}
```

- [ ] **Step 4: Add PRStatusChecker to mockery config**

In `.mockery.yaml`, add under `agent/internal/core` interfaces:

```yaml
      PRStatusChecker:
```

- [ ] **Step 5: Generate mock**

Run: `cd /Users/ericbonet/software/tmux-llm-v1-refactor-brainstorm && mockery`
Expected: Generates `internal/core/mock_pr_status_checker.go`

- [ ] **Step 6: Verify compilation**

Run: `cd /Users/ericbonet/software/tmux-llm-v1-refactor-brainstorm && go build ./...`
Expected: Compiles without errors.

- [ ] **Step 7: Commit**

```bash
git add internal/core/domain.go internal/core/ports.go .mockery.yaml internal/core/mock_pr_status_checker.go
git commit -m "feat: add PRStatus domain type and PRStatusChecker port"
```

---

### Task 3: Implement GitHub PR Status Adapter

**Files:**
- Create: `internal/adapters/client/github/pr_status.go`
- Create: `internal/adapters/client/github/pr_status_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/adapters/client/github/pr_status_test.go`:

```go
package github

import (
	"context"
	"testing"

	"agent/internal/core"
	"agent/internal/pkg/execx"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestGHPRChecker_ReturnsPROpen(t *testing.T) {
	runner := execx.NewMockRunner(t)
	runner.EXPECT().
		Run(mock.Anything, "/tmp/repo", "gh", "pr", "view", "--head", "feat/auth", "--json", "number,state", "--jq", ".number,.state").
		Return(execx.Result{Stdout: "42\nOPEN\n"}, nil).
		Once()

	checker := NewPRStatusChecker(runner)
	status, err := checker.CheckPRStatus(context.Background(), "/tmp/repo", "feat/auth")

	require.NoError(t, err)
	require.Equal(t, core.PRStateOpen, status.State)
	require.Equal(t, 42, status.Number)
}

func TestGHPRChecker_ReturnsPRMerged(t *testing.T) {
	runner := execx.NewMockRunner(t)
	runner.EXPECT().
		Run(mock.Anything, "/tmp/repo", "gh", "pr", "view", "--head", "feat/auth", "--json", "number,state", "--jq", ".number,.state").
		Return(execx.Result{Stdout: "42\nMERGED\n"}, nil).
		Once()

	checker := NewPRStatusChecker(runner)
	status, err := checker.CheckPRStatus(context.Background(), "/tmp/repo", "feat/auth")

	require.NoError(t, err)
	require.Equal(t, core.PRStateMerged, status.State)
	require.Equal(t, 42, status.Number)
}

func TestGHPRChecker_ReturnsNoneWhenNoPR(t *testing.T) {
	runner := execx.NewMockRunner(t)
	runner.EXPECT().
		Run(mock.Anything, "/tmp/repo", "gh", "pr", "view", "--head", "feat/auth", "--json", "number,state", "--jq", ".number,.state").
		Return(execx.Result{Stderr: "no pull requests found"}, &execx.CommandError{Err: context.Canceled}).
		Once()

	checker := NewPRStatusChecker(runner)
	status, err := checker.CheckPRStatus(context.Background(), "/tmp/repo", "feat/auth")

	require.NoError(t, err)
	require.Equal(t, core.PRStateNone, status.State)
	require.Equal(t, 0, status.Number)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/ericbonet/software/tmux-llm-v1-refactor-brainstorm && go test ./internal/adapters/client/github/ -run TestGHPR -v`
Expected: FAIL — package doesn't exist.

- [ ] **Step 3: Write minimal implementation**

Create `internal/adapters/client/github/pr_status.go`:

```go
package github

import (
	"context"
	"strconv"
	"strings"

	"agent/internal/core"
	"agent/internal/pkg/execx"
)

type PRStatusChecker struct {
	runner execx.Runner
}

func NewPRStatusChecker(runner execx.Runner) *PRStatusChecker {
	return &PRStatusChecker{runner: runner}
}

func (c *PRStatusChecker) CheckPRStatus(ctx context.Context, repoRoot string, branchName string) (*core.PRStatus, error) {
	result, err := c.runner.Run(
		ctx, repoRoot,
		"gh", "pr", "view",
		"--head", branchName,
		"--json", "number,state",
		"--jq", ".number,.state",
	)
	if err != nil {
		// gh exits non-zero when no PR exists for the branch.
		return &core.PRStatus{State: core.PRStateNone}, nil
	}

	return parsePROutput(result.Stdout), nil
}

func parsePROutput(output string) *core.PRStatus {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) < 2 {
		return &core.PRStatus{State: core.PRStateNone}
	}

	number, _ := strconv.Atoi(strings.TrimSpace(lines[0]))
	state := strings.TrimSpace(strings.ToLower(lines[1]))

	switch state {
	case "open":
		return &core.PRStatus{State: core.PRStateOpen, Number: number}
	case "merged":
		return &core.PRStatus{State: core.PRStateMerged, Number: number}
	default:
		return &core.PRStatus{State: core.PRStateNone}
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/ericbonet/software/tmux-llm-v1-refactor-brainstorm && go test ./internal/adapters/client/github/ -run TestGHPR -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/client/github/pr_status.go internal/adapters/client/github/pr_status_test.go
git commit -m "feat: add GitHub PR status adapter using gh CLI"
```

---

### Task 4: Add PR Status Caching to Service

**Files:**
- Modify: `internal/core/service.go`
- Create: `internal/core/service_pr_status_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/core/service_pr_status_test.go`:

```go
package core

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestService_GetPRStatus_FetchesAndCaches(t *testing.T) {
	deps := newTestDeps(t)
	prChecker := NewMockPRStatusChecker(t)
	deps.service.prChecker = prChecker

	prChecker.EXPECT().
		CheckPRStatus(mock.Anything, "/tmp/repo", "feat/auth").
		Return(&PRStatus{State: PRStateOpen, Number: 42}, nil).
		Once()

	status1, err := deps.service.GetPRStatus(context.Background(), "/tmp/repo", "feat/auth")
	require.NoError(t, err)
	require.Equal(t, PRStateOpen, status1.State)
	require.Equal(t, 42, status1.Number)

	// Second call should use cache — mock would fail if called again.
	status2, err := deps.service.GetPRStatus(context.Background(), "/tmp/repo", "feat/auth")
	require.NoError(t, err)
	require.Equal(t, PRStateOpen, status2.State)
}

func TestService_GetPRStatus_RefetchesAfterTTL(t *testing.T) {
	deps := newTestDeps(t)
	prChecker := NewMockPRStatusChecker(t)
	deps.service.prChecker = prChecker
	deps.service.prCacheTTL = 10 * time.Millisecond

	prChecker.EXPECT().
		CheckPRStatus(mock.Anything, "/tmp/repo", "feat/auth").
		Return(&PRStatus{State: PRStateOpen, Number: 42}, nil).
		Once()
	prChecker.EXPECT().
		CheckPRStatus(mock.Anything, "/tmp/repo", "feat/auth").
		Return(&PRStatus{State: PRStateMerged, Number: 42}, nil).
		Once()

	_, err := deps.service.GetPRStatus(context.Background(), "/tmp/repo", "feat/auth")
	require.NoError(t, err)

	time.Sleep(15 * time.Millisecond)

	status, err := deps.service.GetPRStatus(context.Background(), "/tmp/repo", "feat/auth")
	require.NoError(t, err)
	require.Equal(t, PRStateMerged, status.State)
}

func TestService_GetPRStatus_ReturnsNoneWhenNoChecker(t *testing.T) {
	deps := newTestDeps(t)
	deps.service.prChecker = nil

	status, err := deps.service.GetPRStatus(context.Background(), "/tmp/repo", "feat/auth")
	require.NoError(t, err)
	require.Equal(t, PRStateNone, status.State)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/ericbonet/software/tmux-llm-v1-refactor-brainstorm && go test ./internal/core/ -run TestService_GetPRStatus -v`
Expected: FAIL — `GetPRStatus`, `prChecker`, `prCacheTTL` not defined.

- [ ] **Step 3: Write minimal implementation**

Add to `internal/core/service.go`. First, add fields to the `Service` struct (you'll need to check the existing struct definition and add these fields):

```go
// Add these fields to the Service struct:
	prChecker  PRStatusChecker
	prCacheTTL time.Duration
	prCache    map[string]prCacheEntry
	prCacheMu  sync.Mutex
```

Add at the top of the file in imports: `"sync"` and `"time"` (if not already imported).

Add the cache entry type and method:

```go
type prCacheEntry struct {
	status    *PRStatus
	fetchedAt time.Time
}

func (s *Service) GetPRStatus(ctx context.Context, repoRoot string, branchName string) (*PRStatus, error) {
	if s.prChecker == nil {
		return &PRStatus{State: PRStateNone}, nil
	}

	key := repoRoot + ":" + branchName
	ttl := s.prCacheTTL
	if ttl == 0 {
		ttl = time.Minute
	}

	s.prCacheMu.Lock()
	if s.prCache == nil {
		s.prCache = make(map[string]prCacheEntry)
	}
	if entry, ok := s.prCache[key]; ok && time.Since(entry.fetchedAt) < ttl {
		s.prCacheMu.Unlock()
		return entry.status, nil
	}
	s.prCacheMu.Unlock()

	status, err := s.prChecker.CheckPRStatus(ctx, repoRoot, branchName)
	if err != nil {
		return &PRStatus{State: PRStateNone}, nil
	}

	s.prCacheMu.Lock()
	s.prCache[key] = prCacheEntry{status: status, fetchedAt: time.Now()}
	s.prCacheMu.Unlock()

	return status, nil
}

func (s *Service) InvalidatePRCache() {
	s.prCacheMu.Lock()
	s.prCache = nil
	s.prCacheMu.Unlock()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/ericbonet/software/tmux-llm-v1-refactor-brainstorm && go test ./internal/core/ -run TestService_GetPRStatus -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/core/service.go internal/core/service_pr_status_test.go
git commit -m "feat: add PR status caching to service with 1-minute TTL"
```

---

### Task 5: Add GetPRStatus to TaskService Interface and Update Mock

**Files:**
- Modify: `internal/adapters/handler/cli/root.go`

- [ ] **Step 1: Add GetPRStatus to the TaskService interface**

In `internal/adapters/handler/cli/root.go`, add to the `TaskService` interface:

```go
	GetPRStatus(ctx context.Context, repoRoot string, branchName string) (*core.PRStatus, error)
	InvalidatePRCache()
```

- [ ] **Step 2: Regenerate mocks**

Run: `cd /Users/ericbonet/software/tmux-llm-v1-refactor-brainstorm && mockery`
Expected: Regenerates `internal/adapters/handler/cli/mock_task_service.go` with new methods.

- [ ] **Step 3: Verify compilation**

Run: `cd /Users/ericbonet/software/tmux-llm-v1-refactor-brainstorm && go build ./...`
Expected: Compiles. (Existing tests may need fixes in next task.)

- [ ] **Step 4: Commit**

```bash
git add internal/adapters/handler/cli/root.go internal/adapters/handler/cli/mock_task_service.go
git commit -m "feat: add GetPRStatus and InvalidatePRCache to TaskService interface"
```

---

### Task 6: Add useNerdFont and icons fields to TUI Model

**Files:**
- Modify: `internal/adapters/handler/cli/tui_model.go`
- Modify: `internal/adapters/handler/cli/root.go`

- [ ] **Step 1: Add fields to model struct**

In `internal/adapters/handler/cli/tui_model.go`, add to the `model` struct:

```go
	icons              IconSet
```

- [ ] **Step 2: Update newTUIModel to accept useNerdFont parameter**

Modify `newTUIModel` signature and body:

```go
func newTUIModel(
	service TaskService,
	defaultCreationCwd string,
	defaultProvider string,
	observerSocketPath string,
	useNerdFont bool,
	initialErr error,
) model {
```

Add inside the function body, in the return statement:

```go
		icons:              activeIcons(useNerdFont),
```

- [ ] **Step 3: Update root.go to pass useNerdFont**

In `internal/adapters/handler/cli/root.go`, update the `newTUIModel` call to pass `true` as the `useNerdFont` argument (default to Nerd Font). Add a `UseNerdFont` field to `Dependencies`:

```go
type Dependencies struct {
	// ... existing fields ...
	UseNerdFont         bool
}
```

Update the call:

```go
newTUIModel(deps.Service, deps.Cwd, deps.DefaultProvider, deps.ObserverSocketPath, deps.UseNerdFont, startupErr),
```

- [ ] **Step 4: Fix test helpers**

In `internal/adapters/handler/cli/tui_model_test.go`, update `newLoadedTUIModelWithProviderAndViews` and `TestModelView_ShowsLoadingBeforeInitialLoadCompletes` to pass the new `useNerdFont` parameter (`false` in tests for simpler assertions):

```go
// In newLoadedTUIModelWithProviderAndViews:
	next, cmd := newTUIModel(
		service,
		"/tmp/default",
		provider,
		"",
		false,
		nil,
	).Update(tasksLoadedMsg{requestID: 1, views: views})

// In TestModelView_ShowsLoadingBeforeInitialLoadCompletes:
	m := newTUIModel(NewMockTaskService(t), "/tmp/default", "codex", "", false, nil)

// In TestListViewShowsInitialError:
	m := newTUIModel(NewMockTaskService(t), "/tmp/default", "codex", "", false, errors.New("observer unavailable"))
```

- [ ] **Step 5: Run tests to verify**

Run: `cd /Users/ericbonet/software/tmux-llm-v1-refactor-brainstorm && go test ./internal/adapters/handler/cli/ -v -count=1`
Expected: All existing tests pass.

- [ ] **Step 6: Commit**

```bash
git add internal/adapters/handler/cli/tui_model.go internal/adapters/handler/cli/tui_model_test.go internal/adapters/handler/cli/root.go
git commit -m "feat: wire icon set into TUI model"
```

---

### Task 7: Rewrite Task List Rows

**Files:**
- Modify: `internal/adapters/handler/cli/tui_model.go`
- Modify: `internal/adapters/handler/cli/tui_model_test.go`

- [ ] **Step 1: Write the failing test for new row format**

Add to `internal/adapters/handler/cli/tui_model_test.go`:

```go
func TestModelView_TaskRowShowsTimeAndPRColumns(t *testing.T) {
	service := NewMockTaskService(t)
	task := tuiTask("auth-rewrite")
	task.DisplayName = "auth-rewrite"
	task.Provider = "codex"
	task.RepoRoot = "/tmp/repo"
	task.BranchName = "feat/auth-rewrite"

	service.EXPECT().
		GetPRStatus(mock.Anything, "/tmp/repo", "feat/auth-rewrite").
		Return(&core.PRStatus{State: core.PRStateOpen, Number: 42}, nil).
		Maybe()

	m := newLoadedTUIModelWithViews(t, service, taskViewWithObserver(task, &core.HookSessionSummary{
		TaskID:    task.ID,
		StartedAt: time.Now().Add(-2*time.Hour - 13*time.Minute),
	}, &core.ObserverSummary{
		TaskID:        task.ID,
		DisplayStatus: core.DisplayStatusWorking,
		ProcessAlive:  true,
	}))

	view := stripANSI(m.View().Content)
	// Should show time column
	require.Contains(t, view, "2h 13m")
	// Should show status
	require.Contains(t, view, "working")
	// Should NOT contain old-style hookPreview inline text
	require.NotContains(t, view, " · ")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/ericbonet/software/tmux-llm-v1-refactor-brainstorm && go test ./internal/adapters/handler/cli/ -run TestModelView_TaskRowShowsTimeAndPRColumns -v`
Expected: FAIL — time column not rendered yet.

- [ ] **Step 3: Add elapsed time helper**

Add to `internal/adapters/handler/cli/tui_model.go`:

```go
func formatElapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

func taskElapsed(view *core.TaskView) string {
	if view == nil {
		return ""
	}
	var started time.Time
	if view.HookSession != nil && !view.HookSession.StartedAt.IsZero() {
		started = view.HookSession.StartedAt
	}
	if started.IsZero() && view.Task != nil {
		started = view.Task.CreatedAt
	}
	if started.IsZero() {
		return ""
	}
	return formatElapsed(time.Since(started))
}
```

- [ ] **Step 4: Rewrite listView task rows**

Replace the task rows loop in `listView()` (the `for i, task := range m.tasks` block) with:

```go
	// Column header
	colHeader := fmt.Sprintf("   %s  %s  %s  %s  %s",
		padRight("TASK", colWidthName),
		padRight("PROVIDER", colWidthProvider),
		padRight("PR", colWidthPR),
		padRight("TIME", colWidthTime),
		padRight("STATUS", colWidthStatus),
	)
	b.WriteString(dimStyle.Render(colHeader) + "\n")

	// Task rows
	for i, task := range m.tasks {
		view := m.taskViewAt(i)
		providerText := providerIcon(task.Provider) + " " + emptyFallback(task.Provider, "-")
		stateText, stateStyle := taskStateText(view)
		elapsed := taskElapsed(view)
		prIcon := m.prIconForTask(view)

		timeText := ""
		if elapsed != "" {
			timeText = m.icons.Time + " " + elapsed
		}

		providerCell := padRight(providerText, colWidthProvider)
		prCell := padRight(prIcon, colWidthPR)
		timeCell := padRight(timeText, colWidthTime)
		stateCell := padRight(stateText, colWidthStatus)

		if i == m.selected {
			nameCell := padRight(truncateStr(iconSelected+" "+task.DisplayName, colWidthName), colWidthName)
			row := nameCell + "  " + primaryStyle.Render(providerCell) + "  " + prCell + "  " + timeCell + "  " + stateStyle.Render(stateCell)
			b.WriteString(selectedRowStyle.Render(row) + "\n")
		} else {
			nameCell := padRight(truncateStr("  "+task.DisplayName, colWidthName), colWidthName)
			row := nameCell + "  " + primaryStyle.Render(providerCell) + "  " + prCell + "  " + timeCell + "  " + stateStyle.Render(stateCell)
			b.WriteString(normalRowStyle.Render(row) + "\n")
		}
	}
```

- [ ] **Step 5: Add new column width constants and prIconForTask**

Update the column width constants:

```go
const (
	colWidthName     = 40
	colWidthProvider = 10
	colWidthPR       = 4
	colWidthTime     = 10
	colWidthStatus   = 18
)
```

Add the PR icon helper:

```go
func (m model) prIconForTask(view *core.TaskView) string {
	if view == nil || view.PR == nil {
		return ""
	}
	switch view.PR.State {
	case core.PRStateOpen:
		return healthyStyle.Render(m.icons.PROpen)
	case core.PRStateMerged:
		return titleStyle.Render(m.icons.PRMerged)
	default:
		return ""
	}
}
```

- [ ] **Step 6: Remove taskPreview, hookPreview, hookEventPreview, firstNonEmpty functions**

Delete the following functions from `tui_model.go` (they are no longer used in the view):
- `taskPreview` (lines 681-687)
- `hookPreview` (lines 689-700)
- `hookEventPreview` (lines 702-712)
- `firstNonEmpty` (lines 714-722)

- [ ] **Step 7: Run all tests**

Run: `cd /Users/ericbonet/software/tmux-llm-v1-refactor-brainstorm && go test ./internal/adapters/handler/cli/ -v -count=1`
Expected: New test passes. Some existing tests that check for old row format (e.g. `TestModelView_TaskRowsUseObserverStatusAndHookPreview`) will need updating in the next step.

- [ ] **Step 8: Update existing tests for new row format**

Update `TestModelView_TaskRowsUseObserverStatusAndHookPreview` — remove assertion for hook preview text in the row, keep the status assertion:

```go
func TestModelView_TaskRowsUseObserverStatusAndHookPreview(t *testing.T) {
	service := NewMockTaskService(t)
	task := tuiTask("billing-retry-flow")
	task.DisplayName = "billing retry flow"

	m := newLoadedTUIModelWithViews(t, service, taskViewWithObserver(task, &core.HookSessionSummary{
		TaskID:          task.ID,
		RuntimePhase:    core.HookRuntimePhaseRunningCommand,
		LastCommandText: "go test ./internal/adapters/handler/cli -count=1",
	}, &core.ObserverSummary{
		TaskID:          task.ID,
		DisplayStatus:   core.DisplayStatusWorking,
		DisplayActivity: core.DisplayActivityCommand,
		ProcessAlive:    true,
	}))
	view := stripANSI(m.View().Content)
	rows := strings.Split(view, "\n")

	for _, row := range rows {
		if !strings.Contains(row, "billing retry flow") {
			continue
		}

		require.Contains(t, row, "working")
		return
	}

	t.Fatalf("did not find row for %q in view:\n%s", "billing retry flow", view)
}
```

Remove the `GetTaskHookEvents` mock expectation since that test no longer triggers hook event loading (the observer view doesn't have a HookSession that would trigger it — wait, it does have one. We'll handle removing hook events in Task 8).

- [ ] **Step 9: Run all tests again**

Run: `cd /Users/ericbonet/software/tmux-llm-v1-refactor-brainstorm && go test ./internal/adapters/handler/cli/ -v -count=1`
Expected: PASS

- [ ] **Step 10: Commit**

```bash
git add internal/adapters/handler/cli/tui_model.go internal/adapters/handler/cli/tui_model_test.go
git commit -m "feat: rewrite task list rows with time and PR columns"
```

---

### Task 8: Remove Hook Event Display and Loading

**Files:**
- Modify: `internal/adapters/handler/cli/tui_model.go`
- Modify: `internal/adapters/handler/cli/tui_model_test.go`

- [ ] **Step 1: Remove hook event fields from model struct**

In `internal/adapters/handler/cli/tui_model.go`, remove from the `model` struct:

```go
	hookEvents         []core.HookEvent
	hookEventsTaskID   string
```

- [ ] **Step 2: Remove hook event message type and loading functions**

Remove:
- `hookEventsLoadedMsg` struct
- `loadTaskHookEventsCmd` function
- `loadSelectedHookEventsCmd` method on model
- `clearHookEvents` method on model

- [ ] **Step 3: Remove hook event handling from Update**

In the `Update` method, remove the `case hookEventsLoadedMsg:` block entirely.

Remove `loadSelectedHookEventsCmd` calls from:
- `tasksLoadedMsg` handler (line ~169: `return m, m.loadSelectedHookEventsCmd()`)
- `observerTaskUpdatedMsg` handler (the `nextCmds = append(nextCmds, m.loadSelectedHookEventsCmd())` line)
- `updateListKey` — the `j`, `k`, `g`, `G` cases that return `m.loadSelectedHookEventsCmd()`

For all navigation keys (`j`, `k`, `g`, `G`), change the return to just `return m, nil`.

For `tasksLoadedMsg`, change the return at the end to `return m, nil`.

For `observerTaskUpdatedMsg`, simplify to:
```go
	case observerTaskUpdatedMsg:
		m.applyObserverTaskUpdate(msg.update)
		return m, waitForObserverUpdateCmd(m.observerUpdates)
```

- [ ] **Step 4: Remove clearHookEvents calls**

Remove `m.clearHookEvents()` calls in:
- `replaceTask` method
- `upsertTask` method
- `tasksLoadedMsg` handler (the `m.clearHookEvents()` when tasks are empty)

- [ ] **Step 5: Remove GetTaskHookEvents from TaskService interface**

In `internal/adapters/handler/cli/root.go`, remove from the `TaskService` interface:

```go
	GetTaskHookEvents(ctx context.Context, taskID string, limit int) ([]core.HookEvent, error)
```

- [ ] **Step 6: Regenerate mocks**

Run: `cd /Users/ericbonet/software/tmux-llm-v1-refactor-brainstorm && mockery`

- [ ] **Step 7: Update all tests**

Remove all `GetTaskHookEvents` mock expectations from tests. Key tests to update:
- `TestModelView_TaskRowsUseObserverStatusAndHookPreview` — remove the `service.EXPECT().GetTaskHookEvents(...)` call
- `TestModelView_SelectedTaskDetailShowsHookMetadataAndRecentEvents` — will be fully rewritten in Task 9
- Any test in `newLoadedTUIModelWithViews` that sets up hook event expectations

- [ ] **Step 8: Verify compilation and tests**

Run: `cd /Users/ericbonet/software/tmux-llm-v1-refactor-brainstorm && go test ./internal/adapters/handler/cli/ -v -count=1`
Expected: PASS (some detail-view tests may fail — they'll be rewritten in Task 9).

- [ ] **Step 9: Commit**

```bash
git add internal/adapters/handler/cli/tui_model.go internal/adapters/handler/cli/tui_model_test.go internal/adapters/handler/cli/root.go internal/adapters/handler/cli/mock_task_service.go
git commit -m "refactor: remove hook event display and loading from TUI"
```

---

### Task 9: Rewrite Detail Panel — Two-Column Layout

**Files:**
- Modify: `internal/adapters/handler/cli/tui_model.go`
- Modify: `internal/adapters/handler/cli/tui_model_test.go`

- [ ] **Step 1: Write the failing test for new detail panel**

Replace `TestModelView_SelectedTaskDetailShowsHookMetadataAndRecentEvents` in `tui_model_test.go`:

```go
func TestModelView_DetailPanelShowsGitAndSessionColumns(t *testing.T) {
	service := NewMockTaskService(t)
	task := tuiTask("auth-rewrite")
	task.DisplayName = "auth rewrite"
	task.Provider = "codex"
	task.RepoName = "tmux-llm"
	task.BranchName = "feat/auth-rewrite"

	summary := &core.HookSessionSummary{
		TaskID:               task.ID,
		StartedAt:            time.Now().Add(-2*time.Hour - 13*time.Minute),
		LastPromptText:       "refactor the token validation to use JWT",
		LastAssistantMessage: "Updated validateToken() to use jwt.Parse",
		LastEventName:        "PostToolUse",
	}
	observerSummary := &core.ObserverSummary{
		TaskID:        task.ID,
		DisplayStatus: core.DisplayStatusWorking,
		ProcessAlive:  true,
	}

	m := newLoadedTUIModelWithViews(t, service,
		taskViewWithObserver(task, summary, observerSummary),
	)
	view := stripANSI(m.View().Content)

	// Git column
	require.Contains(t, view, "Git")
	require.Contains(t, view, "feat/auth-rewrite")
	require.Contains(t, view, "tmux-llm")

	// Session column
	require.Contains(t, view, "Session")
	require.Contains(t, view, "2h 13m")
	require.Contains(t, view, "connected")
	require.Contains(t, view, "refactor the token validation to use JWT")
	require.Contains(t, view, "Updated validateToken() to use jwt.Parse")

	// Removed fields should NOT appear
	require.NotContains(t, view, "Selected Task")
	require.NotContains(t, view, "Session Activity")
	require.NotContains(t, view, "Recent Hook Events")
	require.NotContains(t, view, "Session ID")
	require.NotContains(t, view, "Transcript")
	require.NotContains(t, view, "Start Source")
}

func TestModelView_DetailPanelRecencyHighlightsLatest(t *testing.T) {
	service := NewMockTaskService(t)
	task := tuiTask("auth-rewrite")

	// LastEventName is "PostToolUse" -> LLM output is latest
	summary := &core.HookSessionSummary{
		TaskID:               task.ID,
		StartedAt:            time.Now().Add(-1 * time.Hour),
		LastPromptText:       "the prompt",
		LastAssistantMessage: "the output",
		LastEventName:        "PostToolUse",
	}

	m := newLoadedTUIModelWithViews(t, service,
		taskView(task, summary),
	)
	// We verify recency by checking the model can determine which is latest
	require.True(t, isLLMOutputLatest(summary))

	// UserPromptSubmit -> prompt is latest
	summary2 := &core.HookSessionSummary{
		LastEventName:        "UserPromptSubmit",
		LastPromptText:       "the prompt",
		LastAssistantMessage: "the output",
	}
	require.False(t, isLLMOutputLatest(summary2))
	_ = m // suppress unused
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/ericbonet/software/tmux-llm-v1-refactor-brainstorm && go test ./internal/adapters/handler/cli/ -run "TestModelView_DetailPanel" -v`
Expected: FAIL — new detail panel not implemented yet.

- [ ] **Step 3: Add recency helper**

Add to `internal/adapters/handler/cli/tui_model.go`:

```go
// isLLMOutputLatest returns true when the last hook event was NOT a user prompt,
// meaning the LLM output is the most recent activity.
func isLLMOutputLatest(hook *core.HookSessionSummary) bool {
	if hook == nil {
		return false
	}
	return hook.LastEventName != "UserPromptSubmit"
}
```

- [ ] **Step 4: Rewrite selectedTaskDetailView**

Replace the entire `selectedTaskDetailView` method:

```go
func (m model) selectedTaskDetailView() string {
	task := m.selectedTask()
	if task == nil {
		return ""
	}

	view := m.selectedTaskView()
	var b strings.Builder

	// Git column
	var gitCol strings.Builder
	gitCol.WriteString(titleStyle.Render("Git") + "\n")
	if strings.TrimSpace(task.BranchName) != "" {
		gitCol.WriteString(dimStyle.Render(m.icons.Branch) + " " + truncateStr(task.BranchName, 38) + "\n")
	}
	if strings.TrimSpace(task.RepoName) != "" {
		gitCol.WriteString(dimStyle.Render(m.icons.Repo) + " " + task.RepoName + "\n")
	}
	if view != nil && view.PR != nil && view.PR.State != core.PRStateNone {
		prIcon, prStyle := m.prStatusDisplay(view.PR)
		gitCol.WriteString(prStyle.Render(prIcon+fmt.Sprintf(" #%d %s", view.PR.Number, view.PR.State)) + "\n")
	}

	// Session column
	var sessCol strings.Builder
	sessCol.WriteString(titleStyle.Render("Session") + "\n")
	elapsed := taskElapsed(view)
	if elapsed != "" {
		sessCol.WriteString(dimStyle.Render(m.icons.Time) + " " + elapsed + "\n")
	}
	if view != nil && view.Observer != nil {
		if view.Observer.ProcessAlive {
			sessCol.WriteString(dimStyle.Render(m.icons.Process) + " " + healthyStyle.Render("connected") + "\n")
		} else {
			sessCol.WriteString(dimStyle.Render(m.icons.Process) + " " + dimStyle.Render("disconnected") + "\n")
		}
	}
	if view != nil && view.HookSession != nil {
		hook := view.HookSession
		llmLatest := isLLMOutputLatest(hook)
		promptText := truncateStr(strings.TrimSpace(hook.LastPromptText), 40)
		outputText := truncateStr(strings.TrimSpace(hook.LastAssistantMessage), 40)

		if promptText != "" {
			icon := dimStyle.Render(m.icons.Prompt)
			if llmLatest {
				sessCol.WriteString(icon + " " + dimStyle.Render(promptText) + "\n")
			} else {
				sessCol.WriteString(icon + " " + primaryStyle.Bold(true).Render(promptText) + "\n")
			}
		}
		if outputText != "" {
			icon := dimStyle.Render(m.icons.LLMOutput)
			if llmLatest {
				sessCol.WriteString(icon + " " + primaryStyle.Bold(true).Render(outputText) + "\n")
			} else {
				sessCol.WriteString(icon + " " + dimStyle.Render(outputText) + "\n")
			}
		}
	}

	// Combine two columns side by side
	gitLines := strings.Split(strings.TrimRight(gitCol.String(), "\n"), "\n")
	sessLines := strings.Split(strings.TrimRight(sessCol.String(), "\n"), "\n")
	maxLines := len(gitLines)
	if len(sessLines) > maxLines {
		maxLines = len(sessLines)
	}

	colWidth := 42
	for i := 0; i < maxLines; i++ {
		left := ""
		if i < len(gitLines) {
			left = gitLines[i]
		}
		right := ""
		if i < len(sessLines) {
			right = sessLines[i]
		}
		b.WriteString(padRight(left, colWidth) + right + "\n")
	}

	return strings.TrimRight(b.String(), "\n")
}

func (m model) prStatusDisplay(pr *core.PRStatus) (string, lipgloss.Style) {
	if pr == nil {
		return "", dimStyle
	}
	switch pr.State {
	case core.PRStateOpen:
		return m.icons.PROpen, healthyStyle
	case core.PRStateMerged:
		return m.icons.PRMerged, titleStyle
	default:
		return "", dimStyle
	}
}
```

- [ ] **Step 5: Update fallback test for missing hook data**

Update `TestModelView_SelectedTaskDetailShowsFallbackWhenHookDataMissing`:

```go
func TestModelView_SelectedTaskDetailShowsFallbackWhenHookDataMissing(t *testing.T) {
	service := NewMockTaskService(t)
	task := tuiTask("billing-retry-flow")
	task.DisplayName = "billing retry flow"
	task.Provider = "claude"
	task.RepoName = "tmux-llm"
	task.BranchName = "feat/billing-retry-flow"

	m := newLoadedTUIModel(t, service, task)
	view := stripANSI(m.View().Content)

	require.Contains(t, view, "Git")
	require.Contains(t, view, "Session")
	require.Contains(t, view, "feat/billing-retry-flow")
	require.Contains(t, view, "tmux-llm")
	// No session details since no hook data
	require.NotContains(t, view, "connected")
}
```

- [ ] **Step 6: Run all tests**

Run: `cd /Users/ericbonet/software/tmux-llm-v1-refactor-brainstorm && go test ./internal/adapters/handler/cli/ -v -count=1`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/adapters/handler/cli/tui_model.go internal/adapters/handler/cli/tui_model_test.go
git commit -m "feat: rewrite detail panel with two-column Git/Session layout"
```

---

### Task 10: Wire PR Status Loading into TUI

**Files:**
- Modify: `internal/adapters/handler/cli/tui_model.go`
- Modify: `internal/adapters/handler/cli/tui_model_test.go`

- [ ] **Step 1: Write the failing test**

Add to `tui_model_test.go`:

```go
func TestModelView_PRStatusShownInDetailPanel(t *testing.T) {
	service := NewMockTaskService(t)
	task := tuiTask("auth-rewrite")
	task.RepoName = "tmux-llm"
	task.RepoRoot = "/tmp/repo"
	task.BranchName = "feat/auth-rewrite"

	service.EXPECT().
		GetPRStatus(mock.Anything, "/tmp/repo", "feat/auth-rewrite").
		Return(&core.PRStatus{State: core.PRStateOpen, Number: 42}, nil).
		Once()

	view := &core.TaskView{Task: task}
	m := newLoadedTUIModelWithViews(t, service, view)

	// The tasksLoadedMsg handler fires fetchPRStatusCmd.
	// Simulate the PR status response arriving.
	m, _ = updateTUIModel(t, m, prStatusLoadedMsg{
		taskID: task.ID,
		status: &core.PRStatus{State: core.PRStateOpen, Number: 42},
	})

	rendered := stripANSI(m.View().Content)
	require.Contains(t, rendered, "#42 open")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/ericbonet/software/tmux-llm-v1-refactor-brainstorm && go test ./internal/adapters/handler/cli/ -run TestModelView_PRStatusShownInDetailPanel -v`
Expected: FAIL

- [ ] **Step 3: Add PR status message types and loading**

Add to `tui_model.go`:

```go
type prStatusLoadedMsg struct {
	taskID string
	status *core.PRStatus
}
```

Add a method to fetch PR status:

```go
func fetchPRStatusCmd(service TaskService, taskID, repoRoot, branch string) tea.Cmd {
	return func() tea.Msg {
		status, err := service.GetPRStatus(context.Background(), repoRoot, branch)
		if err != nil {
			return prStatusLoadedMsg{taskID: taskID, status: &core.PRStatus{State: core.PRStateNone}}
		}
		return prStatusLoadedMsg{taskID: taskID, status: status}
	}
}
```

- [ ] **Step 4: Handle prStatusLoadedMsg in Update**

Add to the `Update` switch:

```go
	case prStatusLoadedMsg:
		for _, view := range m.taskViews {
			if view != nil && view.Task != nil && view.Task.ID == msg.taskID {
				view.PR = msg.status
				break
			}
		}
		return m, nil
```

- [ ] **Step 5: Trigger PR fetch on task load and navigation**

In `tasksLoadedMsg` handler, after setting tasks, add a batch command to fetch PR statuses:

```go
		var prCmds []tea.Cmd
		for _, view := range m.taskViews {
			if view != nil && view.Task != nil && strings.TrimSpace(view.Task.BranchName) != "" && strings.TrimSpace(view.Task.RepoRoot) != "" {
				prCmds = append(prCmds, fetchPRStatusCmd(m.service, view.Task.ID, view.Task.RepoRoot, view.Task.BranchName))
			}
		}
		if len(prCmds) > 0 {
			return m, tea.Batch(prCmds...)
		}
		return m, nil
```

In the `r` (refresh) key handler, add `m.service.InvalidatePRCache()` before the refresh.

- [ ] **Step 6: Run all tests**

Run: `cd /Users/ericbonet/software/tmux-llm-v1-refactor-brainstorm && go test ./internal/adapters/handler/cli/ -v -count=1`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/adapters/handler/cli/tui_model.go internal/adapters/handler/cli/tui_model_test.go
git commit -m "feat: wire PR status loading into TUI with cache invalidation on refresh"
```

---

### Task 11: Wire PR Status Checker into Main

**Files:**
- Modify: `cmd/agent/main.go`
- Modify: `internal/core/service.go`

- [ ] **Step 1: Add prChecker to Service constructor**

Check how the `Service` is constructed in `internal/core/service.go`. Add `PRStatusChecker` as an optional dependency. Add a functional option or constructor parameter:

```go
func (s *Service) SetPRStatusChecker(checker PRStatusChecker) {
	s.prChecker = checker
}
```

- [ ] **Step 2: Wire in main.go**

In `cmd/agent/main.go`, after creating the service and the exec runner, add:

```go
import ghclient "agent/internal/adapters/client/github"

// After service is created:
prChecker := ghclient.NewPRStatusChecker(execx.ExecRunner{})
svc.SetPRStatusChecker(prChecker)
```

- [ ] **Step 3: Verify compilation**

Run: `cd /Users/ericbonet/software/tmux-llm-v1-refactor-brainstorm && go build ./...`
Expected: Compiles.

- [ ] **Step 4: Commit**

```bash
git add cmd/agent/main.go internal/core/service.go
git commit -m "feat: wire GitHub PR status checker into main"
```

---

### Task 12: Final Test Pass and Cleanup

**Files:**
- All modified files

- [ ] **Step 1: Run full test suite**

Run: `cd /Users/ericbonet/software/tmux-llm-v1-refactor-brainstorm && go test ./... -count=1`
Expected: All tests pass.

- [ ] **Step 2: Fix any remaining test failures**

Address any tests that still reference removed fields or old view format. Common fixes:
- Tests checking for `"Selected Task"` header → now `"Git"` and `"Session"`
- Tests checking for `"Session Activity"` → removed
- Tests checking for `"Session ID"`, `"Transcript"`, etc. → removed
- Tests with `GetTaskHookEvents` mock expectations → remove those expectations

- [ ] **Step 3: Run linter**

Run: `cd /Users/ericbonet/software/tmux-llm-v1-refactor-brainstorm && golangci-lint run ./...`
Expected: No new lint errors.

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "chore: final cleanup and test fixes for TUI redesign"
```
