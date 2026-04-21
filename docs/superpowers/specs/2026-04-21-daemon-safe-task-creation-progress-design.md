# Daemon-Safe Task Creation Progress Design

**Date:** 2026-04-21

## Goal

Restore live task-creation progress feedback in the new daemon-backed `rig`
flow without reintroducing UI-shaped callbacks into the public service
contract.

The user should again see what is happening while a task is being created,
including the major create-task stages before the task appears in the list.

## Problem

Before the refactor, task creation surfaced a stream of progress information in
the UI. During the active daemon-backed refactor slice, that behavior was
removed in favor of a simpler one-shot `CreateTask` flow:

- the TUI sends `CreateTask`
- the daemon returns one final task record or an error
- the TUI shows only a generic pending state until completion

That removed useful feedback during the slowest part of task creation:

- naming
- worktree creation
- workspace preparation
- session startup

The active refactor direction in `REFACTOR.md` still applies:

- `cmd/rig` is the real entrypoint
- the TUI talks only to `TaskFrontend`
- taskdaemon owns transport and lifecycle
- `TaskService` stays business-shaped

The restored progress model must respect those boundaries.

## Decision

Add a daemon-streamed task-creation progress path to the active frontend flow.

Task creation over the daemon socket should become a streamed request:

- zero or more `task_create_progress` messages
- exactly one terminal message:
  - `task_created`
  - or `error`

The TUI will use that streamed path for prompt-backed task creation and render a
step list while the request remains in flight.

`TaskService.CreateTask(ctx, input) (*Task, error)` remains the public business
operation. Progress reporting is not added to the service interface or the
`TaskFrontend` one-shot create method. Instead, the create flow will use an
internal progress sink that is only visible to the active create-task path.

## Why This Approach

This restores the old experience while keeping the current architecture honest.

It avoids three bad outcomes:

- reintroducing handler-facing progress callbacks into the public core contract
- coupling the TUI directly to in-process wiring that bypasses taskdaemon
- persisting ephemeral progress state that exists only to support live UX

It also preserves a clean split:

- `TaskService` owns create orchestration
- taskdaemon owns transport
- the TUI owns presentation of progress steps

## Non-Goals

This design does not:

- persist task-creation progress to SQLite
- add replay of historical create-progress events
- expose a general-purpose long-lived event stream for all operations
- change task status streaming for already-created tasks
- redesign the create UX beyond restoring progress feedback

## Progress Model

The create-progress model should be small and stable.

Add a core progress-step enum for the create flow with these initial stages:

- `suggesting_name`
- `creating_worktree`
- `preparing_workspace`
- `starting_session`

These steps intentionally describe business-relevant milestones rather than
adapter internals.

The server should stream stable step identifiers. The TUI should own the final
rendered copy, styling, and labels.

That keeps presentation text out of the core and daemon protocol while still
letting the UI show rich status.

## Public And Internal Boundaries

### Public Contracts

Keep these public contracts intact:

- `TaskService.CreateTask(ctx, input) (*Task, error)`
- the existing one-shot `TaskFrontend.CreateTask(ctx, input) (*Task, error)`

The one-shot create method remains useful for narrow callers and tests that do
not need progress.

### New Frontend Capability

Add a streamed create capability to the active frontend path used by the TUI.

This can be done either by:

- extending `TaskFrontend` with a streaming create method
- or adding a second frontend-facing interface used only by the TUI adapter

The preferred shape is to extend `TaskFrontend` directly because the daemon
frontend is already the active application-facing contract for the TUI and task
creation is a first-class frontend operation.

Recommended method shape:

`CreateTaskStream(ctx, input) (<-chan TaskCreateEvent, error)`

Where `TaskCreateEvent` is a small sum type or tagged struct that can carry:

- a progress step
- a final task
- a terminal error

The stream closes after the terminal event.

## Core Orchestration

`TaskService` should emit progress at the existing orchestration boundaries in
`createTaskFromPrompt`:

1. before task-name suggestion
2. before worktree creation
3. before workspace preparation
4. before session startup

This should not be done through a new public callback parameter.

Instead, use an internal progress reporter resolved from context, for example:

- a private context key in `internal/core`
- a private reporter interface with a no-op fallback

That keeps the public service API unchanged while still letting the daemon-backed
create flow observe milestones.

## Taskdaemon Protocol

The Unix socket protocol should gain a streamed create mode.

### Request

Reuse the existing `create_task` request command.

### Responses

Allow the server to emit a sequence of envelopes for that request:

- `task_create_progress`
- `task_created`
- `error`

`task_create_progress` should include only the step enum value.

`task_created` remains the final durable task snapshot.

`error` remains terminal and should end the stream.

The taskdaemon frontend client should own reading this stream and translating it
into `TaskCreateEvent` values for the TUI.

## TUI Behavior

When the user submits a prompt:

1. the prompt screen enters a create-progress state immediately
2. the TUI starts the streamed create request
3. each progress event updates the active step
4. on `task_created`, the TUI exits the create screen and returns to browse mode
5. on `error`, the TUI stays in the create screen and preserves the prompt

The progress view should show:

- completed steps with a completed state
- the current step with shimmer
- future steps dimmed

The initial restored labels should be:

- `Suggesting name`
- `Creating worktree`
- `Preparing workspace`
- `Starting session`

## Failure Behavior

If create fails after one or more progress events:

- keep completed steps visible
- mark the current step as failed
- show the returned error below the steps
- keep the user’s drafted prompt intact for retry or editing

If create fails before the first progress event, show the error in the same
create screen with no completed steps.

## Success Behavior

On success:

- keep the current optimistic insert behavior only if needed for responsiveness
- refresh from the authoritative task list after create completion
- preserve selection on the newly created task ID

That ensures the TUI ends on the durable task snapshot even if the streamed
terminal `task_created` payload is thin or a stale daemon is later involved.

## Backward Compatibility

The non-stream taskdaemon behaviors must keep working:

- list tasks
- latest task status
- subscribe task status
- delete task
- open task session

If keeping the existing one-shot `CreateTask` method proves useful for tests or
future non-TUI callers, it can be implemented on top of the streamed method by
draining progress events until the terminal result arrives.

## Rollout Order

1. Add create-progress event and step types.
2. Add failing core tests for expected create-progress emission order.
3. Add a private progress sink to the core create flow.
4. Add streamed create request handling to taskdaemon server and frontend.
5. Add failing TUI tests for progress rendering and failure retention.
6. Switch the TUI create path from one-shot create to streamed create.
7. Keep the authoritative post-create refresh behavior.

## Testing Focus

Core:

- prompt-backed create emits:
  - `suggesting_name`
  - `creating_worktree`
  - `preparing_workspace`
  - `starting_session`
- emission order matches the orchestration order
- no progress emission leaks into unrelated paths

Taskdaemon:

- streamed create returns progress envelopes before terminal completion
- mid-stream errors propagate as terminal `error`
- one-shot callers can still obtain the final task if that path is preserved

TUI:

- submitting a prompt enters progress mode
- progress steps advance in order
- failure preserves prompt and rendered steps
- success returns to browse mode and selects the created task

## Consequences

Benefits:

- restores lost user feedback during create
- keeps the daemon-backed architecture intact
- avoids putting presentation callbacks back into public core APIs
- gives the TUI enough structured signal to render a stable step experience

Tradeoffs:

- adds a second create interaction mode to the frontend path
- introduces streamed request handling for a previously one-shot command
- requires careful tests for terminal-event and stream-close behavior
