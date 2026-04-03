# Agent

`agent` is a Go CLI for creating Codex-backed git worktrees and tmux sessions from a task prompt.

## V1 Scope

The first version supports:

- `agent new`
- `agent ls`
- `agent open`
- `agent status`
- `agent doctor`

The first release only supports `codex` as the provider, uses SQLite for persisted task state, and defaults to a single-window tmux session per task.

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

## Current Limitations

- No cleanup or merge commands yet
- No multi-window tmux layouts yet
- No Claude provider yet
- Config file loading is not implemented yet
