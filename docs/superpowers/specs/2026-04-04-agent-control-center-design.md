# Agent Control Center Design

## Summary

Refocus `agent` from a CLI-first task launcher into a TUI-first agent control center for task-oriented development sessions. The core workflow remains one task per git worktree and one task per tmux session, but the product should now treat a managed tmux session as a structured workspace with a stable `agent` window for automation and a seeded `editor` window for human work.

The first implementation can stay single-repo in practice: create tasks from the current repository, track them in SQLite, and drive most user interaction through `agent tui`. The state model and identifiers should still be designed so a future multi-repo dashboard can be added without changing the core task contract.

## Goals

- Make `agent tui` the primary interface for day-to-day task management.
- Preserve the task model of one task = one git worktree = one tmux session.
- Support a hybrid tmux ownership model:
  - `agent` owns the existence and naming of the `agent` window
  - `agent` seeds an `editor` window by default
  - users own any extra windows, splits, and editor customization
- Ensure all automation targets the `agent` window explicitly rather than the active pane.
- Keep CLI commands as the orchestration backend for creation, listing, opening, status, and health checks.
- Store enough task metadata to support a later global multi-repo control center.

## Non-Goals

- Full multi-repo task creation and browsing in the first cut.
- A generic multi-tool or multi-agent orchestration platform.
- Owning the entire tmux layout after creation.
- Web UI, Docker sandboxing, MCP management, or provider-agnostic mission-control scope.
- Rich inferred agent-state semantics based on transcripts or process introspection.

## Product Shape

### Primary Interface

The product should be TUI-first.

The expected user behavior is:

- launch `agent tui`
- browse tracked tasks
- create or open a task from that screen
- switch into a task session when focused work is needed
- return to the TUI to move between tasks

The CLI remains necessary, but mainly as:

- the backend command surface used by the TUI
- a fallback interface for scripting or quick shell use
- the lowest-friction way to integrate future automation

The design should not optimize for frequent direct CLI use at the expense of the TUI workflow.

### V1 Scope

V1 only needs to manage tasks created from the current repository, but all persisted task records should include the repository root and repo name so later multi-repo views can filter and group cleanly.

The single-repo limitation is a workflow boundary, not a data-model boundary.

## Session Contract

Each managed task session follows a hybrid contract.

### Required Invariants

- Every task has exactly one managed tmux session.
- Every managed session has an `agent` window.
- All provider launch, restart, and send-input actions target `session:agent`.
- Every task has exactly one managed worktree and branch.

If the `agent` window is missing, the task is no longer safely automatable and should be treated as broken drift.

### Seeded Defaults

At creation time, `agent` should also create:

- an `editor` window rooted in the worktree

The default expectation is that users will run `nvim` there, but the tool does not need to launch or monitor `nvim` itself in v1.

### User-Owned Surface

After creation, users may:

- add extra windows
- split panes
- rename panes inside user-owned windows
- customize the editor workflow however they like

This drift is acceptable as long as the required `agent` window remains intact.

### Drift Policy

Drift handling should match the hybrid contract:

- missing `agent` window: broken
- missing tmux session: broken
- missing worktree: broken
- missing `editor` window: degraded but recoverable
- extra windows or splits: informational only

The tool should only repair the minimum contract needed for automation in a future repair command.

## TUI Design

### Purpose

`agent tui` should evolve from a cleanup screen into the main control center for active tasks.

The first TUI should answer these questions quickly:

- what tasks exist
- which task belongs to which repo and branch
- whether the worktree and tmux session still exist
- whether the `agent` and `editor` windows are present
- which task should be opened next

### Main List

Each row should show at least:

- display name
- repo name
- branch name
- provider
- high-level status
- tmux session presence
- worktree presence
- `agent` window presence
- `editor` window presence

The selected row is the active task for keyboard actions.

### Primary Actions

The TUI should prioritize these actions:

- open the selected task session
- refresh task state
- create a new task
- inspect task details
- clean up task runtime resources

Cleanup remains useful, but it should no longer define the entire product.

### New Task Flow

The TUI should be able to trigger the same orchestration path as `agent new`.

That flow should:

1. collect a task prompt
2. generate a proposed task title
3. allow confirmation or editing of the title
4. create branch and worktree
5. create the tmux session with `agent` and `editor` windows
6. launch Codex in the `agent` window
7. refresh the task list
8. attach or switch into the created session

The prompt-entry UX can stay simple in the first cut. A modal, inline form, or dedicated creation screen are all acceptable as long as the flow stays keyboard-driven.

## CLI Surface

