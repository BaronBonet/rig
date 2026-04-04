# TUI Visual Polish Design

## Goal

Transform the plain-text Bubble Tea TUI into a visually polished interface using lipgloss styling. The design follows a **minimal + icons hybrid**: calm, muted palette with selective color and emoji iconography for clarity.

## Files Changed

- **New: `internal/adapters/handler/cli/tui_style.go`** — All lipgloss styles, color constants, and icon definitions. Keeps styling separate from logic.
- **Modified: `internal/adapters/handler/cli/tui_model.go`** — Update the 4 view methods to use lipgloss-styled rendering.
- **Modified: `internal/adapters/handler/cli/tui_model_test.go`** — Update test assertions to account for styled output (ANSI escape codes).

## Color Palette

| Role        | Hex       | Usage                                      |
|-------------|-----------|---------------------------------------------|
| Primary     | `#c8c8d4` | Normal text                                 |
| Dimmed      | `#7b7b8e` | Secondary text, keybindings, unselected rows |
| Accent      | `#6c6ce0` | Selected row border, prompt cursor, title    |
| Healthy     | `#5a9e6f` | Active status, healthy indicators, "yes"     |
| Warning     | `#c4a24e` | Creating/in-progress status, cleanup header  |
| Error       | `#c05050` | Error messages, cancel hints                 |

Background is implicit (terminal default) for maximum compatibility.

## Icons

### Context Icons (detail bar)

| Icon | Meaning   |
|------|-----------|
| 📁   | Repository |
| 🤖   | Agent window |
| 📝   | Editor window |
| 🌿   | Branch     |
| 💻   | Tmux session |
| 🌳   | Worktree   |

### Status Indicators

| Symbol | Meaning                |
|--------|------------------------|
| `●`    | Active / healthy       |
| `○`    | Idle / missing         |
| `◐`    | In-progress / creating |

### View Header Icons

| Icon | View              |
|------|-------------------|
| `◈`  | Control Center (list) |
| `✦`  | Create Task        |
| `⚠`  | Confirm Cleanup    |

## View Designs

### List View

```
◈ Control Center                    j/k move · enter open · n new · x clean · q quit

📁 myapp  🤖 healthy  📝 healthy  🌿 feat/fix-auth  💻 yes  🌳 yes

▸ fix-auth-bug                                                    ● active
  add-user-profile                                                ○ idle
  refactor-db-layer                                               ○ idle
  migrate-to-postgres                                             ◐ creating
```

- Title rendered in accent color, keybindings dimmed, separated by a thin horizontal rule (lipgloss border)
- Detail bar: shows selected task metadata with icons, dimmed text, colored health values
- Selected row: purple left-border accent (`BorderLeft`), bold/bright text, `▸` marker
- Unselected rows: dimmed text, two-space indent
- Status column: color-coded per status (green=active, dimmed=idle, amber=creating)
- Loading state: "Loading tasks..." in dimmed text
- Busy state: "Working..." in dimmed text
- Empty state: "No tasks found. Press n to create one." in dimmed text
- Error: rendered in error color above the task list

### Prompt Input View

```
✦ Create Task

Enter the task prompt. Press Enter to continue, Esc to cancel.

❯ _
```

- Header in accent color with ✦ icon
- Instructions in dimmed text
- Prompt character `❯` in accent color (replaces `> `)
- Error (if any) in error color between instructions and input

### Name Confirm View

```
✦ Confirm Task Name

Edit the suggested name if needed. Press Enter to create, Esc to cancel.

prompt: Fix the authentication bug in login flow

❯ fix-auth-bug_
```

- Same header style as prompt input
- Prompt text in dimmed color
- Input with accent-colored `❯`

### Cleanup Confirmation View

```
⚠ Confirm Cleanup

Task: fix-auth-bug
The tmux session and worktree will be deleted.
The branch will be kept.

y confirm · n cancel
```

- Header in warning/amber color with ⚠ icon
- Task name in primary (bright) text
- Description in dimmed text
- `y` in green, `n` in red

## Style Architecture (`tui_style.go`)

```go
// Color constants
const (
    colorPrimary = lipgloss.Color("#c8c8d4")
    colorDimmed  = lipgloss.Color("#7b7b8e")
    colorAccent  = lipgloss.Color("#6c6ce0")
    colorHealthy = lipgloss.Color("#5a9e6f")
    colorWarning = lipgloss.Color("#c4a24e")
    colorError   = lipgloss.Color("#c05050")
)

// Icon constants
const (
    iconRepo     = "📁"
    iconAgent    = "🤖"
    iconEditor   = "📝"
    iconBranch   = "🌿"
    iconTmux     = "💻"
    iconWorktree = "🌳"
    // ... status and header icons
)

// Lipgloss styles
var (
    titleStyle       // accent color, bold
    dimStyle         // dimmed foreground
    errorStyle       // error color
    warningStyle     // warning color
    selectedRowStyle // left border accent, bold
    normalRowStyle   // dimmed
    healthyStyle     // green foreground
    statusStyles     // map of status -> style
)
```

## Test Strategy

The existing tests in `tui_model_test.go` assert on view output using `strings.Contains`. Since lipgloss wraps text in ANSI escape codes, tests need to be updated:

- Use `lipgloss.NewRenderer(io.Discard)` or strip ANSI codes before asserting content
- Alternatively, assert on the presence of key content strings which will still be embedded within the ANSI sequences — `strings.Contains` works through ANSI codes for the text payload
- Verify: read the existing tests to confirm which approach is simpler before implementing

## Status Display

Status indicators are purely cosmetic mappings of the existing `task.Status` string. No new status detection logic is added. The color mapping is:

- Green (`●`): status values indicating active/running states
- Amber (`◐`): status values indicating transitional states
- Dimmed (`○`): everything else (idle, unknown, empty)

The exact mapping will be determined by reading the existing status values in the codebase.

## Out of Scope

- Terminal size / responsive layout (not needed for current views)
- Configurable themes / user color preferences
- Animated transitions or spinners
- New status detection logic (e.g., LLM active vs waiting for input)
