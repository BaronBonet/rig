package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/BaronBonet/rig/internal/core"
)

// ReadSessionTokenUsage sums per-request token usage from a Claude Code
// transcript. Each assistant API request reports its own usage; one logical
// message spans multiple transcript lines that repeat identical usage, so
// usage is counted once per message ID.
func (r *repository) ReadSessionTokenUsage(
	ctx context.Context,
	transcriptPath string,
) (*core.SessionTokenUsage, error) {
	var total core.SessionTokenUsage
	seen := make(map[string]bool)
	lineNumber := 0

	err := scanTranscriptLines(ctx, transcriptPath, func(line []byte) {
		lineNumber++

		var entry claudeTranscriptLine
		if err := json.Unmarshal(line, &entry); err != nil {
			return
		}
		if entry.Type != "assistant" || entry.Message.Usage == nil {
			return
		}

		key := strings.TrimSpace(entry.Message.ID)
		if key == "" {
			key = strings.TrimSpace(entry.RequestID)
		}
		if key == "" {
			key = "line-" + strconv.Itoa(lineNumber)
		}
		if seen[key] {
			return
		}
		seen[key] = true

		usage := entry.Message.Usage
		total.InputTokens += usage.InputTokens
		total.OutputTokens += usage.OutputTokens
		total.CachedInputTokens += usage.CacheReadInputTokens
		total.CacheCreationInputTokens += usage.CacheCreationInputTokens
		total.TotalTokens += usage.InputTokens +
			usage.OutputTokens +
			usage.CacheReadInputTokens +
			usage.CacheCreationInputTokens
	})
	if err != nil {
		return nil, err
	}
	if total.IsZero() {
		return nil, nil
	}

	return &total, nil
}

type claudeTranscriptLine struct {
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
	Message   struct {
		ID    string       `json:"id"`
		Usage *claudeUsage `json:"usage"`
	} `json:"message"`
}

type claudeUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

// scanTranscriptLines streams a JSONL transcript line by line. Claude
// transcript lines can far exceed bufio.Scanner's default limit, so this
// reads unbounded lines. A missing transcript is not an error.
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
