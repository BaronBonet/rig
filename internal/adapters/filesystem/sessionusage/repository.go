package sessionusage

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"strings"

	"agent/internal/core"
)

type Repository struct{}

func NewRepository() *Repository {
	return &Repository{}
}

func (r *Repository) ReadSessionTokenUsage(
	ctx context.Context,
	provider string,
	transcriptPath string,
) (*core.SessionTokenUsage, error) {
	switch strings.TrimSpace(provider) {
	case "codex":
		return readCodexTokenUsage(ctx, transcriptPath)
	case "claude":
		return readClaudeTokenUsage(ctx, transcriptPath)
	default:
		return nil, nil
	}
}

type codexTranscriptEnvelope struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type codexEventPayload struct {
	Type string `json:"type"`
	Info struct {
		TotalTokenUsage struct {
			InputTokens           int `json:"input_tokens"`
			CachedInputTokens     int `json:"cached_input_tokens"`
			OutputTokens          int `json:"output_tokens"`
			ReasoningOutputTokens int `json:"reasoning_output_tokens"`
			TotalTokens           int `json:"total_tokens"`
		} `json:"total_token_usage"`
	} `json:"info"`
}

func readCodexTokenUsage(ctx context.Context, transcriptPath string) (*core.SessionTokenUsage, error) {
	if strings.TrimSpace(transcriptPath) == "" {
		return nil, nil
	}

	f, err := os.Open(transcriptPath)
	if err != nil {
		return nil, nil
	}
	defer f.Close()

	var latest *core.SessionTokenUsage
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		line := scanner.Bytes()
		var envelope codexTranscriptEnvelope
		if err := json.Unmarshal(line, &envelope); err != nil {
			continue
		}
		if envelope.Type != "event_msg" || len(envelope.Payload) == 0 {
			continue
		}

		var payload codexEventPayload
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			continue
		}
		if payload.Type != "token_count" {
			continue
		}

		usage := payload.Info.TotalTokenUsage
		if usage.TotalTokens == 0 && usage.InputTokens == 0 && usage.OutputTokens == 0 {
			continue
		}

		latest = &core.SessionTokenUsage{
			InputTokens:           usage.InputTokens,
			OutputTokens:          usage.OutputTokens,
			CachedInputTokens:     usage.CachedInputTokens,
			ReasoningOutputTokens: usage.ReasoningOutputTokens,
			TotalTokens:           usage.TotalTokens,
		}
	}

	return latest, nil
}

// Claude transcript types. Each JSONL line may be an assistant message with
// a nested message.usage block. The same message ID can appear multiple times
// (streaming updates); we keep only the last occurrence per ID, then sum
// across unique messages.

type claudeTranscriptLine struct {
	Type    string         `json:"type"`
	Message *claudeMessage `json:"message,omitempty"`
}

type claudeMessage struct {
	ID    string       `json:"id"`
	Usage *claudeUsage `json:"usage,omitempty"`
}

type claudeUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

func readClaudeTokenUsage(ctx context.Context, transcriptPath string) (*core.SessionTokenUsage, error) {
	if strings.TrimSpace(transcriptPath) == "" {
		return nil, nil
	}

	f, err := os.Open(transcriptPath)
	if err != nil {
		return nil, nil
	}
	defer f.Close()

	// Track the last usage seen per unique message ID to deduplicate
	// streaming updates that repeat the same message with growing output.
	lastUsageByID := make(map[string]*claudeUsage)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		line := scanner.Bytes()
		var entry claudeTranscriptLine
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}
		if entry.Message == nil || entry.Message.Usage == nil || entry.Message.ID == "" {
			continue
		}

		u := entry.Message.Usage
		if u.InputTokens == 0 && u.OutputTokens == 0 &&
			u.CacheReadInputTokens == 0 && u.CacheCreationInputTokens == 0 {
			continue
		}

		lastUsageByID[entry.Message.ID] = u
	}

	if len(lastUsageByID) == 0 {
		return nil, nil
	}

	var total core.SessionTokenUsage
	for _, u := range lastUsageByID {
		total.InputTokens += u.InputTokens + u.CacheCreationInputTokens
		total.OutputTokens += u.OutputTokens
		total.CacheCreationInputTokens += u.CacheCreationInputTokens
		total.CacheReadInputTokens += u.CacheReadInputTokens
		total.CachedInputTokens = total.CacheReadInputTokens
	}
	total.TotalTokens = total.InputTokens + total.OutputTokens + total.CachedInputTokens

	if total.IsZero() {
		return nil, nil
	}

	return &total, nil
}
