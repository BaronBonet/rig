# TUI Runtime Session State

**Date:** 2026-04-05
**Status:** Approved

## Summary

Add live runtime state detection to the TUI so every task row can show whether the agent is currently running, waiting for user input, finished, or has no detectable active agent session. Use a persistent tmux control-mode monitor instead of provider APIs or ad hoc pane polling.

Phase 1 supports Codex tasks only. Claude tasks continue to show no runtime badge until equally strong Claude-specific detection is implemented.

## Approach

Persistent runtime monitor approach: keep the existing structural task reconciliation for worktree/session/window health, then enrich each task with a separate live runtime state derived from a tmux control-mode monitor plus provider-specific pane detectors.

This is intentionally larger than a simple refresh-time polling change. The goal is stability: fewer timing races, fewer repeated subprocess calls, clearer provider boundaries, and a clean path to Claude support later.

## Design

### 1. Separate structural status from runtime state

The current `Task.Status` continues to represent structural health only:

- `creating`
- `running`
- `degraded`
- `broken`
- `cleaned`

Add a separate live `RuntimeState` concept for UI/runtime classification:

```go
type RuntimeState string

const (
    RuntimeStateNone       RuntimeState = ""
    RuntimeStateRunning    RuntimeState = "running"
    RuntimeStateNeedsInput RuntimeState = "needs_input"
    RuntimeStateFinished   RuntimeState = "finished"
)
```

Add `RuntimeState` to `core.Task` as a derived field populated during reconciliation. It is not a durable workflow status and does not replace `Task.Status`.

Optional live metadata such as `RuntimeStateUpdatedAt` may be added to `Task` if useful for debugging or future notification logic, but runtime state is not persisted in SQLite in this phase.

### 2. Add a tmux runtime monitor abstraction

Introduce a dedicated runtime-monitor port in core for live tmux inspection. The monitor is responsible for the agent runtime signal, not task storage or Bubble Tea rendering.

The tmux-backed implementation should provide enough functionality to:

- connect to a tmux session in control mode
- receive `%output` activity events without spawning a new tmux subprocess each refresh
- inspect panes in the task's agent window
- capture pane content on demand
- query foreground process information for a pane

This monitor should manage a persistent control-mode connection per active session and reuse it across repeated `ListTasks` and `GetTask` calls. The design follows the stable part of Agent Deck's approach: tmux control-mode events plus tool-specific prompt detection, not provider APIs.

### 3. Bind to a stable agent pane

Runtime classification should operate on a specific agent pane, not on the entire session or window as a whole. This matters when users split the `agent` window and one pane is running the provider while another is a shell.

Each monitored task keeps an in-memory stable pane binding:

- If a pane is already bound and still exists, reuse it.
- Otherwise, if exactly one pane in the agent window is running a Codex foreground process, bind to that pane.
- Otherwise, if the window contains exactly one pane, bind to that pane.
- Otherwise, leave the task unbound and report no runtime state.

This prevents flapping between panes and avoids false classification when multiple panes exist in the agent window.

### 4. Codex runtime detector

Phase 1 implements a Codex-specific detector that classifies a bound pane into one of the runtime states.

Use a 2-second recent-activity window by default when interpreting tmux `%output` events. This mirrors the practical "active just now" threshold used by Agent Deck's documented running state and keeps the initial implementation concrete.

Evidence priority:

1. tmux session/window/pane existence
2. bound pane foreground process
3. recent `%output` activity from the control pipe
4. explicit busy markers in live pane content
5. explicit prompt markers in the live prompt area
6. shell return after Codex exits

Codex classification rules:

- **`running`**
  - bound pane exists
  - foreground process is Codex
  - and at least one of:
    - recent output activity has occurred within the 2-second activity window
    - live pane content contains an explicit busy marker such as `esc to interrupt`, `ctrl+c to interrupt`, or Codex's `Working (...)` footer

- **`needs_input`**
  - bound pane exists
  - foreground process is Codex
  - no recent output activity in the 2-second activity window
  - live prompt area contains a Codex input marker such as `›`, `Continue?`, or another explicit Codex prompt
  - busy markers always win over prompt markers if both appear in scrollback

- **`finished`**
  - a bound pane previously associated with Codex now has a shell foreground process
  - this represents "the agent finished and returned to the shell"

- **empty runtime state**
  - no tmux session
  - no agent window
  - no bindable agent pane
  - unsupported provider
  - ambiguous multi-pane state where no single Codex pane can be selected confidently

The Codex detector should be a pure component that accepts a pane snapshot and returns a runtime state. It should not know about storage, services, or TUI rendering.

### 5. Service integration

`Service.ListTasks` and `Service.GetTask` continue to run the existing structural reconciliation first.

After structural reconciliation:

- if the task is broken/cleaned or has no agent session, runtime state becomes empty
- if the provider is unsupported in this phase, runtime state becomes empty
- otherwise the runtime monitor enriches the task with the live runtime state

This keeps the runtime layer additive and prevents any change in the meaning of `Task.Status`.

### 6. TUI presentation

Every task row in the TUI list should display the existing structural task status plus a separate runtime badge.

Examples:

- `● running`
- `◐ needs input`
- `○ finished`

No runtime badge is shown when runtime state is empty, including Claude tasks during phase 1.

The row-level runtime badge is the primary requirement. The selected-task detail bar may later include the same runtime state, but that is not required for this phase.

### 7. Tests

Add tests for the new runtime monitor and detector boundaries:

- tmux monitor tests:
  - control-mode parsing for output events
  - pane listing/binding behavior
  - pane capture behavior
  - session cleanup when control pipes close

- Codex detector tests:
  - explicit busy markers classify as `running`
  - prompt marker with no busy marker classifies as `needs_input`
  - shell return after Codex classifies as `finished`
  - stale prompt text does not override a live busy marker
  - ambiguous multi-pane windows produce empty runtime state

- service tests:
  - runtime enrichment does not affect structural `Task.Status`
  - unsupported providers produce empty runtime state
  - missing sessions still classify structurally as broken and runtime as empty

- TUI tests:
  - every row renders the runtime badge independently of structural status
  - tasks with empty runtime state render no runtime badge

## Scope boundaries

- No SQLite schema changes for runtime state in phase 1
- No provider API integration
- No Claude runtime badge until a separate high-confidence Claude detector exists
- No notification system or historical runtime-state tracking in this phase
- No attempt to infer runtime state from arbitrary shell output in unsupported tools

## Notes

This design is intentionally optimized for stability over minimal code churn. A simpler refresh-time polling design would be smaller, but it would be less reliable, more expensive, and a weaker base for future Claude support.
