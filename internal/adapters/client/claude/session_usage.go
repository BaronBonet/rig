package claude

import (
	"bufio"
	"bytes"
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

// compactBoundaryMarker is the subtype of the system entry Claude Code
// records where it compacted the conversation, and assistantEntryMarker the
// type of the entry that records an API request's usage. Lines with neither
// are skipped undecoded.
var (
	compactBoundaryMarker = []byte(`"compact_boundary"`)
	assistantEntryMarker  = []byte(`"type":"assistant"`)
)

// ReadConversationUsage reads, in one pass over a conversation's transcript,
// the compactions Claude Code recorded and how full the root agent's context
// is. Compactions are split by what triggered each: running out of context,
// or /compact. The context is what the root agent's latest request put in it,
// as Claude Code's status line counts it. A compaction since then leaves it
// unknown until the next request: the size Claude Code records for the
// compacted conversation leaves out the system prompt and tools.
//
// The transcript does not record the context window, so the window is the
// one Claude reports for the model the conversation records, which the
// context window cache holds, and 0 until Claude has reported it. The window
// is looked up even while the context is unknown, so the first read after the
// conversation starts sets Claude probing before the agent's first request.
func (r *repository) ReadConversationUsage(
	ctx context.Context,
	conversation []core.TaskProviderSession,
) (core.ConversationUsage, error) {
	session := claudeConversationTranscript(conversation)
	if session == nil {
		return core.ConversationUsage{}, nil
	}
	window := r.contextWindows.lookup(ctx, session.Model)

	var usage core.ConversationUsage
	err := scanTranscriptLines(ctx, session.TranscriptPath, func(line []byte) {
		if !bytes.Contains(line, compactBoundaryMarker) && !bytes.Contains(line, assistantEntryMarker) {
			return
		}
		var entry claudeConversationUsageLine
		if err := json.Unmarshal(line, &entry); err != nil || entry.IsSidechain {
			return
		}

		switch {
		case entry.Type == "system" && entry.Subtype == "compact_boundary":
			usage.Compactions.Total++
			switch entry.CompactMetadata.Trigger {
			case "auto":
				usage.Compactions.Auto++
			case "manual":
				usage.Compactions.Manual++
			}
			usage.Context = nil
		case entry.Type == "assistant" && entry.Message.Usage != nil:
			// Output tokens are left out, as the status line leaves them. A
			// synthetic message, such as an API error, reports no usage and
			// says nothing about the context.
			requestUsage := entry.Message.Usage
			if tokens := requestUsage.InputTokens +
				requestUsage.CacheCreationInputTokens +
				requestUsage.CacheReadInputTokens; tokens > 0 {
				usage.Context = &core.ContextUsage{Tokens: tokens}
			}
		}
	})
	if err != nil {
		return core.ConversationUsage{}, err
	}
	if usage.Context != nil {
		usage.Context.Window = window
	}

	return usage, nil
}

// claudeConversationUsageLine decodes only what ReadConversationUsage needs,
// as assistant entries carry the whole response.
type claudeConversationUsageLine struct {
	Type        string `json:"type"`
	Subtype     string `json:"subtype"`
	IsSidechain bool   `json:"isSidechain"`
	Message     struct {
		Usage *claudeUsage `json:"usage"`
	} `json:"message"`
	CompactMetadata struct {
		Trigger string `json:"trigger"`
	} `json:"compactMetadata"`
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
