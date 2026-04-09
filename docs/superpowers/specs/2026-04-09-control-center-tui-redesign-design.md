# Control Center TUI Redesign

## Problem

The current TUI suffers from information overload in the task detail panel. It displays internal identifiers (session ID, transcript path, hook cwd, start source) and raw hook events that provide no actionable value to the user. The task list rows also show noisy preview text (last command run) that doesn't add clarity.

## Design

### Task List Rows

Each task occupies one line with these columns:

| Column | Description |
|--------|-------------|
| TASK | Display name of the task |
| PROVIDER | Provider icon + name (⚡ codex, ✦ claude) |
| PR | PR status icon — green for open, purple for merged, blank if none |
| TIME | Clock icon + elapsed running time (e.g. `2h 13m`) |
| STATUS | Status icon + label (● working, ◐ needs input, ○ finished, etc.) |

**Removed from rows:** The `hookPreview` text (last command/prompt/assistant message) that was appended after the task name.

### Detail Panel (Two-Column Layout)

Below the task list, separated by a horizontal rule, the selected task's details appear in two columns:

**Left column — Git:**
- Branch name (icon: nf-dev-git_branch / 🌿)
- Repo name (icon: nf-oct-repo / 📁)
- PR status with number (icon: nf-dev-git_pull_request or nf-dev-git_merge / ◉ or ✔)

**Right column — Session:**
- Running time (icon: nf-fa-clock_o / 🕐)
- Process connection status (icon: nf-fa-plug / 🔌)
- Last human prompt (icon: nf-fa-user / 👤)
- Last LLM output (icon: nf-md-robot / 🤖)

**Recency indicator:** The most recent of prompt/output renders in bright text with bold weight. The older one renders dimmed. This makes it immediately clear whether the LLM responded last or the user prompted last.

**Truncation:** All text values (branch names, prompts, outputs) are truncated with `…` when they exceed the available column width.

### Fields Removed from Detail Panel

The following fields from the current `selectedTaskDetailView` are removed:

- Repo Root (`task.RepoRoot`)
- Worktree Path (`task.WorktreePath`)
- Tmux Session (`task.TmuxSession`)
- Session ID (`hook.SessionID`)
- Model (`hook.Model`)
- Hook Cwd (`hook.Cwd`)
- Transcript Path (`hook.TranscriptPath`)
- Start Source (`hook.StartSource`)
- Preview (replaced by separate prompt/output fields)
- Entire "Recent Hook Events" section

### Fields Added

- PR status (new data — requires GitHub API integration)
- Elapsed running time in task list rows
- Separate last human prompt and last LLM output with recency indicator

### PR Status Data

PR status is not currently available in `core.Task`. Implementation requires:

1. **New field on Task or TaskView:** PR state (open/merged/none) and PR number.
2. **GitHub API integration:** Use `gh` CLI or GitHub API to check PR status for a branch.
3. **Caching strategy:** Lazy-load with a 1-minute TTL cache.
   - On any TUI interaction (navigation, selection), check if the cache is stale (>1 minute).
   - If stale, trigger a background refresh.
   - On explicit refresh (`r` key), invalidate cache and fetch immediately.
   - Cache is per-task, keyed by branch name.

### Icon System

The TUI uses Nerd Font icons as primary glyphs with Unicode fallbacks. Detection of Nerd Font availability determines which set to use.

| Field | Nerd Font | Codepoint | Unicode Fallback |
|-------|-----------|-----------|-----------------|
| branch | nf-dev-git_branch | U+E725 | 🌿 |
| repo | nf-oct-repo | U+F401 | 📁 |
| pr open | nf-dev-git_pull_request | U+E726 | ◉ (green) |
| pr merged | nf-dev-git_merge | U+E727 | ✔ (purple) |
| time | nf-fa-clock_o | U+F017 | 🕐 |
| process | nf-fa-plug | U+F1E6 | 🔌 |
| human prompt | nf-fa-user | U+F007 | 👤 |
| llm output | nf-md-robot | U+F06A9 | 🤖 |

**Provider icons remain unchanged:** ⚡ codex, ✦ claude.

### Hook Events

Raw hook events are no longer displayed in the UI. However, hook event recording must continue — `IngestHookEvent` updates `HookSessionSummary`, which is the source of the `LastPromptText` and `LastAssistantMessage` fields shown in the Session column.

The `GetTaskHookEvents` service method and `loadSelectedHookEventsCmd` in the TUI can be removed, along with the `hookEvents` / `hookEventsTaskID` fields on the model.

### Files to Modify

| File | Changes |
|------|---------|
| `internal/adapters/handler/cli/tui_model.go` | Rewrite `listView`, `selectedTaskDetailView`, remove hook event loading, add PR column and time column, add two-column detail layout |
| `internal/adapters/handler/cli/tui_style.go` | Add icon constants (Nerd Font + fallback), add Nerd Font detection helper |
| `internal/core/domain.go` | Add PR status fields to `TaskView` or new `PRStatus` struct |
| `internal/core/ports.go` | Add `PRStatusChecker` port interface |
| `internal/core/service.go` | Add PR status caching logic, remove `GetTaskHookEvents` if unused elsewhere |
| New: `internal/adapters/client/github/pr_status.go` | GitHub API adapter for PR status lookup |

### Existing Behavior Preserved

- All keyboard shortcuts unchanged (j/k, enter, n, x, r, q, g/G)
- Task creation flow (prompt input, name confirm) unchanged
- Cleanup confirmation dialog unchanged
- Observer subscription and real-time updates unchanged
- Task filtering (visible = session or worktree exists) unchanged
