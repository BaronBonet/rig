# TUI-Only CLI Dead Code Reduction

**Date:** 2026-04-06
**Status:** Approved

## Summary

Reduce the application surface to the paths that matter for the current product: the TUI and a narrow `doctor` command. Remove non-TUI command handlers, compatibility methods, and test-only wrappers that no longer serve a runtime path. Preserve the existing TUI behavior while making the codebase smaller and easier to understand.

## Goals

- Make `agent` launch the TUI directly.
- Keep `agent doctor` as the only non-TUI command.
- Remove dead CLI code for commands the product no longer exposes.
- Remove service methods and helpers that survive only for tests or deleted codepaths.
- Preserve the current TUI behavior and workflow.

## Non-goals

- Changing the behavior or feature set of the TUI.
- Reworking core architecture beyond the dead-code reduction needed for this pass.
- Adding new commands or replacement CLI flows.

## Approach

Treat the TUI as the primary application interface and `doctor` as the only operational escape hatch. Everything else should justify its existence by serving one of those two entrypoints. If a method, helper, or test exists only to support a deleted command or an obsolete wrapper, it should be removed.

## Design

### 1. Command surface

The binary should expose exactly two entry paths:

- `agent`
- `agent doctor`

`agent` starts the Bubble Tea TUI directly. The current `agent tui` subcommand goes away because the root command itself becomes the application launcher.

The following command paths are removed:

- `agent tui`
- `agent new`
- `agent ls`
- `agent open`
- `agent status`

### 2. CLI package shape

The CLI handler package should shrink to the code needed for:

- launching the TUI from the root command
- running `doctor`
- rendering and updating the TUI model

That means the root command should stop acting like a command tree for operational subcommands and instead behave like an application entrypoint with a single retained diagnostic subcommand.

The CLI-facing service interface should contain only the methods still used by the TUI or `doctor`:

- `Doctor`
- `ListTasks`
- `OpenTask`
- `DeleteTaskResources`
- `SuggestTaskName`
- `CreateTaskWithProgress`

Any broader compatibility interface should be removed.

### 3. Dead-code removal rules

This pass should be intentionally aggressive about dead surface removal.

Remove code when any of the following is true:

- the code exists only for a deleted command
- the code is used only by tests for a deleted command
- the code is a thin wrapper whose richer replacement is already the real runtime path
- the code exists only to keep an interface artificially broad

Concrete examples:

- `Service.NewTask(...)` should be removed if the runtime uses `CreateTaskWithProgress(...)` and the wrapper survives only because tests call it
- the private `createTask(...)` helper should be inlined into `CreateTaskWithProgress(...)` if it no longer improves structure after `NewTask(...)` is removed
- fake CLI service implementations should be narrowed so tests only satisfy the methods still used by TUI or `doctor`

The rule is not “keep a convenience API because it might be useful later.” The rule is “keep only what serves the current product surface.”

### 4. Core API expectations

`core.Service` should remain business-oriented, but this pass should remove dead or compatibility-only methods from its exported surface where possible.

If a service method is only used in tests and does not provide independent runtime value, it should not survive this cleanup just to preserve an old shape. Tests should follow the real runtime path rather than forcing production code to carry obsolete entrypoints.

Core helpers should also be re-evaluated with the same standard. If a helper only exists to support a wrapper that has been removed, inline it or delete it.

### 5. Testing strategy

Tests should be updated to match the reduced product surface.

Keep tests for:

- root command launching the TUI
- `doctor`
- TUI behavior and flows
- core behavior still used by the TUI and `doctor`

Remove tests for:

- deleted command handlers
- deleted service wrappers
- interface shapes that no longer exist

The test suite should validate that the application still behaves correctly through its real entrypoints, not preserve legacy abstractions.

## Migration plan

Implementation should proceed in three focused steps:

1. Reduce the CLI surface so `agent` launches the TUI and only `doctor` remains as a subcommand.
2. Delete command handlers, tests, and interface methods that are now unreachable.
3. Remove dead core wrappers and helpers, then update the remaining tests to follow the real TUI-backed creation flow.

## Scope boundaries

- `doctor` remains intentionally available outside the TUI.
- No replacement command aliases should be added for removed subcommands.
- The success criterion is a smaller codebase with the same TUI behavior, not preservation of previous command compatibility.
