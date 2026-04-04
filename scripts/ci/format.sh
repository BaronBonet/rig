#!/bin/bash
set -e

# Get the module name from go.mod
MODULE_NAME=$(grep -m 1 "^module " go.mod | awk '{print $2}')

# Get the line-length from .golangci.yaml
LINE_LENGTH=$(awk '/line-length/ {print $2}' .golangci.yaml)

# Run formatting tools
echo "Running gofmt..."
gofmt -w .

echo "Running goimports with local flag..."
go tool goimports -local ${MODULE_NAME}/ -w internal/ cmd/

echo "Running golines, using ${LINE_LENGTH} as the line-length..."
go tool golines -w --base-formatter gofmt --ignore-generated --chain-split-dots -m ${LINE_LENGTH} internal/ cmd/

echo "Formatting complete! ✨"
