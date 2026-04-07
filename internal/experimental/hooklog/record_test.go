package hooklog

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewRecord_PreservesValidJSONAndExtractsFields(t *testing.T) {
	receivedAt := time.Date(2026, 4, 7, 10, 0, 0, 0, time.FixedZone("CEST", 2*60*60))
	body := []byte(`{"session_id":"sess-1","turn_id":"turn-2","hook_event_name":"PreToolUse","last_assistant_message":"done","tool_input":{"command":"go test ./..."}}`)

	record := NewRecord(receivedAt, "PreToolUse", "127.0.0.1:9000", "/hook", body)

	require.Equal(t, receivedAt.UTC(), record.ReceivedAt)
	require.Equal(t, "PreToolUse", record.EventName)
	require.Equal(t, "127.0.0.1:9000", record.RemoteAddr)
	require.Equal(t, "/hook", record.RequestPath)
	require.Empty(t, record.RawText)
	require.Empty(t, record.ParseError)
	require.Equal(t, json.RawMessage(body), record.RawPayload)
	require.Equal(t, "sess-1", record.SessionID())
	require.Equal(t, "turn-2", record.TurnID())
	require.Equal(t, "done", record.LastAssistantMessage())

	var payload map[string]any
	require.NoError(t, json.Unmarshal(record.RawPayload, &payload))
	require.Equal(t, "go test ./...", payload["tool_input"].(map[string]any)["command"])
}

func TestNewRecord_PreservesRawTextWhenBodyIsInvalidJSON(t *testing.T) {
	receivedAt := time.Date(2026, 4, 7, 10, 1, 0, 0, time.UTC)
	body := []byte(" \n\t {not-json  ")

	record := NewRecord(receivedAt, "Stop", "127.0.0.1:9000", "/hook", body)

	require.Equal(t, receivedAt.UTC(), record.ReceivedAt)
	require.Equal(t, "Stop", record.EventName)
	require.Empty(t, record.RawPayload)
	require.Equal(t, "{not-json", record.RawText)
	require.Equal(t, "invalid JSON payload", record.ParseError)
	require.Empty(t, record.SessionID())
	require.Empty(t, record.TurnID())
	require.Empty(t, record.LastAssistantMessage())
}
