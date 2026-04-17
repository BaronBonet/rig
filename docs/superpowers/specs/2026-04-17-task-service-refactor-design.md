# Rig Task Service Refactor Design

## Summary

Refactor Rig toward a lighter hexagonal architecture that preserves clear boundaries around external systems while removing unnecessary abstraction.

The target design keeps three adapter families:

- `handler` for the TUI and CLI entrypoints
- `repository` for durable local state such as SQLite and filesystem-backed project state
- `client` for operational integrations such as tmux, git, GitHub, Codex, and Claude

The core application layer should expose one business-facing `TaskService` whose methods are shaped around task workflows rather than implementation details. Internally, the service may use smaller private workflows, but those should not leak into the public API.

The migration should start with `CreateTask` as a vertical slice and proceed incrementally behind a compatibility facade so the TUI continues to depend on a single handler-facing interface during the transition.

## Goals

- Reduce the number of public abstractions and make task flows easier to trace locally.
- Preserve hexagonal architecture at the level of meaningful external boundaries.
- Collapse adapters into three clear families: `handler`, `repository`, and `client`.
- Replace implementation-shaped service methods with business-shaped use cases.
- Hide provider-specific behavior behind a single `AgentClient` contract.
- Keep the TUI insulated from migration churn through a temporary compatibility facade.
- Prove the new shape with a `CreateTask`-first migration before refactoring the rest of the app.

## Non-Goals

- Rewriting the entire application in one large migration.
- Replacing hexagonal architecture with a layered or framework-driven design.
- Forcing strict CQRS or command/query service splits.
- Changing user-visible behavior unless required to simplify naming and boundaries.
- Eliminating all internal helper types inside the application layer.

## Existing Problems

The current repository already has useful separation between `core`, `adapters`, and infrastructure wiring, but the abstraction surface is too granular for the size of the app.

Current pressure points include:

- `internal/core/service.go` acting as a large orchestration unit that mixes many workflows.
- TUI code depending on a handler-facing interface that mirrors infrastructure details such as name suggestion and progress callbacks.
- Multiple observability and runtime concepts exposed inconsistently across the service layer and the TUI.
- Provider-specific behavior influencing service shape instead of being hidden behind a business capability.
- Ports that follow implementation packages too closely instead of following business needs.

The result is that the app still pays the complexity cost of hexagonal architecture while losing some of the readability benefit.

## Architectural Decision

Hexagonal architecture remains the right architectural idea for Rig because the application has real external boundaries:

- terminal UI
- SQLite and filesystem-backed persistence
- git worktrees
- tmux sessions
- GitHub pull requests
- multiple agent runtimes

Those boundaries justify ports and adapters. The issue is not the architectural style itself; it is that the current code models too many fine-grained seams.

The refactor should therefore keep hexagonal architecture, but in a compressed form:

- one business-facing application service
- three adapter families
- a small set of business-shaped ports
- private internal workflows instead of additional public service interfaces

## Target Adapter Model

### Handler

The handler layer contains the TUI and Cobra command entrypoints.

Responsibilities:

- translate user input into `TaskService` calls
- render task state, activity, and diagnostics
- remain unaware of provider-specific behavior, restoration mechanics, or low-level infrastructure concerns

### Repository

The repository layer contains durable local persistence and project-local state.

Responsibilities:

- task persistence
- task activity/event persistence
- project config loading
- filesystem-backed setup/bootstrap state when that concern is fundamentally about reading or writing durable local state

Examples:

- SQLite task and activity persistence
- `.rig.yaml` or `rig.yaml` config loading
- filesystem-backed local metadata or setup assets

### Client

The client layer contains operational integrations with external tools and services.

Responsibilities:

- agent runtime interaction
- session/runtime interaction
- git/worktree operations
- GitHub pull request integration

Examples:

- tmux
- git
- GitHub
- Codex
- Claude

## Target Port Set

Ports should exist because the business workflow needs a capability, not because an adapter has its own package.

The expected port set is:

- `TaskStore`
  Create, update, load, and list tasks and task details.
- `TaskActivityStore`
  Persist and read task activity events and, if needed, derived runtime summaries.
- `AgentClient`
  Build launch/restore requests and encapsulate provider-specific runtime behavior behind one contract.
- `WorkspaceClient`
  Detect project context, create/remove/inspect task workspaces, and perform git/worktree operations needed by task workflows.
- `RuntimeClient`
  Start sessions, attach to sessions, inspect runtime state, and stream runtime updates such as `working` and `needs_input`.
- `PullRequestClient`
  List pull requests and inspect PR-related task metadata.
- `ProjectConfigRepository`
  Load repository-local Rig configuration.

The exact naming may shift during implementation, but the contract surface should stay at roughly this level.

## Public Service Surface

The new business-facing application service should be a single `TaskService`.

The expected public methods are:

- `CreateTask`
- `ListTasks`
- `GetTaskDetails`
- `AttachToTask`
- `CleanupTask`
- `ListPullRequests`
- `GetTaskActivityEvents`
- `SubscribeTaskRuntimeUpdates`
- `Doctor`

### Method Intent

- `CreateTask`
  Create a new task from prompt input, including naming, provider selection, workspace preparation, and session launch. Task creation may optionally be sourced from a pull request through input metadata rather than a separate top-level use case.
- `ListTasks`
  Return task summaries for the main list UI and other task overview views.
