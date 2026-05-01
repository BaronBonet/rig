package codex

import (
	"bufio"
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
	return readCodexTokenUsage(ctx, transcriptPath)
}

func scanTranscriptLines(ctx context.Context, path string, fn func(line []byte)) error {
	path = strings.TrimSpace(path)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("open transcript %q: %w", path, err)
	}
	defer f.Close()

	reader := bufio.NewReader(f)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			fn(line)
		}
		if readErr == nil {
			continue
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		return readErr
	}
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

	status, err := readLatestCodexTranscriptStatus(ctx, session.TranscriptPath)
	if err != nil {
		return nil, err
	}
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
	return readCodexSessionActivity(ctx, session, after)
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

func readLatestCodexTranscriptStatus(ctx context.Context, transcriptPath string) (*codexTranscriptStatus, error) {
	var latest *codexTranscriptStatus

	err := scanTranscriptLines(ctx, transcriptPath, func(line []byte) {
		var envelope codexTranscriptEnvelope
		if err := json.Unmarshal(line, &envelope); err != nil {
			return
		}
		if envelope.Type != "event_msg" || envelope.Timestamp.IsZero() || len(envelope.Payload) == 0 {
			if status := codexResponseItemStatus(envelope); status != nil &&
				(latest == nil || status.observedAt.After(latest.observedAt)) {
				latest = status
			}
			return
		}

		var payload codexTaskCompletePayload
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return
		}
		status := codexEventMessageStatus(envelope.Timestamp, payload.Type)
		if status == nil {
			return
		}
		if latest == nil || status.observedAt.After(latest.observedAt) {
			latest = status
		}
	})

	return latest, err
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

func readCodexSessionActivity(
	ctx context.Context,
	session core.TaskProviderSession,
	after time.Time,
) ([]core.TaskActivityEvent, error) {
	taskID := strings.TrimSpace(session.TaskID)
	if taskID == "" {
		return nil, nil
	}

	var events []core.TaskActivityEvent
	err := scanTranscriptLines(ctx, session.TranscriptPath, func(line []byte) {
		var envelope codexTranscriptEnvelope
		if err := json.Unmarshal(line, &envelope); err != nil {
			return
		}
		if envelope.Timestamp.IsZero() || !envelope.Timestamp.After(after) || len(envelope.Payload) == 0 {
			return
		}

		activity := codexTranscriptActivityEvent(taskID, envelope)
		if activity == nil {
			return
		}
		events = append(events, *activity)
	})
	return events, err
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

func readCodexTokenUsage(ctx context.Context, transcriptPath string) (*core.SessionTokenUsage, error) {
	var latest *core.SessionTokenUsage

	err := scanTranscriptLines(ctx, transcriptPath, func(line []byte) {
		var envelope codexTranscriptEnvelope
		if err := json.Unmarshal(line, &envelope); err != nil {
			return
		}
		if envelope.Type != "event_msg" || len(envelope.Payload) == 0 {
			return
		}

		var payload codexEventPayload
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return
		}
		if payload.Type != "token_count" {
			return
		}

		usage := payload.Info.TotalTokenUsage
		if usage.TotalTokens == 0 && usage.InputTokens == 0 && usage.OutputTokens == 0 {
			return
		}

		latest = &core.SessionTokenUsage{
			InputTokens:              usage.InputTokens,
			OutputTokens:             usage.OutputTokens,
			CachedInputTokens:        usage.CachedInputTokens,
			CacheCreationInputTokens: usage.CacheCreationInputTokens,
			ReasoningOutputTokens:    usage.ReasoningOutputTokens,
			TotalTokens:              usage.TotalTokens,
		}
	})

	return latest, err
}
