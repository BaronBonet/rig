# Context

## Domain

Rig is a local terminal app for running AI-assisted coding tasks in isolated
workspaces. The foreground `rig` command provides the TUI, while a background
task daemon owns long-running task operations, durable task state, provider hook
events, and live status updates.

Use `rig` for the CLI command and Rig for the product or system.

## Glossary

- Task: A durable unit of AI-assisted coding work managed by Rig.
- Task creation: The workflow that turns a prompt or pull request source into a
  prepared task workspace and interactive provider session.
- Creation status: The durable state of task setup: `creating`, `ready`, or
  `failed`.
- Creation step: The retryable task setup milestone, such as suggesting a name,
  creating the worktree, preparing the workspace, or starting the session.
- Workspace: The local filesystem environment where a task runs.
- Worktree: A git worktree used to isolate task changes from the main checkout.
- Repository context: The repository root, name, and base branch used when Rig
  creates or inspects a task.
- Session: The tmux-backed interactive environment for a task.
- Provider: The AI coding runtime backing a task. The current provider is
  Codex.
- Provider session: A provider runtime session observed for a task, including
  its provider session ID, transcript path, model, working directory, and latest
  event name.
- Daemon: The long-lived background Rig process that coordinates task creation,
  task state, provider hook handling, and frontend updates.
- TUI: The foreground terminal interface for creating tasks, browsing task
  state, and attaching to sessions.
- Frontend: The application-facing interface used by the TUI. In normal use it
  talks to the daemon over a local Unix socket.
- Unix socket server: The local control channel between the TUI and daemon.
- Hook server: The loopback HTTP endpoint that receives provider hook events.
- Hook event: A structured provider event, such as session, prompt, tool, or
  stop activity, consumed by the daemon.
- Runtime status: The current live task phase derived from provider hook events,
  separate from the durable task record.
- Activity event: A compact persisted event used by the detail view to show
  recent user prompts and assistant actions.
- Resume metadata: The minimal provider state needed to reconnect a task session
  after its tmux session has been lost.
- Token usage: The summed provider token counts observed across a task's
  provider sessions.
- Pull request status: The GitHub pull request state associated with a task
  branch, if any.

## Language Rules

- Prefer "task" over "job" or "run" when referring to managed coding work.
- Prefer "workspace" for the filesystem environment and "worktree" only for
  the git isolation mechanism.
- Prefer "provider" for Codex or other future AI runtimes.
- Keep durable task fields separate from live runtime observations in design
  discussions.
- Use "daemon" for the background process and "TUI" for the foreground
  terminal interface.
