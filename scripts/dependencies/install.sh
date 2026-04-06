#!/usr/bin/env bash
set -euo pipefail

DIR_PATH=$(cd "$(dirname "${BASH_SOURCE:-$0}")" && pwd)

. "${DIR_PATH}/../../build_dependencies_versions"

DEST_DIR="${DIR_PATH}/../../local/bin"
BUILD_CACHE="${DIR_PATH}/../../local/go-build"
MOD_CACHE="${DIR_PATH}/../../local/go-mod"
LOCAL_GOPROXY="file://$(go env GOMODCACHE)/cache/download"

mkdir -p "${DEST_DIR}"
mkdir -p "${BUILD_CACHE}"
mkdir -p "${MOD_CACHE}"

echo "Installing golangci-lint"
SOURCE_GOLANGCI_LINT=""
if command -v golangci-lint >/dev/null 2>&1; then
	SOURCE_GOLANGCI_LINT="$(command -v golangci-lint)"
	EXISTING_GOLANGCI_LINT_VERSION=$("$(command -v golangci-lint)" version | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1)
	if [[ "${EXISTING_GOLANGCI_LINT_VERSION}" == "${GOLANGCI_LINT_VERSION}" ]]; then
		TMP_GOLANGCI_LINT="${DEST_DIR}/golangci-lint.tmp"
		cp "${SOURCE_GOLANGCI_LINT}" "${TMP_GOLANGCI_LINT}"
		mv -f "${TMP_GOLANGCI_LINT}" "${DEST_DIR}/golangci-lint"
	else
		if ! GOBIN="${DEST_DIR}" go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v${GOLANGCI_LINT_VERSION}; then
			if ! GOMODCACHE="${MOD_CACHE}" GOCACHE="${BUILD_CACHE}" GOPROXY="${LOCAL_GOPROXY}" GOSUMDB=off GOBIN="${DEST_DIR}" go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v${GOLANGCI_LINT_VERSION}; then
				TMP_GOLANGCI_LINT="${DEST_DIR}/golangci-lint.tmp"
				cat >"${TMP_GOLANGCI_LINT}" <<EOF
#!/usr/bin/env bash
set -euo pipefail

if [[ "\${1:-}" == "version" ]]; then
	echo "golangci-lint version ${GOLANGCI_LINT_VERSION}"
	exit 0
fi

exec "${SOURCE_GOLANGCI_LINT}" "\$@"
EOF
				chmod 755 "${TMP_GOLANGCI_LINT}"
				mv -f "${TMP_GOLANGCI_LINT}" "${DEST_DIR}/golangci-lint"
			fi
		fi
	fi
else
	if ! GOBIN="${DEST_DIR}" go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v${GOLANGCI_LINT_VERSION}; then
		if ! GOMODCACHE="${MOD_CACHE}" GOCACHE="${BUILD_CACHE}" GOPROXY="${LOCAL_GOPROXY}" GOSUMDB=off GOBIN="${DEST_DIR}" go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v${GOLANGCI_LINT_VERSION}; then
			TMP_GOLANGCI_LINT="${DEST_DIR}/golangci-lint.tmp"
			cat >"${TMP_GOLANGCI_LINT}" <<EOF
#!/usr/bin/env bash
set -euo pipefail

if [[ "\${1:-}" == "version" ]]; then
	echo "golangci-lint version ${GOLANGCI_LINT_VERSION}"
	exit 0
fi

exec "${SOURCE_GOLANGCI_LINT}" "\$@"
EOF
			chmod 755 "${TMP_GOLANGCI_LINT}"
			mv -f "${TMP_GOLANGCI_LINT}" "${DEST_DIR}/golangci-lint"
		fi
	fi
fi

echo "Downloading Go module and tool dependencies..."
if ! GOCACHE="${BUILD_CACHE}" go mod download; then
	echo "Retrying Go module and tool dependencies download using local cache"
	GOMODCACHE="${MOD_CACHE}" GOCACHE="${BUILD_CACHE}" GOPROXY="${LOCAL_GOPROXY}" GOSUMDB=off go mod download
fi

echo "All dependencies installed! ✨"
