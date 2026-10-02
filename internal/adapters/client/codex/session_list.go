package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/BaronBonet/rig/internal/core"
	"github.com/BaronBonet/rig/internal/pkg/transcript"
)

// ListFolderSessions lists Codex sessions started in folder. Codex keeps one
// rollout per session under <codex home>/sessions/YYYY/MM/DD/, and the first
// line of each names its folder. Subagent rollouts are skipped: they resume
// through their parent.
func (r *repository) ListFolderSessions(
	ctx context.Context,
	folder string,
	limit int,
) ([]core.ProviderSessionSummary, error) {
	codexHome, err := r.resolveCodexHomeDir()
	if err != nil {
		return nil, err
	}
	folder = filepath.Clean(strings.TrimSpace(folder))

	type candidate struct {
		modTime time.Time
		path    string
	}
	var candidates []candidate
	root := filepath.Join(codexHome, "sessions")
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if path == root && os.IsNotExist(err) {
				return fs.SkipAll
			}
			return err
		}
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, "rollout-") || !strings.HasSuffix(name, ".jsonl") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil //nolint:nilerr // a rollout removed mid-walk is simply not listed
		}
		candidates = append(candidates, candidate{modTime: info.ModTime(), path: path})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list codex sessions in %q: %w", root, err)
	}
	slices.SortFunc(candidates, func(a, b candidate) int { return b.modTime.Compare(a.modTime) })

	var sessions []core.ProviderSessionSummary
	for _, candidate := range candidates {
		if limit > 0 && len(sessions) == limit {
			break
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		head, err := transcript.Head(candidate.path)
		if err != nil {
			return nil, err
		}
		id, ok := codexRootSessionIn(head, folder)
		if !ok {
			continue
		}
		title := codexFirstPrompt(head)
		if title == "" {
			continue
		}
		sessions = append(sessions, core.ProviderSessionSummary{
			LastActiveAt:   candidate.modTime,
			Provider:       core.ProviderCodex,
			SessionID:      id,
			Title:          title,
			Cwd:            folder,
			TranscriptPath: candidate.path,
		})
	}
	return sessions, nil
}

// codexRootSessionIn returns the session ID from a rollout's session_meta line
// when the session was started in folder and is not a subagent.
func codexRootSessionIn(head [][]byte, folder string) (string, bool) {
	if len(head) == 0 {
		return "", false
	}
	var meta struct {
		Type    string `json:"type"`
		Payload struct {
			ID     string          `json:"id"`
			Cwd    string          `json:"cwd"`
			Source json.RawMessage `json:"source"`
		} `json:"payload"`
	}
	if json.Unmarshal(head[0], &meta) != nil || meta.Type != "session_meta" {
		return "", false
	}
	// Interactive sessions record their source as a string ("cli", "vscode");
	// subagents record an object naming their parent.
	var source string
	if len(meta.Payload.Source) > 0 && json.Unmarshal(meta.Payload.Source, &source) != nil {
		return "", false
	}
	if filepath.Clean(meta.Payload.Cwd) != folder || strings.TrimSpace(meta.Payload.ID) == "" {
		return "", false
	}
	return strings.TrimSpace(meta.Payload.ID), true
}

// codexFirstPrompt returns the session's first user prompt as a title. Codex
// CLI records it as a user_message event; Codex Desktop as a user message whose
// leading entries are injected context wrapped in tags.
func codexFirstPrompt(head [][]byte) string {
	for _, line := range head {
		var envelope struct {
			Type    string `json:"type"`
			Payload struct {
				Type    string `json:"type"`
				Role    string `json:"role"`
				Message string `json:"message"`
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &envelope) != nil {
			continue
		}
		var prompt string
		switch {
		case envelope.Type == "event_msg" && envelope.Payload.Type == "user_message":
			prompt = envelope.Payload.Message
		case envelope.Type == "response_item" && envelope.Payload.Type == "message" && envelope.Payload.Role == "user":
			for _, part := range envelope.Payload.Content {
				prompt += part.Text
			}
		default:
			continue
		}
		prompt = strings.TrimSpace(prompt)
		if prompt != "" && !strings.HasPrefix(prompt, "<") && !strings.HasPrefix(prompt, "# AGENTS.md") {
			return transcript.Title(prompt)
		}
	}
	return ""
}
