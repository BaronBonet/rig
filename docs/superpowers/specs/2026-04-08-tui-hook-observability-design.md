# TUI Hook Observability Design

## Summary

This design adds richer Codex task observability to the TUI by using Codex hooks as the primary source of session metadata and turn activity when hooks are available. Hook events are written directly into SQLite, summarized into a fast-read session table, and optionally streamed to the TUI over a local socket for push-based updates.

The existing tmux-derived runtime model remains as the fallback path for sessions that do not have hook data.

## Goals

- Show more than `running`, `needs_input`, and `finished` for Codex-backed tasks.
- Surface session metadata and recent activity without requiring the user to open the task.
- Provide a richer selected-task detail pane with recent hook timeline and latest session content.
- Preserve the current restart behavior where the TUI can stop and start again and still show known session state.
- Keep hook ingestion outside the TUI so UI code consumes shaped data, not raw hook payloads.

## Non-Goals

- Redesign the product around Codex app-server.
- Remove the current tmux-based runtime path.
- Infer hidden internal model states such as "thinking" when hooks do not expose them reliably.
- Support unmanaged Codex sessions that were not launched with hooks enabled for this repo.
- Build a remote or browser client transport.

## Current Constraints

- Codex hooks are opt-in and only exist when `codex_hooks = true` is enabled in `config.toml`.
- The current Codex hook surface gives structured lifecycle events, not a full streaming model trace.
- `PreToolUse` and `PostToolUse` currently only expose Bash tool activity.
- `Stop` provides `last_assistant_message`, but hooks do not provide continuous assistant-token streaming.
- The current TUI only models `running`, `needs_input`, and `finished` in [internal/core/domain.go](/Users/ericbonet/software/tmux-llm-v1-refactor-brainstorm/internal/core/domain.go).

## Hook Data Available

Per the current Codex hooks contract, the repo can rely on these fields:

- Common fields on every hook:
  - `session_id`
  - `transcript_path`
  - `cwd`
  - `hook_event_name`
  - `model`
- `SessionStart`:
  - `source` (`startup` or `resume`)
- `UserPromptSubmit`:
  - `turn_id`
  - `prompt`
- `PreToolUse`:
  - `turn_id`
  - `tool_name`
  - `tool_use_id`
  - `tool_input.command`
- `PostToolUse`:
  - `turn_id`
  - `tool_name`
  - `tool_use_id`
  - `tool_input.command`
  - `tool_response`
- `Stop`:
  - `turn_id`
  - `stop_hook_active`
  - `last_assistant_message`

From those fields the TUI can confidently show:

- whether the session started fresh or resumed
- model and working directory
- transcript path
- current or latest turn id
- last prompt preview
- last Bash command preview
- last Bash command result preview
- last assistant message
- command count
- started at, last activity at, and last stop time

## Recommended Approach

Use a hooks-first observability path with two persisted SQLite tables and an optional local push stream:

- `task_hook_events` stores the raw append-only event history
- `task_hook_sessions` stores the latest derived summary per managed task/session
- the hook collector writes both directly
- the TUI loads the current summary from SQLite on startup
- the TUI subscribes to a local stream for live updates when available
- if no hook summary exists for a task, the TUI falls back to the existing tmux-derived runtime display

This approach preserves restartability, keeps the UI fast, and avoids putting hook parsing logic in the TUI.

## Alternatives Considered

### 1. Snapshot-only storage

The collector overwrites one latest-state row per session and does not store raw history.

Rejected because it makes the detail pane weaker and removes the ability to rebuild summary logic later.

### 2. TUI parses raw hook logs on startup and refresh

The collector keeps JSONL logs and the TUI derives all summaries itself.

Rejected because parsing and derivation would bleed into presentation code and the hook model would become harder to test and evolve.

### 3. WebSockets as the main live transport

The collector writes SQLite and pushes live updates over WebSockets.

Rejected for now because this is a local CLI tool. A local Unix domain socket is simpler, local-only by default, and avoids TCP port management. WebSockets can be added later if remote clients become a real requirement.

## Data Model

### `task_hook_events`

Append-only source of truth for hook history.

Fields:

- `id`
- `task_id`
- `session_id`
- `turn_id`
- `event_name`
- `occurred_at`
- `raw_payload_json`
- `last_assistant_message`
- `prompt_preview`
- `command_preview`
- `command_result_preview`
- `tool_use_id`

Notes:

- `task_id` links events to a managed task.
- `raw_payload_json` preserves the original hook payload.
- preview fields are denormalized convenience columns derived during ingestion.

### `task_hook_sessions`

Latest derived summary for fast TUI reads.

Fields:

- `task_id`
- `session_id`
- `model`
- `cwd`
- `transcript_path`
- `start_source`
- `current_turn_id`
- `last_event_name`
- `runtime_phase`
- `started_at`
- `last_activity_at`
- `last_stop_at`
- `last_prompt_preview`
- `last_command_preview`
- `last_command_result_preview`
- `last_assistant_message`
- `command_count`
- `updated_at`

Notes:

- one row per managed task
- rebuilt incrementally on each ingested hook event
- if phase derivation changes later, summaries can be recomputed from `task_hook_events`

## Task Mapping

The collector must associate each incoming hook event with an existing managed task.

