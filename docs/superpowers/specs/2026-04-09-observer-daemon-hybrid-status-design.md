# Observer Daemon Hybrid Status Design

## Summary

This design replaces the current TUI-owned live observability flow with a small long-running local observer daemon. The observer ingests Codex hook events, watches managed tmux sessions for runtime changes, derives a user-facing hybrid status model, persists that state into SQLite, and publishes lightweight live updates to the TUI.

The TUI remains the only intended user entrypoint. Running `agent` should auto-start the observer if it is not already running, then connect to it for live updates. Quitting the TUI should not stop the observer.

## Goals

- Preserve `needs_input` as the highest-value live status for managed tasks.
- Use Codex hooks as enrichment data instead of replacing the existing runtime model.
- Remove raw hook mechanics like `PostToolUse` from primary UI state.
- Provide live TUI updates without manual refresh.
- Keep the TUI restartable by treating SQLite as the durable source of truth.
- Auto-start observability infrastructure from the TUI so users do not manage it manually.

## Non-Goals

- Redesign the product around Codex app-server.
- Replace SQLite with an in-memory or stream-only model.
- Make the TUI parse raw hook payloads or raw tmux streams directly.
- Expose the observer as a remote network service.
- Infer hidden internal LLM states such as "thinking" that current signals do not support reliably.

## Problem With The Current Hook View

The current hook-backed TUI view exposes hook internals rather than user value:

- `needs_input` can be masked by hook-derived phases.
- `PostToolUse` and similar event names show up as visible state even though they only describe low-level hook mechanics.
- Hook phases like `idle` are too vague to explain what the user should care about.

This design corrects that by splitting the model into:

- authoritative top-level task status
- optional activity detail
- rich session metadata in the selected-task pane

## Recommended Approach

Introduce a local observer daemon as the observability backend for `agent`.

Responsibilities:

- receive Codex hook events
- watch tmux activity for managed sessions
- derive task runtime status and activity detail
- persist hook history and current summaries into SQLite
- publish small local push updates to connected TUI clients

The TUI should:

- load task and session snapshots from SQLite on startup
- connect to the observer for live updates
- render the observer-derived hybrid status model
- fall back gracefully to persisted SQLite state if the observer stream is temporarily unavailable

This keeps ingestion and derivation outside the UI while preserving restartability.

## Alternatives Considered

### 1. TUI directly subscribes to hooks and tmux

Rejected because the TUI would become responsible for ingestion, derivation, persistence coordination, and rendering. It would also mean no live backend exists when the TUI is closed.

### 2. Keep the current manual-refresh model

Rejected because it preserves the least useful part of the current workflow. The main value of the observer is push-based state changes without `r`.

### 3. Hook-only status model

Rejected because hooks do not expose a reliable first-class `needs_input` signal, and raw hook events are weaker than the current tmux/runtime path for that state.

## Architecture

The system should split into three parts:

### TUI client

The only normal user entrypoint.

Responsibilities:

- start via `agent`
- ensure the observer is running
- read tasks and observability snapshots from SQLite
- subscribe to observer push updates
- render task list and selected-task detail views

### Observer daemon

A small local-only long-running process.

Responsibilities:

- accept Codex hook events over a local ingestion endpoint
- watch managed tmux sessions for runtime changes
- derive hybrid task status
- update SQLite
- publish local task-update notifications

Non-responsibilities:

- not owning task creation business logic
- not replacing SQLite as source of truth
- not serving remote clients by default

### SQLite store

The durable source of truth.

Responsibilities:

- store tasks
- store raw hook event history
- store latest hook session summaries
- store latest observer-derived runtime snapshots if needed for fast reads

## Observer Lifecycle

The intended workflow is TUI-only.

- user runs `agent`
- `agent` starts the TUI
- before the TUI fully initializes, it checks whether the observer is reachable
- if the observer is not running, `agent` starts it in the background and waits for a healthy response
- if the observer is already running, the TUI reuses it
- when the TUI exits, the observer continues running
- the observer stops only when explicitly stopped from the TUI or by system/process shutdown

This means users do not manually manage the observer in normal operation.

## Transport

The observer should remain local-only.

Recommended transports:

- local HTTP endpoint for Codex hook ingestion
- Unix domain socket for TUI subscription and control traffic

Why Unix sockets instead of WebSockets:

- local-only by default
- no TCP port management
- simpler operational model for a terminal app
- easier to treat as private process-to-process infrastructure

## Data Flow

### Hook path

1. Codex hook script posts a raw event to the observer.
2. The observer maps the event to a managed task using worktree `cwd` and known session metadata.
3. The observer writes the raw event to `task_hook_events`.
4. The observer updates the latest hook session summary in `task_hook_sessions`.
5. The observer recomputes the visible task status if the hook event affects current activity detail.
6. The observer emits a small task-update notification to connected TUI clients.

