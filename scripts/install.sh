#!/bin/sh
set -eu

PREFIX="${PREFIX:-$HOME/.local}"
BIN_DIR="${BIN_DIR:-$PREFIX/bin}"
RIG_INSTALL_REPO="${RIG_INSTALL_REPO:-BaronBonet/rig}"
RIG_INSTALL_API_URL="${RIG_INSTALL_API_URL:-https://api.github.com/repos/$RIG_INSTALL_REPO/releases/latest}"
RIG_INSTALL_DOWNLOAD_ROOT="${RIG_INSTALL_DOWNLOAD_ROOT:-https://github.com/$RIG_INSTALL_REPO/releases/download}"

fail() {
	echo "rig installer: $*" >&2
	exit 1
}

require_cmd() {
	command -v "$1" >/dev/null 2>&1 || fail "missing required command: $1"
}

github_token() {
	if [ -n "${GH_TOKEN:-}" ]; then
		printf '%s' "$GH_TOKEN"
		return
	fi

	if [ -n "${GITHUB_TOKEN:-}" ]; then
		printf '%s' "$GITHUB_TOKEN"
		return
	fi

	printf '%s' ""
}

can_use_gh() {
	command -v gh >/dev/null 2>&1 || return 1
	gh auth status >/dev/null 2>&1
}

curl_fetch() {
	url=$1
	token="$(github_token)"

	if [ -n "$token" ]; then
		curl -fsSL -H "Authorization: Bearer $token" "$url"
		return
	fi

	curl -fsSL "$url"
}

curl_download() {
	url=$1
	dest=$2
	token="$(github_token)"

	if [ -n "$token" ]; then
		curl -fsSL -H "Authorization: Bearer $token" "$url" -o "$dest"
		return
	fi

	curl -fsSL "$url" -o "$dest"
}

detect_goos() {
	case "$(uname -s)" in
	Darwin) echo "darwin" ;;
	Linux) echo "linux" ;;
	*) fail "unsupported operating system: $(uname -s); supported platforms are macOS and Linux" ;;
	esac
}

detect_goarch() {
	case "$(uname -m)" in
	arm64 | aarch64) echo "arm64" ;;
	x86_64) echo "amd64" ;;
	*) fail "unsupported CPU architecture: $(uname -m)" ;;
	esac
}

latest_tag() {
	if can_use_gh; then
		tag="$(gh api "repos/$RIG_INSTALL_REPO/releases/latest" --jq .tag_name)"
	else
		tag="$(curl_fetch "$RIG_INSTALL_API_URL" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)"
	fi

	[ -n "$tag" ] || fail "could not resolve the latest GitHub release; authenticate with gh or set GH_TOKEN"
	echo "$tag"
}

download_release_assets() {
	version=$1
	archive=$2
	tmpdir=$3

	if can_use_gh; then
		gh release download "$version" -R "$RIG_INSTALL_REPO" -p "$archive" -p checksums.txt -D "$tmpdir"
		return
	fi

	download_base="$RIG_INSTALL_DOWNLOAD_ROOT/$version"
	curl_download "$download_base/$archive" "$tmpdir/$archive"
	curl_download "$download_base/checksums.txt" "$tmpdir/checksums.txt"
}

verify_checksum() {
	archive=$1
	tmpdir=$2

	grep -F "  $archive" "$tmpdir/checksums.txt" >"$tmpdir/checksum.txt" || fail "missing checksum for $archive"
	(
		cd "$tmpdir"
		if command -v shasum >/dev/null 2>&1; then
			shasum -a 256 -c checksum.txt >/dev/null
		elif command -v sha256sum >/dev/null 2>&1; then
			sha256sum -c checksum.txt >/dev/null
		else
			fail "missing required command: shasum or sha256sum"
		fi
	) || fail "checksum verification failed"
}

detect_shell_rc() {
	shell_name="$(basename "${SHELL:-}")"
	case "$shell_name" in
	zsh)
		echo "$HOME/.zshrc"
		;;
	bash)
		if [ "$(uname -s)" = "Darwin" ] && [ -f "$HOME/.bash_profile" ]; then
			echo "$HOME/.bash_profile"
		else
			echo "$HOME/.bashrc"
		fi
		;;
	*)
		echo ""
		;;
	esac
}

ensure_on_path() {
	case ":${PATH}:" in
	*":${BIN_DIR}:"*) return ;;
	esac

	export_line="export PATH=\"${BIN_DIR}:\$PATH\""
	rc_file="$(detect_shell_rc)"

	if [ -z "$rc_file" ]; then
		echo "Add ${BIN_DIR} to your PATH:"
		echo "  $export_line"
		return
	fi

	if [ -f "$rc_file" ] && grep -qF "$export_line" "$rc_file"; then
		return
	fi

	printf '\n# Added by rig installer\n%s\n' "$export_line" >>"$rc_file"
	echo "Added ${BIN_DIR} to PATH in $rc_file"
	echo "Run: source $rc_file"
}

main() {
	require_cmd curl
	require_cmd tar
	require_cmd install

	goos="$(detect_goos)"
	goarch="$(detect_goarch)"
	version="$(latest_tag)"
	archive="rig_${version}_${goos}_${goarch}.tar.gz"
	tmpdir="$(mktemp -d)"
	trap 'rm -rf "$tmpdir"' EXIT INT TERM

	download_release_assets "$version" "$archive" "$tmpdir"

	verify_checksum "$archive" "$tmpdir"

	tar -xzf "$tmpdir/$archive" -C "$tmpdir"
	mkdir -p "$BIN_DIR"
	install -m 0755 "$tmpdir/rig" "$BIN_DIR/rig"

	echo "rig installed to $BIN_DIR/rig"
	ensure_on_path
	echo "Run: rig doctor"
	if [ "$goos" = "darwin" ]; then
		echo "If macOS blocks the binary, run: xattr -d com.apple.quarantine $BIN_DIR/rig"
	fi
}

main "$@"
