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
`agent tui` is the first TUI: it shows tracked tasks, live tmux/worktree state, lets you jump into a selected session, and lets you remove runtime resources for a task.

## Requirements

The CLI expects these binaries to be available on `PATH`:

- `git`
- `tmux`
- `codex`

## Defaults

- Config is loaded from an optional repo-local `agent.yaml` when present.
- SQLite state path defaults to `~/.local/share/agent/state.db`.
- Worktrees default to sibling directories such as `../repo-billing-retry-flow`.
- Branches default to `feat/<slug>`.
- tmux sessions default to `<repo>-<slug>`.

## Repo Config

Place `agent.yaml` at the root of the repository you run `agent` inside:

```yaml
seed:
  copy:
    - .env
    - .lazy.lua
    - local/
```

Each `seed.copy` entry is copied from the repo root into the new worktree after `git worktree add` and before tmux starts. Entries are literal repo-relative paths only, so glob patterns are not supported.

If a configured source path is missing, the `new` command fails. If the destination already exists in the worktree, the command also fails. Symlink sources are rejected explicitly.

## Usage

Show environment health:

```bash
go run ./cmd/agent doctor
```

Create a task interactively:

```bash
go run ./cmd/agent new "add billing retry flow"
```

The command now prints stage-by-stage progress to stderr and then opens the tmux session automatically.
When seeding is configured, it also prints `Seeding workspace...` followed by one `Copied ...` line per seeded path.

When prompted for the proposed name, press Enter to accept it or type a replacement.
Typing `y` or `yes` also accepts the suggested name.

Create a task non-interactively and print JSON:

```bash
go run ./cmd/agent new --non-interactive --json "add billing retry flow"
```

`--json` keeps stdout machine-readable and does not auto-open the tmux session.

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
- `Enter` opens the selected task session
- `x` starts cleanup for the selected task
- `y` confirms cleanup
- `n`, `esc`, or `q` cancel the confirmation prompt
- `r` refreshes task state
- `q` quits the TUI from the main view

Cleanup deletes the tmux session and worktree for the selected task, but keeps the branch.

## Current Limitations

- No multi-window tmux layouts yet
- No Claude provider yet
- `agent.yaml` only supports `seed.copy` in v1
