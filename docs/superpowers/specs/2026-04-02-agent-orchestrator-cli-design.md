# Agent Orchestrator CLI Design

## Summary

Build a new Go CLI named `agent` that orchestrates task-focused development work in isolated git worktrees and tmux sessions. The first version should support a human workflow and an agent-assisted workflow equally well, with `codex` as the only supported provider in v1.

The CLI is the source of truth for naming, state, worktree creation, tmux setup, and task reconciliation. It should use Codex for name proposal and task launch, but it should not depend on Codex for core orchestration correctness.

## Goals

- Create a task from a prompt with one command.
- Generate a task name from the prompt via Codex, then let the user confirm or edit it.
- Create a git branch and sibling worktree from the current repo.
- Create a tmux session for that task and autostart Codex inside it.
- Attach or switch directly into the new tmux session after creation.
- Persist task state in SQLite and reconcile that state against git and tmux on read operations.
- Provide a small initial command set: `new`, `ls`, `open`, `status`, `doctor`.

## Non-Goals

- Multi-provider support in v1 beyond shaping the interfaces for future expansion.
- Merge automation, cleanup workflows, garbage collection, dashboards, or hooks in v1.
- Deep inference of Codex runtime status from transcripts or process internals.
- Replacing tmux, git, or Codex-native capabilities.

## Architecture

The project should follow the same broad shape as other Go projects in this workspace, especially the hexagonal split used in `fws-facade`.

### Entrypoint

- `cmd/agent/main.go`
  - Owns Cobra bootstrap only.
  - Registers the root command and subcommands.
  - Delegates immediately into CLI handlers.

### Core

- `internal/core`
  - Defines the `Task` model, service interfaces, orchestration rules, reconciliation behavior, and port interfaces.
  - Owns the lifecycle logic for `new`, `ls`, `open`, `status`, and `doctor`.
  - Contains no direct shelling out to `git`, `tmux`, or `codex`.

### Handlers

- `internal/adapters/handler/cli`
  - Maps Cobra commands and flags into core service calls.
  - Handles user-facing formatting and interactive confirmation for proposed task names.

### Repositories And System Adapters

- `internal/adapters/repository/sqlite`
  - Stores tasks and task events.
- `internal/adapters/repository/git`
  - Wraps `git` commands needed for repo validation, branch creation, worktree creation, and reconciliation checks.
- `internal/adapters/repository/tmux`
  - Wraps tmux session creation, existence checks, attach/switch behavior, and command injection.
- `internal/adapters/repository/codex`
  - Proposes a task title from the prompt.
  - Builds the Codex launch command for a created task.

### Shared Helpers

- `internal/pkg/...`
  - Shared low-level helpers such as command execution, clock abstractions, slug normalization, or output formatting helpers if they become broadly useful.

## Task Model

The main unit of orchestration is a persisted `Task`.

### Required Fields

- `id`
- `prompt`
- `display_name`
- `slug`
- `repo_root`
- `base_branch`
- `branch_name`
- `worktree_path`
- `tmux_session`
- `provider`
- `status`
- `created_at`
- `updated_at`
- `last_error`

### Reconciled Fields

These can be persisted for observability, but the live value should be refreshed during read operations:

- `worktree_exists`
- `branch_exists`
- `session_exists`
- `last_reconciled_at`

### Status Values

V1 only needs a small status model:

- `creating`
- `ready`
- `running`
- `broken`

This is intentionally narrow. The tool should not claim richer agent-state semantics until it can support them reliably.

## Command Design

### `agent new "<prompt>"`

Expected flow:

1. Validate that the current directory is inside a git repo.
2. Detect repo root and base branch.
3. Ask the Codex adapter to propose a short task title from the prompt.
4. Present the proposed title and allow the user to confirm or edit it interactively.
5. Normalize the final title into a slug.
6. Create branch and sibling worktree.
7. Create a tmux session with a single window rooted in the new worktree.
8. Launch Codex in that tmux session using the original prompt.
9. Persist the task and append lifecycle events.
10. Attach or switch the user into the created tmux session.

If Codex title generation fails, the CLI should fall back to a deterministic local title derived from the prompt, still allowing interactive confirmation or editing before proceeding.

### `agent ls`

- Lists persisted tasks with reconciled live state.
- Should show enough information to understand the task quickly:
  - display name
  - provider
  - status
  - repo
  - tmux session
  - branch

### `agent open <task>`

- Resolves a task by exact or unambiguous identifier.
- Reconciles the tmux session before opening.
- Attaches or switches into the task session.
- Returns a clear error if the session no longer exists.

