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
- Task operation: A lifecycle mutation for one Task, such as retrying creation,
  reconnecting its Session, or deleting it. At most one Task operation may run
  for a Task at a time; unrelated Tasks remain independent.
- Workspace: The local filesystem environment where a task runs.
- Worktree: A git worktree used to isolate task changes from the main checkout.
- Repository context: The repository root, name, and base branch used when Rig
  creates or inspects a task.
- Session: The tmux-backed interactive environment for a task.
- Session launcher: The core component that resolves a task's configured
  provider, prepares its workspace, starts or bootstraps its session and the
  agents in it, and records the Agent sessions it launches. Shared by task
  creation and session reconnect.
- Task observation: The core component that consumes provider hook events and
  provider session history to maintain a task's agent sessions, their runtime
  status, activity, and token usage — including liveness checks against the
  Session and recovery of stale status.
- Provider: An AI coding runtime that can back a task, such as Codex.
- Supported provider: A provider Rig knows how to integrate with.
- Configured provider: A supported provider the user has enabled for task
  creation and for agents started in a task.
- Provider setup: The mandatory first-run flow where the user chooses configured
  providers and a default provider.
- Launch provider: The provider Rig launches for a task at creation, and on
  Reconnect when there is no Agent session it can restore. Nothing changes it
  afterwards: other agents started in the task are Agent sessions.
- Default provider: The user-level provider Rig preselects when creating a
  task.
- Agent session: One agent process, such as a Claude or Codex CLI, running in
  one tmux pane for a task. Rig records it when it launches the agent, or
  else from the first hook event it can trace to the pane, and it holds the
  agent's current Provider session and hook-driven status. A Codex using its
  shared daemon runs hooks outside its pane, so Rig places it by the task's
  one untracked Codex pane instead.
- Provider session: One conversation of a provider, observed for a task,
  including its provider session ID, transcript path, model, working
  directory, and latest event name. `/clear` and `/resume` start a new one in
  the same agent process.
- Provider transcript: An append-oriented record emitted by a Provider session
  from which Rig can recover Runtime status, Activity events, and Token usage.
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
  observes, the runtime phase, if any, each drives, and any hook timeout that
  must replace the provider's default. Rig derives hook registration, provider
  health checks, and status mapping from it.
- Runtime status: The current live phase of an Agent session, derived from its
  hook evidence, its pane's live state, and the recoverable history of its
  current Provider session. A Task's Runtime status is its most urgent live
  Agent session's: needs input, then working, then working in the background,
  then starting. It is a live view rather than an event history, separate from
  the durable task record.
- Background work: Provider work the root agent started that is still in
  flight when its turn ends, such as background subagents, shells, monitors,
  and workflows. A Task with background work is working in the background, not
  waiting for input. Claude wakes the agent when the work completes; Codex
  does not, so a Codex Task returns to needs input once its last subagent
  finishes.
- Activity event: A compact persisted event used by the detail view to show
  recent user prompts and assistant actions.
- Resume metadata: An Agent session's provider and current Provider session,
  which Reconnect resumes after the task's tmux session has been lost. Only
  hook events Rig places in the Agent session change it, and reports of a
  Provider session ending never do.
- Token usage: The summed provider token counts observed across a task's
  provider sessions.
- Pull request status: The GitHub pull request state associated with a task
  branch, if any.

## Relationships

- A Task has exactly one Launch provider.
- A Default provider becomes the Launch provider for a new Task unless the user
  selects a different provider during Task creation.
- An environment-selected Default provider must be one of the user's Configured
  providers.
- A Configured provider must also be a Supported provider.
- A Configured provider must pass Rig's provider setup checks before it can be
  used for Task creation.
- The Task creation UI offers only Configured providers.
- Provider setup produces the user's Configured providers.
- Tasks remain visible even when their Launch provider is not currently a
  Configured provider.
- A Task may have many Provider sessions over time.
- A Provider session belongs to exactly one Task and one Provider.
- A Task may have many Agent sessions, which may use different Providers. At
  most one open Agent session runs in a tmux pane.
- An Agent session has exactly one current Provider session at a time. Only
  events of its current Provider session drive its status.
- An Activity event from a hook event Rig placed in an Agent session belongs
  to that Agent session, which is how a Task's Agent sessions each show their
  latest prompt. Activity recovered from transcripts belongs to none.
- An Agent session's Runtime status is driven by its root agent, plus any
  subagent permission request that needs user action. Subagent work hooks and
  transcript completion do not otherwise drive it; in-flight subagents reach it
  only as Background work after the root agent's turn end, reported by the
  turn-end hook (Claude) or recovered from subagent transcripts (Codex).
- A Task's Runtime status follows its most urgent live Agent session; ties go
  to the most recent status change. A Task with no live Agent session is
  stopped, and one that never had an Agent session has no Runtime status.
- An Agent session ends when its agent exits or its pane closes. A clean exit
  of Claude, or of a Codex without the shared daemon, ends it at once, as the
  provider reports its current Provider session ending; tmux liveness covers
  kills, crashes, closed panes, and a Codex on the shared daemon, which keeps
  its Provider session after the agent exits. A new Provider session in the
  same agent, as after `/clear` or `/resume`, keeps the Agent session. It
  stays open while its Task's Session is gone, so Reconnect can restore it.
- Starting a Provider in a Task workspace never changes the Task's Launch
  provider: the agent becomes another Agent session.
- Rig never starts new agents in a Task: the user splits a pane in the Task's
  Session and starts one there. Rig launches agents only at Task creation and
  on Reconnect, and each counts as starting until its first hook event.
- Every ready Task workspace registers hook forwarding for all Configured
  providers, so any Configured provider launched there is observable as an
  Agent session.
- Reconnect recreates a Task's missing Session and restores each of its open
  Agent sessions there, oldest first: the first in the task window's pane and
  each of the rest in a pane split in beside it, left to right, so the Session
  keeps one window for its agents. Each resumes its own current Provider
  session. It skips an Agent session whose Provider is no longer configured,
  which then ends like any whose pane is gone, and ends one with no Provider
  session to resume. With nothing to restore, it launches the Launch provider
  fresh. It never changes the Launch provider.

## Language Rules

- Prefer "task" over "job" or "run" when referring to managed coding work.
- Prefer "workspace" for the filesystem environment and "worktree" only for
  the git isolation mechanism.
- Prefer "provider" for Codex or other future AI runtimes.
- Keep durable task fields separate from live runtime observations in design
  discussions.
- Use "daemon" for the background process and "TUI" for the foreground
  terminal interface.
