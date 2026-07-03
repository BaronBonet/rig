package claude

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/BaronBonet/rig/internal/core"
)

type HTTPHandler struct {
	handle     func(context.Context, core.HookEventInput) error
	hookSecret string
	now        func() time.Time
}

const (
	// #nosec G101 -- This is the HTTP header name, not credential material.
	hookSecretHeader        = "X-Rig-Hook-Secret"
	hookEventHeader         = "X-Claude-Hook-Event"
	maxHookRequestBodyBytes = 1024 * 1024
)

func NewHookHTTPHandler(service core.TaskService, now func() time.Time, hookSecret string) *HTTPHandler {
	return newHTTPHandler(now, hookSecret, func(ctx context.Context, input core.HookEventInput) error {
		if service == nil {
			return fmt.Errorf("task service not configured")
		}

		return service.HandleHookEvent(ctx, input)
	})
}

func NewHookRoutes(
	service core.TaskService,
	now func() time.Time,
	hookSecret string,
) []core.TaskDaemonHookRoute {
	handler := NewHookHTTPHandler(service, now, hookSecret)
	return []core.TaskDaemonHookRoute{
		{Path: claudeHookPath, Handler: handler},
	}
}

func newHTTPHandler(
	now func() time.Time,
	hookSecret string,
	handle func(context.Context, core.HookEventInput) error,
) *HTTPHandler {
	if now == nil {
		now = time.Now
	}

	return &HTTPHandler{
		handle:     handle,
		hookSecret: strings.TrimSpace(hookSecret),
		now:        now,
	}
}

func (h *HTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !h.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxHookRequestBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if h.handle == nil {
		http.Error(w, "hook handler not configured", http.StatusInternalServerError)
		return
	}

	input := DecodeHookEventInput(h.now, r.Header.Get(hookEventHeader), body)
	if err := h.handle(r.Context(), input); err != nil && !errors.Is(err, core.ErrUnmanagedHookEvent) {
		http.Error(w, "handle hook event: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

func (h *HTTPHandler) authorized(r *http.Request) bool {
	if h.hookSecret == "" {
		return true
	}

	return subtle.ConstantTimeCompare([]byte(r.Header.Get(hookSecretHeader)), []byte(h.hookSecret)) == 1
}

// DecodeHookEventInput normalizes a Claude Code hook payload into Rig's hook
// event input. Claude payloads carry no task ID; the task is resolved from the
// hook's working directory by the task service.
func DecodeHookEventInput(now func() time.Time, headerEventName string, body []byte) core.HookEventInput {
	if now == nil {
		now = time.Now
	}

	input := core.HookEventInput{
		OccurredAt:     now().UTC(),
		EventName:      strings.TrimSpace(headerEventName),
		Provider:       core.ProviderClaude,
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

	input.SessionID = strings.TrimSpace(payload.SessionID)
	input.Model = strings.TrimSpace(payload.Model)
	input.Cwd = strings.TrimSpace(payload.Cwd)
	input.TranscriptPath = strings.TrimSpace(payload.TranscriptPath)
	input.StartSource = strings.TrimSpace(payload.Source)
	input.PromptText = strings.TrimSpace(payload.Prompt)
	input.CommandText = strings.TrimSpace(payload.ToolInput.Command)
	input.CommandResultText = flattenPayloadText(payload.ToolResponse)
	input.ToolUseID = strings.TrimSpace(payload.ToolUseID)
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
	case "Stop", "Notification":
		phase = core.TaskStatusPhaseWaitingForInput
	default:
		return nil, nil
	}

	return &core.TaskStatusUpdate{
		TaskID:       taskID,
		Provider:     core.ProviderClaude,
		Phase:        phase,
		RawEventName: eventName,
		ObservedAt:   input.OccurredAt,
	}, nil
}

type hookPayload struct {
	SessionID      string          `json:"session_id"`
	HookEventName  string          `json:"hook_event_name"`
	Prompt         string          `json:"prompt"`
	ToolUseID      string          `json:"tool_use_id"`
	Model          string          `json:"model"`
	Cwd            string          `json:"cwd"`
	TranscriptPath string          `json:"transcript_path"`
	Source         string          `json:"source"`
	Message        string          `json:"message"`
	ToolInput      hookToolInput   `json:"tool_input"`
	ToolResponse   json.RawMessage `json:"tool_response"`
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
