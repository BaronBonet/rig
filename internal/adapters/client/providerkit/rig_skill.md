---
name: rig
description: Hand work off to a new Rig task, which runs its own agent in its own git worktree, branch and tmux session, in this repository or another one. Use it only when the user asks for work in another repository ("hand this off to the frontend", "make the matching change in the api repo") or for a separate, parallel Rig task ("start a rig task for this"). Do not use it for work you can do here yourself.
---

# Rig: hand work off to a new task

Rig runs AI coding tasks, each in its own git worktree, branch and tmux session. `rig task create` starts
one and returns: it never attaches or takes over a terminal. The new task opens in its own tmux session and
shows up in the user's Rig TUI.

## 1. Run rig outside your sandbox

rig talks to a local daemon over a Unix socket, which sandboxes block.

- Codex: request escalated permissions for every rig command. rig refuses to run inside the Codex sandbox
  when it can detect it.
- Claude Code: if a sandbox is on, run rig commands with the sandbox disabled.

If rig cannot reach its daemon, with a socket or permission error such as `operation not permitted`, or
`task daemon did not become healthy`, you are probably sandboxed: rerun the command outside the sandbox.

## 2. Find the target repository

- Your own repository: omit `--repo`.
- Another repository: run `rig repo list` (or `rig repo list --json`) and pass `--repo <name>`, or
  `--repo <path>` when you know its path.
- If no repository has the name, or several do, ask the user for the repository's path. Never guess.

A new task branches off the branch checked out in the repository's main checkout, not off yours, so it
does not see your changes unless your prompt points it at them.

## 3. Know where you are

`rig task show` with no argument describes the task you run in: its ID, branch and worktree, its parent
and the tasks it spawned. Outside a task's worktree it fails. A task you create from inside your task
records yours as its parent automatically; pass `--no-parent` only for unrelated work.

## 4. Write a self-contained prompt

The new agent starts with none of your context. Say:

- what was done and where: repository, branch, files, endpoints, payloads;
- what to do;
- how to verify it.

If `rig task show` found your task, tell the new agent it can run `rig task show <your-task-id>` to find
your worktree and branch. Whenever your prompt asks it to run rig, tell it to run rig outside its sandbox.

## 5. Create the task

Pass `--submit` so the agent starts at once, and pass the prompt on stdin with a quoted heredoc:

```bash
rig task create --repo frontend --submit --json - <<'EOF'
<the prompt>
EOF
```

- `--json` prints the new task as JSON (`id`, `name`, `repo_name`, `branch`, `worktree`, ...). Progress
  goes to stderr.
- `--provider claude` or `--provider codex` picks the agent. The default is the user's default provider.
- The command waits until the task is ready: naming it, creating the worktree, running the repository's
  setup script and starting the agent. That can take a few minutes, so give it a long timeout, such as
  10 minutes.
- If the command is interrupted or times out, the daemon is still creating the task. Check
  `rig task list` before trying again: never create the same task twice.
- If creation fails after the task exists, the error names the task and the step it failed at. The user
  retries it from the Rig TUI.

## 6. Report back

Tell the user the new task's name, repository, branch and worktree.

Don't delete, reconnect or attach to tasks: those stay in the Rig TUI.
