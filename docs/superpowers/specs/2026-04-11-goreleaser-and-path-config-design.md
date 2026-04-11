# GoReleaser Migration and PATH Auto-Configuration

## Summary

Replace the custom build/release scripts with GoReleaser and add automatic PATH configuration to the install script so users don't need to manually edit their shell rc files.

## 1. GoReleaser Migration

### What changes

**Add:** `.goreleaser.yaml` at repo root with:
- Build target: `cmd/agent` for `darwin/arm64` and `darwin/amd64`, `CGO_ENABLED=0`
- `before` hook runs `make generate` (sqlc + mockery code generation required before compilation)
- Archive name template: `{{ .ProjectName }}_{{ .Tag }}_{{ .Os }}_{{ .Arch }}` to match what `install.sh` expects (e.g., `agent_v0.1.0_darwin_arm64.tar.gz`)
- Checksums generated automatically (SHA256, `checksums.txt`)

**Simplify:** `.github/workflows/release.yml`:
- Same trigger: push tags matching `v*`
- Setup Go, run `make test`
- Replace custom `build.sh` + `softprops/action-gh-release@v2` with `goreleaser/goreleaser-action@v6`

**Delete:**
- `scripts/release/build.sh`
- `scripts/release/build_test.sh`

**Update:**
- `scripts/release/release_repo_test.sh` — update assertions to check for GoReleaser references instead of the old build.sh + softprops patterns

### What stays the same

- `install.sh` — still needed; GoReleaser doesn't provide a curl-pipe-sh installer
- `install_test.sh` — still validates the installer
- Archive format and naming convention — install.sh expectations are preserved
- Tag-based release trigger

## 2. PATH Auto-Configuration in `install.sh`

After installing the binary, the script checks if `$BIN_DIR` is already in `$PATH`. If not:

1. Detect shell from `$SHELL`:
   - **zsh** (macOS default): target `~/.zshrc`
   - **bash**: target `~/.bashrc` on Linux, `~/.bash_profile` on macOS (checking existence of both, preferring the one that exists)
2. Check if the rc file already contains the PATH export line (avoid duplicates)
3. Append `export PATH="$HOME/.local/bin:$PATH"` if not present
4. Print a message: which file was modified and instruct user to restart their shell or `source` the file
5. If shell is unrecognized: print manual instructions only, do not modify any files

### Edge cases

- `$BIN_DIR` already in `$PATH`: skip modification, print nothing about PATH
- rc file doesn't exist: create it with just the export line
- rc file already has the export: skip modification, print nothing about PATH
- Non-interactive install (e.g., CI): works fine — PATH export in rc file is harmless

## Testing

- **GoReleaser:** `goreleaser check` validates the config. The `release_repo_test.sh` is updated to verify the workflow references GoReleaser.
- **PATH config:** Extend `install_test.sh` to verify the rc file is modified after install when `BIN_DIR` is not on PATH, and NOT modified when it already is.
