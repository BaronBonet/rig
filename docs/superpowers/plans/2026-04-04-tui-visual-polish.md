# TUI Visual Polish Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Transform the plain-text Bubble Tea TUI into a visually polished interface using lipgloss, with a minimal + icons hybrid design.

**Architecture:** A new `tui_style.go` file holds all lipgloss styles, color constants, and icon definitions. The four view methods in `tui_model.go` are updated to use these styles. Tests are updated to strip ANSI codes before asserting on content.

**Tech Stack:** Go, Bubble Tea, Lipgloss v1.1.0 (already a transitive dependency)

---

## File Structure

| File | Action | Responsibility |
|------|--------|----------------|
| `internal/adapters/handler/cli/tui_style.go` | Create | Color constants, icon constants, lipgloss style definitions, status-to-style mapping, helper for stripping ANSI |
| `internal/adapters/handler/cli/tui_model.go` | Modify | Update `listView()`, `promptInputView()`, `nameConfirmView()`, `confirmationView()`, `windowHealth()`, `yesNo()` to use styled rendering |
| `internal/adapters/handler/cli/tui_model_test.go` | Modify | Add ANSI-stripping helper, update view assertions that break due to styled output |

---

### Task 1: Create `tui_style.go` with colors, icons, and lipgloss styles

**Files:**
- Create: `internal/adapters/handler/cli/tui_style.go`

- [ ] **Step 1: Create the style definitions file**

```go
package cli

import "github.com/charmbracelet/lipgloss"

// Colors
var (
	colorPrimary = lipgloss.Color("#c8c8d4")
	colorDimmed  = lipgloss.Color("#7b7b8e")
	colorAccent  = lipgloss.Color("#6c6ce0")
	colorHealthy = lipgloss.Color("#5a9e6f")
	colorWarning = lipgloss.Color("#c4a24e")
	colorError   = lipgloss.Color("#c05050")
)

// Icons
const (
	iconRepo     = "📁"
	iconAgent    = "🤖"
	iconEditor   = "📝"
	iconBranch   = "🌿"
	iconTmux     = "💻"
	iconWorktree = "🌳"

	iconStatusActive   = "●"
	iconStatusIdle     = "○"
	iconStatusProgress = "◐"

	iconHeaderList    = "◈"
	iconHeaderCreate  = "✦"
	iconHeaderCleanup = "⚠"

	iconSelected = "▸"
)

// Styles
var (
	titleStyle = lipgloss.NewStyle().
			Foreground(colorAccent).
			Bold(true)

	dimStyle = lipgloss.NewStyle().
			Foreground(colorDimmed)

	primaryStyle = lipgloss.NewStyle().
			Foreground(colorPrimary)

	errorStyle = lipgloss.NewStyle().
			Foreground(colorError)

	warningStyle = lipgloss.NewStyle().
			Foreground(colorWarning)

	healthyStyle = lipgloss.NewStyle().
			Foreground(colorHealthy)

	selectedRowStyle = lipgloss.NewStyle().
				BorderLeft(true).
				BorderStyle(lipgloss.ThickBorder()).
				BorderForeground(colorAccent).
				PaddingLeft(1).
				Bold(true).
				Foreground(colorPrimary)

	normalRowStyle = lipgloss.NewStyle().
			PaddingLeft(3).
			Foreground(colorDimmed)

	separatorStyle = lipgloss.NewStyle().
			Foreground(colorDimmed)

	detailBarStyle = lipgloss.NewStyle().
			Foreground(colorDimmed)
)

// statusStyle returns the icon and style for a given task status.
func statusStyle(status string) (string, lipgloss.Style) {
	switch status {
	case "running":
		return iconStatusActive, healthyStyle
	case "creating":
		return iconStatusProgress, warningStyle
	case "degraded":
		return iconStatusProgress, warningStyle
	case "broken":
		return iconStatusActive, errorStyle
	default:
		return iconStatusIdle, dimStyle
	}
}

// healthStyle returns the styled string for a boolean health indicator.
func healthStyle(ok bool) string {
	if ok {
		return healthyStyle.Render("healthy")
	}
	return dimStyle.Render("missing")
}

// yesNoStyled returns a styled yes/no string.
func yesNoStyled(ok bool) string {
	if ok {
		return healthyStyle.Render("yes")
	}
	return dimStyle.Render("no")
}
```

- [ ] **Step 2: Verify it compiles**

Run: `cd /Users/ericbonet/software/tmux-llm-polish-tui-visual-design && go build ./internal/adapters/handler/cli/...`
Expected: No errors (file only defines constants and styles, no consumers yet)

- [ ] **Step 3: Commit**

