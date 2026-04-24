package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"rig/internal/core"
)

type HTTPHandler struct {
	handle func(context.Context, core.HookEventInput) error
	now    func() time.Time
}

func NewHookHTTPHandler(service core.TaskService, now func() time.Time) *HTTPHandler {
	return newHTTPHandler(now, func(ctx context.Context, input core.HookEventInput) error {
		if service == nil {
			return fmt.Errorf("task service not configured")
		}

		return service.HandleHookEvent(ctx, input)
	})
}

func newHTTPHandler(now func() time.Time, handle func(context.Context, core.HookEventInput) error) *HTTPHandler {
	if now == nil {
		now = time.Now
	}

	return &HTTPHandler{
		handle: handle,
		now:    now,
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
	if h.handle == nil {
		http.Error(w, "hook handler not configured", http.StatusInternalServerError)
		return
	}

	input := DecodeHookEventInput(h.now, r.Header.Get("X-Codex-Hook-Event"), body)
	if err := h.handle(r.Context(), input); err != nil && !errors.Is(err, core.ErrUnmanagedHookEvent) {
		http.Error(w, "handle hook event: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

func DecodeHookEventInput(now func() time.Time, headerEventName string, body []byte) core.HookEventInput {
	if now == nil {
		now = time.Now
	}

	input := core.HookEventInput{
		OccurredAt:     now().UTC(),
		EventName:      strings.TrimSpace(headerEventName),
		Provider:       core.ProviderCodex,
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

func (r *repository) HookEventToTaskStatus(input core.HookEventInput) (*core.TaskStatusUpdate, error) {
	taskID := strings.TrimSpace(input.TaskID)
	if taskID == "" {
		return nil, core.ErrUnmanagedHookEvent
	}

	eventName := strings.TrimSpace(input.EventName)
	if eventName == "" {
		return nil, core.ErrUnmanagedHookEvent
	}

	var phase core.TaskStatusPhase
	switch eventName {
	case "SessionStart":
		phase = core.TaskStatusPhaseStarting
	case "UserPromptSubmit", "PreToolUse", "PostToolUse":
		phase = core.TaskStatusPhaseWorking
	case "Stop", "PermissionRequest":
		phase = core.TaskStatusPhaseWaitingForInput
	default:
		return nil, nil
	}

	return &core.TaskStatusUpdate{
		TaskID:       taskID,
		Provider:     core.ProviderCodex,
		Phase:        phase,
		RawEventName: eventName,
		ObservedAt:   input.OccurredAt,
	}, nil
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
