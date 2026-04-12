#!/bin/sh
set -eu

line_number() {
    file="$1"
    pattern="$2"

    rg -n -F -m 1 "$pattern" "$file" | cut -d: -f1 || true
}

check() {
    label="$1"
    shift

    if ! "$@"; then
        echo "failed: $label" >&2
        exit 1
    fi
}

check_absent() {
    label="$1"
    file="$2"
    pattern="$3"

    if rg -F -q "$pattern" "$file"; then
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

lint_workflow=.github/workflows/lint.yml
release_workflow=.github/workflows/release.yml

check_run lint-installs-deps "$lint_workflow" ./scripts/dependencies/install.sh
check_run lint-runs-generate "$lint_workflow" ./scripts/generate/go.sh
check_run lint-runs-linter "$lint_workflow" ./scripts/ci/lint-go.sh
check_run lint-runs-regression "$lint_workflow" sh ./scripts/generate/go_test.sh
check_run lint-runs-tests "$lint_workflow" go test ./...
check_run lint-runs-assertions "$lint_workflow" sh ./scripts/ci/github_actions_test.sh
check_absent lint-no-make-deps "$lint_workflow" 'make dependencies install'
check_absent lint-no-make-lint "$lint_workflow" 'make lint-all'
check_absent lint-no-make-test "$lint_workflow" 'make test'
check_order lint-order-install-before-generate "$lint_workflow" 'run: ./scripts/dependencies/install.sh' 'run: ./scripts/generate/go.sh'
check_order lint-order-generate-before-lint "$lint_workflow" 'run: ./scripts/generate/go.sh' 'run: ./scripts/ci/lint-go.sh'
check_order lint-order-lint-before-regression "$lint_workflow" 'run: ./scripts/ci/lint-go.sh' 'run: sh ./scripts/generate/go_test.sh'
check_order lint-order-regression-before-test "$lint_workflow" 'run: sh ./scripts/generate/go_test.sh' 'run: go test ./...'

check_run release-runs-generate "$release_workflow" ./scripts/generate/go.sh
check_run release-runs-regression "$release_workflow" sh ./scripts/generate/go_test.sh
check_run release-runs-tests "$release_workflow" go test ./...
check_run release-runs-assertions "$release_workflow" sh ./scripts/release/release_repo_test.sh
check_absent release-no-make-test "$release_workflow" 'make test'
check_order release-order-generate-before-regression "$release_workflow" 'run: ./scripts/generate/go.sh' 'run: sh ./scripts/generate/go_test.sh'
check_order release-order-regression-before-test "$release_workflow" 'run: sh ./scripts/generate/go_test.sh' 'run: go test ./...'

exit 0
