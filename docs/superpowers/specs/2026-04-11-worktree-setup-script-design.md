# Worktree Post-Creation Setup Script

## Problem

When a new worktree is created, users must manually run setup commands (`make dependencies-install`, `source ./scripts/config-local-dev.sh`, `make generate`) before the environment is ready. This is repetitive and error-prone.

## Solution

Add a `seed.setup_script` field to `agent.yaml` that points to a version-controlled shell script. After creating and seeding a worktree, the agent runs this script inside the worktree before launching the tmux session. If the script fails, task creation is aborted.

## Configuration

```yaml
seed:
  copy:
    - .env
    - local/
  setup_script: scripts/worktree-setup.sh
```

## Validation

Same rules as `seed.copy` paths:

- Non-empty string
- Repository-relative (no absolute paths)
- No path traversal (`..`)
- No glob patterns
- Must exist in the repo
- Cannot be a symlink
- Must be a file (not a directory)

## Execution

- **When**: After `SeedWorkspace`, before `BootstrapWorkspace` and tmux launch
- **Where**: Working directory set to the worktree root
- **How**: `bash <script_path>` (explicit bash invocation, not relying on shebang)
- **Output**: Streamed live to the user (stdout + stderr)
- **Timeout**: None
- **On failure**: Non-zero exit code aborts task creation with the error message. Worktree is left on disk for debugging.

## Task Creation Flow (updated)

1. Detect repository
2. Load `agent.yaml`
3. Name task
4. Create task record
5. Create git worktree
6. Seed workspace (copy files)
7. **Run setup script** (new step)
8. Bootstrap workspace (codex hooks)
9. Write setup files
10. Launch tmux session

## Components Changed

- **`agent.yaml` schema**: Add `setup_script` field to `seed`
- **`SeedConfig` domain type**: Add `SetupScript string`
- **Config loader/validator**: Validate the new field
- **`core/ports.go`**: New port interface for running the script (or extend existing)
- **`core/service.go`**: Call setup script runner between seed and bootstrap steps
- **Filesystem adapter**: Implementation that executes the script, streams output, checks exit code

## What's NOT Included

- No timeout configuration
- No list of scripts (single script only)
- No special environment variables injected
- No retry logic
