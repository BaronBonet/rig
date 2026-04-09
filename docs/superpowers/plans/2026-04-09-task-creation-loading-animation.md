# Task Creation Loading Animation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a shimmer loading animation during task creation that streams progress status messages back to the TUI, and redesign the name confirmation view with a checkmark recap style.

**Architecture:** The existing `CreateTaskWithProgress` service method already emits `TaskProgress` events via a callback — currently discarded. We'll bridge these into Bubble Tea's message system using a channel, add a tick-based shimmer renderer in `tui_style.go`, and update both the prompt input and name confirm views to show animated progress inline.

**Tech Stack:** Go, Bubble Tea v2, Lipgloss v2

---

### Task 1: Add shimmer rendering function

**Files:**
- Modify: `internal/adapters/handler/cli/tui_style.go:120-135` (after existing styles)
- Test: `internal/adapters/handler/cli/tui_style_test.go`

- [ ] **Step 1: Write the failing test for renderShimmer**

In `internal/adapters/handler/cli/tui_style_test.go`, add:

```go
func TestRenderShimmer_ReturnsStringWithSameVisibleLength(t *testing.T) {
	result := renderShimmer("Creating worktree...", 5)
	// Strip ANSI to get visible text
	visible := stripANSI(result)
	require.Equal(t, "Creating worktree...", visible)
}

func TestRenderShimmer_DifferentTicksProduceDifferentOutput(t *testing.T) {
	a := renderShimmer("Loading...", 0)
	b := renderShimmer("Loading...", 5)
	require.NotEqual(t, a, b)
}

func TestRenderShimmer_WrapsAroundAfterTextLength(t *testing.T) {
	text := "Hi"
	// tick well past the text length should wrap and still produce valid output
	result := renderShimmer(text, 100)
	visible := stripANSI(result)
	require.Equal(t, text, visible)
}
```

Note: `stripANSI` is defined in `tui_model_test.go` (same package), so it's available here.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/ericbonet/software/tmux-llm-task-creation-loading-animation && go test ./internal/adapters/handler/cli/ -run TestRenderShimmer -v`
Expected: FAIL — `renderShimmer` undefined

- [ ] **Step 3: Implement renderShimmer**

In `internal/adapters/handler/cli/tui_style.go`, add at the end of the file:

```go
// shimmerWidth is the number of characters in the bright "wave" of the shimmer.
const shimmerWidth = 4

// renderShimmer renders text with a left-to-right shimmer highlight.
// Most characters use colorDimmed; a window of shimmerWidth characters near
// the tick position interpolates toward colorPrimary.
func renderShimmer(text string, tick int) string {
	runes := []rune(text)
	if len(runes) == 0 {
		return ""
	}

	// Wrap tick so the shimmer cycles continuously.
	cycle := len(runes) + shimmerWidth + 2
	pos := tick % cycle

	var b strings.Builder
	for i, r := range runes {
		dist := pos - i
		if dist >= 0 && dist < shimmerWidth {
			intensity := 1.0 - float64(dist)/float64(shimmerWidth)
			col := lerpColor(colorDimmed, colorPrimary, intensity)
			b.WriteString(lipgloss.NewStyle().Foreground(col).Render(string(r)))
		} else {
			b.WriteString(dimStyle.Render(string(r)))
		}
	}
	return b.String()
}

// lerpColor linearly interpolates between two lipgloss hex colors.
func lerpColor(from, to lipgloss.Color, t float64) lipgloss.Color {
	fr, fg, fb := hexToRGB(string(from))
	tr, tg, tb := hexToRGB(string(to))
	r := fr + int(float64(tr-fr)*t)
	g := fg + int(float64(tg-fg)*t)
	b := fb + int(float64(tb-fb)*t)
	return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", r, g, b))
}

// hexToRGB parses a "#rrggbb" string into r, g, b ints.
func hexToRGB(hex string) (int, int, int) {
	if len(hex) == 7 && hex[0] == '#' {
		var r, g, b int
		fmt.Sscanf(hex[1:], "%02x%02x%02x", &r, &g, &b)
		return r, g, b
	}
	return 0, 0, 0
}
```

Add these imports to the top of `tui_style.go`:

```go
import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/ericbonet/software/tmux-llm-task-creation-loading-animation && go test ./internal/adapters/handler/cli/ -run TestRenderShimmer -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/handler/cli/tui_style.go internal/adapters/handler/cli/tui_style_test.go
git commit -m "feat: add shimmer text rendering function for loading animations"
```

