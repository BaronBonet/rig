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
