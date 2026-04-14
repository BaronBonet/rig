# PR-Backed Task Creation Design

## Summary

Add a PR-backed task creation flow to Rig so a user can create a task directly from an open or draft pull request for the currently selected repository.

This flow is separate from the existing freeform `n` task creation flow. A new `Ctrl+P` shortcut opens a repo-scoped PR picker. Selecting an eligible PR creates a Rig task whose workspace is attached to the PR's head branch itself, not to a newly generated task branch.

If a workspace already exists in Rig for the same repository and branch, the PR remains visible in the picker but is clearly marked as already having a workspace and cannot be selected.

After selection, Rig should immediately create the workspace and launch the chosen Codex or Claude session without collecting an additional prompt. The user writes their instruction directly in the launched session.

## Goals

- Allow creating Rig tasks directly from open or draft PRs.
- Keep PR creation scoped to the currently selected repository.
- Prevent duplicate workspaces for the same repo and branch.
- Make PR-backed tasks obvious in the task list via explicit naming.
- Preserve the existing freeform task creation flow.

## Non-Goals

- Supporting PR picking across multiple repositories in one picker.
- Creating a new branch from the PR branch.
- Asking for an extra task prompt before launching the provider session.
- Hiding duplicate PRs from the picker entirely.

## Existing State

Rig currently supports:

- Freeform task creation from the TUI via `n`.
- Prompt-first creation that may use provider-generated task naming.
- Git worktree creation for new task branches.
- PR status display for existing tasks via `gh pr view`.

Rig does not currently support:

- Listing available PRs for a repository.
- Creating a task directly from an existing PR branch.
- Distinguishing PR-backed task creation from freeform creation in the TUI state machine.

## UX Design

### Entry Points

- `n` continues to open the existing freeform prompt-entry flow.
- `Ctrl+P` opens a PR picker for the currently selected repository.
- Inside text-entry views, `Ctrl+P` may also switch into the PR picker without conflicting with normal typing.

Plain `p` must remain normal text input and must not trigger PR picking.

### PR Picker

The PR picker is a dedicated TUI mode.

Its header should clearly identify the repository, for example:

- `RIG   PRs: repo-name`

The picker lists open and draft PRs for that repository only. Each row should include:

- PR number
- PR title
- PR state (`open` or `draft`)
- Head branch name

If a Rig task already exists for the same `repo_root` and PR head branch, the row remains visible but disabled and should include a clear visual indicator such as `already has workspace`.

Disabled rows should be visibly distinct from selectable rows.

### Creation Behavior

Selecting an eligible PR immediately starts task creation. There is no follow-up prompt screen.

The provider is still chosen from Rig's existing provider selection state. The launched Codex or Claude session starts with no initial user prompt so the user can type directly into the session.

### Task Naming

PR-backed tasks should default to an explicit PR-oriented display name derived from the PR number and title, for example:

- `PR #123 billing retry fixes`

The exact formatting can be implementation-defined, but it should always make it obvious in the main task list that the task comes from a PR.

## Domain and Service Design

### PR Listing

Add a GitHub adapter that can list open and draft PRs for a repository using `gh` from that repository root.

The core service should expose a repo-scoped PR listing method that returns the PR metadata needed by the picker:

- PR number
- Title
- Head branch name
- Draft/open state

### PR-Backed Task Creation

Add a dedicated PR-backed creation path in the core service. This path is separate from prompt-based task creation and should not call provider naming.

PR-backed creation input should contain:

- Repository context or working directory
- PR metadata
- Selected provider

The created task should use:

- `DisplayName`: explicit PR-style display name derived from PR metadata
- `BranchName`: the PR head branch exactly
- `Prompt`: empty
- `Provider`: selected provider

The rest of the task metadata should follow existing task creation conventions where still applicable, including workspace path, tmux session naming, and status transitions.

### Duplicate Protection

Before creating a PR-backed task, the service must reject creation if an existing task already uses the same:

- `RepoRoot`
- `BranchName`

This invariant prevents multiple Rig workspaces from targeting the same PR branch.

The picker should use the same duplicate-detection rule to mark rows disabled before the user selects them. The service must still enforce the invariant so UI and backend remain consistent.

## Git Behavior

Freeform tasks continue to create a new branch and worktree as they do today.

PR-backed tasks require a second git worktree mode:

- create a worktree from an existing branch without creating a new branch

Conceptually, this is:

- `git worktree add <worktree-path> <existing-branch>`

Rig must not create a new branch for PR-backed tasks. The workspace should point directly at the PR head branch.

## Provider Launch Behavior

PR-backed task creation should reuse the existing session bootstrap flow, but the provider launch request should not contain an initial user prompt for the task.

This means the session starts and waits for the user to type the first instruction directly in Codex or Claude.

## Error Handling

- If no repository is selected or resolvable for `Ctrl+P`, Rig should show a clear error and not open the picker.
- If `gh` is unavailable, PR listing should fail clearly without affecting the rest of Rig.
- If PR listing fails for another reason, the picker should surface the error and allow clean exit.
- If a duplicate branch is detected, the picker should show the PR as disabled and the service should also reject creation if called anyway.
- If worktree creation fails, Rig should follow the existing broken-task behavior used in normal task creation.

## Testing Strategy

Implementation should add or update tests for:

- GitHub PR list parsing for open and draft PRs.
- Repo-scoped PR listing through the service.
- PR-backed task creation in the core service.
- Duplicate-branch rejection before persistence or git operations.
- Git adapter support for creating a worktree from an existing branch.
- TUI `Ctrl+P` entry into the PR picker.
- Repo name rendering in the picker header.
- Disabled duplicate PR rows.
- Creation from an eligible PR row.
- Regression coverage for the existing freeform `n` flow.

## Open Decisions Resolved

- Picker trigger: `Ctrl+P`, not plain `p`.
- Picker scope: only the currently selected repository.
- Duplicate handling: visible but disabled in the picker, with backend enforcement.
- Branch behavior: use the PR head branch directly.
- Post-selection flow: launch immediately, no extra prompt step.
- Task naming: default from PR title and make PR origin explicit.