Preferred mapping order:

1. exact `cwd` match to the task `worktree_path`
2. previously known `session_id` for that task
3. if neither match succeeds, reject the event as unmanaged and do not surface it in the TUI

This keeps observability scoped to tasks created and managed by this repo.

## Runtime Phase Model

The visible hook-derived phases should stay small and scannable.

Proposed phases:

- `ready`
  - hook summary exists
  - latest relevant event is `SessionStart`
  - no prompt submitted yet for the current session lifecycle
- `prompted`
  - latest relevant event is `UserPromptSubmit`
  - no later `PreToolUse`, `PostToolUse`, or `Stop` for that turn
- `running_command`
  - a `PreToolUse` exists without a later matching `PostToolUse` for the same `tool_use_id`
- `idle`
  - latest relevant event is `PostToolUse` or `Stop`
  - task/session remains otherwise active under existing task rules
- `resumed`
  - variant marker for sessions whose latest `SessionStart.source` is `resume`
  - shown as metadata and may optionally tint `ready` or `idle`, not a wholly separate badge if the UI becomes too noisy
- `finished`
  - existing terminal/end state from current task/session reconciliation rules

The design explicitly does not add a `thinking` phase because current hooks do not provide a reliable first-class signal for that state.

## Collector Responsibilities

The collector becomes the only component that interprets raw hook payloads.

Responsibilities:

- receive hook payloads from Codex hook scripts
- map each event to a managed task
- write one row into `task_hook_events`
- update the corresponding `task_hook_sessions` summary row
- emit a small derived update event to local subscribers

The collector should not depend on the TUI. It owns ingestion and summary derivation even when the TUI is not running.

## Live Update Transport

SQLite is the source of truth. The stream is only a push optimization.

Recommended transport:

- local Unix domain socket

Why:

- local-only by default
- no TCP port management
- simpler than WebSockets for a terminal app
- easy to treat as optional

Push payloads should be small, derived, and task-focused. They should not be raw hook payloads.

Suggested payload:

- `task_id`
- `session_id`
- `runtime_phase`
- `last_activity_at`
- `last_prompt_preview`
- `last_command_preview`
- `last_assistant_message`
- `command_count`

If the socket is unavailable, the TUI should continue working from SQLite and retain manual refresh behavior.

## TUI Layout

### Task List

Each task row should show:

- task name
- provider
- hook-derived phase badge when available, otherwise existing runtime badge
- compact elapsed or last-activity indicator
- one preview field

Preview priority:

1. `last_command_preview` while phase is `running_command`
2. `last_assistant_message`
3. `last_prompt_preview`

This keeps the list dense enough to triage quickly without turning every row into a transcript.

### Selected Task Detail Pane

The right-hand pane should show richer hook context for the selected task:

- top summary strip:
  - phase
  - model
  - session source
  - last activity
- session details:
  - session id
  - current turn id
  - cwd
  - transcript path
- latest content blocks:
  - last prompt
  - last command
  - last command result summary
  - last assistant message
- recent event timeline:
  - `SessionStart`
  - `UserPromptSubmit`
  - `PreToolUse`
  - `PostToolUse`
  - `Stop`

If no hook data exists for the selected task, the pane should explicitly say that hooks are unavailable for this session and continue showing the existing task metadata instead of rendering an empty state.

## Service-Layer Changes

The TUI should not query the collector directly.

The core/service layer should expose:

- task list plus optional hook session summary
- selected-task recent hook events
- a stream subscription interface for live summary updates

This keeps the TUI focused on rendering and input handling.

## Failure Handling

- If hook ingestion fails, the collector should log the failure and leave existing summaries untouched.
- If a hook payload cannot be parsed, the raw payload should still be preserved when possible and the event should be marked invalid rather than silently dropped.
- If task mapping fails, the event should be treated as unmanaged and excluded from the TUI.
- If the stream breaks, the TUI should remain usable from SQLite and allow manual refresh.
- If hook data is stale, the existing tmux/runtime reconciliation remains the fallback source for coarse task status.

## Testing Strategy

Tests should cover:

- SQLite schema creation and backward-compatible schema upgrades
- hook-event to task mapping from `cwd` and `session_id`
- phase derivation across realistic event sequences
- summary-table updates after each hook event type
- push payload emission from the collector
- TUI fallback behavior when hook summaries are absent
- TUI rendering of hook-derived list rows and selected-task detail pane
- restart behavior where a newly launched TUI reads persisted summaries and recent events correctly

## Implementation Notes

- Follow the repository's existing SQLite schema-init pattern in [internal/adapters/repository/sqlite/repository.go](/Users/ericbonet/software/tmux-llm-v1-refactor-brainstorm/internal/adapters/repository/sqlite/repository.go) rather than introducing a separate migration framework.
- Start with Codex-only hook enrichment. Other providers can adopt the same shape later if they gain equivalent structured lifecycle hooks.
- Keep the existing tmux runtime state fields until the hook-backed path has proven itself in the TUI.

## Open Questions Resolved

- Persistence should use SQLite, not JSONL, for the real feature.
- Live updates should use a local socket/stream, not require manual refresh.
- WebSockets are not the default transport; local socket push is the preferred first implementation.
- Hook-derived UI should be optional and only appear when hooks are actually available for the session.
