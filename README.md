# Rig

Rig runs your Codex and Claude Code agents side by side, each task in its own
git worktree, branch and tmux session, and shows in one terminal list which of
them needs you.

![Rig task list](docs/diagrams/rig-example.png)

## What it's for

- **Parallel work without collisions.** Start several tasks at once. Each gets
  its own worktree and branch, so agents never step on each other or on your
  main checkout.
- **Knowing who needs you.** Every task, and every agent in it, shows whether
  it is working, waiting on you, or stopped, beside its pull requests and how
  full its context window is. `enter` jumps to the agent that most needs you.
- **Starting from wherever the work is.** Describe the task, name an issue
  ("work on #18" names the task `GH #18 …`), or pick an open pull request to
  review or continue.
- **Handing work to another repo.** Ask an agent to start a task in another
  repository and it creates one, linked back to the task it came from.
- **Picking up where you left off.** If tmux dies or you reboot, reconnecting
  brings back every agent, each resuming its own conversation.

Rig is built for people who already live in tmux and a keyboard-driven editor.

## Install

On macOS or Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/BaronBonet/rig/main/scripts/install.sh | sh
```

The installer places `rig` in `~/.local/bin` and adds it to your `PATH` for zsh
and bash. Run `rig update` to update later.

On macOS, if the system blocks the binary on first run, clear the quarantine
flag once:

```bash
xattr -d com.apple.quarantine ~/.local/bin/rig
```

Rig needs `git`, `tmux`, and at least one of `codex` and `claude` on your
`PATH`. `gh` is optional: it adds pull request status and PR-backed tasks. If a
provider lives elsewhere, point Rig at it with `RIG_CODEX_BINARY` or
`RIG_CLAUDE_BINARY`.

## Setup

Run `rig` in a git repository. The first run opens setup: enable the providers
you want, pick a default, and optionally add your repo directories (such as
`~/code`) so agents can find your other repositories by name. Rerun it any
time with `rig setup`.

**Codex** needs hooks turned on in `~/.codex/config.toml`:

```toml
[features]
hooks = true
```

Codex asks you once to trust Rig's hooks, and again when a Rig update changes
them. If you run more than one Codex agent in a task, start them with
`--no-daemon` so Rig can tell them apart:

```bash
alias codex='codex --no-daemon'
```

**Claude Code** needs nothing more. Rig registers its hooks in each task's
worktree, in an untracked `.claude/settings.local.json`, and never touches your
`~/.claude/settings.json`.

Run `rig doctor` to check that everything is in place.

## Usage

Press `n`, describe the task, and press `enter`. Rig names the task, creates its
branch and worktree, and starts your default provider in a new tmux session. If
the prompt names an issue or ticket, the provider looks it up with whatever
read-only access you have given it (`gh`, or a Jira, Linear, YouTrack or
Shortcut MCP server), so only give it tools you trust with your tickets.

To add another agent to a task, split a pane in its tmux session and run
`claude` or `codex` there. Rig shows it as a sub-row of the task once you send
it a prompt.

| Key | Action |
|-----|--------|
| `n` | Create a task |
| `tab` | Switch provider while writing the prompt |
| `ctrl+o` / `ctrl+r` | Pick the model / reasoning level while writing the prompt |
| `ctrl+n` | Pick the repository while writing the prompt |
| `ctrl+p` | Create the task from a GitHub pull request |
| `j` / `k` | Move between tasks and their agents |
| `enter` | Jump to the agent that most needs you, or to the selected agent |
| `m` | Jump to the tmux session of the task's main repository |
| `c` | Copy the task's prompt to the clipboard (needs OSC 52; in tmux, `set-clipboard on`) |
| `R` | Retry a failed task creation |
| `x` | Remove the task's tmux session and worktree (its branch stays), or on an agent, close only that agent |
| `q` | Quit |

### Reading the task list

Each task is one row, led by its status symbol:

```text
◆ Fix login redirect                ✓●#127+  claude   45%  ↻2   7h 05m
```

After the status come the task's name (with `↳` when another task created it),
its pull requests, and then, muted: the provider, how full the context window
is, how many times it has compacted, and the task's age. A task with several
agents gets a sub-row per agent. A row is highlighted until you have looked at
the agent that needs you. The panel under the list describes the selected row
in words.

| Status | Meaning |
|:------:|---------|
| <img src="docs/legend/status-needs-input.svg" height="22" alt="amber ◆"> | Needs input: the agent ended its turn, or asks for your approval or an answer |
| <img src="docs/legend/status-background.svg" height="22" alt="amber ◐"> | Needs input, while background work the turn started still runs |
| <img src="docs/legend/status-working.svg" height="22" alt="green spinner"> | Working |
| <img src="docs/legend/status-starting.svg" height="22" alt="amber ◌"> | Starting |
| <img src="docs/legend/status-stopped.svg" height="22" alt="○"> | Stopped: no agent is running |
| <img src="docs/legend/status-none.svg" height="22" alt="·"> | No agent has run yet |
| <img src="docs/legend/status-creating.svg" height="22" alt="muted spinner"> | Being created |
| <img src="docs/legend/status-failed.svg" height="22" alt="red ×"> | Creation failed; `R` retries it |
| <img src="docs/legend/status-removing.svg" height="22" alt="⊖"> | Being removed |

| Pull requests | Meaning |
|:-------------:|---------|
| <img src="docs/legend/pr-open.svg" height="22" alt="green ●#127"> | Open |
| <img src="docs/legend/pr-draft.svg" height="22" alt="amber ◐#127"> | Draft |
| <img src="docs/legend/pr-merged.svg" height="22" alt="purple ✓#127"> | Merged |
| <img src="docs/legend/pr-closed.svg" height="22" alt="red ×#127"> | Closed |
| <img src="docs/legend/pr-history.svg" height="22" alt="purple ✓✓ then green ●#127"> | Earlier pull requests come first, as symbols; `…` in front means older ones are left out |
| <img src="docs/legend/pr-unshipped.svg" height="22" alt="green ●#127 then +"> | The branch has work no pull request contains yet |

## Per-repo setup

To copy files into each new worktree and run a setup script there, add a
`.rig.yaml` to the repository root:

```yaml
seed:
  copy:
    - .env
    - local/
  setup_script: scripts/worktree-setup.sh
```

Paths must be repo-relative: absolute paths, `..` and globs are rejected.
Symlinks inside copied directories are followed only when they stay inside the
repository.

## Troubleshooting

- `rig doctor` checks your tools, providers, hooks and Rig skills.
- If `rig` says `task daemon did not become healthy`, run it again. If that
  doesn't help, run `rig daemon restart` and check
  `~/.local/share/rig/daemon-startup.log`.

How Rig works under the hood, including its command line for scripts and
agents, is in [docs/how-it-works.md](docs/how-it-works.md).
