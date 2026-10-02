package claude

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/BaronBonet/rig/internal/core"
	"github.com/BaronBonet/rig/internal/pkg/transcript"
)

// ListFolderSessions lists Claude Code sessions started in folder. Claude Code
// keeps a folder's session transcripts in <config>/projects/<folder with every
// non-alphanumeric character replaced by "-">/<session id>.jsonl.
func (r *repository) ListFolderSessions(
	ctx context.Context,
	folder string,
	limit int,
) ([]core.ProviderSessionSummary, error) {
	configDir, err := r.resolveConfigDir()
	if err != nil {
		return nil, err
	}
	folder = filepath.Clean(strings.TrimSpace(folder))
	projectDir := filepath.Join(configDir, "projects", claudeProjectDirName(folder))
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list claude sessions in %q: %w", projectDir, err)
	}

	type candidate struct {
		modTime time.Time
		path    string
		id      string
	}
	candidates := make([]candidate, 0, len(entries))
	for _, entry := range entries {
		id, ok := strings.CutSuffix(entry.Name(), ".jsonl")
		if entry.IsDir() || !ok {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		candidates = append(
			candidates,
			candidate{modTime: info.ModTime(), path: filepath.Join(projectDir, entry.Name()),
				id: id},
		)
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
		title, err := claudeSessionTitle(candidate.path)
		if err != nil {
			return nil, err
		}
		// A session without a prompt has nothing to resume.
		if title == "" {
			continue
		}
		sessions = append(sessions, core.ProviderSessionSummary{
			LastActiveAt:   candidate.modTime,
			Provider:       core.ProviderClaude,
			SessionID:      candidate.id,
			Title:          title,
			Cwd:            folder,
			TranscriptPath: candidate.path,
		})
	}
	return sessions, nil
}

var nonAlphanumeric = regexp.MustCompile(`[^a-zA-Z0-9]`)

func claudeProjectDirName(folder string) string {
	return nonAlphanumeric.ReplaceAllString(folder, "-")
}

// claudeSessionTitle names a session the way Claude Code does: by the title the
// user gave it, else the title Claude Code generated, else an older-style
// summary, else its first prompt. Title records are rewritten throughout a
// transcript, so the newest sit near the end; the first prompt sits near the
// start. Only those two windows are read.
func claudeSessionTitle(path string) (string, error) {
	tail, err := transcript.Tail(path)
	if err != nil {
		return "", err
	}
	var customTitle, aiTitle, summary string
	for index := len(tail) - 1; index >= 0; index-- {
		var record struct {
			Type        string `json:"type"`
			CustomTitle string `json:"customTitle"`
			AITitle     string `json:"aiTitle"`
			Summary     string `json:"summary"`
		}
		if json.Unmarshal(tail[index], &record) != nil {
			continue
		}
		switch record.Type {
		case "custom-title":
			customTitle = cmp.Or(customTitle, strings.TrimSpace(record.CustomTitle))
		case "ai-title":
			aiTitle = cmp.Or(aiTitle, strings.TrimSpace(record.AITitle))
		case "summary":
			summary = cmp.Or(summary, strings.TrimSpace(record.Summary))
		}
	}
	if title := cmp.Or(customTitle, aiTitle, summary); title != "" {
		return transcript.Title(title), nil
	}

	head, err := transcript.Head(path)
	if err != nil {
		return "", err
	}
	for _, line := range head {
		var entry claudeActivityLine
		if json.Unmarshal(line, &entry) != nil || entry.Type != "user" || entry.IsMeta || entry.IsSidechain {
			continue
		}
		prompt := strings.TrimSpace(claudeUserMessageText(entry.Message.Content))
		// Slash commands and injected context are wrapped in tags.
		if prompt != "" && !strings.HasPrefix(prompt, "<") {
			return transcript.Title(prompt), nil
		}
	}
	return "", nil
}

func (r *repository) resolveConfigDir() (string, error) {
	if r.claudeConfigDir != nil {
		return r.claudeConfigDir()
	}
	if custom := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); custom != "" {
		return custom, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve claude config dir: %w", err)
	}
	return filepath.Join(home, ".claude"), nil
}
