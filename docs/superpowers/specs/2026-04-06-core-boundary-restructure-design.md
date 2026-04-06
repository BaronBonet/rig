# Core Boundary Restructure

**Date:** 2026-04-06
**Status:** Approved

## Summary

Restructure the application around a clearer hexagonal boundary. `internal/core` remains the central package, but it is reduced to a small set of conventional Go files whose contents are understandable at the business-logic level. Infrastructure concerns such as environment loading, filesystem/bootstrap setup, tmux mechanics, sqlite schema handling, and provider CLI quirks move out of `core` into `internal/infrastructure` or concrete adapter packages.

## Goals

- Make the architecture readable from the filesystem.
- Keep `internal/core` small and obvious: `domain.go`, `service.go`, `ports.go`, `errors.go`.
- Ensure `core` contains only business logic and orchestration through ports.
- Move application composition into `cmd/agent/main.go`.
- Load environment configuration in `internal/infrastructure/config.go` using `github.com/caarlos0/env/v11`.

## Non-goals

- Adding new end-user features.
- Preserving every current constructor or package API.
- Splitting the domain into more packages such as `internal/app` or `internal/usecase`.

## Approach

Keep a single `internal/core` package, but make it the strict application boundary. The core should read like product behavior, not tool integration code. If a function depends on how tmux, sqlite, the filesystem, or a provider binary behaves internally, that logic belongs outside `core`.

## Design

### 1. Core file layout

`internal/core` is collapsed into four production files:

- `domain.go`
- `service.go`
- `ports.go`
- `errors.go`

`domain.go` contains the task entity, status enums, runtime state enums, progress/event types, and any application-facing config structs that truly belong to the core contract.

`ports.go` contains the interfaces that adapters implement. These ports should describe business capabilities, not low-level command sequences.

`service.go` contains the use-case orchestration for task creation, listing, reconciliation, opening, cleanup, and doctor checks.

`errors.go` contains exported application errors such as task-not-found or invalid task state errors.

### 2. Boundary rules

`core` may coordinate workflows through ports, but it may not contain infrastructure mechanics.

Examples of logic that stays in `core`:

- deciding whether a task is `running`, `degraded`, `broken`, or `cleaned`
- deciding when a task may be opened
- selecting the provider for a task
- applying naming fallback rules
- orchestrating task creation and cleanup steps through ports

Examples of logic that must move out of `core`:

- env parsing and default loading
- creating parent directories for databases or other process bootstrap work
- tmux command construction, prompt polling, pane capture, pane binding, or control-pipe parsing
- sqlite schema creation, migrations, row scanning, and data backfills
- provider-specific CLI command assembly details and prompt-detection mechanics
- raw filesystem probing that exists only to support a concrete integration

The litmus test is simple: someone from a product department should be able to read `core/service.go` and understand the behavior at a high level without needing to know tmux or sqlite internals.

### 3. Composition and config loading

Application wiring moves to the composition root in `cmd/agent/main.go`.

`internal/infrastructure/config.go` loads application configuration from environment variables using `github.com/caarlos0/env/v11`. It applies defaults there and returns a composed application config structure.

That configuration may contain:

- a `core` service config for values that are part of the application contract
- adapter-specific config structs for sqlite, providers, tmux-related wiring, or other concrete dependencies

`core` may define config structs only when those values are meaningful to the application layer. The code that parses env vars and applies defaults does not belong in `core`.

### 4. Composition root responsibilities

`cmd/agent/main.go` should:

- load config from `internal/infrastructure`
- construct concrete adapters
- construct the `core` service once
- pass that service into the CLI handler layer

The current lazy factory-style `runtimeService` should be removed. It mixes composition concerns with command-facing behavior and obscures the dependency graph. The main package should make the wiring explicit.

### 5. Port shape changes

Ports should become more business-oriented where the current surface leaks infrastructure detail.

In particular, the core should avoid orchestrating raw tmux command sequences such as:

- creating a session and then sending keys
- polling panes for prompt markers
- capturing pane content directly

Instead, ports should express higher-level capabilities such as:

- creating or removing task runtime resources
- launching the configured provider in the task session
- inspecting runtime state for a task
- validating or seeding workspace content

This keeps `service.go` focused on the lifecycle of a task rather than the mechanics of tmux automation.

During implementation, ports should be consolidated toward a smaller set of coarse-grained business capabilities. Adding new low-level verbs to mirror tmux command sequences is explicitly out of scope for this restructuring.

### 6. Adapter responsibilities

Adapters remain under `internal/adapters/...` and own the details of the external systems they integrate with.

The adapter taxonomy should carry meaning:

- `internal/adapters/repository`: persisted state or document-backed storage
- `internal/adapters/client`: external tools or service integrations
- `internal/adapters/filesystem`: local filesystem operations

Expected responsibilities:

- `repository/sqlite`: persistence, schema management, row mapping
- `repository/agentconfig`: parsing and loading the repo-local `agent.yaml` document
- `client/git`: repository detection, branch/worktree operations through the git CLI
- `client/tmux`: session/window lifecycle, launch orchestration, runtime inspection internals
- `client/codex` and `client/claude`: availability checks, task-name proposal, provider launch behavior
- `filesystem/workspace`: seed validation and copy behavior

This naming is intentional: `repository` should not become the catch-all bucket for every adapter. If an integration is primarily a tool client rather than persisted data access, it belongs under `client`.

Adapters may depend on `core` port and domain types, but `core` must not depend on adapters.

### 7. Target repository shape

The target shape after restructuring is:

```text
cmd/agent/main.go
internal/core/domain.go
internal/core/service.go
internal/core/ports.go
internal/core/errors.go
internal/infrastructure/config.go
internal/adapters/handler/cli/...
internal/adapters/repository/sqlite/...
internal/adapters/repository/agentconfig/...
internal/adapters/client/git/...
internal/adapters/client/tmux/...
internal/adapters/client/codex/...
internal/adapters/client/claude/...
internal/adapters/filesystem/workspace/...
```

This keeps the filesystem aligned with the intended architecture:

- `cmd` composes the application
- `infrastructure` loads process-level configuration
- `adapters` implement ports
- `core` defines business behavior

### 8. Testing strategy

Core tests stay focused on business behavior:

- task creation flow
- reconciliation outcomes
- open/cleanup rules
- doctor behavior at the application level

Adapter tests stay in adapter packages and cover infrastructure details such as:

- sqlite schema and persistence behavior
- tmux command execution and runtime-monitor behavior
- provider parsing and launch behavior
- workspace copy/validation behavior

This preserves the simple core layout without losing isolation where integration detail actually lives.

## Migration plan

Implementation should proceed in small, behavior-preserving steps:

1. Introduce `internal/infrastructure/config.go` and move env/default loading there.
2. Replace `runtimeService` with explicit composition in `cmd/agent/main.go`.
3. Collapse current core type files into `domain.go`, `ports.go`, and `errors.go`.
4. Refactor `core/service.go` so infrastructure-heavy logic is pushed behind ports.
5. Update adapters to satisfy the revised port interfaces.
6. Update tests to reflect the new boundaries and constructor shape.

## Scope boundaries

- No new user-facing commands are required as part of this restructuring.
- No architectural split beyond a single `internal/core` package is required.
- The main success criterion is clarity of boundaries, not maximal file minimization in adapter packages.
