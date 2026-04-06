#!/usr/bin/env bash
set -euo pipefail

DIR_PATH=$(cd "$(dirname "${BASH_SOURCE:-$0}")" && pwd)

. "${DIR_PATH}/../../build_dependencies_versions"

DEST_DIR="${DIR_PATH}/../../local/bin"

mkdir -p "${DEST_DIR}"

echo "Installing golangci-lint"
GOBIN="${DEST_DIR}" go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v${GOLANGCI_LINT_VERSION}

echo "Downloading Go module and tool dependencies..."
go mod download

echo "All dependencies installed! ✨"