- `GetTaskDetails`
  Return the richer view of a single task needed for the detail panel and other direct task inspection.
- `AttachToTask`
  Perform side effects needed to attach the operator to the task session, including restoring or repairing runtime/session state when appropriate.
- `CleanupTask`
  Tear down task resources and persist the resulting task status.
- `ListPullRequests`
  List pull requests for the current repository and annotate them with task-related context when helpful.
- `GetTaskActivityEvents`
  Return recent persisted activity history for a selected task detail view.
- `SubscribeTaskRuntimeUpdates`
  Stream live runtime status updates such as `working` and `needs_input`.
- `Doctor`
  Run environment and dependency checks.

## Explicit Service Decisions

The following decisions were agreed during brainstorming:

- `SuggestTaskName` should not be a public service method.
  Name and branch derivation belong inside `CreateTask`.
- `OpenTask` should be renamed to `AttachToTask`.
  The new name makes the side effect explicit and reads less like a handler-level concern.
- `GetTask` should become `GetTaskDetails`.
  The method is a read-only query that returns richer detail state.
- `GetTaskActivityEvents` and `SubscribeTaskRuntimeUpdates` should remain separate methods.
  They serve different UI concerns and should be named explicitly.
- `Doctor` should remain on the same service rather than moving to a separate diagnostics service.

## Internal Workflow Shape

The public API should stay small, but `TaskService` should not remain a single large orchestration file forever.

Internally, the service may be decomposed into smaller private workflows or helpers such as:

- task creation
- task attachment and restore
- task cleanup
- task listing and enrichment
- task detail loading

These are internal implementation units, not separate public services.

This keeps the public API compact while still letting the application layer shrink large methods into focused business workflows.

## CreateTask Flow

`CreateTask` is the first migration slice because it exercises the most important boundaries:

- project detection
- config loading
- task naming and slug generation
- task persistence
- workspace creation
- workspace seeding/bootstrap
- agent launch request creation
- runtime/session startup
- final task state persistence

The intended flow is:

1. `TaskService.CreateTask(input)`
2. `WorkspaceClient` detects repository/project context.
3. `ProjectConfigRepository` loads project-local Rig config.
4. `AgentClient` or internal naming logic derives a task name when the input does not explicitly provide one.
5. `TaskStore` creates the task in status `creating`.
6. `WorkspaceClient` creates and inspects the workspace.
7. repository/bootstrap concerns seed files and run setup behavior when configured.
8. `AgentClient` builds the launch request.
9. `RuntimeClient` starts the task session.
10. `TaskStore` persists the final ready/running task state.

Failure at any stage should produce a durable broken/degraded task state rather than leaving the handler to reconstruct what happened.

## Activity And Runtime Reporting

Public progress callbacks should not remain in the service contract.

The service should expose durable outcomes and app-facing read models rather than UI-shaped callback hooks. The TUI can render progress from persisted activity and runtime updates instead of depending on callback-oriented orchestration APIs.

Two app-facing read concerns remain distinct:

- `GetTaskActivityEvents`
  historical or recent activity shown in a selected task detail view
- `SubscribeTaskRuntimeUpdates`
  live runtime status updates used for UI states such as `working` and `needs_input`

During the refactor, the TUI should stop depending directly on observer infrastructure and should instead consume these through the service boundary.

## Migration Strategy

The migration should be incremental, not a big-bang rewrite.

Recommended strategy:

1. Introduce the new `TaskService` and collapsed port set.
2. Add a temporary compatibility facade for the handler layer.
3. Route `CreateTask` through the new service first.
4. Keep all remaining use cases on the existing implementation behind the same handler-facing interface.
5. Migrate additional use cases one vertical slice at a time.
6. Remove the old service once all handler-facing behavior is served by the new implementation.

The handler should ideally depend on one interface throughout the migration. The temporary split between old and new application logic should be hidden in the composition root rather than leaked into the TUI.

## Suggested Migration Order

The preferred order is:

1. `CreateTask`
2. `AttachToTask`
3. `ListTasks`
4. `GetTaskDetails`
5. `GetTaskActivityEvents`
6. `SubscribeTaskRuntimeUpdates`
7. `ListPullRequests`
8. `Doctor`
9. cleanup of compatibility layers and old ports

This order proves the target architecture with the highest-value workflows first while keeping the system operational between slices.

## Risks And Guardrails

### Risks

- The new service can still become a large orchestration unit if internal workflows are not extracted as it grows.
- Over-collapsing ports could blur important boundaries between persistence and operational clients.
- The TUI may continue to depend on infrastructure details if migration shortcuts bypass the compatibility facade.
- Existing tests may mirror the old abstraction shape and need deliberate re-centering on business workflows.

### Guardrails

- Keep the public service surface fixed to business use cases.
- Do not add new public methods for preflight or implementation-specific operations.
- Prefer moving provider-specific behavior behind `AgentClient` rather than branching in the service API.
- Keep the handler layer unaware of migration internals.
- Validate the new shape with `CreateTask` before committing to broader restructuring.

## Open Implementation Questions

These do not block planning, but they should be settled early in implementation:

- Final naming of some ports, especially whether `ProjectConfigRepository` should instead be named `ProjectConfigStore` for consistency.
- Whether filesystem bootstrap/setup logic stays grouped under repository concerns or is folded into a narrower dedicated capability owned by `CreateTask`.
- The exact app-facing shape of runtime update payloads for `SubscribeTaskRuntimeUpdates`.

