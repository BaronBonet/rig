package sessionusage

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"strings"

	"rig/internal/core"
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
	default:
		return nil, nil
	}
}

// scanTranscriptLines opens a JSONL transcript file and calls fn for each line.
// It handles file opening, buffered scanning, and context cancellation.
func scanTranscriptLines(ctx context.Context, path string, fn func(line []byte)) error {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		fn(scanner.Bytes())
	}
	return nil
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
			InputTokens:           usage.InputTokens,
			OutputTokens:          usage.OutputTokens,
			CachedInputTokens:     usage.CachedInputTokens,
			ReasoningOutputTokens: usage.ReasoningOutputTokens,
			TotalTokens:           usage.TotalTokens,
		}
	})

	return latest, err
}
