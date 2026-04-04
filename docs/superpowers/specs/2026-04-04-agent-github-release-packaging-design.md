# Agent GitHub Release Packaging Design

## Summary

Add a first packaging and distribution path for `agent` so the CLI can be installed on personal macOS machines without cloning the repo or running `go run`.

The first cut should use GitHub Releases as the source of truth, a tag-driven GitHub Actions workflow to build and publish release assets, and a repo-hosted `install.sh` script that supports a `curl | sh` install flow.

This release path is intentionally prototype-oriented. The first version should support unsigned macOS binaries with clear user guidance for the one-time quarantine workaround that may be required after download. Signing and notarization should be deferred and called out as a required follow-up once the tool is stable enough for broader distribution.

## Goals

- Make `agent` installable on macOS without a local Go toolchain.
- Publish release binaries through GitHub Releases.
- Support a one-command `curl | sh` install flow.
- Keep the release process repeatable through GitHub Actions rather than manual asset uploads.
- Generate checksums and verify them in the installer.
- Keep the first version narrow and easy to evolve into broader multi-platform packaging later.

## Non-Goals

- Homebrew packaging in this change.
- Linux or Windows distribution in this first cut.
- Apple code signing or notarization in this first cut.
- Auto-update behavior.
- A custom package repository or installer service outside GitHub.

## Target Platforms

The first packaged release should support macOS only.

Recommended initial build targets:

- `darwin/arm64`
- `darwin/amd64`

Supporting both architectures keeps the prototype usable across Apple Silicon and Intel Macs with little additional release complexity.

## Distribution Model

GitHub Releases should be the release artifact host and version source.

Each tagged release should publish:

- `agent_<version>_darwin_arm64.tar.gz`
- `agent_<version>_darwin_amd64.tar.gz`
- `checksums.txt`

The release archives should contain the compiled `agent` binary only. The installer script should remain in the repository so it can always fetch the latest release assets from GitHub.

## Versioning

Releases should be driven by git tags in the form `vX.Y.Z`.

Examples:

- `v0.1.0`
- `v0.1.1`

The workflow should treat a pushed version tag as the signal to run tests, build release artifacts, and publish a GitHub Release for that version.

## Installer UX

The primary install path should be:

```bash
curl -fsSL https://raw.githubusercontent.com/BaronBonet/tmux-llm/main/install.sh | sh
```

The installer should:

1. Detect that the host is macOS.
2. Detect the current architecture.
3. Resolve the latest GitHub Release version.
4. Download the matching tarball and `checksums.txt`.
5. Verify the archive checksum.
6. Extract the `agent` binary.
7. Install it into a user-local directory by default.
8. Print the next steps clearly.

Default install location:

- `~/.local/bin`

The script should support an override such as `PREFIX` so the install destination can be changed without editing the script.

## macOS Trust Handling

For the prototype release path, binaries may be unsigned and downloaded from GitHub. On macOS, that can result in quarantine metadata or a trust prompt on first use.

The installer and README should explain this explicitly. They should print a one-time remediation command for the installed binary when needed:

```bash
xattr -d com.apple.quarantine ~/.local/bin/agent
```

This should be described as a temporary prototype compromise, not the long-term distribution model.

## Release Workflow

Add a GitHub Actions workflow triggered by pushed version tags.

Recommended workflow stages:

1. Check out the repository.
2. Set up Go.
3. Run `go test ./...`.
4. Build `agent` for the macOS target matrix.
5. Package each build into a versioned tarball.
6. Generate `checksums.txt`.
7. Create or publish the corresponding GitHub Release.
8. Upload the release assets.

The workflow should stay intentionally small and explicit. It does not need preview releases, nightly builds, or branch-based publishing in the first cut.

## Implementation Constraints

The project currently builds as a single Go CLI binary with `cmd/agent/main.go` as the entrypoint. Packaging should preserve that shape rather than introducing wrapper binaries or install-time build steps.

Because the project uses `modernc.org/sqlite`, the implementation should explicitly verify that the release build works correctly as a standalone macOS binary in the target workflow. The release path should not assume packaging correctness without testing the produced binary.

## Repository Changes

This design expects the following repository additions or updates:

- add `.github/workflows/release.yml`
- add `install.sh`
- update [README.md](/Users/ericbonet/software/tmux-llm/README.md) with install and release instructions

Optional but not required in the first cut:

- a local helper command or Make target for tag-testing the packaging flow

## Documentation Changes

The README should stop implying that `go run ./cmd/agent` is the only normal entrypoint.

It should add:

- install instructions using `curl | sh`
- a short explanation of GitHub Releases as the packaged distribution path
- supported macOS architectures
- the unsigned-binary quarantine note
- a recommendation to run `agent doctor` after installation

Developer-focused `go run` examples can remain for local development, but packaged installation should be documented first once this release path exists.

## Error Handling

The installer should fail clearly and early for unsupported environments.

Expected installer error cases:

- non-macOS platform
- unsupported CPU architecture
- failed GitHub release lookup
- missing expected release asset
- checksum mismatch
- download or extraction failure
- destination directory not writable

Each failure should produce a short actionable message and a non-zero exit code.

## Testing

Testing should cover both repository automation and the installed-user path.

### Workflow Verification

- the release workflow runs successfully on a version tag
- `go test ./...` passes in the workflow
- release archives are named consistently
- checksums are generated and uploaded

### Installer Verification

- macOS architecture detection selects the correct asset
- checksum verification rejects tampered downloads
- default install path works
- `PREFIX` override works
- the installed binary runs `agent doctor`

### Manual Prototype Verification

Before treating the feature as complete, validate on a clean macOS machine or profile:

- run the `curl | sh` command
- confirm the binary lands in the expected directory
- confirm `agent doctor` runs
- confirm the quarantine workaround instructions are accurate if macOS blocks first execution

## Future Follow-Ups

These should be deferred, but the design should leave room for them:

- Apple code signing
- Apple notarization
- Linux and Windows builds
- Homebrew distribution
- auto-update support
- richer release metadata such as changelog generation
