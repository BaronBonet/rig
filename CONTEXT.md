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
- Task draft: The in-progress task the TUI user is assembling before
  submission: the prompt text, the chosen provider, and the optional pull
  request source. Discarded on cancel; cleared once creation is submitted.
- Creation status: The durable state of task setup: `creating`, `ready`, or
  `failed`.
- Creation step: The retryable task setup milestone, such as suggesting a name,
  creating the worktree, preparing the workspace, or starting the session.
- Workspace: The local filesystem environment where a task runs.
- Worktree: A git worktree used to isolate task changes from the main checkout.
- Repository context: The repository root, name, and base branch used when Rig
  creates or inspects a task.
- Session: The tmux-backed interactive environment for a task.
- Session launcher: The core component that resolves a task's configured
  provider, prepares its workspace, and starts or bootstraps its session.
  Shared by task creation, session reconnect, and provider switching.
- Task observation: The core component that consumes provider hook events and
  provider session history to maintain a task's runtime status, activity, and
  token usage — including provider adoption and recovery of stale status.
- Provider: An AI coding runtime that can back a task, such as Codex.
- Supported provider: A provider Rig knows how to integrate with.
- Configured provider: A supported provider the user has enabled for task
  creation and switching.
- Provider setup: The mandatory first-run flow where the user chooses configured
  providers and a default provider.
- Active provider: The single provider Rig should launch or resume for a task.
- Default provider: The user-level provider Rig preselects when creating a
  task.
- Provider adoption: Rig making a manually launched provider session the active
  provider for a task.
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
- Hook event catalog: A provider's single declaration of which hook events it
  observes and the runtime phase each drives. Rig derives hook registration,
  provider health checks, and status mapping from it.
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

## Relationships

- A Task has exactly one Active provider.
- A Default provider becomes the Active provider for a new Task unless the user
  selects a different provider during Task creation.
- An environment-selected Default provider must be one of the user's Configured
  providers.
- A Configured provider must also be a Supported provider.
- A Configured provider must pass Rig's provider setup checks before it can be
  used for Task creation.
- The Task creation UI offers only Configured providers.
- Provider setup produces the user's Configured providers.
- Tasks remain visible even when their Active provider is not currently a
  Configured provider.
- A Task may have many Provider sessions over time.
- A Provider session belongs to exactly one Task and one Provider.
- A Task's Runtime status is driven only by its Active provider.
- Provider adoption changes a Task's Active provider without creating a new
  Task.
- Provider adoption occurs when Rig observes the start of a manually launched
  Provider session in a Task workspace.
- Every ready Task workspace registers hook forwarding for all Configured
  providers, so any Configured provider launched there is observable and
  adoptable.
- The Active provider reflects the provider expected to own the Task's
  interactive Session.

## Language Rules

- Prefer "task" over "job" or "run" when referring to managed coding work.
- Prefer "workspace" for the filesystem environment and "worktree" only for
  the git isolation mechanism.
- Prefer "provider" for Codex or other future AI runtimes.
- Keep durable task fields separate from live runtime observations in design
  discussions.
- Use "daemon" for the background process and "TUI" for the foreground
  terminal interface.
