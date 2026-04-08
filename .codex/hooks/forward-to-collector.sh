#!/bin/sh

collector_url=${CODEX_HOOK_COLLECTOR_URL:-http://127.0.0.1:4123/hook}
error_log=${CODEX_HOOK_ERROR_LOG:-.agent/observability/hook-forwarder-errors.log}
event_name=$1

payload_file=${TMPDIR:-/tmp}/codex-hook-payload-$$
error_file=${TMPDIR:-/tmp}/codex-hook-curl-error-$$

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
	rm -f "$payload_file" "$error_file"
}

trap cleanup EXIT HUP INT TERM

if ! cat >"$payload_file"; then
	log_failure "" "" "failed_to_read_stdin"
	exit 0
fi

status=$(curl \
	--silent \
	--show-error \
	--output /dev/null \
	--write-out '%{http_code}' \
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
