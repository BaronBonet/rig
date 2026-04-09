# Task Creation Loading Animation

## Problem

When creating a new task, there is no visual feedback after pressing Enter. The user sees only "Working..." with no indication of what's happening or that progress is being made. The task creation flow has two async stages (name suggestion and task creation) that both feel like dead air.

## Solution

Add a shimmer animation that sweeps left-to-right across status text during both stages of task creation. Additionally, redesign the name confirmation view with a checkmark recap style so it's visually distinct from the prompt input view.

## Design

### New Model State

Add to the `model` struct in `tui_model.go`:

- `creationProgress core.TaskProgressStep` — tracks the current progress step
- `creationSteps []creationStepEntry` — accumulates completed steps for checkmark display
- `shimmerTick int` — counter for shimmer animation position (incremented by tick command)

New types:

```go
type creationStepEntry struct {
    label string
    done  bool
}

type taskProgressMsg struct {
    step    core.TaskProgressStep
    message string
}

type shimmerTickMsg struct{}
```

### Shimmer Rendering

A `renderShimmer(text string, tick int) string` function in `tui_style.go`:

- Takes a string and the current tick position
- Most characters render in `colorDimmed` (#7b7b8e)
- A window of ~4 characters around the tick position renders brighter, interpolating toward `colorPrimary` (#c8c8d4)
- When the tick exceeds text length + padding, it wraps to 0
- Returns a string with per-character ANSI color codes via lipgloss
- Prefixed with `●` in `warningStyle` (#c4a24e)

Tick command fires every ~60ms via `tea.Tick` and increments `m.shimmerTick`.

### Prompt Input View — During Name Suggestion

Current behavior: user presses Enter, sees "Working..." in list header.

New behavior: after pressing Enter on the prompt input:
1. `m.creationProgress` is set to `TaskProgressNaming`
2. Shimmer tick command starts firing
3. `promptInputView()` renders the shimmer text below the (now blurred) input:

```
✦ Create Task

Enter the task prompt. Press Enter to submit, or Esc to cancel.
tab to switch provider: codex / claude

add dark mode toggle to settings page

● Suggesting name...          ← shimmer sweep left-to-right
```

### Name Confirm View — Checkmark Recap

Redesign `nameConfirmView()` to visually distinguish it from prompt input:

```
✦ Create Task

✔ add dark mode toggle to settings page
✔ provider: codex

▸ Name: dark-mode-settings-toggle|

Enter to create · Esc to cancel
```

- Prompt line: green checkmark (`healthyStyle`) + dimmed text
- Provider line: green checkmark + provider in its color (codex=teal, claude=coral)
- Name input: `▸ Name:` label in `warningStyle`, input value active
- Help text at bottom in `dimStyle`

### Name Confirm View — During Task Creation

After user confirms name and presses Enter, progress steps appear below with shimmer on the active step:

```
✦ Create Task

✔ add dark mode toggle to settings page
✔ provider: codex
✔ name: dark-mode-settings-toggle

✔ Creating worktree
● Launching agent...          ← shimmer sweep
```

Completed steps show green checkmarks. The current step shows `●` with shimmer animation. Steps accumulate as they complete.

### Progress Callback Wiring

The `createTaskCmd` function bridges the progress callback into Bubble Tea messages:

1. Create a `chan core.TaskProgress` channel
2. Pass a callback that sends each progress event to the channel
3. Launch the `CreateTaskWithProgress` call in a goroutine
4. Return a command that reads from the channel — each read yields a `taskProgressMsg`
5. When the channel closes (creation done), the final `createFinishedMsg` is returned from a separate command watching the goroutine result

For the name suggestion stage, no callback wiring is needed — just set `creationProgress = TaskProgressNaming` before dispatching and clear it when `suggestNameFinishedMsg` arrives.

### Progress Step Labels

| TaskProgressStep | Display Label |
|---|---|
| `naming` | Suggesting name... |
| `worktree_creating` | Creating worktree... |
| `workspace_seeding` | Seeding workspace... |
| `tmux_starting` | Starting session... |
| `agent_launching` | Launching agent... |
| `task_created` | Task created |

Steps `name_selected`, `workspace_seeded`, and `session_opening` are skipped (transitional, not user-visible).

### State Cleanup

When `createFinishedMsg` is received:
- Clear `creationProgress`, `creationSteps`, `shimmerTick`
- Stop the tick command (by not returning a new tick in `Update`)
- Transition to list view as before

When creation fails (error in `createFinishedMsg`):
- Same cleanup
- Show error in the name confirm view via `m.err`

### Files to Modify

1. **`internal/adapters/handler/cli/tui_model.go`** — model struct, new message types, Update handlers for `taskProgressMsg` and `shimmerTickMsg`, view rendering changes for `promptInputView()` and `nameConfirmView()`
2. **`internal/adapters/handler/cli/tui_style.go`** — `renderShimmer()` function
3. **`internal/adapters/handler/cli/tui_model.go`** — `createTaskCmd()` rewrite to use channel-based progress forwarding