```bash
git add internal/adapters/handler/cli/tui_style.go
git commit -m "feat: add TUI style definitions with colors, icons, and lipgloss styles"
```

---

### Task 2: Add ANSI-stripping test helper

**Files:**
- Modify: `internal/adapters/handler/cli/tui_model_test.go`

- [ ] **Step 1: Add the stripANSI helper function to the test file**

Add this import and function near the bottom of the test file, before the existing `tuiTask` helper:

```go
import "regexp"

// stripANSI removes ANSI escape sequences so view assertions can match plain text.
var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string {
	return ansiPattern.ReplaceAllString(s, "")
}
```

Note: add `"regexp"` to the existing import block at the top of the file.

- [ ] **Step 2: Verify tests still pass**

Run: `cd /Users/ericbonet/software/tmux-llm-polish-tui-visual-design && go test ./internal/adapters/handler/cli/ -v -count=1`
Expected: All tests pass (helper is added but not yet used)

- [ ] **Step 3: Commit**

```bash
git add internal/adapters/handler/cli/tui_model_test.go
git commit -m "test: add ANSI-stripping helper for styled view assertions"
```

---

### Task 3: Restyle the list view

**Files:**
- Modify: `internal/adapters/handler/cli/tui_model.go` (lines 332-384: `listView()` method, lines 558-580: `windowHealth()` and `yesNo()` helpers)

- [ ] **Step 1: Update the `listView()` method**

Replace the current `listView()` method (lines 332-384) with:

```go
func (m model) listView() string {
	var b strings.Builder

	// Header
	header := titleStyle.Render(iconHeaderList + " Control Center")
	keys := dimStyle.Render("j/k move · enter open · n new · x clean · r refresh · q quit")
	b.WriteString(header + "  " + keys + "\n")
	b.WriteString(dimStyle.Render(strings.Repeat("─", 72)) + "\n")

	if m.err != nil {
		b.WriteString(errorStyle.Render("Error: "+m.err.Error()) + "\n\n")
	}

	if m.loading {
		b.WriteString(dimStyle.Render("Loading tasks..."))
		return b.String()
	}

	if m.busy {
		b.WriteString(dimStyle.Render("Working...") + "\n\n")
	}

	if len(m.tasks) == 0 {
		b.WriteString(dimStyle.Render("No tasks found.") + "\n")
		b.WriteString(dimStyle.Render("Press n to create one."))
		return b.String()
	}

	// Detail bar for selected task
	task := m.selectedTask()
	details := fmt.Sprintf(
		"%s %s  %s %s  %s %s  %s %s  %s %s  %s %s",
		iconRepo, emptyFallback(taskRepoName(task), "-"),
		iconAgent, healthStyle(task.AgentWindowExists),
		iconEditor, healthStyle(task.EditorWindowExists),
		iconBranch, dimStyle.Render(emptyFallback(task.BranchName, "-")),
		iconTmux, yesNoStyled(task.SessionExists),
		iconWorktree, yesNoStyled(task.WorktreeExists),
	)
	b.WriteString(detailBarStyle.Render(details) + "\n\n")

	// Task rows
	for i, task := range m.tasks {
		icon, style := statusStyle(string(task.Status))
		status := style.Render(icon + " " + string(task.Status))

		if i == m.selected {
			name := iconSelected + " " + task.DisplayName
			row := fmt.Sprintf("%-40s %s", name, status)
			b.WriteString(selectedRowStyle.Render(row) + "\n")
		} else {
			name := "  " + task.DisplayName
			row := fmt.Sprintf("%-40s %s", name, status)
			b.WriteString(normalRowStyle.Render(row) + "\n")
		}
	}

	return strings.TrimRight(b.String(), "\n")
}
```

- [ ] **Step 2: Add lipgloss import**

Add `"github.com/charmbracelet/lipgloss"` to the import block in `tui_model.go`. Note: lipgloss is used indirectly through the style vars, but the import is needed if any lipgloss types are referenced directly. Check if the compiler requires it — if not (because all lipgloss usage is in `tui_style.go`), skip this step.

- [ ] **Step 3: Run tests and identify failures**

Run: `cd /Users/ericbonet/software/tmux-llm-polish-tui-visual-design && go test ./internal/adapters/handler/cli/ -v -count=1`
Expected: Some tests will fail because view assertions now see ANSI escape codes. Note which tests fail.

- [ ] **Step 4: Update failing test assertions to use stripANSI**

In `tui_model_test.go`, wrap `m.View()` calls in `stripANSI()` for tests that assert on view content. The tests that need updating are the ones checking `View()` output with `require.Contains` or `require.NotContains`:

