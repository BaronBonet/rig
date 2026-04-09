# Codex Hooks Observability Spike Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build an in-repo spike that captures raw Codex hook payloads into a repo-local JSONL log and provides a small reader command so we can inspect what semantic runtime data hooks actually expose.

**Architecture:** Keep the spike outside the existing `agent` product path. A shared `internal/experimental/hooklog` package owns record creation and lightweight payload introspection, `cmd/hook-collector` owns loopback HTTP ingestion plus JSONL append, `cmd/hook-log` owns offline inspection, and repo-local `.codex/hooks.json` plus a shell forwarder wire real Codex hooks into the collector without mutating the original payload.

**Tech Stack:** Go 1.26, standard library HTTP/JSON/file I/O, `github.com/stretchr/testify`, Codex hooks, POSIX shell, `curl`

---

## File Structure

| Action | File | Responsibility |
|--------|------|----------------|
| Create | `internal/experimental/hooklog/record.go` | Define the persisted hook-log record shape, valid/invalid body handling, and lightweight field extraction helpers |
| Create | `internal/experimental/hooklog/record_test.go` | Verify record creation preserves raw payloads and handles invalid JSON safely |
| Create | `cmd/hook-collector/main.go` | Parse CLI flags and start the loopback collector server |
| Create | `cmd/hook-collector/server.go` | HTTP handler, log-file append, and record serialization |
| Create | `cmd/hook-collector/server_test.go` | Verify POST requests append JSONL records with the expected envelope |
| Create | `cmd/hook-log/main.go` | Parse CLI flags and run the log summary flow |
| Create | `cmd/hook-log/reader.go` | Read JSONL records and render a readable summary grouped by session and event type |
| Create | `cmd/hook-log/reader_test.go` | Verify JSONL parsing and summary output for mixed hook events |
| Create | `.codex/hooks.json` | Repo-local Codex hook configuration for `SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, and `Stop` |
| Create | `.codex/hooks/forward-to-collector.sh` | Read hook payload from stdin and POST it unchanged to the collector |
| Create | `scripts/observability/run-codex-with-hooks.sh` | Convenience launcher that enables the `codex_hooks` feature for local manual verification |

---

### Task 1: Add Shared Hook Log Record Parsing

**Files:**
- Create: `internal/experimental/hooklog/record.go`
- Create: `internal/experimental/hooklog/record_test.go`

- [ ] **Step 1: Write the failing tests for valid and invalid hook bodies**

Create `internal/experimental/hooklog/record_test.go`:

```go
package hooklog

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewRecord_PreservesValidJSONAndExtractsIDs(t *testing.T) {
	receivedAt := time.Date(2026, 4, 7, 10, 0, 0, 0, time.UTC)
	body := []byte(`{"session_id":"sess-1","turn_id":"turn-2","hook_event_name":"PreToolUse","tool_input":{"command":"go test ./..."}}`)

	record := NewRecord(receivedAt, "PreToolUse", "127.0.0.1:9000", "/hook", body)

	require.Equal(t, receivedAt, record.ReceivedAt)
	require.Equal(t, "PreToolUse", record.EventName)
	require.Equal(t, "127.0.0.1:9000", record.RemoteAddr)
	require.Equal(t, "/hook", record.RequestPath)
	require.Empty(t, record.ParseError)
	require.Equal(t, body, []byte(record.RawPayload))
	require.Equal(t, "sess-1", record.SessionID())
	require.Equal(t, "turn-2", record.TurnID())

	var payload map[string]any
	require.NoError(t, json.Unmarshal(record.RawPayload, &payload))
	require.Equal(t, "go test ./...", payload["tool_input"].(map[string]any)["command"])
}

