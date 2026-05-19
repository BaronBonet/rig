# Rig

`rig` is a local terminal app for running AI-assisted coding tasks in isolated
git worktrees and tmux sessions.

Rig gives each task its own workspace, branch, terminal session, Codex runtime,
and durable task record. The foreground TUI stays focused on browsing, creating,
attaching to, and cleaning up tasks while a background daemon handles longer
running orchestration.

![Rig task list](docs/diagrams/rig-example.png)

## Features

- **Task dashboard**: browse all known tasks in a terminal UI grouped by
  repository, with live status, PR state, elapsed time, and token usage.
- **Prompt-backed task creation**: start a new Codex task from a prompt; Rig
  suggests a task name, creates the branch and worktree, prepares the workspace,
  and starts the tmux session.
- **Pull request-backed task creation**: pick an open GitHub pull request and
  create a local task workspace for reviewing or continuing that branch.
- **Isolated workspaces**: every task runs in its own git worktree so parallel
  tasks do not collide with the main checkout or each other.
- **Tmux sessions**: attach to any task from the TUI, reconnect missing sessions
  from provider resume metadata, and keep work running outside the foreground
  `rig` process.
- **Codex integration**: Rig starts Codex, installs local hooks, captures session
  and activity events, and stores compact task history for the detail view.
- **Live observability**: the daemon records task status, recent prompt and
  assistant activity, provider sessions, transcript metadata, and token usage in
  SQLite.
- **Retry and cleanup**: retry failed task setup from the recorded creation step
  or remove a task's tmux session and worktree while keeping its branch.
- **Workspace seeding**: copy repo-local files and run a repo-local setup script
  in each new task workspace through `.rig.yaml`.
- **Environment checks**: `rig doctor` verifies the local tools Rig depends on.

## Install

Install the latest GitHub release on macOS or Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/BaronBonet/rig/main/scripts/install.sh | sh
```

The installer places `rig` in `~/.local/bin` and adds it to your `PATH`
automatically for zsh and bash.

On macOS, if the system blocks the binary on first run, clear the quarantine flag
once:

```bash
xattr -d com.apple.quarantine ~/.local/bin/rig
```

## Requirements

`rig` expects these binaries to be available on `PATH`:

- `git`
- `tmux`
- `codex`
- `gh` (optional, needed for PR-backed task creation and PR status checks)

## Codex Hooks

Rig uses Codex hooks to capture live task status, recent activity, provider
session metadata, transcript paths, and token usage. Hooks must be enabled in
Codex for the task dashboard and detail view to stay up to date.

Enable hooks in `~/.codex/config.toml`:

```toml
[features]
hooks = true
```

Rig installs and updates its own Codex hook forwarding entries automatically
when it starts task sessions. The forwarding hooks post local Codex events to
Rig's background daemon; other Codex hooks and plugins can remain enabled.

Task status is eventually consistent with Codex hook delivery. Rig does not
watch tmux keystrokes or infer state from text typed into a task session. If a
task still shows `needs input` after you submit a prompt, Rig has not yet
received the next Codex hook, such as `UserPromptSubmit`, `PreToolUse`, or
`PostToolUse`, that marks the task as working.

Use `rig doctor` to verify that Codex is available and that Rig's hook
forwarding is installed correctly. Do not disable Codex hooks globally if you
want Rig observability to work.

## Usage

Launch the terminal UI from a git repository:

```bash
rig
```

Create a task with `n`, enter a prompt, and press `enter`. Use `ctrl+p` from the
prompt view to create from a GitHub pull request instead.

Common TUI keys:

| Key | Action |
|-----|--------|
| `n` | Create a task from a prompt |
| `ctrl+p` | Pick a GitHub pull request while creating a task |
| `enter` | Attach to the selected task's tmux session |
| `r` | Refresh task data |
| `R` | Retry a failed task creation |
| `x` | Clean up the selected task's tmux session and worktree |
| `q` | Quit |

Check environment health:

```bash
rig doctor
```

Manage the background task daemon:

```bash
rig daemon status
rig daemon start
rig daemon stop
rig daemon restart
```

## Workspace Seeding

Configure repository-specific workspace setup with a `.rig.yaml` file in the
repo root:

```yaml
seed:
  copy:
    - .env
    - local/
  setup_script: scripts/worktree-setup.sh
```

- `seed.copy` copies repo-relative files or directories into the new worktree.
- Symlinks inside copied directories are followed only when they resolve within
  the repo root; symlinks that resolve outside the repo are rejected.
- `seed.setup_script` runs a repo-relative script inside the new worktree after
  copying completes.
- Paths in `.rig.yaml` must be repo-relative. Absolute paths, `..`, and glob
  patterns are rejected.

## Architecture

Rig is split between a foreground terminal UI and a background task daemon. The
daemon owns task creation, local state, provider hooks, and live update streams.

![High-Level Architecture](docs/diagrams/architecture.svg)

| Component | Role |
|-----------|------|
| **TUI** | The foreground interface for creating tasks, browsing task state, and attaching to running work. |
| **Background task daemon** | A long-lived `rig` process that creates tasks, starts or resumes Codex, records status, and serves updates back to the TUI. |
| **Unix socket server** | The local control channel between the TUI and daemon. It carries commands such as creating tasks and streams live task updates back to the TUI. |
| **HTTP hook server** | A loopback-only endpoint used by Codex hooks to report session, prompt, tool, and stop events back to Rig. |
| **SQLite** | The local task database. It stores task records, latest status, activity snippets, token usage, and resume metadata. |
| **Codex CLI** | The provider Rig starts for each task. Codex runs in an isolated task workspace and sends hook events back to the daemon. |

When you launch `rig`, the foreground process ensures the task daemon is running
and then opens the TUI. The TUI talks to the daemon over a local Unix socket
instead of doing task orchestration itself.

When you create a task, the daemon prepares the isolated workspace, starts or
resumes Codex, records the task in SQLite, and streams status updates back to the
TUI. Codex hook events are posted to the daemon's local HTTP hook server, which
updates SQLite and any active TUI subscriptions.

This split keeps the terminal UI responsive while task setup, Codex sessions,
and status collection continue in the background.