- `TestModelUpdate_MainListViewRendersControlCenterDetails` (line 401): change all `require.Contains(t, view, ...)` to use `view := stripANSI(m.View())`
- `TestModelUpdate_LoadedTasksHideTasksWithoutLiveResources` (line 427): same pattern
- `TestModelUpdate_EnterFailureRendersInlineErrorAndKeepsTUIOpen` (line 327): same
- `TestModelUpdate_CleanupFailureRendersInlineErrorAndKeepsTUIUsable` (line 450): same
- `TestModelUpdate_CleanupSuccessRefreshFailureRemovesTaskFromVisibleList` (line 470): same
- `TestModelView_ShowsLoadingBeforeInitialLoadCompletes` (line 498): same
- `TestModelUpdate_SuggestNameFailureReturnsToPromptModeAndRendersError` (line 125): same
- `TestModelUpdate_CreateFailureReturnsToNameConfirmModeAndRendersError` (line 147): same
- `TestModelUpdate_CreateFailureWithPersistedTaskReturnsToListModeAndPreservesError` (line 176): same
- `TestModelUpdate_ConfirmationViewExplainsDeletionScope` (line 441): same

For each, the pattern is:
```go
// Before:
view := m.View()
require.Contains(t, view, "something")

// After:
view := stripANSI(m.View())
require.Contains(t, view, "something")
```

Also update the assertion content strings where the format changed:
- `"repo: tmux-llm"` → `"tmux-llm"` (the icon replaces the "repo:" prefix)
- `"agent: healthy"` → `"healthy"` (icon replaces "agent:" prefix)
- `"editor: missing"` → `"missing"`
- `"tmux: yes"` → `"yes"`
- `"worktree: no"` → `"no"`
- `"running"` stays the same (still in the status text)
- `"feat/billing-retry-flow"` stays the same

- [ ] **Step 5: Run tests to verify all pass**

Run: `cd /Users/ericbonet/software/tmux-llm-polish-tui-visual-design && go test ./internal/adapters/handler/cli/ -v -count=1`
Expected: All tests pass

- [ ] **Step 6: Commit**

```bash
git add internal/adapters/handler/cli/tui_model.go internal/adapters/handler/cli/tui_model_test.go
git commit -m "feat: restyle list view with lipgloss colors and icons"
```

---

### Task 4: Restyle the prompt input view

**Files:**
- Modify: `internal/adapters/handler/cli/tui_model.go` (lines 387-397: `promptInputView()`)

- [ ] **Step 1: Update the `promptInputView()` method**

Replace the current `promptInputView()` method with:

```go
func (m model) promptInputView() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(iconHeaderCreate+" Create Task") + "\n\n")
	b.WriteString(dimStyle.Render("Enter the task prompt. Press Enter to suggest a name, or Esc to cancel.") + "\n\n")
	if m.err != nil {
		b.WriteString(errorStyle.Render("Error: "+m.err.Error()) + "\n\n")
	}
	b.WriteString(m.promptInput.View())
	return b.String()
}
```

- [ ] **Step 2: Run tests**

Run: `cd /Users/ericbonet/software/tmux-llm-polish-tui-visual-design && go test ./internal/adapters/handler/cli/ -v -count=1`
Expected: All tests pass (the suggest-name-failure test already uses stripANSI from Task 3)

- [ ] **Step 3: Commit**

```bash
git add internal/adapters/handler/cli/tui_model.go
git commit -m "feat: restyle prompt input view with lipgloss"
```

---

### Task 5: Restyle the name confirm view

**Files:**
- Modify: `internal/adapters/handler/cli/tui_model.go` (lines 400-411: `nameConfirmView()`)

- [ ] **Step 1: Update the `nameConfirmView()` method**

Replace the current `nameConfirmView()` method with:

```go
func (m model) nameConfirmView() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(iconHeaderCreate+" Confirm Task Name") + "\n\n")
	b.WriteString(dimStyle.Render("Edit the suggested name if needed. Press Enter to create and open the session, or Esc to cancel.") + "\n\n")
	if m.err != nil {
		b.WriteString(errorStyle.Render("Error: "+m.err.Error()) + "\n\n")
	}
	b.WriteString(dimStyle.Render("prompt: "+m.createInput.Prompt) + "\n\n")
	b.WriteString(m.nameInput.View())
	return b.String()
}
```

- [ ] **Step 2: Run tests**

Run: `cd /Users/ericbonet/software/tmux-llm-polish-tui-visual-design && go test ./internal/adapters/handler/cli/ -v -count=1`
Expected: All tests pass

- [ ] **Step 3: Commit**

```bash
git add internal/adapters/handler/cli/tui_model.go
git commit -m "feat: restyle name confirm view with lipgloss"
```

---

### Task 6: Restyle the cleanup confirmation view

