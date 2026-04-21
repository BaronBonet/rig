package codexagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"rig/internal/core"
)

type HookEventIngestor interface {
	IngestHookEvent(context.Context, core.HookEventInput) (*core.HookSessionSummary, error)
}

type HTTPHandler struct {
	handle func(context.Context, core.HookEventInput) error
	now    func() time.Time
}

func NewHTTPHandler(repo HookEventIngestor, now func() time.Time) *HTTPHandler {
	return newHTTPHandler(now, func(ctx context.Context, input core.HookEventInput) error {
		if repo == nil {
			return fmt.Errorf("hook ingestor not configured")
		}

		_, err := repo.IngestHookEvent(ctx, input)
		return err
	})
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
		Provider:       core.AgentProviderCodex,
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
	case "Stop":
		phase = core.TaskStatusPhaseWaitingForInput
	default:
		return nil, nil
	}

	return &core.TaskStatusUpdate{
		TaskID:       taskID,
		Provider:     core.AgentProviderCodex,
		Phase:        phase,
		RawEventName: eventName,
		ObservedAt:   input.OccurredAt,
	}, nil
}

type Forwarder struct {
	CollectorURL string
	Ingestor     HookEventIngestor
	Client       *http.Client
	Now          func() time.Time
	ErrorLogPath string
}

func (f Forwarder) Forward(ctx context.Context, eventName string, body []byte) error {
	eventName = strings.TrimSpace(eventName)
	body = bytes.TrimSpace(body)

	var failures []string

	if collectorURL := strings.TrimSpace(f.CollectorURL); collectorURL != "" {
		if err := f.postToCollector(ctx, collectorURL, eventName, body); err == nil {
			return nil
		} else {
			failures = append(failures, "collector="+err.Error())
		}
	}

	if f.Ingestor != nil {
		input := DecodeHookEventInput(f.now(), eventName, body)
		if _, err := f.Ingestor.IngestHookEvent(ctx, input); err == nil || errors.Is(err, core.ErrUnmanagedHookEvent) {
			return nil
		} else {
			failures = append(failures, "ingest="+err.Error())
		}
	}

	if len(failures) == 0 {
		failures = append(failures, "no_forwarding_path_configured")
	}

	f.logFailure(eventName, strings.Join(failures, " "))
	return nil
}

func (f Forwarder) postToCollector(ctx context.Context, collectorURL string, eventName string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, collectorURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Codex-Hook-Event", eventName)

	resp, err := f.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	return nil
}

func (f Forwarder) client() *http.Client {
	if f.Client != nil {
		return f.Client
	}

	return &http.Client{Timeout: 2 * time.Second}
}

func (f Forwarder) now() func() time.Time {
	if f.Now != nil {
		return f.Now
	}

	return time.Now
}

func (f Forwarder) errorLogPath() string {
	if strings.TrimSpace(f.ErrorLogPath) != "" {
		return f.ErrorLogPath
	}

	return filepath.Join(".agent", "observability", "hook-forwarder-errors.log")
}

func (f Forwarder) logFailure(eventName string, detail string) {
	path := f.errorLogPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}

	entry := fmt.Sprintf(
		"%s event=%s detail=%s\n",
		f.now()().UTC().Format(time.RFC3339),
		strings.TrimSpace(eventName),
		strings.TrimSpace(detail),
	)

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer file.Close()

	_, _ = io.WriteString(file, entry)
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
