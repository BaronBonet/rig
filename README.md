# Rig

Run coding agents in the tmux setup you already use.

Rig is for developers who like their terminal setup and want to work on
several tasks at once with coding agents. It gives each task its own branch,
worktree and tmux session, and shows which agents need your attention.

- **Less setup and cleanup.** Describe a task; Rig prepares its workspace and
  launches the agent. Remove the workspace when you're done.
- **Less checking every pane.** Open the task list as a popup, see who needs
  you, and jump straight to that agent.
- **Keep your workflow.** Use your terminal, editor, tmux configuration and
  the agents' own interfaces.

Runs on macOS and Linux, locally or over SSH.

![Rig task list](docs/diagrams/rig-example.png)

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

Rig needs `git`, `tmux`, and a supported coding agent on your `PATH`.
Currently supported: Claude Code (`claude`) and Codex (`codex`). `gh` is
optional: it adds pull request status and tasks created from pull requests.

## Setup

Run `rig` in a git repository. On first run, choose which agents to enable
and pick a default. You can also add repo directories (such as `~/code`) so
agents can find your other repositories by name. Rerun setup with `rig setup`.

**Codex** needs hooks turned on in `~/.codex/config.toml`:

```toml
[features]
hooks = true
```

Codex asks you to trust Rig's hooks, including when an update changes them.
**Claude Code** needs no extra setup.

Run `rig doctor` to check that everything is in place.

## Your first task

1. Press `n`, describe the task, and press `enter`.
2. Rig creates the branch, worktree and tmux session, then opens your default
   agent with the prompt ready to send.
3. In the task list, select a task and press `enter` to jump to its agent.
4. When you're done, press `x` on the task to remove its session and worktree.
   Its branch stays.

To open Rig as a popup over whatever you're doing, bind it in `~/.tmux.conf`:

```tmux
bind R display-popup -E -w 80% -h 70% rig
```

See the [guide](docs/how-it-works.md) for
[keybindings and status symbols](docs/how-it-works.md#terminal-ui),
[workspace setup scripts](docs/how-it-works.md#per-repo-setup),
[multiple agents](docs/how-it-works.md#running-more-agents), and
[troubleshooting](docs/how-it-works.md#daemon-startup-failures).
