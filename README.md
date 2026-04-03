# Agent

`agent` is a Go CLI for creating Codex-backed git worktrees and tmux sessions from a task prompt.

## V1 Scope

The first version supports:

- `agent new`
- `agent ls`
- `agent open`
- `agent status`
- `agent doctor`
- `agent tui`

The first release only supports `codex` as the provider, uses SQLite for persisted task state, and defaults to a single-window tmux session per task.
`agent tui` is the first TUI and is cleanup-focused: it shows tracked tasks, live tmux/worktree state, and lets you remove runtime resources for a task.

## Requirements

The CLI expects these binaries to be available on `PATH`:

- `git`
- `tmux`
- `codex`

## Defaults

- Config is currently code-defined rather than file-loaded.
- SQLite state path defaults to `~/.local/share/agent/state.db`.
- Worktrees default to sibling directories such as `../repo-billing-retry-flow`.
- Branches default to `feat/<slug>`.
- tmux sessions default to `<repo>-<slug>`.

## Usage

Show environment health:

```bash
go run ./cmd/agent doctor
```

Create a task interactively:

```bash
go run ./cmd/agent new "add billing retry flow"
```

When prompted for the proposed name, press Enter to accept it or type a replacement.
Typing `y` or `yes` also accepts the suggested name.

Create a task non-interactively and print JSON:

```bash
go run ./cmd/agent new --non-interactive --json "add billing retry flow"
```

List tasks:

```bash
go run ./cmd/agent ls
```

Show task status:

```bash
go run ./cmd/agent status billing-retry-flow
```

Open a task session:

```bash
go run ./cmd/agent open billing-retry-flow
```

Open the cleanup TUI:

```bash
go run ./cmd/agent tui
```

Keybindings in the TUI:

- `j` / `k` or arrow keys move between tasks
- `g` / `G` or home/end jump to the top or bottom
- `x` starts cleanup for the selected task
- `y` confirms cleanup
- `n`, `esc`, or `q` cancel the confirmation prompt
- `r` refreshes task state
- `q` quits the TUI from the main view

Cleanup deletes the tmux session and worktree for the selected task, but keeps the branch.

## Current Limitations

- No multi-window tmux layouts yet
- No Claude provider yet
- Config file loading is not implemented yet
