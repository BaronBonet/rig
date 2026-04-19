package claudehooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"rig/internal/core"
)

type HookEventIngestor interface {
	IngestHookEvent(context.Context, core.HookEventInput) (*core.HookSessionSummary, error)
}

type HTTPHandler struct {
	repo HookEventIngestor
	now  func() time.Time
}

func NewHTTPHandler(repo HookEventIngestor, now func() time.Time) *HTTPHandler {
	if now == nil {
		now = time.Now
	}

	return &HTTPHandler{
		repo: repo,
		now:  now,
	}
}

func (h *HTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

	input := DecodeHookEventInput(h.now, body)
	if _, err := h.repo.IngestHookEvent(r.Context(), input); err != nil && !errors.Is(err, core.ErrUnmanagedHookEvent) {
		http.Error(w, "ingest hook event: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

func DecodeHookEventInput(now func() time.Time, body []byte) core.HookEventInput {
	input := core.HookEventInput{
		OccurredAt:     now().UTC(),
		Provider:       "claude",
		RawPayloadJSON: string(bytes.TrimSpace(body)),
	}

	var payload hookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		input.EventName = "unknown"
		return input
	}

	input.EventName = strings.TrimSpace(payload.HookEventName)
	if input.EventName == "" {
		input.EventName = "unknown"
	}

	input.SessionID = strings.TrimSpace(payload.SessionID)
	input.Cwd = strings.TrimSpace(payload.Cwd)
	input.TranscriptPath = strings.TrimSpace(payload.TranscriptPath)
	input.ToolUseID = strings.TrimSpace(payload.ToolUseID)
	input.Model = strings.TrimSpace(payload.Model)
	input.StartSource = strings.TrimSpace(payload.Source)
	input.PromptText = strings.TrimSpace(payload.Prompt)
	input.CommandText = deriveCommandText(payload)
	input.CommandResultText = flattenToolResponse(payload.ToolResponse)

	return input
}

type hookPayload struct {
	SessionID      string          `json:"session_id"`
	HookEventName  string          `json:"hook_event_name"`
	Cwd            string          `json:"cwd"`
	TranscriptPath string          `json:"transcript_path"`
	Model          string          `json:"model"`
	Source         string          `json:"source"`
	Prompt         string          `json:"prompt"`
	ToolName       string          `json:"tool_name"`
	ToolUseID      string          `json:"tool_use_id"`
	ToolInput      json.RawMessage `json:"tool_input"`
	ToolResponse   json.RawMessage `json:"tool_response"`
}

type bashToolInput struct {
	Command string `json:"command"`
}

type editToolInput struct {
	FilePath string `json:"file_path"`
}

func deriveCommandText(payload hookPayload) string {
	if len(payload.ToolInput) == 0 {
		return ""
	}

	switch payload.ToolName {
	case "Bash":
		var input bashToolInput
		if err := json.Unmarshal(payload.ToolInput, &input); err == nil && input.Command != "" {
			return strings.TrimSpace(input.Command)
		}
	case "Edit", "Write", "Read":
		var input editToolInput
		if err := json.Unmarshal(payload.ToolInput, &input); err == nil && input.FilePath != "" {
			return payload.ToolName + " " + strings.TrimSpace(input.FilePath)
		}
	default:
		if payload.ToolName != "" {
			return payload.ToolName
		}
	}

	return ""
}

func flattenToolResponse(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return ""
	}

	var text string
	if err := json.Unmarshal(trimmed, &text); err == nil {
		return strings.TrimSpace(text)
	}

	var obj map[string]any
	if err := json.Unmarshal(trimmed, &obj); err == nil {
		if stdout, ok := obj["stdout"].(string); ok && stdout != "" {
			return strings.TrimSpace(stdout)
		}
	}

	return string(trimmed)
}