### `agent status <task>`

- Shows the full persisted task record plus live reconciliation results.
- Must make drift explicit rather than masking it.

### `agent doctor`

- Verifies required binaries exist: `git`, `tmux`, `codex`.
- Verifies the SQLite database path is usable.
- Verifies the config file is readable if present.
- Verifies the current directory is a usable repo context when relevant.

## Naming Strategy

### Inputs

- repo name
- original prompt
- Codex-proposed short title
- user-confirmed or user-edited final title

### Outputs

- `display_name`
- `slug`
- `branch_name`
- `worktree_path`
- `tmux_session`

### Rules

- The CLI owns final naming decisions.
- Codex proposes the initial human-friendly title.
- The user can edit the title before resources are created.
- Slug normalization must be deterministic and shell-safe.
- Name collisions should be handled by suffixing the slug predictably.

### Default Templates

- branch: `feat/<slug>`
- worktree path: sibling to repo root, e.g. `../<repo>-<slug>`
- tmux session: `<repo>:<slug>`

The worktree location should be configurable later, but the default in v1 is sibling-based.

## Persistence

SQLite is the v1 state store.

### Tables

#### `tasks`

Stores the latest known state for each task.

#### `events`

Append-only lifecycle records for observability and recovery. Example event types:

- `task_created`
- `name_proposed`
- `name_confirmed`
- `branch_created`
- `worktree_created`
- `tmux_session_created`
- `codex_launch_requested`
- `reconciled`
- `error_recorded`

The `tasks` table is the operational state surface. The `events` table exists to explain how a task got there.

## Config

V1 should support a minimal config file at `~/.config/agent/config.yaml`.

### Initial Config Surface

- default base branch
- worktree root strategy
- codex binary path
- attach behavior
- slug normalization limits

The tool should remain usable without a config file for the common case.

## Provider Model

V1 supports `codex` only, but the core should not hard-code provider behavior directly into command handlers.

The Codex adapter needs two distinct responsibilities:

1. `ProposeTaskName(prompt)` to generate the initial title before any branch or session exists.
2. `BuildLaunchCommand(task)` to start Codex inside the created tmux session.

Keeping these separate prevents the naming flow from leaking into launch behavior.

## Reconciliation

The CLI must reconcile persisted state with the real world on read-oriented commands.

### Checks

- Does the worktree path still exist?
- Does the branch still exist?
- Does the tmux session still exist?

### Behavior

- `ls`, `status`, and `open` trigger reconciliation.
- Missing resources should move the task to `broken` with a concrete reason.
- The tool should not silently report `running` if tmux or the worktree has been removed manually.

## Error Handling

The system should preserve partial progress instead of failing opaquely.

### Examples

- If branch creation succeeds but tmux creation fails, persist the task with a failure reason.
- If tmux session creation succeeds but Codex launch fails, reflect that as a broken task rather than pretending the task is healthy.
- If the naming step fails, continue with a deterministic local fallback title and an interactive confirmation step.

This preserves recoverability and makes manual intervention possible.

## Testing Strategy

Testing should focus on orchestration behavior in `internal/core`.

### Unit Tests

- task creation workflow
- local fallback naming behavior
- collision handling for slugs and branch/session naming
- reconciliation transitions
- partial failure persistence

### Adapter Tests

- SQLite repository behavior
- git command construction and parsing
- tmux command construction and parsing
- Codex command construction

### Out Of Scope For V1 Tests

- depending on real tmux sessions in most tests
- depending on live Codex execution in most tests

Those can be added later as smoke tests once the basic command surface is stable.

## Project Layout

The initial structure should look roughly like this:

```text
cmd/
  agent/
    main.go
internal/
  core/
  adapters/
    handler/
      cli/
    repository/
      codex/
      git/
      sqlite/
      tmux/
  pkg/
docs/
  superpowers/
    specs/
```

## Acceptance Criteria For V1

The first version is successful if a user can:

1. Run `agent new "<prompt>"` inside a git repo.
2. See a proposed task title, edit or confirm it, and continue.
3. Get a new branch, sibling worktree, tmux session, and Codex launch from that one command.
4. Land directly in the created tmux session after task creation.
5. Run `agent ls` and understand current tasks at a glance.
6. Run `agent status <task>` and see drift clearly if tmux or the worktree was changed manually.
7. Run `agent doctor` and get actionable environment diagnostics.

## Deferred Work

These are intentionally deferred until after v1 is stable:

- Claude support
- cleanup and garbage collection commands
- merge assistance
- hooks
- multi-window tmux layouts
- dashboard or TUI
- richer agent status extraction
