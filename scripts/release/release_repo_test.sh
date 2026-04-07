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
check release-build rg -q 'sh scripts/release/build.sh' .github/workflows/release.yml
check release-action rg -q 'softprops/action-gh-release@v2' .github/workflows/release.yml

check readme-install rg -F -q 'curl -fsSL https://raw.githubusercontent.com/BaronBonet/tmux-llm/main/install.sh | sh' README.md
check readme-doctor rg -F -q 'agent doctor' README.md
check readme-quarantine rg -F -q 'xattr -d com.apple.quarantine' README.md
