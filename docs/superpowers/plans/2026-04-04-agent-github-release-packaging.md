# Agent GitHub Release Packaging Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a macOS-first GitHub Release packaging path for `agent` with a `curl | sh` installer and a tag-driven GitHub Actions release workflow.

**Architecture:** Keep the release logic in small shell scripts that are locally testable, then make the GitHub Actions workflow a thin wrapper around those scripts. Put the installer at the repo root for the public install URL, keep packaging helpers under `scripts/release`, and update the README so installed usage is the default path while `go run` remains documented for local development.

**Tech Stack:** Go, POSIX shell, GitHub Actions, GitHub Releases, `tar`, `curl`, `shasum`, `rg`

---

## File Structure

Create or modify the following files during implementation.

### Release Scripts

- Create: `scripts/release/build.sh`
- Create: `scripts/release/build_test.sh`
- Create: `scripts/release/install_test.sh`
- Create: `scripts/release/release_repo_test.sh`

### Public Install Surface

- Create: `install.sh`

### GitHub Automation

- Create: `.github/workflows/release.yml`

### Documentation

- Modify: `README.md`

## Implementation Notes

- Keep the first cut macOS-only even though the build script should emit both `darwin/arm64` and `darwin/amd64` archives.
- Use GitHub Releases as the only packaged distribution source in this plan.
- Keep the installer POSIX `sh` compatible because the public command is `curl ... | sh`.
- Do not add Homebrew, Linux, Windows, signing, notarization, or auto-update behavior in this plan.
- Leave `agent` itself unchanged; packaging should wrap the existing `./cmd/agent` entrypoint rather than changing application behavior.
- The repo already ignores `dist/`, so write release artifacts there without changing `.gitignore`.

### Release Asset Names

The packaging scripts and workflow should agree on these filenames:

```text
agent_<version>_darwin_arm64.tar.gz
agent_<version>_darwin_amd64.tar.gz
checksums.txt
```

### Installer Environment Contract

Keep these environment variables stable:

```sh
PREFIX="${PREFIX:-$HOME/.local}"
AGENT_INSTALL_REPO="${AGENT_INSTALL_REPO:-BaronBonet/tmux-llm}"
AGENT_INSTALL_API_URL="${AGENT_INSTALL_API_URL:-https://api.github.com/repos/$AGENT_INSTALL_REPO/releases/latest}"
AGENT_INSTALL_DOWNLOAD_ROOT="${AGENT_INSTALL_DOWNLOAD_ROOT:-https://github.com/$AGENT_INSTALL_REPO/releases/download}"
```

That gives the production installer sane defaults while making the script testable against local `file://` fixtures.

## Task 1: Add The Release Build Script And Packaging Tests

**Files:**
- Create: `scripts/release/build.sh`
- Create: `scripts/release/build_test.sh`

- [ ] **Step 1: Write the failing packaging test**

Create `scripts/release/build_test.sh`:

```sh
#!/bin/sh
set -eu

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT INT TERM

mkdir -p "$tmpdir/bin" "$tmpdir/dist"

cat >"$tmpdir/bin/go" <<'EOF'
#!/bin/sh
set -eu

out=""
while [ "$#" -gt 0 ]; do
    if [ "$1" = "-o" ]; then
        out="$2"
        shift 2
        continue
    fi
    shift
done

if [ -z "$out" ]; then
    echo "missing -o output" >&2
    exit 1
fi

printf '#!/bin/sh\necho packaged-agent\n' >"$out"
chmod +x "$out"
EOF
chmod +x "$tmpdir/bin/go"

PATH="$tmpdir/bin:$PATH" VERSION="v0.1.0" DIST_DIR="$tmpdir/dist" sh ./scripts/release/build.sh

[ -f "$tmpdir/dist/agent_v0.1.0_darwin_arm64.tar.gz" ]
[ -f "$tmpdir/dist/agent_v0.1.0_darwin_amd64.tar.gz" ]
[ -f "$tmpdir/dist/checksums.txt" ]

tar -xzf "$tmpdir/dist/agent_v0.1.0_darwin_arm64.tar.gz" -C "$tmpdir"
[ -x "$tmpdir/agent" ]

rg -q 'agent_v0.1.0_darwin_arm64.tar.gz' "$tmpdir/dist/checksums.txt"
rg -q 'agent_v0.1.0_darwin_amd64.tar.gz' "$tmpdir/dist/checksums.txt"
```

