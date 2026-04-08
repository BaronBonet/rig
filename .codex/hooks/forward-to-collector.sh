#!/bin/sh

umask 077

collector_url=${CODEX_HOOK_COLLECTOR_URL:-http://127.0.0.1:4123/hook}
event_name=$1

script_dir=$(CDPATH= cd -- "$(dirname "$0")" 2>/dev/null && pwd -P)
repo_root=
if [ -n "$script_dir" ]; then
	repo_root=$(CDPATH= cd -- "$script_dir/../.." 2>/dev/null && pwd -P)
fi

default_error_log=.agent/observability/hook-forwarder-errors.log
if [ -n "$repo_root" ]; then
	default_error_log=$repo_root/.agent/observability/hook-forwarder-errors.log
fi
error_log=${CODEX_HOOK_ERROR_LOG:-$default_error_log}

payload_file=
error_file=

log_failure() {
	error_dir=$(dirname "$error_log")
	if [ ! -d "$error_dir" ]; then
		mkdir -p "$error_dir" 2>/dev/null || return 0
	fi

	{
		printf '%s event=%s url=%s' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" "$event_name" "$collector_url"
		if [ -n "$1" ]; then
			printf ' status=%s' "$1"
		fi
		if [ -n "$2" ]; then
			printf ' curl_exit=%s' "$2"
		fi
		if [ -n "$3" ]; then
			printf ' error=%s' "$3"
		fi
		printf '\n'
	} >>"$error_log" 2>/dev/null || true
}

cleanup() {
	if [ -n "$payload_file" ]; then
		rm -f "$payload_file"
	fi
	if [ -n "$error_file" ]; then
		rm -f "$error_file"
	fi
}

trap cleanup EXIT HUP INT TERM

payload_file=$(mktemp "${TMPDIR:-/tmp}/codex-hook-payload.XXXXXX") || {
	log_failure "" "" "failed_to_create_payload_tempfile"
	exit 0
}

error_file=$(mktemp "${TMPDIR:-/tmp}/codex-hook-curl-error.XXXXXX") || {
	log_failure "" "" "failed_to_create_error_tempfile"
	exit 0
}

if ! cat >"$payload_file"; then
	log_failure "" "" "failed_to_read_stdin"
	exit 0
fi

status=$(curl \
	--silent \
	--show-error \
	--output /dev/null \
	--write-out '%{http_code}' \
	--connect-timeout 1 \
	--max-time 2 \
	--header 'Content-Type: application/json' \
	--header "X-Codex-Hook-Event: $event_name" \
	--data-binary @"$payload_file" \
	"$collector_url" 2>"$error_file")
curl_exit=$?

case "$status" in
	2??) ;;
	*)
		error_message=$(cat "$error_file" 2>/dev/null)
		if [ "$curl_exit" -eq 0 ]; then
			log_failure "$status" "" "$error_message"
		else
			log_failure "$status" "$curl_exit" "$error_message"
		fi
		;;
esac

exit 0
