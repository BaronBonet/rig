package codex

import (
	"context"
	"encoding/json"
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

func (r *repository) RecoverLatestTaskStatus(
	ctx context.Context,
	current core.TaskStatusUpdate,
	sessions []core.TaskProviderSession,
) (*core.TaskStatusUpdate, error) {
	if current.Phase == core.TaskStatusPhaseStopped {
		return nil, nil
	}

	session := newestCodexTranscriptSession(sessions)
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
	if current.Phase == core.TaskStatusPhaseWaitingForInput && status.phase == core.TaskStatusPhaseWaitingForInput {
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
