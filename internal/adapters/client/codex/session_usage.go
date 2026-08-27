package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/BaronBonet/rig/internal/core"
)

func (r *repository) ReadSessionTokenUsage(
	ctx context.Context,
	transcriptPath string,
) (*core.SessionTokenUsage, error) {
	snapshot, err := r.getTranscriptIndex().read(ctx, transcriptPath)
	return snapshot.usage, err
}

type codexTranscriptEnvelope struct {
	Timestamp time.Time       `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type codexEventPayload struct {
	Type string `json:"type"`
	Info struct {
		TotalTokenUsage struct {
			InputTokens              int `json:"input_tokens"`
			CachedInputTokens        int `json:"cached_input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			ReasoningOutputTokens    int `json:"reasoning_output_tokens"`
			TotalTokens              int `json:"total_tokens"`
		} `json:"total_token_usage"`
	} `json:"info"`
}

type codexTaskCompletePayload struct {
	Arguments string `json:"arguments"`
	Message   string `json:"message"`
	Phase     string `json:"phase"`
	Type      string `json:"type"`
}

type codexFunctionCallArguments struct {
	Command string `json:"cmd"`
}

type codexTranscriptStatus struct {
	observedAt   time.Time
	rawEventName string
	phase        core.TaskStatusPhase
}

type codexTranscriptKind uint8

const (
	codexTranscriptKindUnknown codexTranscriptKind = iota
	codexTranscriptKindRoot
	codexTranscriptKindSubagent
)

const maxCodexTranscriptKindCacheEntries = 4096

func (r *repository) RecoverLatestTaskStatus(
	ctx context.Context,
	current core.TaskStatusUpdate,
	sessions []core.TaskProviderSession,
) (*core.TaskStatusUpdate, error) {
	// Transcript recovery repairs stale in-progress status when a terminal hook
	// was missed. It must not replace explicit needs-input hook evidence with
	// transcript activity: Codex writes activity records around turn completion,
	// and treating those as resumed work makes the live TUI fall back to working.
	// A real resumed turn emits its own working hook and updates the persisted
	// status directly.
	if current.Phase == core.TaskStatusPhaseStopped ||
		current.Phase == core.TaskStatusPhaseWaitingForInput {
		return nil, nil
	}

	session, err := r.newestRootCodexTranscriptSession(ctx, sessions)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, nil
	}

	snapshot, err := r.getTranscriptIndex().read(ctx, session.TranscriptPath)
	if err != nil {
		return nil, err
	}
	status := snapshot.status
	if status == nil || !status.observedAt.After(current.ObservedAt) {
		return nil, nil
	}
	return &core.TaskStatusUpdate{
		TaskID:       current.TaskID,
		Provider:     current.Provider,
		Phase:        status.phase,
		RawEventName: status.rawEventName,
		ObservedAt:   status.observedAt,
	}, nil
}

func (r *repository) ReadSessionActivity(
	ctx context.Context,
	session core.TaskProviderSession,
	after time.Time,
) ([]core.TaskActivityEvent, error) {
	taskID := strings.TrimSpace(session.TaskID)
	if taskID == "" {
		return nil, nil
	}
	snapshot, err := r.getTranscriptIndex().read(ctx, session.TranscriptPath)
	if err != nil {
		return nil, err
	}
	events := make([]core.TaskActivityEvent, 0, len(snapshot.activities))
	for _, activity := range snapshot.activities {
		if !activity.ObservedAt.After(after) {
			continue
		}
		activity.TaskID = taskID
		events = append(events, activity)
	}
	return events, nil
}

func newestCodexTranscriptSession(sessions []core.TaskProviderSession) *core.TaskProviderSession {
	var latest *core.TaskProviderSession
	for _, session := range sessions {
		transcriptPath := strings.TrimSpace(session.TranscriptPath)
		if session.Provider != core.ProviderCodex || transcriptPath == "" {
			continue
		}

		session.TranscriptPath = transcriptPath
		if latest == nil || session.LastObservedAt.After(latest.LastObservedAt) {
			copy := session
			latest = &copy
		}
	}
	return latest
}

