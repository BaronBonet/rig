# Agent

`agent` is a terminal app for managing coding tasks as git worktrees and tmux sessions.

## Install

Install the latest GitHub release on macOS:

```bash
curl -fsSL https://raw.githubusercontent.com/BaronBonet/tmux-llm/main/install.sh | sh
```

The installer places `agent` in `~/.local/bin` and adds it to your `PATH` automatically (zsh and bash). If macOS blocks the binary on first run, clear the quarantine flag once:

```bash
xattr -d com.apple.quarantine ~/.local/bin/agent
```

## Requirements

`agent` expects these binaries to be available on `PATH`:

- `git`
- `tmux`
- `codex` and/or `claude` (at least one LLM provider)
- `gh` (optional — needed for PR status checks)

## Usage

Launch the terminal UI:

```bash
agent
```

Check environment health:

```bash
agent doctor
```
