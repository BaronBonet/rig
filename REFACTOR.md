# Refactor Direction

## What We Are Trying To Accomplish

This refactor is simplifying the codebase while keeping the core idea of hexagonal architecture.

The problem in the current code is not the existence of boundaries. The problem is that there are too many abstractions, too many ports, and too many adapter-specific concepts leaking into the application layer.

The target shape is:

- one business-facing `TaskService`
- a small set of adapter families only:
  - `handler`
  - `repository`
  - `client`
  - `taskdaemon`
- a small set of business-shaped ports
- fewer public methods
- fewer implementation details exposed in `core`

`taskdaemon` is the one explicit exception to the old “only handler/repository/client”
rule. It is now a single cohesive adapter subsystem that owns:

- the local daemon server
- the Unix-socket frontend client
- the daemon process lifecycle

Trying to force those pieces back into separate top-level `handler/...` and
`client/...` packages made the subsystem harder to understand, not simpler.

Additional working rules:

- constructors for adapters that implement a core port should be named `New`
- those constructors should return the port interface, not the concrete adapter struct
- adapter-specific config types should live with the adapter package itself
- runtime wiring values that are not real operator config should stay in composition code, not infrastructure config

The goal is to make the code easier to trace, easier to reason about, and easier to extend without carrying forward legacy abstraction debt.

## Current Refactor Strategy

We are not trying to migrate the whole application at once.

We are doing this one vertical slice at a time, starting with `CreateTask`.

That means:

- define the final ports we actually want
- define the final domain objects we actually want
- wire one working path end to end
- prove it with real execution
- then move to the next feature

This is intentionally not a compatibility-first migration. If legacy code breaks while we are nailing the new architecture, that is acceptable.

## Current Source Of Truth

Right now, the active implementation path is the daemon-backed task/TUI slice:

- [cmd/rig/main.go](/Users/ebon/personal_software/rig/cmd/rig/main.go)
- [cmd/debug/main.go](/Users/ebon/personal_software/rig/cmd/debug/main.go)

This is the path we are using to wire the new architecture together and verify
that the task lifecycle works end to end.

That means:

- if the new architecture works through `cmd/rig/main.go`, that is the primary signal
- `cmd/debug/main.go` is still useful as a narrow development harness
- old paths that are outside this slice are not the current priority

Verified on 2026-04-21:

- `rig` now runs through the daemon-backed TUI path
- the daemon is auto-started by the root entrypoint when needed
- task creation, task listing, latest-status reads, and per-task status subscriptions
  run through `TaskFrontend`
- Codex hooks are ingested through the unified `taskdaemon` adapter
- `SessionStart` streams as `starting`
- `UserPromptSubmit`, `PreToolUse`, and `PostToolUse` stream as `working`
- `Stop` streams as `waiting_for_input`
- approval-selector / permission-request state remains deferred

## Status Of `cmd/rig/main.go`

- [cmd/rig/main.go](/Users/ebon/personal_software/rig/cmd/rig/main.go) is now the real
  entrypoint for the active slice
- it ensures the local task daemon is running and launches the new TUI
- it is no longer a legacy compatibility path

`cmd/debug/main.go` still exists as a useful narrow harness for task creation
and hook/status debugging, but it is not the primary product entrypoint anymore.

## Current Runtime Shape

The active runtime shape is now:

- `cmd/rig` composes the application and ensures the task daemon exists
- `internal/adapters/taskdaemon` owns:
  - daemon serving
  - Unix-socket frontend transport
  - daemon process management
- `internal/adapters/handler/tui` renders the browse/create MVP against `TaskFrontend`
- `TaskService` owns:
  - `CreateTask`
  - `ListTasks`
  - `LatestTaskStatus`
  - `SubscribeTaskStatus`
  - `HandleHookEvent`
- `tasksqlite` is the active durable task repository
- `gitworktree`, `tmuxsession`, and `codexagent` are the active operational adapters
- `workspace` owns repo-local setup plus provider bootstrap file writing

This slice is intentionally Codex-only for now. Claude support and other legacy
provider/runtime paths were removed rather than carried forward as dead weight.

## What “Done” Looks Like

This refactor is successful when:

- `TaskService` exposes the business operations we actually want
- ports are small and explicit
- provider-specific behavior lives in the right adapters
- repository adapters handle persistence and local file-backed concerns
- client adapters handle external tools and runtimes
- handlers stay thin
- each feature is migrated onto the new architecture one slice at a time

## Current Rule

Until further notice:

- optimize for the new architecture
- treat `cmd/rig/main.go` as the primary execution path
- use `cmd/debug/main.go` as a focused harness when that is useful
- keep the active slice Codex-only
- treat Codex status streaming as normal-turn streaming only:
  - `SessionStart` -> `starting`
  - `UserPromptSubmit`, `PreToolUse`, `PostToolUse` -> `working`
  - `Stop` -> `waiting_for_input`
- approval-selector / permission-request detection is deferred until Codex exposes a reliable hook for it
- do not preserve legacy complexity just to keep removed providers or dead paths alive
- prefer smaller, more direct boundaries over “helpful” extra abstractions
- when a package exists to satisfy a core port, expose it through that port at construction time
- keep daemon protocol concerns inside `internal/adapters/taskdaemon` rather than splitting them across multiple top-level packages
