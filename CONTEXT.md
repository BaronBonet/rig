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
  submission: the prompt text, the chosen provider, its launch options, and
  the optional pull request source; or, for a new session, the continuation
  prompt and whether to write a handoff. Discarded on cancel; cleared once
  submitted.
- Launch options: The model and effort a Task's provider starts and resumes
  with, chosen when the Task or a new session is composed, kept on the Task,
  and remembered per provider as the preselection for the next launch.
- New session: Starting a fresh provider session in an existing Task, in the
  same workspace and tmux session, usually from a handoff note, instead of
  compacting a long conversation or creating another Task. It either replaces
  the Task's session in its window or runs alongside it as a Child task.
- Continuation prompt: What a new session is asked: the Task and its original
  ask, where to pick up its state (the handoff note, or the workspace), then
  the user's own instruction for that session.
- Child task: A Task that is one session of another, its parent: it shares the
  parent's workspace, branch and tmux session, runs in its own window there
  with its own task ID, and has its own status, activity and token usage. It
  is listed under its parent. A parent with its children is a task group.
- Shelf: Where Tasks the user is not working on now are kept, off the current
  list. Shelving a Task ends its provider sessions and keeps everything else:
  its record, child tasks, launch options, history and workspace. A shelved
  Task comes back when it is unshelved, and its latest session resumes when
  it is opened.
- Rename: Giving a Task a new name, usually because it is being reused for
  another purpose. It happens in Rig, or by renaming the Task's provider
  session, whose new title the Task takes. A rename drops the Task's original
  ask, so new sessions are not briefed on what it was first for.
- Handoff note: A note the previous provider session writes, in print mode on
  a fork that is not saved, for a new session to start from: goal, state,
  decisions, where things are, gotchas, next step. Kept under Rig's data dir.
- Creation status: The durable state of task setup: `creating`, `ready`, or
  `failed`.
- Creation step: The retryable task setup milestone, such as suggesting a name,
  creating the worktree, preparing the workspace, or starting the session.
- Task operation: A lifecycle mutation for one Task, such as retrying creation,
  reconnecting its Session, switching its Provider, or deleting it. At most one
  Task operation may run for a Task at a time; unrelated Tasks remain independent.
- Workspace: The local filesystem environment where a task runs.
- Worktree: A git worktree used to isolate task changes from the main checkout.
- Session import: Making a provider session started outside Rig, in the launch
  folder or below it, a folder task in the folder the session was started in,
  and resuming it in the task's own Session through the reconnect path.
- Provider configuration: The variables that choose a provider's account and
  session store (CLAUDE_CONFIG_DIR, CODEX_HOME), captured from the rig window a
  task came from and kept on the task, with an unset variable meaning the
  provider's default.
- Folder task: A task whose workspace is an existing folder, Git or not, used as
  it is. It has no branch of its own, may share its folder with other tasks,
  and is never seeded or removed by Rig.
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
- Provider reconciliation: Rig restoring provider ownership from live Session
  process evidence when the recorded Active provider has exited.
- Provider session: A provider runtime session observed for a task, including
  its provider session ID, transcript path, model, working directory, and latest
  event name.
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
  observes and the runtime phase, if any, each drives. Rig derives hook
  registration, provider health checks, and status mapping from it.
- Runtime status: The current live task phase derived from persisted provider
  evidence, the task's live session state, and recoverable provider session
  history. It is a live view rather than an event history, separate from the
  durable task record.
- Background work: Provider work the root agent started that is still in
  flight when its turn ends, such as background subagents, shells, monitors,
  and workflows. A Task with background work is working in the background, not
  waiting for input. Claude wakes the agent when the work completes; Codex
  does not, so a Codex Task returns to needs input once its last subagent
  finishes.
- Activity event: A compact persisted event used by the detail view to show
  recent user prompts and assistant actions.
- Resume metadata: The minimal provider state needed to reconnect a task session
  after its tmux session has been lost.
- Session readiness: The point at which a freshly launched provider can take
  the task prompt: its session-start hook has been observed and its ready
  marker is on screen on a line other than the shell's echo of the launch
  command. The prompt is typed then, never on the marker alone.
- Token usage: The summed provider token counts observed across a task's
  provider sessions.
- Touched worktree: A Git worktree a task's provider sessions have edited, shown
  with the branch it has checked out now. Rig records the branch at the task's
  latest edit there; a different branch now means the worktree moved on to
  other work.
- Pull request status: The GitHub pull request state associated with a task
  branch, if any.

## Relationships

- A Task has exactly one Active provider.
- A Session carries its Task's ID in `RIG_TASK_ID`. A Hook event carrying a
  known Task ID belongs to that Task; otherwise it belongs to the single Task
  whose Workspace matches the hook's working directory, and a directory shared
  by several Tasks attributes nothing.
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
- A Task may have many Provider sessions over time; a New session adds one
  without changing the Task's identity, workspace, or tmux session.
- A New session that replaces the Task's session ends the Provider in the
  Task's Session first, with the Provider's own exit command, once it sits
  idle at its prompt; it is refused while the Provider is working. One that
  runs alongside leaves the running Provider alone.
- A Child task is a folder Task of its parent's workspace: never seeded, never
  removed. Deleting a Child task closes only its window; deleting the parent
  closes the Session and removes every Child task's record.
- A Child task's hooks carry its own Task ID, set on its window, so they are
  never attributed to the parent. A New session of a Child task joins the
  parent's group.
- Shelving or unshelving a Task moves its Child tasks with it; a Child task
  can also be shelved on its own, which closes only its window. Shelving is
  refused while any of them is working.
- A Rename changes only the Task's display name and original ask; its slug,
  and so its workspace, branch and tmux session, stays. A provider session
  renames its Task only when the title the user gave it changes, so a Rename
  made in Rig stands until the session is renamed again. Only the Active
  provider's sessions rename the Task.
- A Handoff note is written by the Task's latest Provider session of its
  Active provider, and only by a Provider that supports it.
- A Provider session belongs to exactly one Task and one Provider.
- A Task's Runtime status is driven by the root agent of its Active provider,
  plus any subagent permission request that needs user action. Subagent work
  hooks and transcript completion do not otherwise drive it; in-flight
  subagents reach it only as Background work after the root agent's turn end,
  reported by the turn-end hook (Claude) or recovered from subagent transcripts
  (Codex).
- Provider adoption changes a Task's Active provider without creating a new
  Task.
- Provider adoption occurs when Rig observes the start of a manually launched
  Provider session in a Task workspace.
- Provider reconciliation changes the Active provider only when its process is
  absent and exactly one other Configured provider process is present. Missing
  or ambiguous process evidence never changes provider ownership.
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
