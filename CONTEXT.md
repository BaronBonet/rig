# Context

## Domain

Rig is a local terminal app for running AI-assisted coding tasks in isolated
workspaces. The foreground `rig` command provides the TUI, while a background
task daemon owns long-running task operations, durable task state, provider hook
events, and live status updates. The CLI's non-interactive commands let people,
scripts and agents create and inspect tasks beside the TUI.

Use `rig` for the CLI command and Rig for the product or system.

## Glossary

- Task: A durable unit of AI-assisted coding work managed by Rig.
- Task creation: The workflow that turns a prompt or pull request source into a
  prepared task workspace and interactive provider session, started from the
  TUI or the CLI.
- Parent task: The Task another Task was created from: the one whose worktree
  contained the working directory of the `rig task create` that created it.
- Child task: A Task with a Parent task. The TUI marks its row with `↳`, and
  its parent's detail panel lists it as spawned.
- Task draft: The in-progress task the TUI user is assembling before
  submission: the prompt text, the chosen provider and a Launch choice for
  it, and the optional pull request source. The Launch choice starts from
  the chosen provider's Launch default, and a reasoning level can be chosen
  only together with a model. Discarded on cancel; cleared once creation is
  submitted.
- Creation status: The durable state of task setup: `creating`, `ready`, or
  `failed`.
- Creation step: The retryable task setup milestone, such as suggesting a name,
  creating the worktree, preparing the workspace, or starting the session.
- Ticket reference: The issue or ticket a prompt asks to work on, such as
  `GH #18` or `ABC-123`. It leads the task name, followed by a description of
  the work when the provider manages to look the ticket up.
- Task operation: A lifecycle mutation for one Task, such as retrying creation,
  reconnecting its Session, or deleting it. At most one Task operation may run
  for a Task at a time; unrelated Tasks remain independent.
- Workspace: The local filesystem environment where a task runs.
- Worktree: A git worktree used to isolate task changes from the main checkout.
- Repository context: The repository root, name, and base branch used when Rig
  creates or inspects a task.
- Repo directory: A parent directory, such as `~/code`, whose git repositories
  Rig knows by name. Chosen during Setup and kept in the User config.
- Known repository: A repository a task can be created in by name: the
  repository of an existing Task, or a primary git checkout directly inside a
  Repo directory. A worktree is never one.
- Session: The tmux-backed interactive environment for a task.
- Session launcher: The core component that resolves a task's configured
  provider, prepares its workspace, starts or bootstraps its session and the
  agents in it, and records the Agent sessions it launches. Shared by task
  creation and session reconnect.
- Task observation: The core component that consumes provider hook events and
  provider session history to maintain a task's agent sessions, their runtime
  status, activity, token usage, compactions and context usage — including
  liveness checks against the Session and recovery of stale status.
- Provider: An AI coding runtime that can back a task, such as Codex.
- Supported provider: A provider Rig knows how to integrate with.
- Configured provider: A supported provider the user has enabled for task
  creation and for agents started in a task.
- Setup: The flow, `rig setup` or the TUI's first-run screen, that produces the
  User config: the mandatory Provider setup, then an optional step for Repo
  directories. It installs the Rig skill of each Configured provider.
- Provider setup: The mandatory part of Setup where the user chooses configured
  providers and a default provider.
- User config: Rig's user-level configuration, written by Setup: the
  Configured providers, the Default provider and the Repo directories. It is
  not kept in the task database.
- Rig skill: The user-level skill Setup installs for each Configured provider,
  which teaches its agents when and how to hand work off to a new Task with the
  CLI. Rig owns it and rewrites it on every Setup.
- Launch provider: The provider Rig launches for a task at creation, and on
  Reconnect when there is no Agent session it can restore. Nothing changes it
  afterwards: other agents started in the task are Agent sessions.
- Launch model: The model of the Launch provider chosen at task creation.
  Empty means no choice: Rig passes the provider CLI no model, so its own
  default applies. Nothing changes it afterwards.
- Launch reasoning level: The Reasoning level chosen at task creation for the
  Launch model. Empty means no choice: Rig passes the provider CLI no
  reasoning level, so its own default applies. Nothing changes it afterwards.
- Launch choice: A Task's Launch model and Launch reasoning level, made for
  its Launch provider.
- Launch default: A provider's saved Launch choice, which a Task draft
  starts from when that provider is chosen. It lives in user config, beside
  Provider setup. Creating a Task from a Task draft for a chosen provider
  saves the Task's Launch choice as that provider's Launch default, and an
  empty Launch choice clears it; a Task created from the command line leaves
  it alone. The provider setup screen shows each enabled provider's Launch
  default and can edit it, by the Task draft's rules; it saves an edited one
  only once the Provider setup itself has saved.
- Default provider: The user-level provider Rig preselects when creating a
  task.
- Reasoning level: How much a provider's model reasons before it answers,
  such as `low` or `high`. Each model accepts its own levels, in the order its
  provider CLI reports them, and some accept none.
- Agent session: One agent process, such as a Claude or Codex CLI, running in
  one tmux pane for a task. Rig records it when it launches the agent, or
  else from the first hook event it can trace to the pane, and it holds the
  agent's current Provider session and hook-driven status. A Codex using its
  shared daemon runs hooks outside its pane, so Rig places it by the task's
  one untracked Codex pane instead.
- Provider session: One conversation of a provider, observed for a task,
  including its provider session ID, transcript path, model, working
  directory, and latest event name. Its model is the latest one the
  conversation switched to, or else the one it started with. `/clear` and
  `/resume` start a new one in the same agent process.
