# Semantic-Release Trunk-Based Release Design

## Summary

Move release automation from a manually-created `v*` tag workflow to a trunk-based flow driven by pushes to `main`.

Pull requests targeting `main` remain the quality gate. They must pass the existing CI checks and must also enforce [Conventional Commits 1.0.0](https://www.conventionalcommits.org/en/v1.0.0/) for every commit that will be rebased onto `main`.

Once commits land on `main`, a dedicated release workflow will trust the PR checks and run release automation without re-running the full test suite. That workflow will use `semantic-release` to determine the next semantic version, generate GitHub release notes from commit history, create the release tag, and publish the GitHub release. It will then invoke GoReleaser to build and upload release artifacts for the computed version.

No `CHANGELOG.md` file will be committed to the repository. The changelog will live in the GitHub release notes only.

## Goals

- Remove manual version tagging from the release process.
- Support trunk-based development with releases produced from commits on `main`.
- Enforce Conventional Commits for commits merged via rebase.
- Automatically determine the next semantic version from commit history.
- Automatically generate GitHub release notes.
- Preserve GoReleaser as the artifact packaging and publishing tool.

## Non-Goals

- Supporting direct pushes to `main`.
- Maintaining a checked-in `CHANGELOG.md`.
- Releasing from branches other than `main`.
- Introducing a manual approval step before publishing.

## Existing State

The repository currently has:

- A PR workflow at `.github/workflows/on-pr-main.yml` that runs linting and tests for pull requests targeting `main`.
- A release workflow at `.github/workflows/release.yml` that runs only when a tag matching `v*` is pushed.
- A `.goreleaser.yaml` file that builds Darwin archives and publishes a GitHub release, but does not currently own version calculation or changelog generation.

This means packaging is automated today, but version selection is manual because a person must choose and push the tag.

## Proposed Workflow

### PR workflow

The PR workflow remains the required gate for merging into `main` and gains a commit-message validation step.

That validation must check every commit that would be rebased onto `main`, not just the PR title. This matches the repository's rebase-merge policy and ensures `semantic-release` can safely infer the version bump from the commits that actually land on `main`.

The PR workflow responsibilities will be:

- Install dependencies needed for Go CI and commit-message validation.
- Validate every commit in the PR range against Conventional Commits.
- Generate code.
- Run linters.
- Run tests.

### Main release workflow

A new workflow will run on `push` to `main`.

This workflow will trust PR checks and will not repeat the full test suite. Its purpose is release orchestration, not re-validation. The workflow will:

- Check out the repository with full history and tags.
- Set up Go for GoReleaser.
- Set up Node.js for `semantic-release`.
- Install `semantic-release` and the minimal plugin set needed for GitHub releases plus GoReleaser integration.
- Run `semantic-release`.

If there are no releasable commits since the previous tag, `semantic-release` will exit without publishing a release.

## Release Responsibilities

### semantic-release

`semantic-release` becomes the source of truth for release orchestration. It is responsible for:

- Determining the previous release from Git tags.
- Analyzing commit messages on `main` since that release.
- Computing the next semantic version.
- Generating GitHub release notes from those commits.
- Creating the new Git tag.
- Creating the GitHub release.

The release notes will be attached to the GitHub release only. They will not be written back into the repository.

### GoReleaser

GoReleaser remains responsible for packaging and artifact publishing. It is responsible for:

- Running any pre-build hooks already defined in `.goreleaser.yaml`.
- Building the `rig` binaries.
- Producing archives and checksums.
- Uploading those artifacts to the GitHub release created by `semantic-release`.

GoReleaser will not decide the version number. It will run after `semantic-release` has already created the tag for the current release.

## Conventional Commit Policy

The repository will enforce the Conventional Commits 1.0.0 format on every commit in PRs targeting `main`.

At minimum, the release process depends on these mappings:

- `fix:` triggers a patch release.
- `feat:` triggers a minor release.
- A commit containing a breaking-change indicator triggers a major release.

The enforcement mechanism should accept standard Conventional Commits forms, including scopes and breaking markers such as:

- `feat: add background task filtering`
- `fix(tui): restore prompt focus after submit`
- `refactor!: remove legacy observer startup path`

Commits that do not imply a release, such as `docs:` or `chore:`, should still be valid Conventional Commits, but they should not cause a release on their own.

## CI and Tooling Implications

The release path adds a Node.js dependency to GitHub Actions because `semantic-release` is a Node-based tool.

This does not change the runtime requirements for the `rig` binary. It only affects CI. Go-based packaging remains in GoReleaser and the existing install flow continues to consume GitHub releases as it does today.

## Error Handling

- If a PR contains any non-conforming commit message, the PR workflow fails before merge.
- If the release workflow runs on `main` and finds no releasable commits, no release is created.
- If `semantic-release` succeeds in computing a release but GoReleaser fails, the workflow fails and investigation is required because version/tag creation and artifact publishing are no longer atomic.

The implementation should minimize this risk by making GoReleaser execution part of the `semantic-release` publish path so the packaging step runs in the same workflow that computed the version.

## Testing Strategy

Testing remains split across two workflows:

- PR workflow tests correctness and policy before merge.
- Main release workflow validates release mechanics, not product behavior.

Implementation should include local dry-run validation where practical, especially for:

- Commit-message linting against a representative commit range.
- `semantic-release` configuration in dry-run mode.
- GoReleaser invocation shape used by the release workflow.

## Migration Plan

1. Add commit-message enforcement to the PR workflow.
2. Add `semantic-release` configuration and a new `push`-to-`main` release workflow.
3. Update GoReleaser invocation so it is called from `semantic-release` rather than from a tag-triggered workflow.
4. Remove or retire the existing tag-triggered release workflow once the new path is verified.

## Open Decisions Resolved

- Release trigger: every passing push to `main` publishes immediately.
- Main workflow trust model: trust PR checks and do not re-run the full test suite.
- Changelog location: GitHub release notes only.
- Merge strategy assumption: rebase merge.
- Commit policy scope: enforce Conventional Commits on PRs targeting `main`; direct pushes to `main` are not supported.
