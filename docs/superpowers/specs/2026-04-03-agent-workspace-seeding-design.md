# Agent Workspace Seeding Design

## Summary

Add a repo-local workspace seeding feature to `agent new` so a newly created worktree can be bootstrapped with ignored local files and directories copied from the main repo worktree before tmux and Codex start.

The first version should be driven by an optional `agent.yaml` file at the repo root. If present, it can declare a `seed.copy` list of repo-relative paths to copy from the main repo root into the new worktree at the same relative paths.

This is intended for files and directories such as `.env`, `.lazy.lua`, and `local/` that are needed locally but are not part of the shared git history.

## Goals

- Let `agent new` copy configured ignored files and directories from the main repo into the new worktree automatically.
- Keep the configuration repo-local and easy to discover.
- Fail safely on missing source paths or destination conflicts.
- Make seeding progress visible during `agent new`.
- Keep the behavior deterministic and testable without adding new runtime dependencies.

## Non-Goals

- General hook execution in this change.
- Arbitrary source-to-destination remapping.
- Glob support.
- Merge-time or cleanup-time sync behavior.
- Continuous syncing between the main repo and task worktrees.

## Configuration

The feature is configured through an optional `agent.yaml` file located at the detected repo root.

### Initial Schema

```yaml
seed:
  copy:
    - .env
    - .lazy.lua
    - local/
```

### Rules

- `seed.copy` is optional.
- Each entry is a literal repo-relative path.
- A trailing slash is allowed but not required for directories.
- The destination path in the new worktree is always the same relative path.
- If `agent.yaml` is absent, seeding is disabled.
- If `agent.yaml` exists but is invalid, `agent new` fails immediately.

## Workflow Integration

Workspace seeding is a first-class creation stage in `agent new`.

Expected flow:

1. Detect repo root and repo metadata.
2. Load repo-local `agent.yaml` if present.
3. Propose and confirm the task name.
4. Create the git worktree.
5. Seed configured files and directories from the main repo root into the new worktree.
6. Create the tmux session.
7. Launch Codex.
8. Open the tmux session.

Seeding must happen after `git worktree add` succeeds and before tmux or Codex start, so the new workspace is fully prepared before the agent begins working.

## Architecture

This feature should be implemented as a separate orchestration concern, not embedded inside the git adapter.

### Config Loading

- Add a small repo-local config loader that looks for `agent.yaml` at the detected repo root.
- The loader should return parsed config or a config error.
- The loader should not silently ignore invalid YAML or invalid `seed.copy` entries.

### Core Service

- Extend task creation so it loads repo-local config during `agent new`.
- If `seed.copy` contains entries, run a seeding stage immediately after worktree creation.
- If seeding fails, mark the task broken with a specific error message.

### Workspace Seeder Port

Add a dedicated port for workspace seeding. It should accept:

- repo root
- worktree path
- configured seed paths

The port owns validating and copying the configured files and directories. This keeps the seeding behavior isolated from git-specific worktree creation and makes it testable independently.

## Copy Semantics

The first version should be strict and conservative.

### Source Validation

- Every configured source path must exist under the main repo root at creation time.
- If any configured source path does not exist, `agent new` fails.

### Destination Validation

- If the destination path already exists in the new worktree, `agent new` fails.
- The tool must not overwrite existing files or directories in the new worktree.

### File And Directory Behavior

- Files are copied as files.
- Directories are copied recursively.
- File mode should be preserved where practical.
- Paths are resolved relative to the repo root.

### Symlinks

For the first cut, symlinks should fail explicitly rather than being copied implicitly. This avoids surprising behavior and keeps the implementation easier to reason about.

## Progress Reporting

The existing progress-aware `agent new` flow should surface workspace seeding clearly.

Recommended progress messages:

- `Seeding workspace...`
- `Copied .env`
- `Copied .lazy.lua`
- `Copied local/`

These messages should be emitted only after the relevant step succeeds so the output reflects real progress rather than intentions.

## Error Handling

If seeding fails:

- the command should stop immediately
- the worktree should be left in place for inspection
- the task should be marked `broken`
- the recorded error should identify the exact failure

Examples:

- `seed workspace: source path .env not found`
- `seed workspace: destination local already exists`
- `seed workspace: symlinks are not supported: .env`

This keeps failures debuggable and avoids hiding partially created resources.

## Doctor Behavior

When `agent doctor` runs inside a repo, it should validate repo-local seeding config if `agent.yaml` exists.

Doctor should report:

- whether `agent.yaml` exists
- whether the YAML is valid
- whether configured seed paths are valid relative to the repo root

Doctor should not mutate anything. It is only a validation surface for diagnosing repo-local setup problems before task creation.

## Testing

Testing should cover:

- missing `agent.yaml` meaning no seeding
- valid file copy
- valid recursive directory copy
- missing configured source path failing task creation
- destination conflict failing task creation
- invalid YAML producing a config error
- symlink rejection
- progress events including seeding messages
- `doctor` validating repo-local seed configuration

## Open Questions Deferred

These are intentionally out of scope for the first cut:

- source-to-destination remapping
- glob patterns
- shell hooks
- config inheritance between global and repo-local settings
- a TUI surface for repo-local config inspection
