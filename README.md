# Rig

`rig` is a terminal app for managing coding tasks as git worktrees and tmux sessions.

## Install

Install the latest GitHub release on macOS:

```bash
curl -fsSL https://raw.githubusercontent.com/BaronBonet/rig/main/install.sh | sh
```

If the repository is private, fetch the installer through `gh` instead:

```bash
gh api repos/BaronBonet/rig/contents/install.sh --jq .content | base64 --decode | sh
```

The installer will use authenticated `gh` release downloads when available. If you prefer token-based auth, export `GH_TOKEN` (or `GITHUB_TOKEN`) before running the installer.

The installer places `rig` in `~/.local/bin` and adds it to your `PATH` automatically (zsh and bash). If macOS blocks the binary on first run, clear the quarantine flag once:

```bash
xattr -d com.apple.quarantine ~/.local/bin/rig
```

## Requirements

`rig` expects these binaries to be available on `PATH`:

- `git`
- `tmux`
- `codex` and/or `claude` (at least one LLM provider)
- `gh` (optional — needed for PR-backed task creation, PR status checks, and private-repo installs)

## Usage

Launch the terminal UI:

```bash
rig
```

Check environment health:

```bash
rig doctor
```

Manage the background task daemon:

```bash
rig daemon status
rig daemon start
rig daemon stop
rig daemon restart
```

Configure repository-specific workspace seeding with a `.rig.yaml` file in the repo root:

```yaml
seed:
  copy:
    - .env
    - local/
  setup_script: scripts/worktree-setup.sh
```

- `seed.copy` copies repo-relative files or directories into the new worktree.
- Symlinks inside copied directories are followed only when they resolve within the repo root; symlinks that resolve outside the repo are rejected.
- `seed.setup_script` runs a repo-relative script inside the new worktree after copying completes.
- Paths in `.rig.yaml` must be repo-relative. Absolute paths, `..`, and glob patterns are rejected.
- `rig.yaml` is still supported for compatibility, but a repo must define only one of `.rig.yaml` or `rig.yaml`.