### tmux path

1. The observer watches managed tmux sessions for activity changes.
2. When a task session changes, the observer reruns existing runtime detection for that task.
3. The observer updates the persisted task runtime snapshot.
4. The observer recomputes the hybrid display status.
5. The observer emits a task-update notification to connected TUI clients.

### TUI path

1. On startup, the TUI loads tasks and observability summaries from SQLite.
2. The TUI renders immediately from persisted state.
3. The TUI subscribes to the observer stream.
4. As task updates arrive, the TUI refreshes only affected rows and the selected-task pane.

## User-Facing Status Model

The visible model should be intentionally small.

### Primary status

- `finished`
- `needs_input`
- `working`
- `disconnected`

### Optional activity detail

- `command`

### Display forms

- `finished`
- `needs_input`
- `working`
- `working · command`
- `disconnected`

This replaces vague states like `idle` and prevents raw hook events from becoming top-level state.

## Status Derivation

Status precedence:

1. `finished`
2. `needs_input`
3. `working`
4. `disconnected`

Derivation rules:

- `finished`
  - derived from existing task lifecycle and session reconciliation rules
- `needs_input`
  - derived from the current tmux/runtime detector
  - highest-priority live state
- `working`
  - a live Codex or Claude process is currently detected in the managed tmux session
- `working · command`
  - same as `working`, plus hooks show an unmatched `PreToolUse` or another active command/tool execution
- `disconnected`
  - no live managed Codex or Claude process is currently detected in the managed tmux session

Important rule:

- hooks refine `working`
- hooks do not override `needs_input`
- hooks do not replace `finished`

## Hook Metadata Model

Hooks remain valuable, but as enrichment data rather than top-level state.

The TUI can surface:

- last command
- last assistant message
- last activity time
- command count
- session source
- model
- transcript path
- recent event timeline

The observer should continue storing raw hook events and derived session summaries so the selected-task pane can show useful context without leaking hook internals into the main status badge.

## SQLite Model

The existing two-table hook model remains appropriate:

### `task_hook_events`

Append-only raw history for timeline views and future derivation changes.

### `task_hook_sessions`

Latest derived hook session summary for fast reads.

This design may also add a lightweight persisted runtime snapshot if the current task table does not already capture enough live observer-derived status for startup rendering. That decision should be made in implementation planning based on the current repository boundary.

## TUI Presentation

### Task list

Each row should show:

- task name
- provider
- primary status badge
- optional activity suffix when applicable
- compact last-activity indicator
- one short preview, preferring:
  - last command when status is `working · command`
  - otherwise last assistant message
  - otherwise last prompt

### Selected-task pane

The detail pane should show:

- primary status and activity detail
- session metadata such as model, worktree, transcript path, and session id
- last activity time
- last command
- last assistant message
- recent hook timeline

The detail pane should never present raw hook event names as the main answer to "what is happening now?" They are supporting context only.

## Error Handling

- If the observer is unavailable at TUI startup, `agent` should try to auto-start it before failing.
- If observer startup fails, the TUI should still open in degraded mode from SQLite and clearly show that live updates are unavailable.
- If the observer stream disconnects while the TUI is running, the TUI should keep functioning from persisted state and attempt reconnects.
- If a hook event cannot be mapped to a managed task, the observer should drop it as unmanaged without polluting user-facing task state.
- If tmux monitoring fails for a specific task, the observer should preserve the last known persisted state rather than blanking the task.

## Testing Strategy

Tests should focus on behavior, not raw transport mechanics.

- observer startup and singleton behavior
- hook ingestion updates SQLite and emits task updates
- tmux activity updates runtime state without manual refresh
- hybrid precedence preserves `needs_input` over hook-derived activity
- `working · command` appears only when a live task also has active command activity
- `disconnected` appears when the managed process is no longer present
- TUI startup renders correctly from SQLite before live updates arrive
- TUI reconnect behavior after observer disconnect

## Migration Notes

This design supersedes the earlier hook-only TUI phase model where phases like `ready`, `prompted`, `running_command`, and `idle` were surfaced directly in the UI.

Those internal derivations may still exist inside the observer if they help implementation, but they should not be exposed as the primary user-facing status model.

## Open Questions Resolved

- Should hooks replace the current runtime model?
  - No. Hooks enrich runtime state.
- Should `needs_input` remain?
  - Yes. It remains the highest-value live state.
- Should vague states like `idle` remain?
  - No. Replace with `disconnected`.
- Should `running_command` be a top-level peer state?
  - No. It becomes activity detail under `working`.
- Should the TUI own live subscriptions directly?
  - No. The observer daemon owns ingestion and streaming.
- Should the observer be manually managed?
  - No. It should auto-start from the TUI and remain running until explicitly stopped.
