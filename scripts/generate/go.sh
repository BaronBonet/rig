#!/usr/bin/env bash
set -euo pipefail

PROJECT_ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

export PATH="${PROJECT_ROOT_DIR}/local/bin:${PATH}"

cd "${PROJECT_ROOT_DIR}"

echo "Cleaning generated artifacts..."
rm -rf ./internal/adapters/repository/sqlite/generated
find . -type f -name 'mock_*.go' -delete

echo "Generating sqlite code..."
go tool sqlc generate -f ./internal/adapters/repository/sqlite/sqlc.yaml

echo "Generating mocks..."
go tool mockery --config=.mockery.yaml

echo "Generation complete."