func TestNewRecord_PreservesRawTextWhenBodyIsInvalidJSON(t *testing.T) {
	receivedAt := time.Date(2026, 4, 7, 10, 1, 0, 0, time.UTC)
	body := []byte("{not-json")

	record := NewRecord(receivedAt, "Stop", "127.0.0.1:9000", "/hook", body)

	require.Equal(t, receivedAt, record.ReceivedAt)
	require.Equal(t, "Stop", record.EventName)
	require.Empty(t, record.RawPayload)
	require.Equal(t, "{not-json", record.RawText)
	require.Contains(t, record.ParseError, "invalid")
	require.Empty(t, record.SessionID())
	require.Empty(t, record.TurnID())
}
```

- [ ] **Step 2: Run the new tests and verify they fail**

Run: `go test ./internal/experimental/hooklog -run 'TestNewRecord_' -count=1`

Expected: FAIL because `internal/experimental/hooklog/record.go` and `NewRecord` do not exist yet.

- [ ] **Step 3: Implement the shared record type and extraction helpers**

Create `internal/experimental/hooklog/record.go`:

```go
package hooklog

import (
	"bytes"
	"encoding/json"
	"time"
)

type Record struct {
	ReceivedAt time.Time       `json:"received_at"`
	EventName  string          `json:"event_name"`
	RemoteAddr string          `json:"remote_addr,omitempty"`
	RequestPath string         `json:"request_path,omitempty"`
	RawPayload json.RawMessage `json:"raw_payload,omitempty"`
	RawText    string          `json:"raw_text,omitempty"`
	ParseError string          `json:"parse_error,omitempty"`
}

type payloadView struct {
	SessionID            string `json:"session_id"`
	TurnID               string `json:"turn_id"`
	HookEventName        string `json:"hook_event_name"`
	LastAssistantMessage string `json:"last_assistant_message"`
}

func NewRecord(receivedAt time.Time, eventName, remoteAddr, requestPath string, body []byte) Record {
	record := Record{
		ReceivedAt: receivedAt.UTC(),
		EventName:  eventName,
		RemoteAddr: remoteAddr,
		RequestPath: requestPath,
	}

	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return record
	}

	if !json.Valid(trimmed) {
		record.RawText = string(trimmed)
		record.ParseError = "invalid JSON payload"
		return record
	}

	record.RawPayload = append(json.RawMessage(nil), trimmed...)
	return record
}

func (r Record) SessionID() string {
	var payload payloadView
	if len(r.RawPayload) == 0 || json.Unmarshal(r.RawPayload, &payload) != nil {
		return ""
	}
	return payload.SessionID
}

func (r Record) TurnID() string {
	var payload payloadView
	if len(r.RawPayload) == 0 || json.Unmarshal(r.RawPayload, &payload) != nil {
		return ""
	}
	return payload.TurnID
}

func (r Record) LastAssistantMessage() string {
	var payload payloadView
	if len(r.RawPayload) == 0 || json.Unmarshal(r.RawPayload, &payload) != nil {
		return ""
	}
	return payload.LastAssistantMessage
}
```

- [ ] **Step 4: Run the record tests and verify they pass**

Run: `go test ./internal/experimental/hooklog -run 'TestNewRecord_' -count=1`

Expected: PASS

- [ ] **Step 5: Commit the shared hooklog package**

```bash
git add internal/experimental/hooklog/record.go internal/experimental/hooklog/record_test.go
git commit -m "test: add hook log record parsing"
```

### Task 2: Add The Loopback Hook Collector

**Files:**
- Create: `cmd/hook-collector/main.go`
- Create: `cmd/hook-collector/server.go`
- Create: `cmd/hook-collector/server_test.go`

- [ ] **Step 1: Write the failing collector test**

Create `cmd/hook-collector/server_test.go`:

```go
package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestServerHandleHook_AppendsJSONLRecord(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "codex-hooks.jsonl")
	srv := newServer(logPath, func() time.Time {
		return time.Date(2026, 4, 7, 11, 0, 0, 0, time.UTC)
	})

	req := httptest.NewRequest(http.MethodPost, "/hook", strings.NewReader(`{"session_id":"sess-1","hook_event_name":"SessionStart","source":"startup"}`))
	req.Header.Set("X-Codex-Hook-Event", "SessionStart")
	rec := httptest.NewRecorder()

	srv.handleHook(rec, req)

	require.Equal(t, http.StatusAccepted, rec.Code)
	body, err := os.ReadFile(logPath)
	require.NoError(t, err)
	require.Contains(t, string(body), `"event_name":"SessionStart"`)
	require.Contains(t, string(body), `"session_id":"sess-1"`)
	require.Contains(t, string(body), `"received_at":"2026-04-07T11:00:00Z"`)
}
```

- [ ] **Step 2: Run the collector test and verify it fails**

Run: `go test ./cmd/hook-collector -run TestServerHandleHook_AppendsJSONLRecord -count=1`

Expected: FAIL because `newServer` and the collector command do not exist yet.

- [ ] **Step 3: Implement the collector server and append path**

Create `cmd/hook-collector/server.go`:

```go
package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"agent/internal/experimental/hooklog"
)

