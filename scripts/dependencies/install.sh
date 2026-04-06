#!/usr/bin/env bash
set -euo pipefail

DIR_PATH=$(cd "$(dirname "${BASH_SOURCE:-$0}")" && pwd)

. "${DIR_PATH}/../../build_dependencies_versions"

DEST_DIR="${DIR_PATH}/../../local/bin"
BUILD_CACHE="${DIR_PATH}/../../local/go-build"
LOCAL_GOPROXY="file://$(go env GOMODCACHE)/cache/download"

mkdir -p "${DEST_DIR}"
mkdir -p "${BUILD_CACHE}"

echo "Installing golangci-lint"
GOLANGCI_LINT_SRC="$(go env GOMODCACHE)/github.com/golangci/golangci-lint/v2@v${GOLANGCI_LINT_VERSION}"
if command -v golangci-lint >/dev/null 2>&1; then
	echo "Using existing golangci-lint at $(command -v golangci-lint)"
elif [ -d "${GOLANGCI_LINT_SRC}" ]; then
	echo "Building golangci-lint from cached source"
	(cd "${GOLANGCI_LINT_SRC}" && GOCACHE="${BUILD_CACHE}" GOPROXY="${LOCAL_GOPROXY}" go build -o "${DEST_DIR}/golangci-lint" ./cmd/golangci-lint)
else
	GOCACHE="${BUILD_CACHE}" GOPROXY="${LOCAL_GOPROXY}" GOBIN="${DEST_DIR}" go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v${GOLANGCI_LINT_VERSION}
fi

echo "Downloading Go module and tool dependencies..."
GOCACHE="${BUILD_CACHE}" GOPROXY="${LOCAL_GOPROXY}" GOSUMDB=off go mod download

echo "All dependencies installed! ✨"
