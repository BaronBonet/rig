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
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
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
