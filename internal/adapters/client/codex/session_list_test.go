package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/BaronBonet/rig/internal/core"
)

func writeRollout(t *testing.T, path string, modTime time.Time, lines ...string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600))
	require.NoError(t, os.Chtimes(path, modTime, modTime))
}

func sessionMeta(id string, cwd string, source string) string {
	return `{"type":"session_meta","payload":{"id":"` + id + `","cwd":"` + cwd + `","source":` + source + `}}`
}

func TestListFolderSessions_ListsRootCodexSessionsStartedInTheFolder(t *testing.T) {
	codexHome := t.TempDir()
	day := filepath.Join(codexHome, "sessions", "2026", "10", "02")
	base := time.Date(2026, time.October, 2, 9, 0, 0, 0, time.UTC)

	// Codex CLI records the prompt as a user_message event.
	writeRollout(t, filepath.Join(day, "rollout-cli.jsonl"), base,
		sessionMeta("thread-cli", "/src/work/code", `"cli"`),
		`{"type":"event_msg","payload":{"type":"user_message","message":"review the service endpoints"}}`,
	)
	// Codex Desktop records it as a user message after injected context.
	writeRollout(t, filepath.Join(day, "rollout-desktop.jsonl"), base.Add(time.Minute),
		sessionMeta("thread-desktop", "/src/work/code", `"vscode"`),
		`{"type":"response_item","payload":{"type":"message","role":"user",`+
			`"content":[{"type":"input_text","text":"<environment_context>cwd</environment_context>"}]}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user",`+
			`"content":[{"type":"input_text","text":"investigate the failing import job"}]}}`,
	)
	writeRollout(t, filepath.Join(day, "rollout-subagent.jsonl"), base.Add(2*time.Minute),
		sessionMeta("thread-sub", "/src/work/code", `{"subagent":{"thread_spawn":{"parent_thread_id":"thread-cli"}}}`),
		`{"type":"event_msg","payload":{"type":"user_message","message":"subagent work"}}`,
	)
	writeRollout(t, filepath.Join(day, "rollout-elsewhere.jsonl"), base.Add(3*time.Minute),
		sessionMeta("thread-other", "/src/other/code", `"cli"`),
		`{"type":"event_msg","payload":{"type":"user_message","message":"other folder"}}`,
	)

	repo := &repository{codexHomeDir: func() (string, error) { return codexHome, nil }}
	found, err := repo.ListFolderSessions(t.Context(), "/src/work/code", 10, nil)

	require.NoError(t, err)
	require.Len(t, found, 2)
	require.Equal(t, core.ProviderSessionSummary{
		LastActiveAt: base.Add(time.Minute), Provider: core.ProviderCodex, SessionID: "thread-desktop",
		Title: "investigate the failing import job", Cwd: "/src/work/code",
		TranscriptPath: filepath.Join(day, "rollout-desktop.jsonl"),
	}, withUTC(found[0]))
	require.Equal(t, "thread-cli", found[1].SessionID)
	require.Equal(t, "review the service endpoints", found[1].Title)
}

func TestListFolderSessions_IncludesSessionsStartedBelowTheFolder(t *testing.T) {
	codexHome := t.TempDir()
	day := filepath.Join(codexHome, "sessions", "2026", "10", "02")
	base := time.Date(2026, time.October, 2, 9, 0, 0, 0, time.UTC)
	prompt := `{"type":"event_msg","payload":{"type":"user_message","message":"work"}}`
	writeRollout(t, filepath.Join(day, "rollout-below.jsonl"), base,
		sessionMeta("thread-below", "/src/work/code/service", `"cli"`), prompt)
	writeRollout(t, filepath.Join(day, "rollout-sibling.jsonl"), base.Add(time.Minute),
		sessionMeta("thread-sibling", "/src/work/code-old", `"cli"`), prompt)
	writeRollout(t, filepath.Join(day, "rollout-parent.jsonl"), base.Add(2*time.Minute),
		sessionMeta("thread-parent", "/src/work", `"cli"`), prompt)
	writeRollout(t, filepath.Join(day, "rollout-relative.jsonl"), base.Add(3*time.Minute),
		sessionMeta("thread-relative", "service", `"cli"`), prompt)

	repo := &repository{codexHomeDir: func() (string, error) { return codexHome, nil }}
	found, err := repo.ListFolderSessions(t.Context(), "/src/work/code", 10, nil)

	require.NoError(t, err)
	require.Len(t, found, 1)
	require.Equal(t, "thread-below", found[0].SessionID)
	require.Equal(t, "/src/work/code/service", found[0].Cwd)
}

func TestListFolderSessions_ReadsTheHomeTheEnvSelects(t *testing.T) {
	daemonHome, workHome := t.TempDir(), t.TempDir()
	base := time.Date(2026, time.October, 2, 9, 0, 0, 0, time.UTC)
	writeRollout(t, filepath.Join(workHome, "sessions", "2026", "10", "02", "rollout-work.jsonl"), base,
		sessionMeta("thread-work", "/src/work/code", `"cli"`),
		`{"type":"event_msg","payload":{"type":"user_message","message":"work"}}`)
	repo := &repository{codexHomeDir: func() (string, error) { return daemonHome, nil }}

	found, err := repo.ListFolderSessions(t.Context(), "/src/work/code", 10, core.ProviderEnv{"CODEX_HOME": workHome})
	require.NoError(t, err)
	require.Len(t, found, 1)
	require.Equal(t, "thread-work", found[0].SessionID)

	found, err = repo.ListFolderSessions(t.Context(), "/src/work/code", 10, nil)
	require.NoError(t, err)
	require.Empty(t, found)
}

func TestListFolderSessions_ToleratesAMissingSessionsFolder(t *testing.T) {
	repo := &repository{codexHomeDir: func() (string, error) { return t.TempDir(), nil }}

	found, err := repo.ListFolderSessions(t.Context(), "/src/work/code", 10, nil)

	require.NoError(t, err)
	require.Empty(t, found)
}

func withUTC(session core.ProviderSessionSummary) core.ProviderSessionSummary {
	session.LastActiveAt = session.LastActiveAt.UTC()
	return session
}
