# TUI Provider Badge

**Date:** 2026-04-05
**Status:** Approved

## Summary

Show the task provider on every TUI task row so users can immediately see whether a session is running under Codex or Claude.

## Approach

Minimal presentation-only approach: reuse the existing `Task.Provider` field and current provider icon mapping, and update the row formatter so every visible task row renders a provider badge before the structural status and optional runtime badge.

## Design

### 1. Row presentation

Each task row should always render a provider badge, regardless of runtime state.

The row layout becomes:

`<task name>  <provider>  <structural status>  <runtime badge?>`

Examples:

- `billing retry flow  ⚡ codex  ● running  ◐ needs input`
- `release packaging  ✦ claude  ● running`

Provider is a stable identity label for the task, while the runtime badge continues to represent live activity. Rendering provider before the status badges keeps the provider easy to scan and avoids overloading the runtime badge with identity.

### 2. Implementation boundary

This change stays in the TUI rendering layer.

- No changes to `core.Task`, service orchestration, runtime detection, repositories, or SQLite
- No changes to provider selection behavior during task creation
- Reuse the existing `providerIcon()` helper in the TUI

The implementation updates the list row formatter in `internal/adapters/handler/cli/tui_model.go` so provider is always shown for every visible task row.

### 3. Tests

Add or update TUI tests in `internal/adapters/handler/cli/tui_model_test.go` to verify:

- Codex rows show `⚡ codex`
- Claude rows show `✦ claude`
- Provider remains visible when runtime state is empty
- Provider and runtime badges can coexist on the same row

## Scope boundaries

- No provider badge in additional views beyond the task list
- No new provider-specific styling rules beyond the existing icon mapping
- No change to runtime-state semantics or runtime badge placement