func (r *repository) newestRootCodexTranscriptSession(
	ctx context.Context,
	sessions []core.TaskProviderSession,
) (*core.TaskProviderSession, error) {
	latest := newestCodexTranscriptSession(sessions)
	if latest == nil {
		return nil, nil
	}

	// Subagent hooks carry the root session ID but point at the subagent's own
	// transcript. Prefer the transcript that received SessionStart for the
	// newest logical session so a subagent's task_complete cannot make the root
	// task appear to need input while it is still working.
	latestSessionID := strings.TrimSpace(latest.ProviderSessionID)
	if latestSessionID == "" {
		return latest, nil
	}

	var latestRoot *core.TaskProviderSession
	var candidates []core.TaskProviderSession
	for _, session := range sessions {
		transcriptPath := strings.TrimSpace(session.TranscriptPath)
		if session.Provider != core.ProviderCodex ||
			strings.TrimSpace(session.ProviderSessionID) != latestSessionID ||
			transcriptPath == "" {
			continue
		}

		session.TranscriptPath = transcriptPath
		candidates = append(candidates, session)
		if strings.TrimSpace(session.StartSource) == "" {
			continue
		}
		if latestRoot == nil || session.LastObservedAt.After(latestRoot.LastObservedAt) {
			copy := session
			latestRoot = &copy
		}
	}
	if latestRoot != nil {
		return latestRoot, nil
	}

	// SessionStart can be missed while the daemon is unavailable. In that case,
	// recover root/subagent provenance from the first session_meta record. Read
	// only the transcript prefix here: Ultra-mode rollouts can contain large,
	// copied histories, and status recovery runs every two seconds.
	var latestUnknown *core.TaskProviderSession
	var firstErr error
	for _, session := range candidates {
		kind, readErr := r.readCodexTranscriptKind(ctx, session.TranscriptPath)
		if readErr != nil {
			if firstErr == nil {
				firstErr = readErr
			}
			continue
		}

		switch kind {
		case codexTranscriptKindRoot:
			if latestRoot == nil || session.LastObservedAt.After(latestRoot.LastObservedAt) {
				copy := session
				latestRoot = &copy
			}
		case codexTranscriptKindUnknown:
			if latestUnknown == nil || session.LastObservedAt.After(latestUnknown.LastObservedAt) {
				copy := session
				latestUnknown = &copy
			}
		case codexTranscriptKindSubagent:
		}
	}
	if latestRoot != nil {
		return latestRoot, nil
	}
	if latestUnknown != nil {
		return latestUnknown, nil
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return nil, nil
}

func (r *repository) readCodexTranscriptKind(
	ctx context.Context,
	transcriptPath string,
) (codexTranscriptKind, error) {
	transcriptPath = strings.TrimSpace(transcriptPath)
	if transcriptPath == "" {
		return codexTranscriptKindUnknown, nil
	}
	if kind, ok := r.cachedCodexTranscriptKind(transcriptPath); ok {
		return kind, nil
	}

	file, err := os.Open(transcriptPath)
	if err != nil {
		if os.IsNotExist(err) {
			return codexTranscriptKindUnknown, nil
		}
		return codexTranscriptKindUnknown, fmt.Errorf("open transcript %q: %w", transcriptPath, err)
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	for {
		if err := ctx.Err(); err != nil {
			return codexTranscriptKindUnknown, err
		}

		line, readErr := reader.ReadBytes('\n')
		if kind := codexTranscriptLineKind(line); kind != codexTranscriptKindUnknown {
			r.cacheCodexTranscriptKind(transcriptPath, kind)
			return kind, nil
		}
		if readErr == nil {
			continue
		}
		if errors.Is(readErr, io.EOF) {
			return codexTranscriptKindUnknown, nil
		}
		return codexTranscriptKindUnknown, fmt.Errorf("read transcript %q: %w", transcriptPath, readErr)
	}
}

func (r *repository) cachedCodexTranscriptKind(transcriptPath string) (codexTranscriptKind, bool) {
	r.transcriptKindMu.Lock()
	defer r.transcriptKindMu.Unlock()
	if r.transcriptKinds == nil {
		return codexTranscriptKindUnknown, false
	}
	kind, ok := r.transcriptKinds[transcriptPath]
	return kind, ok
}

func (r *repository) cacheCodexTranscriptKind(transcriptPath string, kind codexTranscriptKind) {
	r.transcriptKindMu.Lock()
	defer r.transcriptKindMu.Unlock()
	if r.transcriptKinds == nil {
		r.transcriptKinds = make(map[string]codexTranscriptKind)
	}
	if _, exists := r.transcriptKinds[transcriptPath]; !exists {
		r.transcriptKindOrder = append(r.transcriptKindOrder, transcriptPath)
	}
	r.transcriptKinds[transcriptPath] = kind
	for len(r.transcriptKindOrder) > maxCodexTranscriptKindCacheEntries {
		oldest := r.transcriptKindOrder[0]
		r.transcriptKindOrder = r.transcriptKindOrder[1:]
		delete(r.transcriptKinds, oldest)
	}
}

func codexTranscriptLineKind(line []byte) codexTranscriptKind {
	var envelope codexTranscriptEnvelope
	if err := jsonUnmarshalTranscriptLine(line, &envelope); err != nil ||
		envelope.Type != "session_meta" ||
		len(envelope.Payload) == 0 {
		return codexTranscriptKindUnknown
	}

	var payload struct {
		Source         json.RawMessage `json:"source"`
		ParentThreadID string          `json:"parent_thread_id"`
	}
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return codexTranscriptKindUnknown
	}
	if strings.TrimSpace(payload.ParentThreadID) != "" || codexSessionSourceIsSubagent(payload.Source) {
		return codexTranscriptKindSubagent
	}
	return codexTranscriptKindRoot
}

func codexSessionSourceIsSubagent(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return false
	}

	var source struct {
		Subagent json.RawMessage `json:"subagent"`
	}
	if err := json.Unmarshal(trimmed, &source); err != nil {
		return false
	}
	subagent := bytes.TrimSpace(source.Subagent)
	return len(subagent) > 0 && !bytes.Equal(subagent, []byte("null"))
}

