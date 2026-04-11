# GoReleaser Migration and PATH Auto-Configuration — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace custom build/release scripts with GoReleaser and add automatic PATH configuration to the install script.

**Architecture:** Add a `.goreleaser.yaml` config that builds darwin/arm64+amd64, simplify the GitHub Actions release workflow to use `goreleaser-action`, delete the custom build scripts, and extend `install.sh` with shell rc file detection and PATH export appending.

**Tech Stack:** GoReleaser, GitHub Actions, POSIX shell (`/bin/sh`)

---

### Task 1: Add `.goreleaser.yaml` configuration

**Files:**
- Create: `.goreleaser.yaml`

- [ ] **Step 1: Create the GoReleaser config**

Create `.goreleaser.yaml` at the repo root:

```yaml
version: 2

before:
  hooks:
    - make generate

builds:
  - main: ./cmd/agent
    binary: agent
    env:
      - CGO_ENABLED=0
    goos:
      - darwin
    goarch:
      - arm64
      - amd64

archives:
  - name_template: "{{ .ProjectName }}_{{ .Tag }}_{{ .Os }}_{{ .Arch }}"
    format: tar.gz

checksum:
  name_template: "checksums.txt"
  algorithm: sha256

release:
  github:
    owner: BaronBonet
    name: tmux-llm
```

- [ ] **Step 2: Validate the config**

Run: `goreleaser check`

If `goreleaser` is not installed locally, run: `go install github.com/goreleaser/goreleaser/v2@latest` first.

Expected: config is valid.

- [ ] **Step 3: Commit**

```bash
git add .goreleaser.yaml
git commit -m "build: add goreleaser config"
```

---

### Task 2: Update GitHub Actions release workflow

**Files:**
- Modify: `.github/workflows/release.yml`

- [ ] **Step 1: Replace the release workflow**

Replace the entire contents of `.github/workflows/release.yml` with:

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
        with:
          fetch-depth: 0

      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
          cache: true

      - name: Run tests
        run: make test

      - name: Run GoReleaser
        uses: goreleaser/goreleaser-action@v6
        with:
          version: "~> v2"
          args: release --clean
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

Note: `fetch-depth: 0` is required by GoReleaser to generate changelogs. The `make generate` step is removed from the workflow because GoReleaser's `before.hooks` handles it. The `Install dependencies` step is also removed because `make generate` (called by GoReleaser's before hook) already runs `dependencies-check` via its Makefile dependency — if that check fails in CI, the before hook itself will fail, which is the correct behavior.

- [ ] **Step 2: Commit**

```bash
git add .github/workflows/release.yml
git commit -m "build: use goreleaser in release workflow"
```

---

### Task 3: Delete custom build scripts

**Files:**
- Delete: `scripts/release/build.sh`
- Delete: `scripts/release/build_test.sh`

- [ ] **Step 1: Delete the files**

```bash
rm scripts/release/build.sh scripts/release/build_test.sh
```

- [ ] **Step 2: Commit**

```bash
git add -u scripts/release/build.sh scripts/release/build_test.sh
git commit -m "build: remove custom build scripts replaced by goreleaser"
```

---

### Task 4: Update release repo test

**Files:**
- Modify: `scripts/release/release_repo_test.sh`

- [ ] **Step 1: Update the test assertions**

Replace the entire contents of `scripts/release/release_repo_test.sh` with:

```sh
#!/bin/sh
set -eu

check() {
    label="$1"
    shift

    if ! "$@"; then
        echo "failed: $label" >&2
        exit 1
    fi
}

check release-name rg -q '^name: release$' .github/workflows/release.yml
check release-tags rg -q 'tags:' .github/workflows/release.yml
check release-tag-glob rg -F -q 'v*' .github/workflows/release.yml
check release-tests rg -F -q 'make test' .github/workflows/release.yml
check release-goreleaser rg -q 'goreleaser/goreleaser-action@v6' .github/workflows/release.yml
check goreleaser-config test -f .goreleaser.yaml

check readme-install rg -F -q 'curl -fsSL https://raw.githubusercontent.com/BaronBonet/tmux-llm/main/install.sh | sh' README.md
check readme-doctor rg -F -q 'agent doctor' README.md
check readme-quarantine rg -F -q 'xattr -d com.apple.quarantine' README.md
```

