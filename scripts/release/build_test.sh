#!/bin/sh
set -eu

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT INT TERM

mkdir -p "$tmpdir/bin" "$tmpdir/dist"

cat >"$tmpdir/bin/go" <<'EOF'
#!/bin/sh
set -eu

out=""
while [ "$#" -gt 0 ]; do
    if [ "$1" = "-o" ]; then
        out="$2"
        shift 2
        continue
    fi
    shift
done

if [ -z "$out" ]; then
    echo "missing -o output" >&2
    exit 1
fi

printf '#!/bin/sh\necho packaged-agent\n' >"$out"
chmod +x "$out"
EOF
chmod +x "$tmpdir/bin/go"

PATH="$tmpdir/bin:$PATH" VERSION="v0.1.0" DIST_DIR="$tmpdir/dist" sh ./scripts/release/build.sh

[ -f "$tmpdir/dist/agent_v0.1.0_darwin_arm64.tar.gz" ]
[ -f "$tmpdir/dist/agent_v0.1.0_darwin_amd64.tar.gz" ]
[ -f "$tmpdir/dist/checksums.txt" ]

tar -xzf "$tmpdir/dist/agent_v0.1.0_darwin_arm64.tar.gz" -C "$tmpdir"
[ -x "$tmpdir/agent" ]

(
    cd "$tmpdir/dist"

    set -- $(shasum -a 256 ./agent_v0.1.0_darwin_arm64.tar.gz)
    grep -Fx "$1  $2" checksums.txt

    set -- $(shasum -a 256 ./agent_v0.1.0_darwin_amd64.tar.gz)
    grep -Fx "$1  $2" checksums.txt
)