The CLI remains the backend contract for orchestration.

### `agent new "<prompt>"`

Creates a task from the current repository, sets up the branch and worktree, creates the tmux session, creates the `agent` and `editor` windows, launches Codex in `session:agent`, persists the task, and opens the session.

### `agent ls`

Lists tracked tasks with reconciled live state. It remains useful for scripting and debugging, but it is not the primary operator workflow.

### `agent open <task>`

Opens the task’s tmux session.

### `agent status <task>`

Shows the full persisted task record plus live reconciliation details, including tmux window contract health.

### `agent doctor`

Validates required binaries, state storage, tmux usability, and repository context.

### Future Command

Reserve `agent repair <task>` for restoring the minimum automation contract later:

- recreate a missing `agent` window
- optionally recreate a missing `editor` window
- never rewrite user-owned extra layout

## Architecture

The architecture should stay aligned with the current split between CLI handlers, core orchestration, and system adapters.

### Core Service

The core should own:

- task lifecycle rules
- naming
- reconciliation
- status derivation
- drift classification against the tmux session contract

The core should not shell out directly.

### CLI/TUI Adapter

The CLI adapter remains responsible for:

- Cobra commands
- Bubble Tea screens and keyboard flows
- prompt and confirmation UX
- rendering reconciled task state for humans

The TUI should call the same core service actions as the CLI commands rather than inventing a separate orchestration path.

### Git Adapter

The git adapter remains responsible for:

- repo validation
- base branch detection
- branch creation
- worktree creation
- worktree deletion
- branch and worktree reconciliation checks

### Tmux Adapter

The tmux adapter should expose window-aware operations rather than session-only operations.

It must support:

- create session rooted at a worktree
- create named windows
- check whether a named window exists
- send keys to a named window
- restart or respawn the pane in a named window
- attach or switch to the session
- kill the session

This is the key architectural difference from looser session managers: the tmux adapter must understand the stable `agent` target explicitly.

## Task Model

The persisted `Task` model should include at least:

- `id`
- `prompt`
- `display_name`
- `slug`
- `repo_root`
- `repo_name`
- `base_branch`
- `branch_name`
- `worktree_path`
- `tmux_session`
- `agent_window_name`
- `editor_window_name`
- `provider`
- `status`
- `created_at`
- `updated_at`
- `last_error`

The reconciled live view should also expose:

- `session_exists`
- `worktree_exists`
- `branch_exists`
- `agent_window_exists`
- `editor_window_exists`
- `last_reconciled_at`

## Status Model

The status model should stay narrow and truth-preserving.

Suggested values:

- `creating`
- `ready`
- `running`
- `degraded`
- `broken`
- `cleaned`

Interpretation:

- `ready`: task resources exist and no active provider lifecycle is assumed
- `running`: task resources exist and the task has been launched for interactive use
- `degraded`: the required automation contract still works, but a non-critical seeded resource such as `editor` is missing
- `broken`: a required resource is missing or unrecoverably inconsistent
- `cleaned`: runtime resources were intentionally deleted

The tool should not claim more detailed agent-runtime semantics than it can verify reliably.

## What To Reuse From Agent-Deck

Useful ideas to adapt:

- durable task/session registry
- clear list and status surfaces
- attach/resume ergonomics
- worktree lifecycle visibility
- explicit drift detection between persisted and live state

Ideas to reject for this product:

- tool-agnostic control abstractions
- pane-targeting based on whatever is currently active
- broad mission-control scope beyond task sessions
- web, Docker, conductor, or MCP management features in v1

## Testing

Testing should focus on the session contract and TUI behavior.

### Core Tests

Add or extend service tests for:

- creating a task with both `agent` and `editor` windows
- classifying missing `agent` window as broken
- classifying missing `editor` window as degraded
- keeping extra windows from affecting task health
- opening a task by tmux session name
- cleaning up runtime resources while preserving the branch

### Tmux Adapter Tests

Add adapter tests for:

- creating named windows
- targeting `session:agent` consistently
- reconciling named-window presence
- respawning only the `agent` window pane

### TUI Tests

Add focused model tests for:

- rendering the richer task list
- opening the selected task
- entering task creation flow
- refreshing live state
- cleanup confirmation behavior

## Migration Notes

This design updates the product framing more than the storage model.

The immediate work should:

- keep the CLI commands intact
- make the TUI the primary operator experience
- ensure the tmux adapter and status model reflect the real multi-window contract
- update stale docs that still describe single-window sessions

Multi-repo support should be deferred until the single-repo TUI-first workflow feels complete and stable.
