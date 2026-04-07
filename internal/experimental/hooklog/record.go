package hooklog

import (
	"bytes"
	"encoding/json"
	"time"
)

type Record struct {
	ReceivedAt  time.Time       `json:"received_at"`
	EventName   string          `json:"event_name"`
	RemoteAddr  string          `json:"remote_addr,omitempty"`
	RequestPath string          `json:"request_path,omitempty"`
	RawPayload  json.RawMessage `json:"raw_payload,omitempty"`
	RawText     string          `json:"raw_text,omitempty"`
	ParseError  string          `json:"parse_error,omitempty"`
}

type payloadView struct {
	SessionID            string `json:"session_id"`
	TurnID               string `json:"turn_id"`
	LastAssistantMessage string `json:"last_assistant_message"`
}

func NewRecord(receivedAt time.Time, eventName, remoteAddr, requestPath string, body []byte) Record {
	record := Record{
		ReceivedAt:  receivedAt.UTC(),
		EventName:   eventName,
		RemoteAddr:  remoteAddr,
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

	record.RawPayload = append(json.RawMessage(nil), body...)
	return record
}

func (r Record) SessionID() string {
	var payload payloadView
	if !r.unmarshalPayload(&payload) {
		return ""
	}
	return payload.SessionID
}

func (r Record) TurnID() string {
	var payload payloadView
	if !r.unmarshalPayload(&payload) {
		return ""
	}
	return payload.TurnID
}

func (r Record) LastAssistantMessage() string {
	var payload payloadView
	if !r.unmarshalPayload(&payload) {
		return ""
	}
	return payload.LastAssistantMessage
}

func (r Record) unmarshalPayload(dst any) bool {
	if len(r.RawPayload) == 0 {
		return false
	}
	return json.Unmarshal(r.RawPayload, dst) == nil
}
