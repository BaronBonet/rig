# Rig Codex Hook-Only Reset Design

> Historical design note. Superseded by the 2026-04-18 status-stream design and
> the 2026-04-19 cleanup decisions. Any `PermissionRequest` references here are
> retained for context only and are not part of the supported current
> implementation.

## Summary

Create a new standalone Go repository that resets Rig down to the smallest useful Codex workflow:

- start a Codex task in the current repository
- persist Codex hook events and derived task state in SQLite
- inspect the latest state through a simple CLI

This version intentionally removes git worktrees, tmux, the TUI, PR integration, background observer processes, and any runtime-state derivation that depends on anything other than Codex hooks.

The goal is to make Codex task state trustworthy again by reducing the system to one execution model and one source of truth.

## Goals

- Launch Codex from the current repository with a prompt supplied through the CLI.
- Persist task metadata, raw hook events, and the latest derived task runtime state in SQLite.
- Expose the latest task state through small CLI commands such as `start`, `status`, and `watch`.
- Derive user-facing state like `working`, `needs_input`, and `finished` strictly from hook events and explicit process exit handling.
- Keep the implementation in Go.

## Non-Goals

- Supporting git worktrees in v1.
- Supporting tmux-managed sessions in v1.
- Rebuilding the TUI.
- Combining hook state with process probes, socket daemons, or other observer layers.
- Supporting Claude in the first cut.
- Reusing the existing Rig repository structure as-is.

## Existing State

The current Rig repository already contains useful building blocks:

- Codex hook bootstrap logic that writes `.codex/hooks.json` and a forwarding shell script.
- Codex hook HTTP and CLI ingestion decoding.
- SQLite persistence patterns for tasks and hook observability.

The current Rig repository also carries significant complexity that this reset should remove:

- task state mixed across hooks, tmux observation, and runtime detection
- worktree lifecycle management
- TUI-specific state and rendering logic
- background observer processes and sockets
- PR and multi-provider concerns unrelated to the immediate Codex state bug

## Recommended Approach

Create a new Go repository and selectively extract the narrow pieces that are already defensible:

- Codex hook bootstrapping
- hook payload decoding
- minimal SQLite access patterns

Everything else should be rebuilt with smaller boundaries around the new requirements. This keeps working knowledge about Codex hooks while avoiding hidden coupling to the current architecture.

## Architecture

The new repository should have four primary packages:

- `cmd/rig`
  Cobra entrypoint and command wiring.
- `internal/app`
  Application services for `start`, `status`, `watch`, and hook ingestion.
- `internal/codex`
  Codex-specific workspace bootstrap, launch command construction, and hook payload decoding.
- `internal/store/sqlite`
  SQLite schema, migrations, queries, and repository implementations.

There is only one runtime-state pipeline in v1:

1. `rig start` creates a task record.
2. `rig start` bootstraps Codex hooks in the current repository.
3. Codex emits hook events.
4. The hook forwarder invokes `rig hook ingest <event-name>`.
5. Hook ingestion appends the raw event and updates the latest derived runtime state.
6. `rig status` and `rig watch` read the latest persisted state directly from SQLite.

No other observer or reconciliation layer should exist in v1.

## Command Design

### `rig start "<prompt>"`

Runs inside the current git repository and launches Codex in the same terminal.

Behavior:

- verify the current directory is inside a git repository
- verify the `codex` binary is available
- create a task row with prompt, repo root, and initial launch state `starting`
- ensure Codex hook files exist in the current repository
- launch `codex <prompt>` in the same terminal
- persist hook events while Codex runs
- finalize the task when Codex exits, even if a final hook is missing

This command should not detach or create a background session.

### `rig status`

Shows the latest task for the current repository by default.

Suggested output fields:

- task id
- created time
- prompt preview
- launch state
- derived display status
- last hook event name
- current command preview
- last assistant message preview
- last activity time

### `rig watch`

Displays task-state changes for the current repository as SQLite records update.

