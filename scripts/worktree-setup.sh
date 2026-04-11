#!/usr/bin/env bash
set -euo pipefail

echo "==> Installing dependencies..."
make dependencies-install

echo "==> Configuring local dev environment..."
source ./scripts/config-local-dev.sh

echo "==> Generating code..."
make generate

echo "==> Worktree setup complete!"