Changes from the original:
- Removed: `check release-build rg -q 'sh scripts/release/build.sh'` (build.sh deleted)
- Removed: `check release-action rg -q 'softprops/action-gh-release@v2'` (replaced by GoReleaser)
- Added: `check release-goreleaser rg -q 'goreleaser/goreleaser-action@v6'`
- Added: `check goreleaser-config test -f .goreleaser.yaml`

- [ ] **Step 2: Run the test**

Run: `sh scripts/release/release_repo_test.sh`

Expected: all checks pass.

- [ ] **Step 3: Commit**

```bash
git add scripts/release/release_repo_test.sh
git commit -m "test: update release repo test for goreleaser"
```

---

### Task 5: Add PATH auto-configuration to `install.sh`

**Files:**
- Modify: `install.sh`

- [ ] **Step 1: Add the `ensure_on_path` function and call it**

Replace the entire contents of `install.sh` with:

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

detect_shell_rc() {
    shell_name="$(basename "${SHELL:-}")"
    case "$shell_name" in
        zsh)
            echo "$HOME/.zshrc"
            ;;
        bash)
            if [ "$(uname -s)" = "Darwin" ] && [ -f "$HOME/.bash_profile" ]; then
                echo "$HOME/.bash_profile"
            else
                echo "$HOME/.bashrc"
            fi
            ;;
        *)
            echo ""
            ;;
    esac
}

ensure_on_path() {
    case ":${PATH}:" in
        *":${BIN_DIR}:"*) return ;;
    esac

    export_line="export PATH=\"${BIN_DIR}:\$PATH\""
    rc_file="$(detect_shell_rc)"

    if [ -z "$rc_file" ]; then
        echo "Add ${BIN_DIR} to your PATH:"
        echo "  $export_line"
        return
    fi

    if [ -f "$rc_file" ] && grep -qF "$export_line" "$rc_file"; then
        return
    fi

    printf '\n# Added by agent installer\n%s\n' "$export_line" >>"$rc_file"
    echo "Added ${BIN_DIR} to PATH in $rc_file"
    echo "Run: source $rc_file"
}

main() {
    require_cmd curl
    require_cmd tar
    require_cmd shasum
    require_cmd install

    goos="$(detect_goos)"
    goarch="$(detect_goarch)"
    version="$(latest_tag)"
    archive="agent_${version}_${goos}_${goarch}.tar.gz"
    download_base="$AGENT_INSTALL_DOWNLOAD_ROOT/$version"
    tmpdir="$(mktemp -d)"
    trap 'rm -rf "$tmpdir"' EXIT INT TERM

    curl -fsSL "$download_base/$archive" -o "$tmpdir/$archive"
    curl -fsSL "$download_base/checksums.txt" -o "$tmpdir/checksums.txt"

    grep -F "  $archive" "$tmpdir/checksums.txt" >"$tmpdir/checksum.txt" || fail "missing checksum for $archive"
    (
        cd "$tmpdir"
        shasum -a 256 -c checksum.txt >/dev/null
    ) || fail "checksum verification failed"

    tar -xzf "$tmpdir/$archive" -C "$tmpdir"
    mkdir -p "$BIN_DIR"
    install -m 0755 "$tmpdir/agent" "$BIN_DIR/agent"

    echo "agent installed to $BIN_DIR/agent"
    ensure_on_path
    echo "Run: agent doctor"
    echo "If macOS blocks the binary, run: xattr -d com.apple.quarantine $BIN_DIR/agent"
}

main "$@"
```

Changes from the original:
- Added `detect_shell_rc()` function — returns the appropriate rc file path for zsh or bash, empty string for unknown shells
- Added `ensure_on_path()` function — checks if `BIN_DIR` is in PATH, and if not, appends the export line to the detected rc file
- Replaced the static `echo` messages at the end of `main` to call `ensure_on_path` between the "installed" and "doctor" messages

- [ ] **Step 2: Commit**

```bash
git add install.sh
git commit -m "feat: auto-configure PATH in install script"
```

---

### Task 6: Update install test for PATH configuration

**Files:**
- Modify: `scripts/release/install_test.sh`

- [ ] **Step 1: Replace the install test**

Replace the entire contents of `scripts/release/install_test.sh` with:

```sh
#!/bin/sh
set -eu

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT INT TERM

mkdir -p "$tmpdir/release/v0.1.0" "$tmpdir/work" "$tmpdir/prefix/bin" "$tmpdir/home"