---

### Task 2: Add new model state and message types

**Files:**
- Modify: `internal/adapters/handler/cli/tui_model.go:30-93` (model struct and message types)
- Test: `internal/adapters/handler/cli/tui_model_test.go`

- [ ] **Step 1: Write the failing test for shimmerTickMsg handling**

In `internal/adapters/handler/cli/tui_model_test.go`, add:

```go
func TestModelUpdate_ShimmerTickIncrementsCounter(t *testing.T) {
	m := newLoadedTUIModel(t, NewMockTaskService(t), tuiTask("task-one"))

	m.mode = tuiModePromptInput
	m.creationProgress = core.TaskProgressNaming
	m.shimmerTick = 3

	m, cmd := updateTUIModel(t, m, shimmerTickMsg{})
	require.Equal(t, 4, m.shimmerTick)
	require.NotNil(t, cmd, "should return another tick command to keep animation going")
}

func TestModelUpdate_ShimmerTickIgnoredWhenNoProgress(t *testing.T) {
	m := newLoadedTUIModel(t, NewMockTaskService(t), tuiTask("task-one"))

	m.shimmerTick = 0
	m.creationProgress = ""

	m, cmd := updateTUIModel(t, m, shimmerTickMsg{})
	require.Equal(t, 0, m.shimmerTick, "tick should not increment when no progress active")
	require.Nil(t, cmd, "should not schedule another tick")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/ericbonet/software/tmux-llm-task-creation-loading-animation && go test ./internal/adapters/handler/cli/ -run TestModelUpdate_ShimmerTick -v`
Expected: FAIL — `shimmerTickMsg` and `creationProgress` undefined

- [ ] **Step 3: Add new fields and message types**

In `internal/adapters/handler/cli/tui_model.go`, add fields to the `model` struct (after `busy bool` on line 47):

```go
	creationProgress core.TaskProgressStep
	creationSteps    []string
	shimmerTick      int
```

Add new message types (after `createFinishedMsg` on line 90-93):

```go
type taskProgressMsg struct {
	step    core.TaskProgressStep
	message string
}

type shimmerTickMsg struct{}
```

Add the shimmer tick interval constant (after the imports):

```go
const shimmerTickInterval = 60 * time.Millisecond
```

- [ ] **Step 4: Add Update handlers for the new messages**

In the `Update` method's `switch msg := msg.(type)` block (before the `default:` case on line 263), add:

```go
	case taskProgressMsg:
		m.creationProgress = msg.step
		if msg.step == core.TaskProgressWorktreeCreating ||
			msg.step == core.TaskProgressWorkspaceSeeding ||
			msg.step == core.TaskProgressTmuxStarting ||
			msg.step == core.TaskProgressAgentLaunching ||
			msg.step == core.TaskProgressTaskCreated {
			label := progressStepLabel(msg.step)
			if label != "" {
				// Mark all existing steps as done, add new active one.
				m.creationSteps = append(m.creationSteps, label)
			}
		}
		m.shimmerTick = 0
		return m, tea.Tick(shimmerTickInterval, func(time.Time) tea.Msg { return shimmerTickMsg{} })
	case shimmerTickMsg:
		if m.creationProgress == "" {
			return m, nil
		}
		m.shimmerTick++
		return m, tea.Tick(shimmerTickInterval, func(time.Time) tea.Msg { return shimmerTickMsg{} })
```

Add the `progressStepLabel` helper function (near the other helper functions):

```go
func progressStepLabel(step core.TaskProgressStep) string {
	switch step {
	case core.TaskProgressNaming:
		return "Suggesting name..."
	case core.TaskProgressWorktreeCreating:
		return "Creating worktree..."
	case core.TaskProgressWorkspaceSeeding:
		return "Seeding workspace..."
	case core.TaskProgressTmuxStarting:
		return "Starting session..."
	case core.TaskProgressAgentLaunching:
		return "Launching agent..."
	case core.TaskProgressTaskCreated:
		return "Task created"
	default:
		return ""
	}
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd /Users/ericbonet/software/tmux-llm-task-creation-loading-animation && go test ./internal/adapters/handler/cli/ -run TestModelUpdate_ShimmerTick -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/adapters/handler/cli/tui_model.go internal/adapters/handler/cli/tui_model_test.go
git commit -m "feat: add shimmer tick and task progress message types to TUI model"
```

