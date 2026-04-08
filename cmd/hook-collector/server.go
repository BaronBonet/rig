package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"agent/internal/core"
)

type server struct {
	repo core.HookEventIngestor
	now  func() time.Time
}

func newServer(repo core.HookEventIngestor, now func() time.Time) *server {
	if now == nil {
		now = time.Now
	}

	return &server{
		repo: repo,
		now:  now,
	}
}

func (s *server) handleHook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	input := decodeHookEventInput(s.now, r.Header.Get("X-Codex-Hook-Event"), body)
	if _, err := s.repo.IngestHookEvent(r.Context(), input); err != nil && !errors.Is(err, core.ErrUnmanagedHookEvent) {
		http.Error(w, "ingest hook event: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

func decodeHookEventInput(now func() time.Time, headerEventName string, body []byte) core.HookEventInput {
	input := core.HookEventInput{
		OccurredAt:     now().UTC(),
		EventName:      strings.TrimSpace(headerEventName),
		RawPayloadJSON: string(bytes.TrimSpace(body)),
	}

	var payload hookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		if input.EventName == "" {
			input.EventName = "unknown"
		}
		return input
	}

	if input.EventName == "" {
		input.EventName = strings.TrimSpace(payload.HookEventName)
	}
	if input.EventName == "" {
		input.EventName = "unknown"
	}

	input.TaskID = strings.TrimSpace(payload.TaskID)
	input.SessionID = strings.TrimSpace(payload.SessionID)
	input.TurnID = strings.TrimSpace(payload.TurnID)
	input.ToolUseID = strings.TrimSpace(payload.ToolUseID)
	input.Model = strings.TrimSpace(payload.Model)
	input.Cwd = strings.TrimSpace(payload.Cwd)
	input.TranscriptPath = strings.TrimSpace(payload.TranscriptPath)
	input.StartSource = strings.TrimSpace(payload.Source)
	input.LastAssistantMessage = strings.TrimSpace(payload.LastAssistantMessage)
	input.PromptText = strings.TrimSpace(payload.Prompt)
	input.CommandText = strings.TrimSpace(payload.ToolInput.Command)
	input.CommandResultText = flattenPayloadText(payload.ToolResponse)
	return input
}

type hookPayload struct {
	TaskID               string          `json:"task_id"`
	SessionID            string          `json:"session_id"`
	TurnID               string          `json:"turn_id"`
	HookEventName        string          `json:"hook_event_name"`
	Prompt               string          `json:"prompt"`
	ToolUseID            string          `json:"tool_use_id"`
	Model                string          `json:"model"`
	Cwd                  string          `json:"cwd"`
	TranscriptPath       string          `json:"transcript_path"`
	Source               string          `json:"source"`
	LastAssistantMessage string          `json:"last_assistant_message"`
	ToolInput            hookToolInput   `json:"tool_input"`
	ToolResponse         json.RawMessage `json:"tool_response"`
}

type hookToolInput struct {
	Command string `json:"command"`
}

func flattenPayloadText(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return ""
	}

	var text string
	if err := json.Unmarshal(trimmed, &text); err == nil {
		return strings.TrimSpace(text)
	}

	return string(trimmed)
}
