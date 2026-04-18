# Refactor Direction

## What We Are Trying To Accomplish

This refactor is simplifying the codebase while keeping the core idea of hexagonal architecture.

The problem in the current code is not the existence of boundaries. The problem is that there are too many abstractions, too many ports, and too many adapter-specific concepts leaking into the application layer.

The target shape is:

- one business-facing `TaskService`
- three adapter families only:
  - `handler`
  - `repository`
  - `client`
- a small set of business-shaped ports
- fewer public methods
- fewer implementation details exposed in `core`

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

Right now, the active implementation path is:

- [cmd/debug/main.go](/Users/ebon/personal_software/rig/cmd/debug/main.go)

This is the path we are using to wire the new architecture together and verify that the `CreateTask` flow works end to end.

That means:

- if the new architecture works through `cmd/debug/main.go`, that is progress
- if old application paths still depend on legacy services, that is not the current priority

## Status Of `cmd/rig/main.go`

- [cmd/rig/main.go](/Users/ebon/personal_software/rig/cmd/rig/main.go) is not the current driver of this refactor
- it may still contain legacy wiring
- it should not block architectural cleanup on the debug path

For now, `cmd/rig/main.go` is effectively secondary. It can be kept compiling if convenient, but it is not the source of truth for the refactor.

If maintaining it starts slowing down the cleanup of the new architecture, it is acceptable to delete it and rebuild it later on top of the finished boundaries.

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
- treat `cmd/debug/main.go` as the active execution path
- do not preserve legacy complexity just to keep old paths alive
- only bring `cmd/rig/main.go` along when it is cheap or useful
