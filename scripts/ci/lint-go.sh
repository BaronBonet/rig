#!/bin/bash
set -e
MODULE_NAME=$(grep -m 1 "^module " go.mod | awk '{print $2}')
LINE_LENGTH=$(awk '/line-length/ {print $2}' .golangci.yaml)

function check_no_changes() {
	local command=$1
	local description=$2
	local output
	# Redirect stderr to a filtering command to suppress "Permission denied" errors
	output=$(eval "$command" 2> >(grep -v "Permission denied" >&2))
	if [[ -n "$output" ]]; then
		echo "❌ $description would make changes. Run 'make format' to fix."
		echo "$output"
		return 1
	else
		echo "✅ $description check passed."
		return 0
	fi
}

echo "Running golangci-lint..."
go tool golangci-lint run --timeout=5m --config=.golangci.yaml || exit 1

echo "Checking if goimports would make changes..."

GO_FILES=$(find . -type f -name '*.go' -not -path '*/generated/*')
check_no_changes "go tool goimports -local ${MODULE_NAME}/ -d ${GO_FILES[@]}" "goimports" || exit 1
echo "Checking if golines would make changes..."

check_no_changes "go tool golines --dry-run --base-formatter gofmt --ignore-generated --no-reformat-tags --chain-split-dots -m ${LINE_LENGTH} internal/ cmd/" "golines" || exit 1
echo "All Golang linting checks passed! ✨"
