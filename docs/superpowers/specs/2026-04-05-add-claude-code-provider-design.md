# Add Claude Code Provider Support

**Date:** 2026-04-05
**Status:** Approved

## Summary

Add Claude Code as a second provider alongside Codex. The tool currently only supports Codex; this change introduces a generic provider interface, a Claude Code adapter, per-task provider selection in the TUI, and a `--provider` CLI flag.

## Approach

Minimal adapter approach: rename the Codex-specific interface to a generic one, add a parallel Claude adapter, and wire up provider selection at both the CLI and TUI level.

## Design

### 1. Interface rename

Rename `CodexRepository` to `ProviderRepository` in `internal/core/ports.go`. Same three methods:

```go
type ProviderRepository interface {
    ProposeTaskName(ctx context.Context, prompt string) (string, error)
    BuildLaunchCommand(task *Task) ([]string, error)
    IsAvailable(ctx context.Context) error
}
```

All references in `Service`, fakes, and tests updated accordingly. The existing Codex adapter continues to satisfy this interface unchanged.

### 2. Claude Code adapter

New file: `internal/adapters/repository/claude/repository.go`

Implements `ProviderRepository`:

- **`IsAvailable`**: runs `claude --version`
- **`ProposeTaskName`**: runs `claude -p --output-format json "Reply with only a short task title: <prompt>"`, parses the JSON response `result` field, applies title normalization/filtering similar to the Codex adapter
- **`BuildLaunchCommand`**: returns `["claude", task.Prompt]` (launches Claude Code interactively in the tmux agent window)

### 3. Provider selection

- Add `Provider` field to `core.Config` (default: `"codex"`)
- Add `Provider` field to `core.NewTaskInput` so each task can specify its provider
- Add `--provider` flag to `agent new` CLI command (accepts `"codex"` or `"claude"`, default `"codex"`)
- In `main.go`, `newService` switches on the provider string to instantiate either `codexrepo.NewRepository` or `clauderepo.NewRepository`
- `Task.Provider` is set from `NewTaskInput.Provider` instead of being hardcoded to `"codex"`
- `agent doctor` checks whichever provider is configured
- Rename `TaskProgressCodexLaunching` to `TaskProgressAgentLaunching`; progress message becomes `"Launching <provider>..."` instead of `"Launching Codex..."`

### 4. TUI provider picker

In the prompt input view (`tuiModePromptInput`):

- Display the currently selected provider with an icon and label (e.g. `codex` or `claude`)
- `tab` key cycles between available providers
- Selected provider stored on the TUI `model` and passed through `NewTaskInput` to task creation
- Default provider is `codex`

### 5. Tests

- Rename `fakeCodexRepository` to `fakeProviderRepository` in `fakes_test.go`
- New `internal/adapters/repository/claude/repository_test.go` mirroring Codex test structure: test `ProposeTaskName` JSON parsing, title normalization, `BuildLaunchCommand` output, `IsAvailable`
- Existing core tests unchanged (fake satisfies renamed interface)

## Scope boundaries

- No `agent.yaml` provider configuration
- No provider auto-detection
- No changes to the SQLite schema (the `provider` text column already stores the provider string)