---

### Task 3: Wire progress callback in createTaskCmd

**Files:**
- Modify: `internal/adapters/handler/cli/tui_model.go:1076-1086` (createTaskCmd function)
- Test: `internal/adapters/handler/cli/tui_model_test.go`

- [ ] **Step 1: Write the failing test for progress messages**

In `internal/adapters/handler/cli/tui_model_test.go`, add:

```go
func TestCreateTaskCmd_SendsProgressMessages(t *testing.T) {
	service := NewMockTaskService(t)

	var capturedCallback func(core.TaskProgress)
	service.EXPECT().
		CreateTaskWithProgress(
			mock.Anything,
			mock.Anything,
			mock.Anything,
			mock.Anything,
		).
		Run(func(_ interface{}, _ interface{}, _ interface{}, cb interface{}) {
			capturedCallback = cb.(func(core.TaskProgress))
			capturedCallback(core.TaskProgress{
				Step:    core.TaskProgressWorktreeCreating,
				Message: "Creating worktree...",
			})
			capturedCallback(core.TaskProgress{
				Step:    core.TaskProgressAgentLaunching,
				Message: "Launching codex...",
			})
		}).
		Return(tuiTask("test-task"), nil).
		Once()

	progressCh, cmd := createTaskCmd(service, core.NewTaskInput{
		Prompt:               "test",
		ConfirmedDisplayName: "test-task",
		Provider:             "codex",
	})

	// Drain progress messages from channel.
	var steps []core.TaskProgressStep
	done := false
	for !done {
		select {
		case p, ok := <-progressCh:
			if !ok {
				done = true
			} else {
				steps = append(steps, p.step)
			}
		}
	}
	require.Contains(t, steps, core.TaskProgressWorktreeCreating)
	require.Contains(t, steps, core.TaskProgressAgentLaunching)

	// The final command should return createFinishedMsg.
	msg := cmd()
	finished, ok := msg.(createFinishedMsg)
	require.True(t, ok)
	require.NoError(t, finished.err)
	require.Equal(t, "test-task", finished.task.Slug)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/ericbonet/software/tmux-llm-task-creation-loading-animation && go test ./internal/adapters/handler/cli/ -run TestCreateTaskCmd_SendsProgressMessages -v`
Expected: FAIL — `createTaskCmd` signature doesn't match (returns single `tea.Cmd`, not channel + cmd)

- [ ] **Step 3: Rewrite createTaskCmd to use channel-based progress**

Replace the existing `createTaskCmd` function in `internal/adapters/handler/cli/tui_model.go` (lines 1076-1086):

```go
func createTaskCmd(service TaskService, input core.NewTaskInput) (<-chan taskProgressMsg, tea.Cmd) {
	progressCh := make(chan taskProgressMsg, 8)

	cmd := func() tea.Msg {
		task, err := service.CreateTaskWithProgress(
			context.Background(),
			input,
			core.CreateTaskOptions{OpenSession: false},
			func(p core.TaskProgress) {
				progressCh <- taskProgressMsg{step: p.Step, message: p.Message}
			},
		)
		close(progressCh)
		return createFinishedMsg{task: task, err: err}
	}

	return progressCh, cmd
}

func waitForProgressCmd(ch <-chan taskProgressMsg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}
```

- [ ] **Step 4: Update the call site in updateNameConfirmKey**

In `internal/adapters/handler/cli/tui_model.go`, update `updateNameConfirmKey` (line 426-438). Replace:

```go
		m.err = nil
		m.busy = true
		m.nameInput.Blur()
		input := m.createInput
		input.ConfirmedDisplayName = name
		input.Provider = m.provider
		return m, createTaskCmd(m.service, input)
```

With:

```go
		m.err = nil
		m.busy = true
		m.nameInput.Blur()
		input := m.createInput
		input.ConfirmedDisplayName = name
		input.Provider = m.provider
		progressCh, createCmd := createTaskCmd(m.service, input)
		m.progressCh = progressCh
		return m, tea.Batch(createCmd, waitForProgressCmd(progressCh))
```

Add `progressCh` to the model struct (after `shimmerTick`):

```go
	progressCh <-chan taskProgressMsg
```

- [ ] **Step 5: Update the taskProgressMsg handler to wait for next progress**

In the `Update` method's `taskProgressMsg` case, add at the end (before `return`):

