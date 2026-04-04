#!/usr/bin/env bash
set -euo pipefail

DIR_PATH=$(cd "$(dirname "${BASH_SOURCE:-$0}")" && pwd)
. "${DIR_PATH}/../../build_dependencies_versions"

LOCAL_BIN="${DIR_PATH}/../../local/bin"

exit_code=0

ACTUAL_GOLANGCI_LINT_VERSION=$("${LOCAL_BIN}/golangci-lint" version | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1)

if [[ "${ACTUAL_GOLANGCI_LINT_VERSION}" != "${GOLANGCI_LINT_VERSION}" ]]; then
	echo "golangci-lint version (${ACTUAL_GOLANGCI_LINT_VERSION}) mismatch - expected ${GOLANGCI_LINT_VERSION}"
	exit_code=1
fi

exit ${exit_code}
