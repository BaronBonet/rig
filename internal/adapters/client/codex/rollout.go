package codex

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// rolloutDirs are the directories under the Codex home that hold session
// rollouts: live sessions, filed by date, then archived ones.
var rolloutDirs = []string{"sessions", "archived_sessions"}

// turnContextMarker is in every turn_context record of a rollout. Lines
// without it are skipped undecoded, as a rollout can run to hundreds of
// megabytes.
var turnContextMarker = []byte(`"turn_context"`)

// codexTurnContext is the model and reasoning effort a Codex session ran one
// turn with, as the turn's turn_context record in the rollout holds them.
type codexTurnContext struct {
	Model  string
	Effort string
}

// codexTurnContextPayload is the part of a turn_context record Rig reads. A
// turn's collaboration mode is default, or plan for a plan-mode turn, and is
// absent from rollouts of Codex versions without modes.
type codexTurnContextPayload struct {
	Model             string `json:"model"`
	Effort            string `json:"effort"`
	CollaborationMode struct {
		Mode string `json:"mode"`
	} `json:"collaboration_mode"`
}

// findRollout returns the path of the rollout Codex records sessionID in,
// named rollout-<timestamp>-<session ID>.jsonl, or "" when there is none. The
// search is best effort: a directory it cannot read is skipped.
func findRollout(codexHome string, sessionID string) string {
	suffix := "-" + sessionID + ".jsonl"
	for _, dir := range rolloutDirs {
		var found string
		_ = filepath.WalkDir(filepath.Join(codexHome, dir), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				// WalkDir reports only a missing root or an unreadable
				// directory, and either is skipped.
				return fs.SkipDir
			}
			name := entry.Name()
			if !entry.IsDir() && strings.HasPrefix(name, "rollout-") && strings.HasSuffix(name, suffix) {
				found = path
				return fs.SkipAll
			}
			return nil
		})
		if found != "" {
			return found
		}
	}
	return ""
}

// lastTurnContext returns the last turn_context record of a default-mode turn
// in the rollout at path, which names the model and reasoning effort of the
// session's latest such turn, after any /model change. A plan-mode turn is
// skipped: it runs on plan mode's own reasoning level, which the session's
// other turns do not use. It reports false when the rollout cannot be read or
// holds no such turn_context with a model.
func lastTurnContext(path string) (codexTurnContext, bool) {
	file, err := os.Open(path)
	if err != nil {
		return codexTurnContext{}, false
	}
	defer file.Close()

	var (
		last  codexTurnContext
		found bool
	)
	reader := bufio.NewReaderSize(file, transcriptReadBufferSize)
	for {
		line, readErr := reader.ReadBytes('\n')
		if bytes.Contains(line, turnContextMarker) {
			if turn, ok := parseTurnContext(line); ok {
				last, found = turn, true
			}
		}
		if readErr == nil {
			continue
		}
		if !errors.Is(readErr, io.EOF) {
			return codexTurnContext{}, false
		}
		return last, found
	}
}

func parseTurnContext(line []byte) (codexTurnContext, bool) {
	var envelope codexTranscriptEnvelope
	if err := jsonUnmarshalTranscriptLine(line, &envelope); err != nil || envelope.Type != "turn_context" {
		return codexTurnContext{}, false
	}
	var payload codexTurnContextPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return codexTurnContext{}, false
	}
	if mode := strings.TrimSpace(payload.CollaborationMode.Mode); mode != "" && mode != "default" {
		return codexTurnContext{}, false
	}
	turn := codexTurnContext{
		Model:  strings.TrimSpace(payload.Model),
		Effort: strings.TrimSpace(payload.Effort),
	}
	return turn, turn.Model != ""
}