- [ ] **Step 2: Run the packaging test to verify red**

Run: `sh scripts/release/build_test.sh`
Expected: FAIL because `scripts/release/build.sh` does not exist yet

- [ ] **Step 3: Write the release build script**

Create `scripts/release/build.sh`:

```sh
#!/bin/sh
set -eu

VERSION="${VERSION:?VERSION is required}"
DIST_DIR="${DIST_DIR:-dist}"
APP_NAME="${APP_NAME:-agent}"

mkdir -p "$DIST_DIR"
rm -f "$DIST_DIR"/"${APP_NAME}"_*.tar.gz "$DIST_DIR/checksums.txt"

build_archive() {
    goos="$1"
    goarch="$2"
    archive="${APP_NAME}_${VERSION}_${goos}_${goarch}.tar.gz"
    workdir="$(mktemp -d)"

    GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 go build -o "$workdir/$APP_NAME" ./cmd/agent
    tar -C "$workdir" -czf "$DIST_DIR/$archive" "$APP_NAME"
    rm -rf "$workdir"
}

build_archive darwin arm64
build_archive darwin amd64

(
    cd "$DIST_DIR"
    shasum -a 256 ./*.tar.gz >checksums.txt
)
```

- [ ] **Step 4: Re-run the packaging test**

Run: `sh scripts/release/build_test.sh`
Expected: PASS

- [ ] **Step 5: Run shell syntax checks**

Run: `sh -n scripts/release/build.sh scripts/release/build_test.sh`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add scripts/release/build.sh scripts/release/build_test.sh
git commit -m "build: add release packaging script"
```

## Task 2: Add The Public Installer And Its Black-Box Tests

**Files:**
- Create: `install.sh`
- Create: `scripts/release/install_test.sh`

- [ ] **Step 1: Write the failing installer tests**

Create `scripts/release/install_test.sh`:

```sh
#!/bin/sh
set -eu

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT INT TERM

mkdir -p "$tmpdir/release/v0.1.0" "$tmpdir/work" "$tmpdir/prefix/bin"

cat >"$tmpdir/work/agent" <<'EOF'
#!/bin/sh
echo doctor: ok
EOF
chmod +x "$tmpdir/work/agent"
tar -C "$tmpdir/work" -czf "$tmpdir/release/v0.1.0/agent_v0.1.0_darwin_arm64.tar.gz" agent

(
    cd "$tmpdir/release/v0.1.0"
    shasum -a 256 agent_v0.1.0_darwin_arm64.tar.gz >checksums.txt
)

cat >"$tmpdir/latest.json" <<'EOF'
{"tag_name":"v0.1.0"}
EOF

PATH="/usr/bin:/bin:/usr/sbin:/sbin" \
HOME="$tmpdir/home" \
PREFIX="$tmpdir/prefix" \
AGENT_INSTALL_API_URL="file://$tmpdir/latest.json" \
AGENT_INSTALL_DOWNLOAD_ROOT="file://$tmpdir/release" \
sh ./install.sh

[ -x "$tmpdir/prefix/bin/agent" ]
output="$("$tmpdir/prefix/bin/agent")"
[ "$output" = "doctor: ok" ]

cat >"$tmpdir/release/v0.1.0/checksums.txt" <<'EOF'
0000000000000000000000000000000000000000000000000000000000000000  agent_v0.1.0_darwin_arm64.tar.gz
EOF

if PATH="/usr/bin:/bin:/usr/sbin:/sbin" \
    HOME="$tmpdir/home" \
    PREFIX="$tmpdir/prefix-bad" \
    AGENT_INSTALL_API_URL="file://$tmpdir/latest.json" \
    AGENT_INSTALL_DOWNLOAD_ROOT="file://$tmpdir/release" \
    sh ./install.sh
then
    echo "expected checksum failure" >&2
    exit 1
fi
```

- [ ] **Step 2: Run the installer tests to verify red**

Run: `sh scripts/release/install_test.sh`
Expected: FAIL because `install.sh` does not exist yet

- [ ] **Step 3: Write the installer**

Create `install.sh`:

```sh
#!/bin/sh
set -eu

