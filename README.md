# Agent

`agent` is a terminal app for managing coding tasks as git worktrees and tmux sessions.

## Install

Install the latest GitHub release on macOS:

```bash
curl -fsSL https://raw.githubusercontent.com/BaronBonet/tmux-llm/main/install.sh | sh
```

The installer places `agent` in `~/.local/bin` by default. If that directory is not on your `PATH`, add it before using the app.

If macOS blocks the binary on first run, clear the quarantine flag once:

```bash
xattr -d com.apple.quarantine ~/.local/bin/agent
```

## Requirements

`agent` expects these binaries to be available on `PATH`:

- `git`
- `tmux`
- `codex`

## Usage

Launch the terminal UI:

```bash
agent
```

Check environment health:

```bash
agent doctor
```
