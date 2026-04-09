package codexhooks

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"agent/internal/core"
)

type Bootstrapper struct {
	sqlitePath   string
	collectorURL string
	agentExec    string
	sourceRoot   string
}

func NewBootstrapper(sqlitePath string, collectorURL string, agentExec string, sourceRoot string) *Bootstrapper {
	return &Bootstrapper{
		sqlitePath:   strings.TrimSpace(sqlitePath),
		collectorURL: strings.TrimSpace(collectorURL),
		agentExec:    strings.TrimSpace(agentExec),
		sourceRoot:   strings.TrimSpace(sourceRoot),
	}
}

func (b *Bootstrapper) BootstrapTaskWorkspace(_ context.Context, task *core.Task) error {
	if task == nil || task.Provider != "codex" || strings.TrimSpace(task.WorktreePath) == "" {
		return nil
	}

	hooksDir := filepath.Join(task.WorktreePath, ".codex", "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return err
	}

	hooksPath := filepath.Join(task.WorktreePath, ".codex", "hooks.json")
	if err := os.WriteFile(hooksPath, []byte(renderHooksJSON()), 0o644); err != nil {
		return err
	}

	scriptPath := filepath.Join(hooksDir, "forward-to-collector.sh")
	if err := os.WriteFile(scriptPath, []byte(renderForwarderScript(b.sqlitePath, b.collectorURL, b.agentExec, b.sourceRoot)), 0o755); err != nil {
		return err
	}

	return nil
}

func renderHooksJSON() string {
	return `{
  "hooks": {
    "SessionStart": [
      {
        "matcher": "startup|resume",
        "hooks": [
          {
            "type": "command",
            "command": "/bin/sh -c 'repo_root=$(git rev-parse --show-toplevel 2>/dev/null) || exit 0; exec /bin/sh \"$repo_root/.codex/hooks/forward-to-collector.sh\" SessionStart'"
          }
        ]
      }
    ],
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "/bin/sh -c 'repo_root=$(git rev-parse --show-toplevel 2>/dev/null) || exit 0; exec /bin/sh \"$repo_root/.codex/hooks/forward-to-collector.sh\" PreToolUse'"
          }
        ]
      }
    ],
    "PostToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "/bin/sh -c 'repo_root=$(git rev-parse --show-toplevel 2>/dev/null) || exit 0; exec /bin/sh \"$repo_root/.codex/hooks/forward-to-collector.sh\" PostToolUse'"
          }
        ]
      }
    ],
    "UserPromptSubmit": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "/bin/sh -c 'repo_root=$(git rev-parse --show-toplevel 2>/dev/null) || exit 0; exec /bin/sh \"$repo_root/.codex/hooks/forward-to-collector.sh\" UserPromptSubmit'"
          }
        ]
      }
    ],
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "/bin/sh -c 'repo_root=$(git rev-parse --show-toplevel 2>/dev/null) || exit 0; exec /bin/sh \"$repo_root/.codex/hooks/forward-to-collector.sh\" Stop'"
          }
        ]
      }
    ]
  }
}
`
}

func renderForwarderScript(sqlitePath string, collectorURL string, agentExec string, sourceRoot string) string {
	return fmt.Sprintf(`#!/bin/sh

umask 077

collector_url=%s
sqlite_path=%s
agent_exec=%s
agent_source_root=%s
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
		printf '%%s event=%%s target=%%s' "$(date -u '+%%Y-%%m-%%dT%%H:%%M:%%SZ')" "$event_name" "$1"
		if [ -n "$2" ]; then
			printf ' detail=%%s' "$2"
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

payload_file=$(mktemp "${TMPDIR:-/tmp}/codex-hook-payload.XXXXXX") || exit 0
error_file=$(mktemp "${TMPDIR:-/tmp}/codex-hook-error.XXXXXX") || exit 0

if ! cat >"$payload_file"; then
	log_failure "stdin" "failed_to_read_payload"
	exit 0
fi

if [ -n "$collector_url" ]; then
	status=$(curl \
		--silent \
		--show-error \
		--output /dev/null \
		--write-out '%%{http_code}' \
		--connect-timeout 1 \
		--max-time 2 \
		--header 'Content-Type: application/json' \
		--header "X-Codex-Hook-Event: $event_name" \
		--data-binary @"$payload_file" \
		"$collector_url" 2>"$error_file")
	curl_exit=$?
	case "$status" in
		2??) exit 0 ;;
	esac
fi

if command -v agent >/dev/null 2>&1; then
	if AGENT_SQLITE_PATH="$sqlite_path" agent hook-ingest "$event_name" <"$payload_file" >/dev/null 2>"$error_file"; then
		exit 0
	fi
fi

if [ -n "$agent_exec" ] && [ -x "$agent_exec" ]; then
	if AGENT_SQLITE_PATH="$sqlite_path" "$agent_exec" hook-ingest "$event_name" <"$payload_file" >/dev/null 2>"$error_file"; then
		exit 0
	fi
fi

if command -v go >/dev/null 2>&1 && [ -n "$agent_source_root" ] && [ -f "$agent_source_root/go.mod" ]; then
	if (
		cd "$agent_source_root" &&
		AGENT_SQLITE_PATH="$sqlite_path" go run ./cmd/agent hook-ingest "$event_name" <"$payload_file"
	) >/dev/null 2>"$error_file"; then
		exit 0
	fi
fi

log_failure "ingest" "$(cat "$error_file" 2>/dev/null)"
exit 0
`, shellQuote(collectorURL), shellQuote(sqlitePath), shellQuote(agentExec), shellQuote(sourceRoot))
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}

	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