- Provider transcript: An append-oriented record emitted by a Provider session
  from which Rig can recover Runtime status, Activity events, Token usage,
  Compactions and Context usage.
- Daemon: The long-lived background Rig process that coordinates task creation,
  task state, provider hook handling, and frontend updates.
- TUI: The foreground terminal interface for creating tasks, browsing task
  state, and attaching to sessions.
- CLI: The non-interactive `rig` commands beside the TUI, such as
  `rig task create`, `rig task list`, `rig task show` and `rig repo list`, for
  people, scripts and agents. It never attaches a terminal to a Session.
- Frontend: The application-facing interface used by the TUI and the CLI. In
  normal use it talks to the daemon over a local Unix socket.
- Unix socket server: The local control channel between the daemon and the TUI
  or the CLI.
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
  provider sessions. The detail view shows it; the task list does not.
- Compaction: A provider summarizing an agent's conversation to free room in
  its context window, losing detail. Rig counts an Agent session's compactions
  in its current Provider session's root transcript, so `/clear` starts the
  count over, and splits them into auto and manual (`/compact`) where the
  provider records the trigger, as Claude does and Codex does not.
- Context usage: How full an Agent session's context window is: the tokens its
  latest request put in its context, out of the model's context window where
  Rig knows it. After a compaction it is Codex's own count of what the
  compaction left, while a Claude agent's context is unknown until its next
  request. Rig reads it from the same root transcript as Compactions, so
  `/clear` starts it over.
- Task branch: A branch a Task's worktree has had checked out, as the
  worktree's reflog records, or the branch the Task was created on. Rig
  records each one it finds, so a branch deleted after its pull request merged,
  or one git has expired from the reflog, stays the Task's. The repository's
  default branch on GitHub is never one; while Rig cannot learn it, it adds no
  branches from the reflog.
- Pull request: A GitHub pull request whose head is a Task branch, in the
  Task's own repository rather than a fork. The current one is the Task's
  newest open or draft pull request, or else its newest. The task list marks a
  Task's row with the current one's state symbol and number, led by the state
  symbols of up to four earlier ones, and the detail panel lists the newest
  five. The daemon looks a Task branch's pull requests up at most once a
  minute, or once a quarter of an hour when all of them are merged or closed,
  and keeps the last ones it found while GitHub cannot be reached.
- Unshipped work: Work in a Task's worktree that none of its Pull requests
  contains yet: uncommitted changes, untracked files included, and commits
  that are neither in one of its Pull requests nor on the repository's
  default branch or a branch one of them merges into. A Pull request contains
  its commits, also once a squash merge has left them off the branch it
  merged into and its branch is gone, and an open one also what was pushed to
  its branch. Pushing alone ships nothing, so commits pushed to a branch
  without a Pull request, or after its Pull request merged, are unshipped.
  Merge commits, changes inside a submodule's own working tree, and the files
  Rig itself writes into the workspace, such as Claude's workspace settings,
  are never unshipped work. Rig finds it afresh from git on each refresh, and
  only for a Task with Pull requests; the task list marks it with a `+` after
  the current Pull request, and the detail panel says what it is.

## Relationships

- A Task has exactly one Launch provider.
- A Task's Launch choice belongs to its Launch provider, so making one
  requires choosing the provider too, and no agent of another Provider is
  launched with it. On Reconnect, a Codex agent resumes on the model and
  reasoning level its Provider session last used outside plan mode, or on the
  Task's Launch choice when Rig cannot read them; a Claude agent restores its
  own model and gets the Launch reasoning level again.
- A Default provider becomes the Launch provider for a new Task unless the user
  selects a different provider during Task creation.
- A Provider has at most one Launch default. Creating a Task from a Task draft
  for a chosen provider replaces that provider's. A Task that falls to the
  Default provider without choosing it, a Task created from the command line,
  and a retried Task creation, leave it alone. A Provider
  need not be a Configured provider to have one.
- An environment-selected Default provider must be one of the user's Configured
  providers.
- A Configured provider must also be a Supported provider.
- A Configured provider must pass Rig's provider setup checks before it can be
  used for Task creation.
- The Task creation UI offers only Configured providers.
- Setup produces the User config. Provider setup produces the user's
  Configured providers.
- Each Configured provider gets a Rig skill in its user-level skills, never in
  a repository or worktree. Skills of providers that are not configured are
  left alone. A Rig skill Setup cannot install does not fail Setup:
  `rig doctor` warns about it, as about one that is missing or outdated.
- Tasks remain visible even when their Launch provider is not currently a
  Configured provider.
- A Task has at most one Parent task, set only at Task creation from the CLI
  inside the parent's worktree, unless the CLI is told not to. It may be in
  another repository, and nothing changes it afterwards. The TUI never sets
  one.
- A Task's Child tasks are derived from their Parent task links, so a deleted
  Child task is no longer listed by its parent. Deleting a Parent task leaves
  its Child tasks working and unlinked.
- Known repositories are derived from Tasks and Repo directories each time,
  never stored. A Repo directory that is missing is skipped.
- A Task may have many Task branches over time, and a Task branch many Pull
  requests, so a Task may have many Pull requests. Closed ones show in the
  task list only as the current one.
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
- Removing an Agent session closes its pane, which stops its agent, and ends
  it at once. Its Task, the Task workspace and the Task's other Agent sessions
  stay. One without a pane, which Reconnect would restore, only ends.
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
- Prefer "reasoning level" over the providers' own terms: Codex calls it
  reasoning effort and Claude calls it effort.
- Keep durable task fields separate from live runtime observations in design
  discussions.
- Use "daemon" for the background process and "TUI" for the foreground
  terminal interface.
