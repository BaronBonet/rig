package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/BaronBonet/rig/internal/core"
)

func writeTranscriptAt(t *testing.T, path string, lines ...string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600))
}

func toolUseLine(timestamp string, id string, name string, input string) string {
	return `{"type":"assistant","timestamp":"` + timestamp + `","message":{"content":[` +
		`{"type":"tool_use","id":"` + id + `","name":"` + name + `","input":` + input + `}]}}`
}

func TestReadSessionFileChanges_ReadsEditsFromSessionAndSubagentTranscripts(t *testing.T) {
	dir := t.TempDir()
	transcript := filepath.Join(dir, "sess-1.jsonl")
	writeTranscriptAt(t, transcript,
		`{"type":"user","timestamp":"2026-10-02T09:00:00Z","message":{"content":"fix the null check"}}`,
		toolUseLine("2026-10-02T09:01:00Z", "tu-1", "Edit", `{"file_path":"/src/api-search/app/models/user.rb"}`),
		toolUseLine("2026-10-02T09:02:00Z", "tu-2", "Bash", `{"command":"sed -i '' s/a/b/ /src/api-search/x.rb"}`),
		toolUseLine("2026-10-02T09:03:00Z", "tu-3", "Read", `{"file_path":"/src/api-search/README.md"}`),
		toolUseLine("2026-10-02T09:04:00Z", "tu-4", "Write", `{"file_path":"notes/plan.md"}`),
		// An older Claude Code inlined this subagent call as a sidechain entry;
		// it also appears in the subagent transcript and must count once.
		toolUseLine("2026-10-02T09:05:00Z", "tu-5", "MultiEdit", `{"file_path":"/src/web-search/routes.tsx"}`),
	)
	writeTranscriptAt(t, filepath.Join(dir, "sess-1", "subagents", "agent-a1.jsonl"),
		toolUseLine("2026-10-02T09:05:00Z", "tu-5", "MultiEdit", `{"file_path":"/src/web-search/routes.tsx"}`),
		toolUseLine("2026-10-02T09:06:00Z", "tu-6", "NotebookEdit", `{"notebook_path":"/src/analysis/a.ipynb"}`),
	)

	repo := &repository{}
	changes, err := repo.ReadSessionFileChanges(t.Context(), core.TaskProviderSession{
		TranscriptPath: transcript,
		Cwd:            "/src/api-search",
	})

	require.NoError(t, err)
	require.Equal(t, []core.SessionFileChange{
		{ObservedAt: time.Date(2026, 10, 2, 9, 1, 0, 0, time.UTC), Path: "/src/api-search/app/models/user.rb"},
		{ObservedAt: time.Date(2026, 10, 2, 9, 4, 0, 0, time.UTC), Path: "/src/api-search/notes/plan.md"},
		{ObservedAt: time.Date(2026, 10, 2, 9, 5, 0, 0, time.UTC), Path: "/src/web-search/routes.tsx"},
		{ObservedAt: time.Date(2026, 10, 2, 9, 6, 0, 0, time.UTC), Path: "/src/analysis/a.ipynb"},
	}, changes)
}

func TestReadSessionFileChanges_PicksUpEditsAppendedAfterAnEarlierRead(t *testing.T) {
	transcript := filepath.Join(t.TempDir(), "sess-1.jsonl")
	writeTranscriptAt(t, transcript,
		toolUseLine("2026-10-02T09:01:00Z", "tu-1", "Edit", `{"file_path":"/src/api/a.rb"}`),
	)
	repo := &repository{}
	session := core.TaskProviderSession{TranscriptPath: transcript}

	first, err := repo.ReadSessionFileChanges(t.Context(), session)
	require.NoError(t, err)
	require.Len(t, first, 1)

	writeTranscriptAt(t, transcript,
		toolUseLine("2026-10-02T09:01:00Z", "tu-1", "Edit", `{"file_path":"/src/api/a.rb"}`),
		toolUseLine("2026-10-02T09:02:00Z", "tu-2", "Edit", `{"file_path":"/src/api/b.rb"}`),
	)
	second, err := repo.ReadSessionFileChanges(t.Context(), session)
	require.NoError(t, err)
	require.Len(t, second, 2)
}

func TestReadSessionFileChanges_ToleratesAMissingTranscript(t *testing.T) {
	repo := &repository{}

	changes, err := repo.ReadSessionFileChanges(t.Context(), core.TaskProviderSession{
		TranscriptPath: filepath.Join(t.TempDir(), "gone.jsonl"),
	})

	require.NoError(t, err)
	require.Empty(t, changes)
}