func codexResponseItemStatus(envelope codexTranscriptEnvelope) *codexTranscriptStatus {
	if envelope.Type != "response_item" || envelope.Timestamp.IsZero() || len(envelope.Payload) == 0 {
		return nil
	}

	var payload codexTaskCompletePayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return nil
	}
	if strings.TrimSpace(payload.Type) == "" {
		return nil
	}
	return &codexTranscriptStatus{
		observedAt:   envelope.Timestamp,
		rawEventName: "TranscriptActivity",
		phase:        core.TaskStatusPhaseWorking,
	}
}

func codexEventMessageStatus(observedAt time.Time, eventType string) *codexTranscriptStatus {
	switch strings.TrimSpace(eventType) {
	case "":
		return nil
	case "token_count":
		return nil
	case "task_complete":
		return &codexTranscriptStatus{
			observedAt:   observedAt,
			rawEventName: "TranscriptTaskComplete",
			phase:        core.TaskStatusPhaseWaitingForInput,
		}
	default:
		return &codexTranscriptStatus{
			observedAt:   observedAt,
			rawEventName: "TranscriptActivity",
			phase:        core.TaskStatusPhaseWorking,
		}
	}
}

func codexTranscriptActivityEvent(taskID string, envelope codexTranscriptEnvelope) *core.TaskActivityEvent {
	var payload codexTaskCompletePayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return nil
	}

	event := core.TaskActivityEvent{
		ObservedAt: envelope.Timestamp,
		TaskID:     taskID,
	}
	switch envelope.Type {
	case "event_msg":
		switch payload.Type {
		case "user_message":
			event.EventName = "TranscriptUserMessage"
			event.Role = core.TaskActivityRoleUser
			event.Text = compactTranscriptActivityText(payload.Message)
		case "agent_message":
			if payload.Phase != "final_answer" {
				return nil
			}
			event.EventName = "TranscriptAssistantMessage"
			event.Role = core.TaskActivityRoleAssistant
			event.Text = compactTranscriptActivityText(payload.Message)
		default:
			return nil
		}
	case "response_item":
		if payload.Type != "function_call" {
			return nil
		}
		event.EventName = "TranscriptFunctionCall"
		event.Role = core.TaskActivityRoleAssistant
		event.Text = compactTranscriptActivityText(codexFunctionCallCommand(payload.Arguments))
	default:
		return nil
	}

	if event.Text == "" {
		return nil
	}
	return &event
}

func codexFunctionCallCommand(arguments string) string {
	var parsed codexFunctionCallArguments
	if err := json.Unmarshal([]byte(arguments), &parsed); err != nil {
		return ""
	}
	return parsed.Command
}

func compactTranscriptActivityText(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}