cat >"$tmpdir/work/agent" <<'EOF'
#!/bin/sh
echo doctor: ok
EOF
chmod +x "$tmpdir/work/agent"
tar -C "$tmpdir/work" -czf "$tmpdir/release/v0.1.0/agent_v0.1.0_darwin_arm64.tar.gz" agent
tar -C "$tmpdir/work" -czf "$tmpdir/release/v0.1.0/agent_v0.1.0_darwin_amd64.tar.gz" agent

(
    cd "$tmpdir/release/v0.1.0"
    shasum -a 256 agent_v0.1.0_darwin_arm64.tar.gz >checksums.txt
    shasum -a 256 agent_v0.1.0_darwin_amd64.tar.gz >>checksums.txt
)

cat >"$tmpdir/latest.json" <<'EOF'
{"tag_name":"v0.1.0"}
EOF

# Test 1: basic install + PATH configuration
PATH="/usr/bin:/bin:/usr/sbin:/sbin" \
HOME="$tmpdir/home" \
SHELL="/bin/zsh" \
PREFIX="$tmpdir/prefix" \
AGENT_INSTALL_API_URL="file://$tmpdir/latest.json" \
AGENT_INSTALL_DOWNLOAD_ROOT="file://$tmpdir/release" \
sh ./install.sh

[ -x "$tmpdir/prefix/bin/agent" ]
output="$("$tmpdir/prefix/bin/agent")"
[ "$output" = "doctor: ok" ]

# Verify PATH was added to .zshrc
grep -qF "export PATH=\"$tmpdir/prefix/bin:\$PATH\"" "$tmpdir/home/.zshrc"

# Test 2: re-running install does NOT duplicate the PATH line
PATH="/usr/bin:/bin:/usr/sbin:/sbin" \
HOME="$tmpdir/home" \
SHELL="/bin/zsh" \
PREFIX="$tmpdir/prefix" \
AGENT_INSTALL_API_URL="file://$tmpdir/latest.json" \
AGENT_INSTALL_DOWNLOAD_ROOT="file://$tmpdir/release" \
sh ./install.sh

count="$(grep -cF "export PATH=\"$tmpdir/prefix/bin:\$PATH\"" "$tmpdir/home/.zshrc")"
[ "$count" -eq 1 ]

# Test 3: PATH not modified when BIN_DIR is already in PATH
rm -rf "$tmpdir/home/.zshrc" "$tmpdir/prefix-already"
mkdir -p "$tmpdir/prefix-already/bin"
PATH="$tmpdir/prefix-already/bin:/usr/bin:/bin" \
HOME="$tmpdir/home" \
SHELL="/bin/zsh" \
PREFIX="$tmpdir/prefix-already" \
AGENT_INSTALL_API_URL="file://$tmpdir/latest.json" \
AGENT_INSTALL_DOWNLOAD_ROOT="file://$tmpdir/release" \
sh ./install.sh

# .zshrc should not exist (was deleted above and should not be recreated)
[ ! -f "$tmpdir/home/.zshrc" ]

# Test 4: checksum failure still works
cat >"$tmpdir/release/v0.1.0/checksums.txt" <<'EOF'
0000000000000000000000000000000000000000000000000000000000000000  agent_v0.1.0_darwin_arm64.tar.gz
0000000000000000000000000000000000000000000000000000000000000000  agent_v0.1.0_darwin_amd64.tar.gz
EOF

if PATH="/usr/bin:/bin:/usr/sbin:/sbin" \
    HOME="$tmpdir/home" \
    SHELL="/bin/zsh" \
    PREFIX="$tmpdir/prefix-bad" \
    AGENT_INSTALL_API_URL="file://$tmpdir/latest.json" \
    AGENT_INSTALL_DOWNLOAD_ROOT="file://$tmpdir/release" \
    sh ./install.sh
then
    echo "expected checksum failure" >&2
    exit 1
fi
```

Changes from the original:
- Added `SHELL="/bin/zsh"` and `HOME="$tmpdir/home"` to all test invocations
- Test 1: verifies `.zshrc` gets the PATH export line
- Test 2: verifies re-running doesn't duplicate the line
- Test 3: verifies no rc file modification when BIN_DIR is already in PATH
- Test 4: preserved the original checksum failure test

- [ ] **Step 2: Run the install test**

Run: `sh scripts/release/install_test.sh`

Expected: all tests pass.

- [ ] **Step 3: Commit**

```bash
git add scripts/release/install_test.sh
git commit -m "test: add PATH configuration tests to install test"
```
