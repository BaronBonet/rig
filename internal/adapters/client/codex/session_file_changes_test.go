package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/BaronBonet/rig/internal/core"
)

func rolloutLine(t *testing.T, timestamp string, payload map[string]any) string {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{
		"timestamp": timestamp,
		"type":      "response_item",
		"payload":   payload,
	})
	require.NoError(t, err)
	return string(encoded)
}

func TestReadSessionFileChanges_ReadsPatchedPathsFromRollout(t *testing.T) {
	applyPatch := "*** Begin Patch\n" +
		"*** Update File: /src/service/internal/core/service.go\n@@\n-old\n+new\n" +
		"*** Add File: docs/adr/0001.md\n+# ADR\n" +
		"*** Update File: /src/service/old_name.go\n*** Move to: /src/service/new_name.go\n" +
		"*** End Patch"
	// Codex's code-mode exec tool embeds the patch in a script string, so its
	// line breaks arrive escaped.
	execScript := `const patch = "*** Begin Patch\n*** Delete File: /src/service-2/legacy.go\n*** End Patch";` +
		` await tools.apply_patch(patch);`
	lines := []string{
		rolloutLine(t, "2026-10-02T09:01:00Z", map[string]any{
			"type": "custom_tool_call", "name": "apply_patch", "input": applyPatch,
		}),
		rolloutLine(t, "2026-10-02T09:02:00Z", map[string]any{
			"type": "custom_tool_call", "name": "exec", "input": execScript,
		}),
		// Assistant text that quotes a patch is not an edit.
		rolloutLine(t, "2026-10-02T09:03:00Z", map[string]any{
			"type": "message", "role": "assistant",
			"content": []map[string]any{{"type": "output_text", "text": "*** Begin Patch\n*** Update File: /x.go"}},
		}),
	}
	rollout := filepath.Join(t.TempDir(), "rollout.jsonl")
	require.NoError(t, os.WriteFile(rollout, []byte(strings.Join(lines, "\n")+"\n"), 0o600))

	repo := &repository{}
	changes, err := repo.ReadSessionFileChanges(t.Context(), core.TaskProviderSession{
		TranscriptPath: rollout,
		Cwd:            "/src/service",
	})

	require.NoError(t, err)
	first := time.Date(2026, 10, 2, 9, 1, 0, 0, time.UTC)
	require.Equal(t, []core.SessionFileChange{
		{ObservedAt: first, Path: "/src/service/internal/core/service.go"},
		{ObservedAt: first, Path: "/src/service/docs/adr/0001.md"},
		{ObservedAt: first, Path: "/src/service/old_name.go"},
		{ObservedAt: first, Path: "/src/service/new_name.go"},
		{ObservedAt: time.Date(2026, 10, 2, 9, 2, 0, 0, time.UTC), Path: "/src/service-2/legacy.go"},
	}, changes)
}

func TestReadSessionFileChanges_ReadsPatchesAppendedToTheRollout(t *testing.T) {
	rollout := filepath.Join(t.TempDir(), "rollout.jsonl")
	patch := func(path string) string {
		return rolloutLine(t, "2026-10-02T09:01:00Z", map[string]any{
			"type": "custom_tool_call", "name": "apply_patch",
			"input": "*** Begin Patch\n*** Update File: " + path + "\n*** End Patch",
		})
	}
	require.NoError(t, os.WriteFile(rollout, []byte(patch("/src/a.go")+"\n"), 0o600))
	repo := &repository{}
	session := core.TaskProviderSession{TranscriptPath: rollout}

	first, err := repo.ReadSessionFileChanges(t.Context(), session)
	require.NoError(t, err)
	require.Len(t, first, 1)

	file, err := os.OpenFile(rollout, os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, err = file.WriteString(patch("/src/b.go") + "\n")
	require.NoError(t, err)
	require.NoError(t, file.Close())

	second, err := repo.ReadSessionFileChanges(t.Context(), session)
	require.NoError(t, err)
	require.Equal(t, []string{"/src/a.go", "/src/b.go"}, []string{second[0].Path, second[1].Path})
}
