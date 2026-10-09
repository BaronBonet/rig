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
	"slices"
	"strings"
	"time"

	"github.com/BaronBonet/rig/internal/adapters/client/providerkit"
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

func NewHookHTTPHandler(service core.HookEventHandler, now func() time.Time, hookSecret string) *HTTPHandler {
	return newHTTPHandler(now, hookSecret, func(ctx context.Context, input core.HookEventInput) error {
		if service == nil {
			return fmt.Errorf("hook event handler not configured")
		}

		return service.HandleHookEvent(ctx, input)
	})
}

func NewHookRoutes(
	service core.HookEventHandler,
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
	input.TmuxPane, input.TmuxServer = providerkit.DecodeTmuxHeaders(r.Header)
	input.HookPID = providerkit.DecodeHookPID(r.Header)
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
	input.EndReason = strings.TrimSpace(payload.Reason)
	input.PromptText = userPromptText(payload.Prompt)
	input.CommandText = strings.TrimSpace(payload.ToolInput.Command)
	input.CommandResultText = flattenPayloadText(payload.ToolResponse)
	input.ToolUseID = strings.TrimSpace(payload.ToolUseID)
	input.AgentID = strings.TrimSpace(payload.AgentID)
	input.AgentType = strings.TrimSpace(payload.AgentType)
	input.NotificationType = strings.TrimSpace(payload.NotificationType)
	input.BackgroundWork = backgroundWorkFromTasks(payload.BackgroundTasks)
	return input
}

func (r *repository) HookEventToTaskStatus(input core.HookEventInput) (*core.TaskStatusUpdate, error) {
	eventName := strings.TrimSpace(input.EventName)
	if eventName == core.HookEventNotification && !notificationNeedsInput(input.NotificationType) {
		return nil, nil
	}
	// Claude includes agent_id only on hooks fired from inside a subagent.
	// Subagent work reaches the task as background work on the root agent's
	// Stop, but a notification asking the user to act drives needs-input
	// wherever it fires.
	if strings.TrimSpace(input.AgentID) != "" && eventName != core.HookEventNotification {
		return nil, nil
	}

	update, err := hookCatalog.StatusUpdate(core.ProviderClaude, input)
	if err != nil || update == nil {
		return update, err
	}
	if eventName == core.HookEventStop && !input.BackgroundWork.IsZero() {
		update.Phase = core.TaskStatusPhaseWorkingInBackground
		update.BackgroundWork = input.BackgroundWork
	}
	return update, nil
}

// systemPromptPrefixes start the prompts of turns Claude Code starts on its
// own, which are none of the user's:
//   - <task-notification>: background work reports back, such as a subagent
//     or shell finishing or a monitor emitting an event.
//   - <agent-message : a background subagent sends a message, such as its
//     hand-back.
//   - <cross-session-message : another Claude session sends a message.
var systemPromptPrefixes = []string{
	"<task-notification>",
	"<agent-message ",
	"<cross-session-message ",
}

// userPromptText is the user's prompt that a UserPromptSubmit payload's
// prompt carries. A turn Claude Code starts on its own has none: Claude Code
// fires the hook for it too, but nothing marks the payload as its own except
// the prompt's opening tag. The hook still drives the agent's status.
func userPromptText(prompt string) string {
	prompt = strings.TrimSpace(prompt)
	for _, prefix := range systemPromptPrefixes {
		if strings.HasPrefix(prompt, prefix) {
			return ""
		}
	}
	return prompt
}

func notificationNeedsInput(notificationType string) bool {
	// Payloads without a type predate typed notifications; keep treating them
	// as needs-input.
	if notificationType == "" {
		return true
	}
	return slices.Contains(needsInputNotificationTypes, notificationType)
}

// backgroundWorkFromTasks counts the in-flight work a Stop payload reports.
// Every counted kind wakes the root agent with a new turn when it completes,
// so the next Stop replaces these counts. Claude's own housekeeping tasks
// (dream, auto-mode scan, memory import) finish without a new turn, so they
// are not counted and cannot pin a task at working.
func backgroundWorkFromTasks(tasks []hookBackgroundTask) core.TaskBackgroundWork {
	var work core.TaskBackgroundWork
	for _, task := range tasks {
		if status := strings.TrimSpace(task.Status); status != "running" && status != "pending" {
			continue
		}
		switch strings.TrimSpace(task.Type) {
		case "subagent":
			work.Subagents++
		// Monitor tool watches run as background shells and are reported as
		// such; "monitor" covers MCP and websocket monitors.
		case "shell":
			work.Shells++
		case "monitor":
			work.Monitors++
		case "workflow":
			work.Workflows++
		case "teammate", "cloud session", "MCP task":
			work.Other++
		}
	}
	return work
}

type hookPayload struct {
	SessionID        string               `json:"session_id"`
	HookEventName    string               `json:"hook_event_name"`
	Prompt           string               `json:"prompt"`
	ToolUseID        string               `json:"tool_use_id"`
	AgentID          string               `json:"agent_id"`
	AgentType        string               `json:"agent_type"`
	Model            string               `json:"model"`
	Cwd              string               `json:"cwd"`
	TranscriptPath   string               `json:"transcript_path"`
	Source           string               `json:"source"`
	Reason           string               `json:"reason"`
	Message          string               `json:"message"`
	NotificationType string               `json:"notification_type"`
	ToolInput        hookToolInput        `json:"tool_input"`
	ToolResponse     json.RawMessage      `json:"tool_response"`
	BackgroundTasks  []hookBackgroundTask `json:"background_tasks"`
}

type hookToolInput struct {
	Command string `json:"command"`
}

// hookBackgroundTask is one entry of a Stop payload's background_tasks: the
// work registered in the session that is still in flight at turn end.
type hookBackgroundTask struct {
	Type   string `json:"type"`
	Status string `json:"status"`
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