PREFIX="${PREFIX:-$HOME/.local}"
BIN_DIR="${BIN_DIR:-$PREFIX/bin}"
AGENT_INSTALL_REPO="${AGENT_INSTALL_REPO:-BaronBonet/tmux-llm}"
AGENT_INSTALL_API_URL="${AGENT_INSTALL_API_URL:-https://api.github.com/repos/$AGENT_INSTALL_REPO/releases/latest}"
AGENT_INSTALL_DOWNLOAD_ROOT="${AGENT_INSTALL_DOWNLOAD_ROOT:-https://github.com/$AGENT_INSTALL_REPO/releases/download}"

fail() {
    echo "agent installer: $*" >&2
    exit 1
}

require_cmd() {
    command -v "$1" >/dev/null 2>&1 || fail "missing required command: $1"
}

detect_goos() {
    case "$(uname -s)" in
        Darwin) echo "darwin" ;;
        *) fail "macOS is the only supported platform in this prototype" ;;
    esac
}

detect_goarch() {
    case "$(uname -m)" in
        arm64|aarch64) echo "arm64" ;;
        x86_64) echo "amd64" ;;
        *) fail "unsupported CPU architecture: $(uname -m)" ;;
    esac
}

latest_tag() {
    tag="$(curl -fsSL "$AGENT_INSTALL_API_URL" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)"
    [ -n "$tag" ] || fail "could not resolve the latest GitHub release"
    echo "$tag"
}

main() {
    require_cmd curl
    require_cmd tar
    require_cmd shasum

    goos="$(detect_goos)"
    goarch="$(detect_goarch)"
    version="$(latest_tag)"
    archive="agent_${version}_${goos}_${goarch}.tar.gz"
    download_base="$AGENT_INSTALL_DOWNLOAD_ROOT/$version"
    tmpdir="$(mktemp -d)"
    trap 'rm -rf "$tmpdir"' EXIT INT TERM

    curl -fsSL "$download_base/$archive" -o "$tmpdir/$archive"
    curl -fsSL "$download_base/checksums.txt" -o "$tmpdir/checksums.txt"

    grep "  $archive\$" "$tmpdir/checksums.txt" >"$tmpdir/checksum.txt" || fail "missing checksum for $archive"
    (
        cd "$tmpdir"
        shasum -a 256 -c checksum.txt >/dev/null
    ) || fail "checksum verification failed"

    tar -xzf "$tmpdir/$archive" -C "$tmpdir"
    mkdir -p "$BIN_DIR"
    install -m 0755 "$tmpdir/agent" "$BIN_DIR/agent"

    echo "agent installed to $BIN_DIR/agent"
    echo "Run: agent doctor"
    echo "If macOS blocks the binary, run: xattr -d com.apple.quarantine $BIN_DIR/agent"
}

main "$@"
```

- [ ] **Step 4: Re-run the installer tests**

Run: `sh scripts/release/install_test.sh`
Expected: PASS

- [ ] **Step 5: Run shell syntax checks**

Run: `sh -n install.sh scripts/release/install_test.sh`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add install.sh scripts/release/install_test.sh
git commit -m "feat: add GitHub release installer"
```

## Task 3: Wire The GitHub Release Workflow And Update The README

**Files:**
- Create: `.github/workflows/release.yml`
- Create: `scripts/release/release_repo_test.sh`
- Modify: `README.md`

- [ ] **Step 1: Write the failing repository smoke test**

Create `scripts/release/release_repo_test.sh`:

```sh
#!/bin/sh
set -eu

rg -q '^name: release$' .github/workflows/release.yml
rg -q 'tags:' .github/workflows/release.yml
rg -q 'v\\*' .github/workflows/release.yml
rg -q 'go test ./\\.\\.\\.' .github/workflows/release.yml
rg -q 'sh scripts/release/build.sh' .github/workflows/release.yml
rg -q 'softprops/action-gh-release@v2' .github/workflows/release.yml

rg -q 'curl -fsSL https://raw\\.githubusercontent\\.com/BaronBonet/tmux-llm/main/install\\.sh \\| sh' README.md
rg -q 'agent doctor' README.md
rg -q 'xattr -d com\\.apple\\.quarantine' README.md
```

- [ ] **Step 2: Run the repository smoke test to verify red**

