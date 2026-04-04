#!/bin/sh
set -eu

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT INT TERM

mkdir -p "$tmpdir/release/v0.1.0" "$tmpdir/work" "$tmpdir/prefix/bin"

cat >"$tmpdir/work/agent" <<'EOF'
#!/bin/sh
echo doctor: ok
EOF
chmod +x "$tmpdir/work/agent"
tar -C "$tmpdir/work" -czf "$tmpdir/release/v0.1.0/agent_v0.1.0_darwin_arm64.tar.gz" agent

(
    cd "$tmpdir/release/v0.1.0"
    shasum -a 256 agent_v0.1.0_darwin_arm64.tar.gz >checksums.txt
)

cat >"$tmpdir/latest.json" <<'EOF'
{"tag_name":"v0.1.0"}
EOF

PATH="/usr/bin:/bin:/usr/sbin:/sbin" \
HOME="$tmpdir/home" \
PREFIX="$tmpdir/prefix" \
AGENT_INSTALL_API_URL="file://$tmpdir/latest.json" \
AGENT_INSTALL_DOWNLOAD_ROOT="file://$tmpdir/release" \
sh ./install.sh

[ -x "$tmpdir/prefix/bin/agent" ]
output="$("$tmpdir/prefix/bin/agent")"
[ "$output" = "doctor: ok" ]

cat >"$tmpdir/release/v0.1.0/checksums.txt" <<'EOF'
0000000000000000000000000000000000000000000000000000000000000000  agent_v0.1.0_darwin_arm64.tar.gz
EOF

if PATH="/usr/bin:/bin:/usr/sbin:/sbin" \
    HOME="$tmpdir/home" \
    PREFIX="$tmpdir/prefix-bad" \
    AGENT_INSTALL_API_URL="file://$tmpdir/latest.json" \
    AGENT_INSTALL_DOWNLOAD_ROOT="file://$tmpdir/release" \
    sh ./install.sh
then
    echo "expected checksum failure" >&2
    exit 1
fi
