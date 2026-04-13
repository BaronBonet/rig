#!/usr/bin/env bash
set -euo pipefail

DIR_PATH=$(cd "$(dirname "${BASH_SOURCE:-$0}")" && pwd)

DEST_DIR="${DIR_PATH}/../../local/bin"

mkdir -p "${DEST_DIR}"

echo "Downloading Go module and tool dependencies..."
go mod download

echo "Installing golangci-lint"
GOBIN="${DEST_DIR}" go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint

echo "All dependencies installed! ✨"
