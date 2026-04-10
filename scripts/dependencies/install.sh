#!/usr/bin/env bash
set -euo pipefail

DIR_PATH=$(cd "$(dirname "${BASH_SOURCE:-$0}")" && pwd)

. "${DIR_PATH}/../../build_dependencies_versions"

DEST_DIR="${DIR_PATH}/../../local/bin"
TMP_DIR="${DIR_PATH}/../../local/tmp"

mkdir -p "${DEST_DIR}"
mkdir -p "${TMP_DIR}"

echo "Installing golangci-lint"
TMP_GOBIN=$(mktemp -d "${TMP_DIR}/golangci-lint.XXXXXX")
trap 'rm -rf "${TMP_GOBIN}"' EXIT
GOBIN="${TMP_GOBIN}" go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v${GOLANGCI_LINT_VERSION}
mv -f "${TMP_GOBIN}/golangci-lint" "${DEST_DIR}/golangci-lint"

echo "Downloading Go module and tool dependencies..."
go mod download
go tool sqlc version >/dev/null

echo "All dependencies installed! ✨"