type server struct {
	logPath string
	now     func() time.Time
}

func newServer(logPath string, now func() time.Time) *server {
	if now == nil {
		now = time.Now
	}
	return &server{logPath: logPath, now: now}
}

func (s *server) handleHook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	eventName := r.Header.Get("X-Codex-Hook-Event")
	record := hooklog.NewRecord(s.now(), eventName, r.RemoteAddr, r.URL.Path, body)
	if record.EventName == "" {
		record.EventName = "unknown"
	}

	if err := appendRecord(s.logPath, record); err != nil {
		http.Error(w, "append log: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

func appendRecord(path string, record hooklog.Record) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()

	enc := json.NewEncoder(file)
	return enc.Encode(record)
}
```

Create `cmd/hook-collector/main.go`:

```go
package main

import (
	"flag"
	"log"
	"net/http"
)

func main() {
	listenAddr := flag.String("listen", "127.0.0.1:4123", "loopback listen address")
	logPath := flag.String("log-file", ".agent/observability/codex-hooks.jsonl", "JSONL output path")
	flag.Parse()

	srv := newServer(*logPath, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("/hook", srv.handleHook)

	log.Printf("hook-collector listening on http://%s/hook", *listenAddr)
	log.Fatal(http.ListenAndServe(*listenAddr, mux))
}
```

- [ ] **Step 4: Expand the test to include invalid JSON preservation**

Update `cmd/hook-collector/server_test.go` to import `strings`, then add:

```go
func TestServerHandleHook_PreservesInvalidJSONAsRawText(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "codex-hooks.jsonl")
	srv := newServer(logPath, func() time.Time {
		return time.Date(2026, 4, 7, 11, 1, 0, 0, time.UTC)
	})

	req := httptest.NewRequest(http.MethodPost, "/hook", strings.NewReader("{not-json"))
	req.Header.Set("X-Codex-Hook-Event", "Stop")
	rec := httptest.NewRecorder()

	srv.handleHook(rec, req)

	require.Equal(t, http.StatusAccepted, rec.Code)
	body, err := os.ReadFile(logPath)
	require.NoError(t, err)
	require.Contains(t, string(body), `"event_name":"Stop"`)
	require.Contains(t, string(body), `"raw_text":"{not-json"`)
	require.Contains(t, string(body), `"parse_error":"invalid JSON payload"`)
}
```

- [ ] **Step 5: Run the collector tests and verify they pass**

Run: `go test ./cmd/hook-collector -count=1`

Expected: PASS

- [ ] **Step 6: Commit the collector command**

```bash
git add cmd/hook-collector/main.go cmd/hook-collector/server.go cmd/hook-collector/server_test.go internal/experimental/hooklog/record.go
git commit -m "feat: add codex hook collector spike"
```

### Task 3: Add The Offline Hook Log Reader

**Files:**
- Create: `cmd/hook-log/main.go`
- Create: `cmd/hook-log/reader.go`
- Create: `cmd/hook-log/reader_test.go`

- [ ] **Step 1: Write the failing reader test**

Create `cmd/hook-log/reader_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRenderSummary_GroupsEventsBySessionAndIncludesAssistantMessage(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "codex-hooks.jsonl")
	require.NoError(t, os.WriteFile(logPath, []byte(
		`{"received_at":"2026-04-07T12:00:00Z","event_name":"SessionStart","raw_payload":{"session_id":"sess-1","hook_event_name":"SessionStart","source":"startup"}}`+"\n"+
			`{"received_at":"2026-04-07T12:00:01Z","event_name":"UserPromptSubmit","raw_payload":{"session_id":"sess-1","turn_id":"turn-1","hook_event_name":"UserPromptSubmit","prompt":"fix the bug"}}`+"\n"+
			`{"received_at":"2026-04-07T12:00:02Z","event_name":"Stop","raw_payload":{"session_id":"sess-1","turn_id":"turn-1","hook_event_name":"Stop","last_assistant_message":"I fixed the bug."}}`+"\n",
	), 0o644))

	summary, err := renderSummary(logPath)
	require.NoError(t, err)
	require.Contains(t, summary, "session sess-1")
	require.Contains(t, summary, "SessionStart: 1")
	require.Contains(t, summary, "UserPromptSubmit: 1")
	require.Contains(t, summary, "Stop: 1")
	require.Contains(t, summary, "last assistant message: I fixed the bug.")
}
```

- [ ] **Step 2: Run the reader test and verify it fails**

Run: `go test ./cmd/hook-log -run TestRenderSummary_GroupsEventsBySessionAndIncludesAssistantMessage -count=1`

Expected: FAIL because the reader command does not exist yet.

- [ ] **Step 3: Implement JSONL loading and summary rendering**

Create `cmd/hook-log/reader.go`:

```go
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"agent/internal/experimental/hooklog"
)

