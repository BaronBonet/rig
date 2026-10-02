package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/BaronBonet/rig/internal/core"
)

// ReadSessionFileChanges reads the files a Codex session patched from its
// rollout. A subagent's rollout is a separate file that only names its parent
// thread, so edits made by Codex subagents are not included.
func (r *repository) ReadSessionFileChanges(
	ctx context.Context,
	session core.TaskProviderSession,
) ([]core.SessionFileChange, error) {
	snapshot, err := r.getTranscriptIndex().read(ctx, session.TranscriptPath)
	if err != nil {
		return nil, err
	}

	changes := make([]core.SessionFileChange, 0, len(snapshot.fileChanges))
	for _, change := range snapshot.fileChanges {
		if !filepath.IsAbs(change.Path) {
			if strings.TrimSpace(session.Cwd) == "" {
				continue
			}
			change.Path = filepath.Join(session.Cwd, change.Path)
		}
		change.Path = filepath.Clean(change.Path)
		changes = append(changes, change)
	}
	return changes, nil
}

var (
	codexPatchMarker     = []byte("*** Begin Patch")
	codexPatchPathPrefix = []string{"*** Update File: ", "*** Add File: ", "*** Delete File: ", "*** Move to: "}
)

// codexTranscriptFileChanges extracts the paths a patch tool call touched. Codex
// applies patches through apply_patch, and its code-mode exec tool embeds the
// same patch text inside a script string, where line breaks are escaped.
func codexTranscriptFileChanges(envelope codexTranscriptEnvelope) []core.SessionFileChange {
	if envelope.Type != "response_item" || !bytes.Contains(envelope.Payload, codexPatchMarker) {
		return nil
	}

	var payload struct {
		Type      string `json:"type"`
		Input     string `json:"input"`
		Arguments string `json:"arguments"`
	}
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return nil
	}

	var patch string
	switch payload.Type {
	case "custom_tool_call":
		patch = payload.Input
	case "function_call":
		patch = payload.Arguments
	default:
		return nil
	}

	var changes []core.SessionFileChange
	for _, path := range patchedPaths(patch) {
		changes = append(changes, core.SessionFileChange{ObservedAt: envelope.Timestamp, Path: path})
	}
	return changes
}

func patchedPaths(patch string) []string {
	var paths []string
	for _, line := range splitPatchLines(patch) {
		for _, prefix := range codexPatchPathPrefix {
			if rest, ok := strings.CutPrefix(strings.TrimSpace(line), prefix); ok {
				if path := strings.TrimSpace(strings.Trim(rest, `"`)); path != "" {
					paths = append(paths, path)
				}
				break
			}
		}
	}
	return paths
}

// splitPatchLines splits on real line breaks and on the escaped "\n" a patch
// carries when it is embedded in a script string.
func splitPatchLines(patch string) []string {
	return strings.Split(strings.ReplaceAll(patch, `\n`, "\n"), "\n")
}