```go
		return m, tea.Batch(
			waitForProgressCmd(m.progressCh),
			tea.Tick(shimmerTickInterval, func(time.Time) tea.Msg { return shimmerTickMsg{} }),
		)
```

(Replace the existing return in the `taskProgressMsg` case.)

- [ ] **Step 6: Clear progress state in createFinishedMsg handler**

In the `createFinishedMsg` case of `Update` (line 235-254), add cleanup at the top:

```go
	case createFinishedMsg:
		m.busy = false
		m.creationProgress = ""
		m.creationSteps = nil
		m.shimmerTick = 0
		m.progressCh = nil
		m.err = msg.err
```

- [ ] **Step 7: Run all tests to verify nothing is broken**

Run: `cd /Users/ericbonet/software/tmux-llm-task-creation-loading-animation && go test ./internal/adapters/handler/cli/ -v`
Expected: PASS (existing tests may need minor updates to match new `createTaskCmd` signature — see step 8)

- [ ] **Step 8: Fix existing test for create flow**

The existing `TestModelUpdate_CreateFlowSuggestsNameThenCreatesTask` test calls `createCmd()` directly. Since `createTaskCmd` now returns `(<-chan taskProgressMsg, tea.Cmd)`, the call via `updateNameConfirmKey` returns a `tea.BatchMsg`. Update the test to handle the batch:

In the existing test, after `m, createCmd := updateTUIModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})`, the `createCmd` will be a batch. Execute it and find the `createFinishedMsg` among the results. The simplest approach: since the test mock returns immediately, drain the batch:

```go
		m, createCmd := updateTUIModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
		require.NotNil(t, createCmd)
		require.True(t, m.busy)

		// createCmd is a batch — execute all sub-commands and find createFinishedMsg.
		createMsg := executeBatchUntil[createFinishedMsg](t, createCmd)
		m, refreshCmd := updateTUIModel(t, m, createMsg)
```

Add a helper:

```go
func executeBatchUntil[T tea.Msg](t *testing.T, cmd tea.Cmd) T {
	t.Helper()
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, sub := range batch {
			result := sub()
			if result == nil {
				continue
			}
			if typed, ok := result.(T); ok {
				return typed
			}
			// Recurse for nested batches.
			if innerBatch, ok := result.(tea.BatchMsg); ok {
				for _, inner := range innerBatch {
					innerResult := inner()
					if innerResult == nil {
						continue
					}
					if typed, ok := innerResult.(T); ok {
						return typed
					}
				}
			}
		}
	}
	if typed, ok := msg.(T); ok {
		return typed
	}
	t.Fatalf("expected message of type %T not found in batch", *new(T))
	return *new(T)
}
```

Apply the same pattern to `TestModelUpdate_CreateFlowWithoutTasksUsesModelCwdFallback` and any other test that exercises the create flow.

- [ ] **Step 9: Run all tests**

Run: `cd /Users/ericbonet/software/tmux-llm-task-creation-loading-animation && go test ./internal/adapters/handler/cli/ -v`
Expected: PASS

- [ ] **Step 10: Commit**

```bash
git add internal/adapters/handler/cli/tui_model.go internal/adapters/handler/cli/tui_model_test.go
git commit -m "feat: wire task creation progress callback into Bubble Tea messages"
```

---

### Task 4: Add shimmer to prompt input view during name suggestion

**Files:**
- Modify: `internal/adapters/handler/cli/tui_model.go:393-412` (updatePromptInputKey), `internal/adapters/handler/cli/tui_model.go:759-769` (promptInputView)
- Test: `internal/adapters/handler/cli/tui_model_test.go`

- [ ] **Step 1: Write the failing test**

In `internal/adapters/handler/cli/tui_model_test.go`, add:

```go
func TestPromptInputView_ShowsShimmerDuringNameSuggestion(t *testing.T) {
	m := newLoadedTUIModel(t, NewMockTaskService(t), tuiTask("task-one"))

	m.mode = tuiModePromptInput
	m.createInput.Prompt = "add dark mode toggle"
	m.creationProgress = core.TaskProgressNaming
	m.shimmerTick = 5

	view := stripANSI(m.promptInputView())
	require.Contains(t, view, "Suggesting name...")
}

func TestPromptInputView_NoShimmerWhenNotBusy(t *testing.T) {
	m := newLoadedTUIModel(t, NewMockTaskService(t), tuiTask("task-one"))

	m.mode = tuiModePromptInput
	m.creationProgress = ""

	view := stripANSI(m.promptInputView())
	require.NotContains(t, view, "Suggesting name...")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/ericbonet/software/tmux-llm-task-creation-loading-animation && go test ./internal/adapters/handler/cli/ -run TestPromptInputView -v`
