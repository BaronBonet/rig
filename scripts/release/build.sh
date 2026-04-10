#!/bin/sh
set -eu

VERSION="${VERSION:?VERSION is required}"
DIST_DIR="${DIST_DIR:-dist}"
APP_NAME="${APP_NAME:-agent}"

mkdir -p "$DIST_DIR"
rm -f "$DIST_DIR"/"${APP_NAME}"_*.tar.gz "$DIST_DIR/checksums.txt"

make generate

build_archive() {
    (
        goos="$1"
        goarch="$2"
        archive="${APP_NAME}_${VERSION}_${goos}_${goarch}.tar.gz"
        workdir="$(mktemp -d)"
        trap 'rm -rf "$workdir"' EXIT INT TERM HUP

        GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 go build -o "$workdir/$APP_NAME" ./cmd/agent
        tar -C "$workdir" -czf "$DIST_DIR/$archive" "$APP_NAME"
    )
}

build_archive darwin arm64
build_archive darwin amd64

(
    cd "$DIST_DIR"
    shasum -a 256 ./*.tar.gz >checksums.txt
)
