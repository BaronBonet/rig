# Architecture

Rig is a local terminal app for running AI-assisted coding tasks in isolated
workspaces. The foreground `rig` command shows the TUI, while a background task
daemon owns long-running task operations and durable state.

## High-Level Architecture

![High-Level Architecture](diagrams/architecture.svg)

| Component | Role |
|-----------|------|
| **TUI** | The foreground interface for creating tasks, browsing task state, and attaching to running work. |
| **Background task daemon** | A long-lived `rig` process that creates tasks, starts or resumes Codex, records status, and serves updates back to the TUI. |
| **Unix socket server** | The local control channel between the TUI and daemon. It carries commands such as creating tasks and streams live task updates back to the TUI. |
| **HTTP hook server** | A loopback-only endpoint used by Codex hooks to report session, prompt, tool, and stop events back to Rig. |
| **SQLite** | The local task database. It stores task records, latest status, activity snippets, and resume metadata. |
| **Codex CLI** | The provider Rig starts for each task. Codex runs in an isolated task workspace and sends hook events back to the daemon. |

## How It Fits Together

When you launch `rig`, the foreground process ensures the task daemon is running
and then opens the TUI. The TUI talks to the daemon over a local Unix socket
instead of doing task orchestration itself.

When you create a task, the daemon prepares the isolated workspace, starts or
resumes Codex, records the task in SQLite, and streams status updates back to the
TUI. Codex hook events are posted to the daemon's local HTTP hook server, which
updates SQLite and any active TUI subscriptions.

This split keeps the terminal UI responsive while task setup, Codex sessions,
and status collection continue in the background.
