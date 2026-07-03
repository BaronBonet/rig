package claude

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/BaronBonet/rig/internal/core"
)

// ReadSessionActivity recovers user and assistant activity from a Claude Code
// transcript after the supplied timestamp. Hook-recorded activity is lost
// when the daemon is unreachable while a session runs; the transcript is the
// durable record, so the detail view reads the gap from here.
func (r *repository) ReadSessionActivity(
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
		var entry claudeActivityLine
		if err := json.Unmarshal(line, &entry); err != nil {
			return
		}
		if entry.Timestamp.IsZero() || !entry.Timestamp.After(after) {
			return
		}
		// Meta entries are injected context, and sidechain entries belong to
		// subagents; neither is conversation activity.
		if entry.IsMeta || entry.IsSidechain {
			return
		}

		events = append(events, claudeTranscriptActivityEvents(taskID, entry)...)
	})
	return events, err
}

type claudeActivityLine struct {
	Timestamp   time.Time `json:"timestamp"`
	Type        string    `json:"type"`
	IsMeta      bool      `json:"isMeta"`
	IsSidechain bool      `json:"isSidechain"`
	Message     struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type claudeContentBlock struct {
	Type  string `json:"type"`
	Text  string `json:"text"`
	Name  string `json:"name"`
	Input struct {
		Command string `json:"command"`
	} `json:"input"`
}

func claudeTranscriptActivityEvents(taskID string, entry claudeActivityLine) []core.TaskActivityEvent {
	switch entry.Type {
	case "user":
		text := claudeUserMessageText(entry.Message.Content)
		if text == "" {
			return nil
		}
		return []core.TaskActivityEvent{{
			ObservedAt: entry.Timestamp,
			TaskID:     taskID,
			EventName:  "TranscriptUserMessage",
			Role:       core.TaskActivityRoleUser,
			Text:       text,
		}}
	case "assistant":
		var events []core.TaskActivityEvent
		for _, block := range claudeContentBlocks(entry.Message.Content) {
			event := core.TaskActivityEvent{
				ObservedAt: entry.Timestamp,
				TaskID:     taskID,
				Role:       core.TaskActivityRoleAssistant,
			}
			switch block.Type {
			case "text":
				event.EventName = "TranscriptAssistantMessage"
				event.Text = compactActivityText(block.Text)
			case "tool_use":
				if block.Name != "Bash" || strings.TrimSpace(block.Input.Command) == "" {
					continue
				}
				event.EventName = "TranscriptToolUse"
				event.Text = compactActivityText(block.Input.Command)
			default:
				continue
			}
			if event.Text == "" {
				continue
			}
			events = append(events, event)
		}
		return events
	default:
		return nil
	}
}

// claudeUserMessageText extracts the human-authored text of a user transcript
// entry. Content is a plain string for typed prompts; block content carrying
// tool results is Claude's own bookkeeping, not a user prompt.
func claudeUserMessageText(content json.RawMessage) string {
	var text string
	if err := json.Unmarshal(content, &text); err == nil {
		return compactActivityText(text)
	}

	var parts []string
	for _, block := range claudeContentBlocks(content) {
		if block.Type == "tool_result" {
			return ""
		}
		if block.Type == "text" {
			parts = append(parts, block.Text)
		}
	}
	return compactActivityText(strings.Join(parts, " "))
}

func claudeContentBlocks(content json.RawMessage) []claudeContentBlock {
	var blocks []claudeContentBlock
	if err := json.Unmarshal(content, &blocks); err != nil {
		return nil
	}
	return blocks
}

func compactActivityText(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}