func renderSummary(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	var records []hooklog.Record
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var record hooklog.Record
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return "", err
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}

	sessionEvents := map[string]map[string]int{}
	lastAssistant := map[string]string{}
	for _, record := range records {
		sessionID := record.SessionID()
		if sessionID == "" {
			sessionID = "(unknown session)"
		}
		if sessionEvents[sessionID] == nil {
			sessionEvents[sessionID] = map[string]int{}
		}
		sessionEvents[sessionID][record.EventName]++
		if msg := strings.TrimSpace(record.LastAssistantMessage()); msg != "" {
			lastAssistant[sessionID] = msg
		}
	}

	var sessions []string
	for sessionID := range sessionEvents {
		sessions = append(sessions, sessionID)
	}
	sort.Strings(sessions)

	var b strings.Builder
	for _, sessionID := range sessions {
		fmt.Fprintf(&b, "session %s\n", sessionID)
		var events []string
		for eventName := range sessionEvents[sessionID] {
			events = append(events, eventName)
		}
		sort.Strings(events)
		for _, eventName := range events {
			fmt.Fprintf(&b, "  %s: %d\n", eventName, sessionEvents[sessionID][eventName])
		}
		if msg := lastAssistant[sessionID]; msg != "" {
			fmt.Fprintf(&b, "  last assistant message: %s\n", msg)
		}
	}

	return strings.TrimRight(b.String(), "\n"), nil
}
```

Create `cmd/hook-log/main.go`:

```go
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	logPath := flag.String("log-file", ".agent/observability/codex-hooks.jsonl", "JSONL input path")
	flag.Parse()

	summary, err := renderSummary(*logPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	fmt.Fprintln(os.Stdout, summary)
}
```

- [ ] **Step 4: Run the reader tests and verify they pass**

Run: `go test ./cmd/hook-log -count=1`

Expected: PASS

- [ ] **Step 5: Commit the reader command**

```bash
git add cmd/hook-log/main.go cmd/hook-log/reader.go cmd/hook-log/reader_test.go
git commit -m "feat: add codex hook log reader"
```

### Task 4: Add Repo-Local Hook Wiring And Manual Verification

**Files:**
- Create: `.codex/hooks.json`
- Create: `.codex/hooks/forward-to-collector.sh`
- Create: `scripts/observability/run-codex-with-hooks.sh`

- [ ] **Step 1: Add the repo-local hook forwarder script**

Create `.codex/hooks/forward-to-collector.sh`:

```sh
#!/bin/sh
set -eu