**Files:**
- Modify: `internal/adapters/handler/cli/tui_model.go` (lines 414-426: `confirmationView()`)

- [ ] **Step 1: Update the `confirmationView()` method**

Replace the current `confirmationView()` method with:

```go
func (m model) confirmationView() string {
	task := m.selectedTask()
	if task == nil {
		return dimStyle.Render("No task selected.")
	}

	var b strings.Builder
	b.WriteString(warningStyle.Render(iconHeaderCleanup+" Confirm Cleanup") + "\n\n")
	b.WriteString("Task: " + primaryStyle.Render(task.DisplayName) + "\n")
	b.WriteString(dimStyle.Render("The tmux session and worktree will be deleted.") + "\n")
	b.WriteString(dimStyle.Render("The branch will be kept.") + "\n\n")
	b.WriteString(healthyStyle.Render("y") + dimStyle.Render(" confirm · ") + errorStyle.Render("n") + dimStyle.Render(" cancel"))
	return b.String()
}
```

- [ ] **Step 2: Run tests**

Run: `cd /Users/ericbonet/software/tmux-llm-polish-tui-visual-design && go test ./internal/adapters/handler/cli/ -v -count=1`
Expected: All tests pass (confirmation view test uses stripANSI from Task 3)

- [ ] **Step 3: Commit**

```bash
git add internal/adapters/handler/cli/tui_model.go
git commit -m "feat: restyle cleanup confirmation view with lipgloss"
```

---

### Task 7: Update the prompt input styling

**Files:**
- Modify: `internal/adapters/handler/cli/tui_model.go` (lines 62-80: `newTUIModel()`)

- [ ] **Step 1: Style the text input prompts**

In the `newTUIModel()` function, update the prompt input styling to use the accent-colored prompt character:

```go
func newTUIModel(service TaskService, defaultCreationCwd string) model {
	promptInput := textinput.New()
	promptInput.Prompt = titleStyle.Render("❯") + " "
	promptInput.Placeholder = "Describe the task to create"
	promptInput.Focus()

	nameInput := textinput.New()
	nameInput.Prompt = titleStyle.Render("❯") + " "
	nameInput.Placeholder = "Confirm or edit the suggested task name"

	return model{
		service:            service,
		loading:            true,
		mode:               tuiModeList,
		promptInput:        promptInput,
		nameInput:          nameInput,
		defaultCreationCwd: emptyFallback(defaultCreationCwd, "."),
	}
}
```

- [ ] **Step 2: Run tests**

Run: `cd /Users/ericbonet/software/tmux-llm-polish-tui-visual-design && go test ./internal/adapters/handler/cli/ -v -count=1`
Expected: All tests pass

- [ ] **Step 3: Commit**

```bash
git add internal/adapters/handler/cli/tui_model.go
git commit -m "feat: style text input prompts with accent-colored cursor"
```

---

### Task 8: Clean up unused helpers

**Files:**
- Modify: `internal/adapters/handler/cli/tui_model.go`

- [ ] **Step 1: Remove or update unused helper functions**

The old `windowHealth()` and `yesNo()` functions (used in the old `listView()`) may no longer be called from the styled views (replaced by `healthStyle()` and `yesNoStyled()` in `tui_style.go`). Check if they are still referenced:

Run: `cd /Users/ericbonet/software/tmux-llm-polish-tui-visual-design && grep -n 'windowHealth\|yesNo(' internal/adapters/handler/cli/tui_model.go`

If `windowHealth` and `yesNo` are no longer called anywhere in `tui_model.go`, remove them. If they are still used in other files (check with `grep -rn 'windowHealth\|yesNo(' internal/`), keep them.

- [ ] **Step 2: Run full test suite**

Run: `cd /Users/ericbonet/software/tmux-llm-polish-tui-visual-design && go test ./... -count=1`
Expected: All tests pass across the entire project

- [ ] **Step 3: Commit**

```bash
git add internal/adapters/handler/cli/tui_model.go
git commit -m "refactor: remove unused plain-text helper functions"
```

---

### Task 9: Final verification

- [ ] **Step 1: Run the full test suite**

Run: `cd /Users/ericbonet/software/tmux-llm-polish-tui-visual-design && go test ./... -v -count=1`
Expected: All tests pass

- [ ] **Step 2: Build the binary**

Run: `cd /Users/ericbonet/software/tmux-llm-polish-tui-visual-design && go build -o /tmp/agent-tui-test ./cmd/agent`
Expected: Builds without errors (find the correct main package path by checking `cmd/` directory)

- [ ] **Step 3: Run go vet**

Run: `cd /Users/ericbonet/software/tmux-llm-polish-tui-visual-design && go vet ./...`
Expected: No issues