This should be implemented as a simple polling loop in v1. No daemon, socket, or push bus is required.

### `rig hook ingest <event-name>`

Hidden internal command used by the hook forwarding script.

This command reads the JSON payload from stdin, decodes the hook payload, resolves the active task for the current repository, inserts the raw event, and updates the latest runtime state row.

## Data Model

Use a minimal SQLite schema with three tables.

### `tasks`

Stores task lifecycle metadata:

- `id`
- `repo_root`
- `prompt`
- `launch_state`
- `created_at`
- `updated_at`
- `started_at`
- `ended_at`
- `codex_exit_code`

`launch_state` is a small process-level field, not the user-facing status. Suggested values:

- `starting`
- `running`
- `exited`
- `failed`

### `hook_events`

Append-only raw observability log:

- `id`
- `task_id`
- `occurred_at`
- `event_name`
- `session_id`
- `turn_id`
- `cwd`
- `model`
- `prompt_text`
- `command_text`
- `command_result_text`
- `last_assistant_message`
- `raw_payload_json`

### `task_runtime_state`

One row per task containing the latest derived view:

- `task_id`
- `display_status`
- `display_activity`
- `last_event_name`
- `last_activity_at`
- `session_id`
- `turn_id`
- `model`
- `current_command`
- `last_assistant_message`
- `prompt_preview`
- `updated_at`

## State Derivation

User-facing state should be derived from hooks with explicit, narrow rules:

- `PermissionRequest` => `needs_input`
- `PreToolUse` => `working` with activity `command`
- `UserPromptSubmit` => `working`
- `PostToolUse` => `working`
- `SessionStart` => `working`
- `Stop` => `needs_input`
- explicit terminal hook or Codex process exit => `finished`
- no hook data yet after launch => `starting`

The v1 model should not expose `disconnected`.

That label only becomes meaningful once the system depends on long-lived background execution or secondary runtime observers. In this reset, `disconnected` would mostly encode ambiguity and invite the same class of bug that currently masks useful states.

## Hook Resolution

Because v1 launches Codex directly in the current repository, hook events can be associated to tasks using the repository root rather than worktree or tmux metadata.

Recommended resolution rule:

- when `rig start` begins, mark the new task as the active task for that `repo_root`
- hook ingestion resolves incoming events to the newest active task for the payload `cwd`
- when the Codex process exits, clear the active-task marker for that repository

This keeps task matching simple while preserving correct association for the only supported execution model.

## Hook Bootstrap

Bootstrap should write or update:

- `.codex/hooks.json`
- `.codex/hooks/forward-to-rig.sh`

The forwarder script should invoke the installed `rig` binary through:

- `rig hook ingest <event-name>`

The script should detect the repo root before forwarding so ingestion always runs with the correct repository context.

## Error Handling

- If the current directory is not inside a git repository, `rig start` should fail clearly.
- If `codex` is unavailable, `rig start` should fail before creating a long-lived broken state.
- If hook ingestion cannot match a task for the repository, it should log a clear error and return success to avoid breaking Codex execution.
- If SQLite writes fail during hook ingestion, the failure should be logged locally for diagnosis.
- If Codex exits without any hook traffic, the task should still move out of `starting` into `exited` or `failed`.

## Testing Strategy

Implementation should add tests for:

- hook bootstrap writes the expected Codex files
- hook payload decoding for all supported Codex hook events
- task creation and active-task selection per repository
- hook ingestion inserts raw events and updates derived state
- state derivation for `working`, `needs_input`, `starting`, and `finished`
- `status` output for a repository with and without recent hook activity
- `watch` output when state changes in SQLite
- process-exit finalization when Codex ends without a terminal hook

## Open Decisions Resolved

- Repository strategy: new standalone repository, not a branch inside the current repo
- Language: Go
- Execution model: current repository only, no worktrees
- Session model: same terminal, no tmux
- Persistence: SQLite
- UI: CLI only
- Runtime truth source: Codex hooks plus explicit process exit handling
