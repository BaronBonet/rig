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

line_number() {
    file="$1"
    pattern="$2"

    rg -n -F -m 1 "$pattern" "$file" | cut -d: -f1 || true
}

check_order() {
    label="$1"
    file="$2"
    first="$3"
    second="$4"

    first_line="$(line_number "$file" "$first")"
    second_line="$(line_number "$file" "$second")"

    if [ -z "$first_line" ] || [ -z "$second_line" ] || [ "$first_line" -ge "$second_line" ]; then
        echo "failed: $label" >&2
        exit 1
    fi
}

check_run() {
    label="$1"
    file="$2"
    command="$3"

    check "$label" rg -n -F -q "run: ${command}" "$file"
}

check release-name rg -q '^name: release$' .github/workflows/release.yml
check release-tags rg -q 'tags:' .github/workflows/release.yml
check release-tag-glob rg -F -q 'v*' .github/workflows/release.yml
check_run release-generate .github/workflows/release.yml ./scripts/generate/go.sh
check_run release-regression .github/workflows/release.yml sh ./scripts/generate/go_test.sh
check_run release-tests .github/workflows/release.yml go test ./...
check_run release-assertions .github/workflows/release.yml sh ./scripts/release/release_repo_test.sh
check_order release-order-generate-before-regression .github/workflows/release.yml 'run: ./scripts/generate/go.sh' 'run: sh ./scripts/generate/go_test.sh'
check_order release-order-regression-before-tests .github/workflows/release.yml 'run: sh ./scripts/generate/go_test.sh' 'run: go test ./...'
if rg -F -q 'make test' .github/workflows/release.yml; then
    echo 'failed: release-no-make' >&2
    exit 1
fi
check release-goreleaser rg -q 'goreleaser/goreleaser-action@v6' .github/workflows/release.yml
check goreleaser-config test -f .goreleaser.yaml

check readme-install rg -F -q 'curl -fsSL https://raw.githubusercontent.com/BaronBonet/rig/main/install.sh | sh' README.md
check readme-doctor rg -F -q 'rig doctor' README.md
check readme-quarantine rg -F -q 'xattr -d com.apple.quarantine' README.md