event_name="${1:-unknown}"
collector_url="${CODEX_HOOK_COLLECTOR_URL:-http://127.0.0.1:4123/hook}"
error_log="${CODEX_HOOK_ERROR_LOG:-.agent/observability/hook-forwarder-errors.log}"

payload="$(cat)"

if printf '%s' "$payload" | curl -fsS \
	-X POST \
	-H "Content-Type: application/json" \
	-H "X-Codex-Hook-Event: ${event_name}" \
	--data-binary @- \
	"$collector_url" >/dev/null 2>&1; then
	exit 0
fi

mkdir -p "$(dirname "$error_log")"
printf '%s hook=%s url=%s\n' "$(date -u +%FT%TZ)" "$event_name" "$collector_url" >>"$error_log"
exit 0
```

- [ ] **Step 2: Add repo-local Codex hook configuration**

Create `.codex/hooks.json` using the official hook config shape from the Codex hooks docs:

```json
{
  "hooks": {
    "SessionStart": [
      {
        "matcher": "startup|resume",
        "hooks": [
          {
            "type": "command",
            "command": "/bin/sh -lc '/bin/sh \"$(git rev-parse --show-toplevel)/.codex/hooks/forward-to-collector.sh\" SessionStart'"
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
            "command": "/bin/sh -lc '/bin/sh \"$(git rev-parse --show-toplevel)/.codex/hooks/forward-to-collector.sh\" PreToolUse'"
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
            "command": "/bin/sh -lc '/bin/sh \"$(git rev-parse --show-toplevel)/.codex/hooks/forward-to-collector.sh\" PostToolUse'"
          }
        ]
      }
    ],
    "UserPromptSubmit": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "/bin/sh -lc '/bin/sh \"$(git rev-parse --show-toplevel)/.codex/hooks/forward-to-collector.sh\" UserPromptSubmit'"
          }
        ]
      }
    ],
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "/bin/sh -lc '/bin/sh \"$(git rev-parse --show-toplevel)/.codex/hooks/forward-to-collector.sh\" Stop'"
          }
        ]
      }
    ]
  }
}
```

- [ ] **Step 3: Add a convenience launcher for local manual verification**

Create `scripts/observability/run-codex-with-hooks.sh`:

```sh
#!/bin/sh
set -eu
exec codex --enable codex_hooks "$@"
```

- [ ] **Step 4: Validate the shell and config files before manual testing**

Run:

```bash
/bin/sh -n .codex/hooks/forward-to-collector.sh
/bin/sh -n scripts/observability/run-codex-with-hooks.sh
python3 -m json.tool .codex/hooks.json >/dev/null
```

Expected: no output and exit status `0`.

- [ ] **Step 5: Run the full spike verification flow**

Run the collector in one terminal:

```bash
go run ./cmd/hook-collector
```

Run Codex with hooks in another terminal from this repo:

```bash
/bin/sh scripts/observability/run-codex-with-hooks.sh
```

Inside Codex:

- submit at least one prompt
- trigger at least one Bash tool use
- exit the session

Then inspect the log:

```bash
go run ./cmd/hook-log
```

Expected: the summary includes at least one `SessionStart`, one `UserPromptSubmit`, one `PreToolUse` or `PostToolUse`, and one `Stop`, grouped under a stable session id. The JSONL file at `.agent/observability/codex-hooks.jsonl` should preserve the original hook payloads for deeper inspection.

- [ ] **Step 6: Commit the spike wiring**

```bash
git add .codex/hooks.json .codex/hooks/forward-to-collector.sh scripts/observability/run-codex-with-hooks.sh
git commit -m "chore: wire codex hooks observability spike"
```