Expected: FAIL — promptInputView doesn't render shimmer text yet

- [ ] **Step 3: Set creationProgress when submitting prompt**

In `updatePromptInputKey`, in the `tea.KeyEnter` case (lines 401-412), add after `m.busy = true`:

```go
		m.creationProgress = core.TaskProgressNaming
		m.shimmerTick = 0
```

- [ ] **Step 4: Clear creationProgress when name suggestion arrives**

In the `suggestNameFinishedMsg` case of `Update` (lines 219-234), add after `m.busy = false`:

```go
		m.creationProgress = ""
		m.shimmerTick = 0
```

- [ ] **Step 5: Update promptInputView to show shimmer**

Replace `promptInputView` (lines 759-769):

```go
func (m model) promptInputView() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(iconHeaderCreate+" Create Task") + "\n\n")
	b.WriteString(dimStyle.Render("Enter the task prompt. Press Enter to submit, or Esc to cancel.") + "\n")
	b.WriteString(dimStyle.Render("tab to switch provider: ") + providerToggle(m.provider) + "\n\n")
	if m.err != nil {
		b.WriteString(errorStyle.Render("Error: "+m.err.Error()) + "\n\n")
	}
	b.WriteString(m.promptInput.View())
	if m.creationProgress == core.TaskProgressNaming {
		label := progressStepLabel(core.TaskProgressNaming)
		b.WriteString("\n\n" + warningStyle.Render("●") + " " + renderShimmer(label, m.shimmerTick))
	}
	return b.String()
}
```

- [ ] **Step 6: Also start shimmer tick when entering prompt busy state**

In `updatePromptInputKey`, the return already dispatches `suggestTaskNameCmd`. Add a shimmer tick to start the animation. Replace:

```go
		return m, suggestTaskNameCmd(m.service, prompt, m.provider)
```

With:

```go
		return m, tea.Batch(
			suggestTaskNameCmd(m.service, prompt, m.provider),
			tea.Tick(shimmerTickInterval, func(time.Time) tea.Msg { return shimmerTickMsg{} }),
		)
```

- [ ] **Step 7: Run test to verify it passes**

Run: `cd /Users/ericbonet/software/tmux-llm-task-creation-loading-animation && go test ./internal/adapters/handler/cli/ -run TestPromptInputView -v`
Expected: PASS

- [ ] **Step 8: Run all tests**

Run: `cd /Users/ericbonet/software/tmux-llm-task-creation-loading-animation && go test ./internal/adapters/handler/cli/ -v`
Expected: PASS

- [ ] **Step 9: Commit**

```bash
git add internal/adapters/handler/cli/tui_model.go internal/adapters/handler/cli/tui_model_test.go
git commit -m "feat: show shimmer animation in prompt input during name suggestion"
```

---

### Task 5: Redesign name confirm view with checkmark recap and creation progress

**Files:**
- Modify: `internal/adapters/handler/cli/tui_model.go:771-784` (nameConfirmView)
- Test: `internal/adapters/handler/cli/tui_model_test.go`

- [ ] **Step 1: Write the failing tests**

In `internal/adapters/handler/cli/tui_model_test.go`, add:

```go
func TestNameConfirmView_ShowsCheckmarkRecap(t *testing.T) {
	m := newLoadedTUIModel(t, NewMockTaskService(t), tuiTask("task-one"))

	m.mode = tuiModeNameConfirm
	m.createInput.Prompt = "add dark mode toggle to settings page"
	m.provider = "codex"
	m.nameInput.SetValue("dark-mode-settings-toggle")

	view := stripANSI(m.nameConfirmView())

	require.Contains(t, view, "✔")
	require.Contains(t, view, "add dark mode toggle to settings page")
	require.Contains(t, view, "codex")
	require.Contains(t, view, "Name:")
	require.Contains(t, view, "Enter to create")
}

func TestNameConfirmView_ShowsProgressStepsDuringCreation(t *testing.T) {
	m := newLoadedTUIModel(t, NewMockTaskService(t), tuiTask("task-one"))

	m.mode = tuiModeNameConfirm
	m.createInput.Prompt = "add dark mode toggle"
	m.provider = "codex"
	m.nameInput.SetValue("dark-mode-toggle")
	m.busy = true
	m.creationProgress = core.TaskProgressAgentLaunching
	m.creationSteps = []string{"Creating worktree...", "Starting session...", "Launching agent..."}
	m.shimmerTick = 5

	view := stripANSI(m.nameConfirmView())

	// Completed steps should show checkmarks.
	require.Contains(t, view, "Creating worktree...")
	require.Contains(t, view, "Starting session...")
	// Active step should be visible.
	require.Contains(t, view, "Launching agent...")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/ericbonet/software/tmux-llm-task-creation-loading-animation && go test ./internal/adapters/handler/cli/ -run TestNameConfirmView -v`
