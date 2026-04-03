# Agent Cleanup TUI Design

## Summary

Add a keyboard-driven terminal UI to `agent` for inspecting tracked tasks and cleaning up their runtime resources. The first cut is cleanup-focused: it lists tasks from SQLite, shows their live tmux/worktree state, and lets the user delete the tmux session and worktree with a confirmation flow.

This feature extends the existing CLI rather than replacing it. The TUI is a new frontend over the same orchestration core, with Bubble Tea used for rendering and keyboard handling.

## Goals

- Add a new `agent tui` command.
- Render tracked tasks in a full-screen Bubble Tea interface.
- Support vi-style keyboard navigation.
- Show enough per-task state to understand what still exists before deleting anything.
- Let the user press `x` to trigger cleanup for the selected task.
- Confirm deletion before any destructive action.
- Delete the tmux session and worktree directory, but keep the git branch.
- Persist the reconciled post-cleanup state back to SQLite.

## Non-Goals

- No mouse-first UI.
- No search, fuzzy filtering, sorting controls, or multi-pane dashboard in the first cut.
- No branch deletion.
- No hard deletion of task rows from SQLite.
- No merge-aware cleanup logic in this first version.

## Command Surface

### `agent tui`

`agent tui` opens a Bubble Tea full-screen interface backed by the existing task state store.

The first version is a cleanup-oriented task browser, not a full dashboard. It should read all tracked tasks through the core service, reconcile live tmux/worktree state before display, and refresh on demand.

## TUI Behavior

### Main List

Each row should show at least:

- display name
- status
- tmux session presence
- worktree presence
- branch name

The selected row is the active target for keyboard actions.

### Keybindings

- `j` / `k` move selection
- `g` jumps to the top
- `G` jumps to the bottom
- `r` refreshes the task list
- `x` opens a delete confirmation for the selected task
- `q` quits

### Confirmation Flow

When the user presses `x`, the TUI should switch into a confirmation state for the selected task.

Confirmation keys:

- `y` confirms deletion
- `n` cancels deletion
- `esc` cancels deletion
- `q` cancels deletion but does not quit the entire TUI

The confirmation copy should make the deletion scope explicit: delete the tmux session and worktree, keep the git branch.

## Cleanup Semantics

Cleanup is resource deletion, not task deletion.

When the user confirms cleanup:

1. Kill the tmux session if it exists.
2. Remove the git worktree if it exists.
3. Leave the git branch in place.
4. Reconcile the task record against the new reality.
5. Persist the updated task state to SQLite.

If the tmux session or worktree is already missing, cleanup should still succeed as long as the remaining step completes and the persisted task record reflects the true final state.

## Architecture

The TUI should live in the CLI adapter layer, not in the orchestration core.

### CLI Layer

- Add a new Bubble Tea-backed command in `internal/adapters/handler/cli`.
- The TUI model should own screen state, selected row, confirmation mode, and user-facing error messages.

### Core Layer

- Add a new service action for cleanup, for example `DeleteTaskResources(ctx, idOrSlug string)`.
- The core service should own cleanup orchestration, reconciliation, status updates, and event recording.

### Repository Ports

Extend existing ports rather than creating a parallel cleanup subsystem.

- `TmuxRepository`
  - add `KillSession`
- `GitRepository`
  - add `RemoveWorktree`
- `TaskRepository`
  - keep persisted task rows
  - continue updating task state rather than deleting rows

## Status Model

User-confirmed cleanup is not a failure state, so it should not reuse `broken`.

Add a new explicit task status:

- `cleaned`

This status means the tracked task still exists in SQLite, but its worktree and tmux session were intentionally removed through the tool.

`broken` remains reserved for unexpected drift or cleanup failures.

### Reconciliation Rules

Reconciliation must treat `cleaned` as an intentional absence state rather than applying the normal "missing resource means broken" rule.

Rules:

- if a task status is `cleaned` and both the tmux session and worktree are absent, reconciliation should keep it as `cleaned`
- if a task status is `cleaned` and either resource reappears unexpectedly, reconciliation should mark it `broken`
- if a task is not `cleaned` and either resource is missing unexpectedly, reconciliation should continue marking it `broken`

This prevents a successful cleanup from being reclassified as a failure on the next refresh.

## Error Handling

Cleanup should be stepwise and truth-preserving rather than all-or-nothing.

If tmux deletion succeeds but worktree removal fails:

- the tmux session should remain marked absent
- the worktree should remain marked present
- the task should record the failure reason
- the TUI should surface the failure inline without crashing

Partial cleanup is a failure state, not a successful cleanup state.

Rules:

- successful cleanup ends in `cleaned`
- partial cleanup ends in `broken`
- the persisted booleans must still reflect the real surviving resources
- successful cleanup clears `last_error`
- rerunning cleanup from a partial state is allowed and should be treated as idempotent retry behavior
- if a later retry removes the remaining resources successfully, the task should transition from `broken` to `cleaned`

After each destructive step, the service should reconcile and persist the current truth so the database does not drift away from the filesystem or tmux.

## Testing

Most tests should stay below the TUI.

### Core Tests

Add service tests for:

- deleting a task with both resources present
- deleting a task when the tmux session is already missing
- deleting a task when the worktree is already missing
- partial failure during cleanup
- preserving the git branch while updating task state to `cleaned`

### TUI Tests

Add focused model tests for:

- list navigation with `j`, `k`, `g`, `G`
- entering and leaving confirmation mode
- dispatching cleanup for the selected task
- refreshing the list

Real tmux integration does not need to be tested in the Bubble Tea layer.

## Implementation Notes

- Use Bubble Tea for the first implementation.
- Keep the initial screen single-purpose and minimal.
- Design the cleanup action so a future plain CLI command can reuse it without changing the core behavior.