Run: `sh scripts/release/release_repo_test.sh`
Expected: FAIL because the workflow file and README install section do not exist yet

- [ ] **Step 3: Add the release workflow**

Create `.github/workflows/release.yml`:

```yaml
name: release

on:
  push:
    tags:
      - "v*"

permissions:
  contents: write

jobs:
  release:
    runs-on: macos-latest
    steps:
      - name: Check out repository
        uses: actions/checkout@v4

      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
          cache: true

      - name: Run tests
        run: go test ./...

      - name: Build release archives
        env:
          VERSION: ${{ github.ref_name }}
        run: sh scripts/release/build.sh

      - name: Publish GitHub release
        uses: softprops/action-gh-release@v2
        with:
          files: |
            dist/*.tar.gz
            dist/checksums.txt
```

- [ ] **Step 4: Update the README for packaged installation**

Add a new installation section near the top of `README.md` and switch the usage examples to the installed binary:

````md
## Install

Install the latest GitHub Release on macOS:

```bash
curl -fsSL https://raw.githubusercontent.com/BaronBonet/tmux-llm/main/install.sh | sh
```

The installer places `agent` in `~/.local/bin` by default. If that directory is not on your `PATH`, add it before using the CLI.

This prototype release path uses unsigned binaries. If macOS blocks the installed binary on first run, clear the quarantine flag once:

```bash
xattr -d com.apple.quarantine ~/.local/bin/agent
```

After installation, verify the environment with:

```bash
agent doctor
```
````

Then update the existing usage examples so they read:

```bash
agent new "add billing retry flow"
agent new --non-interactive --json "add billing retry flow"
agent ls
agent status billing-retry-flow
agent open billing-retry-flow
agent tui
```

Keep a short development note later in the README showing `go run ./cmd/agent ...` for local iteration.

- [ ] **Step 5: Re-run the repository smoke test**

Run: `sh scripts/release/release_repo_test.sh`
Expected: PASS

- [ ] **Step 6: Run all release-related verification**

Run: `sh scripts/release/build_test.sh`
Expected: PASS

Run: `sh scripts/release/install_test.sh`
Expected: PASS

Run: `sh scripts/release/release_repo_test.sh`
Expected: PASS

Run: `sh -n install.sh scripts/release/build.sh scripts/release/build_test.sh scripts/release/install_test.sh scripts/release/release_repo_test.sh`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add .github/workflows/release.yml README.md scripts/release/release_repo_test.sh
git commit -m "ci: add GitHub release packaging workflow"
```

## Task 4: Perform A Real GitHub Release Smoke Test

**Files:**
- Modify: none

- [ ] **Step 1: Push the implementation branch**

Run: `git push origin HEAD`
Expected: the packaging branch is available on GitHub for review before tagging

- [ ] **Step 2: Create and push a test release tag**

Run: `git tag -a v0.1.0 -m "v0.1.0"`
Expected: local annotated tag `v0.1.0` exists

Run: `git push origin v0.1.0`
Expected: the `release` workflow starts on GitHub for tag `v0.1.0`

- [ ] **Step 3: Verify the GitHub Release assets**

Check the GitHub Release page for tag `v0.1.0`.

Expected assets:

- `agent_v0.1.0_darwin_arm64.tar.gz`
- `agent_v0.1.0_darwin_amd64.tar.gz`
- `checksums.txt`

- [ ] **Step 4: Verify installation from another macOS environment**

On the second Mac or a clean shell profile, run:

```bash
curl -fsSL https://raw.githubusercontent.com/BaronBonet/tmux-llm/v0.1.0/install.sh | sh
agent doctor
```

If macOS blocks first execution, run:

```bash
xattr -d com.apple.quarantine ~/.local/bin/agent
agent doctor
```

Expected: `agent doctor` succeeds and the install path works without cloning the repository

## Self-Review

- Spec coverage: Task 1 covers versioned archives and checksums, Task 2 covers the `curl | sh` installer and quarantine note, Task 3 covers the tag-driven GitHub Actions workflow plus README changes, and Task 4 covers the real-world macOS smoke test from the approved spec.
- Placeholder scan: no `TODO`, `TBD`, or deferred implementation holes remain in the task steps.
- Type and interface consistency: all paths, asset names, environment variables, and commands are consistent across the plan.
