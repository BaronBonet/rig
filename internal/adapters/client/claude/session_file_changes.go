package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/BaronBonet/rig/internal/core"
)

// claudeEditTools are the Claude Code tools that write files. Edits made
// through Bash (sed, scripts) are not recorded as file changes.
var claudeEditTools = map[string]bool{
	"Edit":         true,
	"MultiEdit":    true,
	"NotebookEdit": true,
	"Write":        true,
}

// ReadSessionFileChanges reads the files a Claude Code session edited from its
// transcript and from the transcripts of its subagents, which Claude Code keeps
// beside it: <session>.jsonl and <session>/subagents/agent-<id>.jsonl.
func (r *repository) ReadSessionFileChanges(
	ctx context.Context,
	session core.TaskProviderSession,
) ([]core.SessionFileChange, error) {
	transcriptPath := strings.TrimSpace(session.TranscriptPath)
	if transcriptPath == "" {
		return nil, nil
	}

	subagentTranscripts, err := filepath.Glob(
		filepath.Join(strings.TrimSuffix(transcriptPath, ".jsonl"), "subagents", "*.jsonl"),
	)
	if err != nil {
		return nil, fmt.Errorf("list subagent transcripts for %q: %w", transcriptPath, err)
	}

	// Older Claude Code versions also recorded subagent turns inline as
	// sidechain entries, so one tool call can appear in two transcripts.
	seenToolUses := make(map[string]bool)
	var changes []core.SessionFileChange
	for _, path := range append([]string{transcriptPath}, subagentTranscripts...) {
		edits, err := r.fileChanges.load(ctx, path)
		if err != nil {
			return nil, err
		}
		for _, edit := range edits {
			if edit.toolUseID != "" {
				if seenToolUses[edit.toolUseID] {
					continue
				}
				seenToolUses[edit.toolUseID] = true
			}

			filePath := edit.path
			if !filepath.IsAbs(filePath) {
				if strings.TrimSpace(session.Cwd) == "" {
					continue
				}
				filePath = filepath.Join(session.Cwd, filePath)
			}
			changes = append(changes, core.SessionFileChange{
				ObservedAt: edit.observedAt,
				Path:       filepath.Clean(filePath),
			})
		}
	}

	return changes, nil
}

type transcriptEdit struct {
	observedAt time.Time
	toolUseID  string
	path       string
}

// transcriptEditCache keeps the edits parsed from each transcript until the
// file changes, so the transcripts of long sessions and their many subagents
// are not re-parsed on every dashboard refresh.
type transcriptEditCache struct {
	mu      sync.Mutex
	entries map[string]transcriptEditCacheEntry
}

type transcriptEditCacheEntry struct {
	modTime time.Time
	edits   []transcriptEdit
	size    int64
}

func (c *transcriptEditCache) load(ctx context.Context, path string) ([]transcriptEdit, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat transcript %q: %w", path, err)
	}

	c.mu.Lock()
	entry, cached := c.entries[path]
	c.mu.Unlock()
	if cached && entry.size == info.Size() && entry.modTime.Equal(info.ModTime()) {
		return entry.edits, nil
	}

	var edits []transcriptEdit
	err = scanTranscriptLines(ctx, path, func(line []byte) {
		edits = append(edits, transcriptLineEdits(line)...)
	})
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	if c.entries == nil {
		c.entries = make(map[string]transcriptEditCacheEntry)
	}
	c.entries[path] = transcriptEditCacheEntry{modTime: info.ModTime(), edits: edits, size: info.Size()}
	c.mu.Unlock()
	return edits, nil
}

var toolUseMarker = []byte(`"tool_use"`)

func transcriptLineEdits(line []byte) []transcriptEdit {
	// Most transcript lines are tool results and messages; skip them before
	// paying for a full decode.
	if !bytes.Contains(line, toolUseMarker) {
		return nil
	}

	var entry claudeFileChangeLine
	if err := json.Unmarshal(line, &entry); err != nil || entry.Type != "assistant" {
		return nil
	}
	var blocks []claudeToolUseBlock
	if err := json.Unmarshal(entry.Message.Content, &blocks); err != nil {
		return nil
	}

	var edits []transcriptEdit
	for _, block := range blocks {
		if block.Type != "tool_use" || !claudeEditTools[block.Name] {
			continue
		}
		path := strings.TrimSpace(block.Input.FilePath)
		if path == "" {
			path = strings.TrimSpace(block.Input.NotebookPath)
		}
		if path == "" {
			continue
		}
		edits = append(edits, transcriptEdit{observedAt: entry.Timestamp, toolUseID: block.ID, path: path})
	}
	return edits
}

type claudeFileChangeLine struct {
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"`
	Message   struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type claudeToolUseBlock struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Name  string `json:"name"`
	Input struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
	} `json:"input"`
}
