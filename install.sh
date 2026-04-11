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
