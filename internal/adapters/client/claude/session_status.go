package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/BaronBonet/rig/internal/core"
)

// transcriptStatusTailBytes bounds how much of a transcript status recovery
// reads. The entries it looks for sit within the last few kilobytes: after an
// interrupt Claude Code appends only bookkeeping entries until the next turn.
const transcriptStatusTailBytes = 256 << 10

// transcriptInterruptMarkers are the texts Claude Code records as a user entry
// when the user interrupts a turn; the tool-use variant marks an interrupt
// during a tool call.
var transcriptInterruptMarkers = []string{
	"[Request interrupted by user]",
	"[Request interrupted by user for tool use]",
}

// RecoverLatestTaskStatus repairs the one stale status no Claude hook ends:
// interrupting a turn fires neither Stop nor any other hook, so the
// transcript's interrupt marker is the only evidence that the agent is idle.
// Every other transition is hook-driven: Stop or StopFailure ends a turn, and
// the next prompt fires UserPromptSubmit.
func (r *repository) RecoverLatestTaskStatus(
	_ context.Context,
	current core.TaskStatusUpdate,
	sessions []core.TaskProviderSession,
	_ time.Time,
) (*core.TaskStatusUpdate, error) {
	switch current.Phase {
	case core.TaskStatusPhaseStarting, core.TaskStatusPhaseWorking, core.TaskStatusPhaseWorkingInBackground:
	default:
		return nil, nil
	}

	session := newestClaudeTranscriptSession(sessions)
	if session == nil {
		return nil, nil
	}
	interruptedAt, err := transcriptInterruptedAt(session.TranscriptPath, transcriptStatusTailBytes)
	if err != nil {
		return nil, err
	}
	// Hook evidence newer than the interrupt, such as the UserPromptSubmit of
	// the next prompt, wins even before the prompt reaches the transcript.
	if !interruptedAt.After(current.ObservedAt) {
		return nil, nil
	}
	return &core.TaskStatusUpdate{
		TaskID:       current.TaskID,
		Provider:     current.Provider,
		Phase:        core.TaskStatusPhaseWaitingForInput,
		RawEventName: "TranscriptTurnInterrupted",
		ObservedAt:   interruptedAt,
	}, nil
}

// newestClaudeTranscriptSession returns the task's most recently observed
// Claude session with a transcript. Hooks fired inside Claude subagents carry
// the root session's transcript path, so this is the root agent's transcript;
// after /clear it is the new conversation's.
func newestClaudeTranscriptSession(sessions []core.TaskProviderSession) *core.TaskProviderSession {
	var latest *core.TaskProviderSession
	for _, session := range sessions {
		session.TranscriptPath = strings.TrimSpace(session.TranscriptPath)
		if session.Provider != core.ProviderClaude || session.TranscriptPath == "" {
			continue
		}
		if latest == nil || session.LastObservedAt.After(latest.LastObservedAt) {
			newest := session
			latest = &newest
		}
	}
	return latest
}

// transcriptInterruptedAt returns when the root agent's latest turn was
// interrupted, or zero when it was not. It reads the newest entries first:
// assistant output means a turn ran after any interrupt. Other user entries
// do not decide: a local command leaves the agent idle, and the next prompt's
// UserPromptSubmit hook is newer than the interrupt. Subagent entries never
// decide.
func transcriptInterruptedAt(path string, tailBytes int64) (time.Time, error) {
	lines, err := readTranscriptTail(path, tailBytes)
	if err != nil {
		return time.Time{}, err
	}
	for i := len(lines) - 1; i >= 0; i-- {
		var entry claudeActivityLine
		if err := json.Unmarshal(lines[i], &entry); err != nil || entry.IsSidechain {
			continue
		}
		switch entry.Type {
		case "assistant":
			return time.Time{}, nil
		case "user":
			if isInterruptMarker(entry.Message.Content) {
				return entry.Timestamp, nil
			}
		}
	}
	return time.Time{}, nil
}

func isInterruptMarker(content json.RawMessage) bool {
	var text string
	if err := json.Unmarshal(content, &text); err == nil {
		return slices.Contains(transcriptInterruptMarkers, strings.TrimSpace(text))
	}
	for _, block := range claudeContentBlocks(content) {
		if block.Type == "text" && slices.Contains(transcriptInterruptMarkers, strings.TrimSpace(block.Text)) {
			return true
		}
	}
	return false
}

// readTranscriptTail returns the lines among a transcript's last maxBytes,
// oldest first, without the partial line the window starts in. Status
// recovery runs every two seconds while a task is watched, so it never reads
// whole transcripts. A missing transcript is not an error.
func readTranscriptTail(path string, maxBytes int64) ([][]byte, error) {
	path = strings.TrimSpace(path)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open transcript %q: %w", path, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat transcript %q: %w", path, err)
	}
	offset := max(info.Size()-maxBytes, 0)
	tail := make([]byte, info.Size()-offset)
	n, err := f.ReadAt(tail, offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("read transcript %q: %w", path, err)
	}
	tail = tail[:n]
	if offset > 0 {
		// The window starts inside a line; that partial line is not an entry.
		newline := bytes.IndexByte(tail, '\n')
		if newline < 0 {
			return nil, nil
		}
		tail = tail[newline+1:]
	}
	return bytes.Split(tail, []byte("\n")), nil
}
