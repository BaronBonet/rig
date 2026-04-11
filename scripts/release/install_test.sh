#!/bin/sh
set -eu

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT INT TERM

mkdir -p "$tmpdir/release/v0.1.0" "$tmpdir/work" "$tmpdir/prefix/bin" "$tmpdir/home"

cat >"$tmpdir/work/agent" <<'EOF'
#!/bin/sh
echo doctor: ok
EOF
chmod +x "$tmpdir/work/agent"
tar -C "$tmpdir/work" -czf "$tmpdir/release/v0.1.0/agent_v0.1.0_darwin_arm64.tar.gz" agent
tar -C "$tmpdir/work" -czf "$tmpdir/release/v0.1.0/agent_v0.1.0_darwin_amd64.tar.gz" agent

(
    cd "$tmpdir/release/v0.1.0"
    shasum -a 256 agent_v0.1.0_darwin_arm64.tar.gz >checksums.txt
    shasum -a 256 agent_v0.1.0_darwin_amd64.tar.gz >>checksums.txt
)

cat >"$tmpdir/latest.json" <<'EOF'
{"tag_name":"v0.1.0"}
EOF

# Test 1: basic install + PATH configuration
PATH="/usr/bin:/bin:/usr/sbin:/sbin" \
HOME="$tmpdir/home" \
SHELL="/bin/zsh" \
PREFIX="$tmpdir/prefix" \
AGENT_INSTALL_API_URL="file://$tmpdir/latest.json" \
AGENT_INSTALL_DOWNLOAD_ROOT="file://$tmpdir/release" \
sh ./install.sh

[ -x "$tmpdir/prefix/bin/agent" ]
output="$("$tmpdir/prefix/bin/agent")"
[ "$output" = "doctor: ok" ]

# Verify PATH was added to .zshrc
grep -qF "export PATH=\"$tmpdir/prefix/bin:\$PATH\"" "$tmpdir/home/.zshrc"

# Test 2: re-running install does NOT duplicate the PATH line
PATH="/usr/bin:/bin:/usr/sbin:/sbin" \
HOME="$tmpdir/home" \
SHELL="/bin/zsh" \
PREFIX="$tmpdir/prefix" \
AGENT_INSTALL_API_URL="file://$tmpdir/latest.json" \
AGENT_INSTALL_DOWNLOAD_ROOT="file://$tmpdir/release" \
sh ./install.sh

count="$(grep -cF "export PATH=\"$tmpdir/prefix/bin:\$PATH\"" "$tmpdir/home/.zshrc")"
[ "$count" -eq 1 ]

# Test 3: PATH not modified when BIN_DIR is already in PATH
rm -rf "$tmpdir/home/.zshrc" "$tmpdir/prefix-already"
mkdir -p "$tmpdir/prefix-already/bin"
PATH="$tmpdir/prefix-already/bin:/usr/bin:/bin" \
HOME="$tmpdir/home" \
SHELL="/bin/zsh" \
PREFIX="$tmpdir/prefix-already" \
AGENT_INSTALL_API_URL="file://$tmpdir/latest.json" \
AGENT_INSTALL_DOWNLOAD_ROOT="file://$tmpdir/release" \
sh ./install.sh

# .zshrc should not exist (was deleted above and should not be recreated)
[ ! -f "$tmpdir/home/.zshrc" ]

# Test 4: checksum failure still works
cat >"$tmpdir/release/v0.1.0/checksums.txt" <<'EOF'
0000000000000000000000000000000000000000000000000000000000000000  agent_v0.1.0_darwin_arm64.tar.gz
0000000000000000000000000000000000000000000000000000000000000000  agent_v0.1.0_darwin_amd64.tar.gz
EOF

if PATH="/usr/bin:/bin:/usr/sbin:/sbin" \
    HOME="$tmpdir/home" \
    SHELL="/bin/zsh" \
    PREFIX="$tmpdir/prefix-bad" \
    AGENT_INSTALL_API_URL="file://$tmpdir/latest.json" \
    AGENT_INSTALL_DOWNLOAD_ROOT="file://$tmpdir/release" \
    sh ./install.sh
then
    echo "expected checksum failure" >&2
    exit 1
fi