Expected: FAIL — current view doesn't have checkmarks or progress steps

- [ ] **Step 3: Rewrite nameConfirmView**

Replace `nameConfirmView` (lines 771-784):

```go
func (m model) nameConfirmView() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(iconHeaderCreate+" Create Task") + "\n\n")

	if m.err != nil {
		b.WriteString(errorStyle.Render("Error: "+m.err.Error()) + "\n\n")
	}

	// Checkmark recap: completed prompt and provider.
	b.WriteString(healthyStyle.Render("✔") + " " + dimStyle.Render(m.createInput.Prompt) + "\n")
	b.WriteString(
		healthyStyle.Render("✔") + " " +
			dimStyle.Render("provider: ") + providerStyle(m.provider).Render(m.provider) + "\n",
	)

	if m.busy && len(m.creationSteps) > 0 {
		// Name is confirmed — show it as a completed step.
		b.WriteString(healthyStyle.Render("✔") + " " + dimStyle.Render("name: "+m.nameInput.Value()) + "\n")
		b.WriteString("\n")

		// Render completed creation steps and active shimmer step.
		for i, label := range m.creationSteps {
			if i == len(m.creationSteps)-1 {
				// Active (last) step gets shimmer.
				b.WriteString(warningStyle.Render("●") + " " + renderShimmer(label, m.shimmerTick) + "\n")
			} else {
				// Completed steps get checkmarks.
				b.WriteString(healthyStyle.Render("✔") + " " + dimStyle.Render(label) + "\n")
			}
		}
	} else {
		// Name input is active — show editable input.
		b.WriteString("\n")
		b.WriteString(warningStyle.Render("▸ Name: ") + m.nameInput.View() + "\n")
		b.WriteString("\n")
		b.WriteString(
			healthyStyle.Render("enter") + dimStyle.Render(" create · ") +
				errorStyle.Render("esc") + dimStyle.Render(" cancel"),
		)
	}

	return b.String()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/ericbonet/software/tmux-llm-task-creation-loading-animation && go test ./internal/adapters/handler/cli/ -run TestNameConfirmView -v`
Expected: PASS

- [ ] **Step 5: Run all tests**

Run: `cd /Users/ericbonet/software/tmux-llm-task-creation-loading-animation && go test ./internal/adapters/handler/cli/ -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/adapters/handler/cli/tui_model.go internal/adapters/handler/cli/tui_model_test.go
git commit -m "feat: redesign name confirm view with checkmark recap and creation progress shimmer"
```

---

### Task 6: End-to-end verification and cleanup

**Files:**
- Verify: `internal/adapters/handler/cli/tui_model.go`, `internal/adapters/handler/cli/tui_style.go`

- [ ] **Step 1: Run the full test suite**

Run: `cd /Users/ericbonet/software/tmux-llm-task-creation-loading-animation && go test ./... -v`
Expected: All PASS

- [ ] **Step 2: Run the TUI manually to verify animation**

Run: `cd /Users/ericbonet/software/tmux-llm-task-creation-loading-animation && go run ./cmd/agent/ tui`

Verify:
1. Press `n` to create a task
2. Type a prompt and press Enter → "Suggesting name..." appears with shimmer sweep below the input
3. Name suggestion arrives → view switches to checkmark recap with `✔ prompt` and `✔ provider`, name input active
4. Press Enter → progress steps appear with shimmer: "Creating worktree..." → "Starting session..." → "Launching agent..."
5. Task creation completes → returns to list view

- [ ] **Step 3: Commit any final adjustments**

```bash
git add -A
git commit -m "chore: final cleanup for task creation loading animation"
```
