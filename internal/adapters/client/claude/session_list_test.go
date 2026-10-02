package claude

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/BaronBonet/rig/internal/core"
)

func TestListFolderSessions_TitlesSessionsLikeClaudeCodeAndSortsByActivity(t *testing.T) {
	configDir := t.TempDir()
	projectDir := filepath.Join(configDir, "projects", "-src-project-code")
	prompt := func(text string) string {
		return `{"type":"user","message":{"role":"user","content":"` + text + `"}}`
	}
	sessions := map[string][]string{
		"custom": {
			prompt("fix the date format"),
			`{"type":"ai-title","aiTitle":"Email URL fixes"}`,
			`{"type":"custom-title","customTitle":"fix-date-format"}`,
		},
		"generated": {prompt("explore rig"), `{"type":"ai-title","aiTitle":"Rig CLI exploration"}`},
		"prompted": {
			`{"type":"user","isMeta":true,"message":{"role":"user","content":"injected context"}}`,
			prompt("<command-name>/effort</command-name>"),
			prompt("build the search integration"),
		},
		"empty": {`{"type":"system","subtype":"init"}`},
	}
	base := time.Date(2026, time.October, 2, 9, 0, 0, 0, time.UTC)
	for index, id := range []string{"prompted", "generated", "custom", "empty"} {
		path := filepath.Join(projectDir, id+".jsonl")
		writeTranscriptAt(t, path, sessions[id]...)
		modTime := base.Add(time.Duration(index) * time.Minute)
		require.NoError(t, os.Chtimes(path, modTime, modTime))
	}
	// Subagent transcripts live in per-session folders and are not sessions.
	writeTranscriptAt(t, filepath.Join(projectDir, "custom", "subagents", "agent-a1.jsonl"), prompt("subagent"))

	repo := &repository{claudeConfigDir: func() (string, error) { return configDir, nil }}
	found, err := repo.ListFolderSessions(t.Context(), "/src/project/code", 10)

	require.NoError(t, err)
	require.Equal(t, []core.ProviderSessionSummary{
		{
			LastActiveAt: base.Add(2 * time.Minute), Provider: core.ProviderClaude, SessionID: "custom",
			Title: "fix-date-format", Cwd: "/src/project/code", TranscriptPath: filepath.Join(projectDir, "custom.jsonl"),
		},
		{
			LastActiveAt: base.Add(time.Minute), Provider: core.ProviderClaude, SessionID: "generated",
			Title: "Rig CLI exploration", Cwd: "/src/project/code",
			TranscriptPath: filepath.Join(projectDir, "generated.jsonl"),
		},
		{
			LastActiveAt: base, Provider: core.ProviderClaude, SessionID: "prompted",
			Title: "build the search integration", Cwd: "/src/project/code",
			TranscriptPath: filepath.Join(projectDir, "prompted.jsonl"),
		},
	}, normalizeTimes(found))

	limited, err := repo.ListFolderSessions(t.Context(), "/src/project/code", 1)
	require.NoError(t, err)
	require.Len(t, limited, 1)
}

func TestListFolderSessions_ReturnsNothingForAFolderWithoutSessions(t *testing.T) {
	repo := &repository{claudeConfigDir: func() (string, error) { return t.TempDir(), nil }}

	found, err := repo.ListFolderSessions(t.Context(), "/src/never-used", 10)

	require.NoError(t, err)
	require.Empty(t, found)
}

func TestClaudeProjectDirName_ReplacesEveryNonAlphanumericCharacter(t *testing.T) {
	require.Equal(t, "-Users-me-dev-project-code", claudeProjectDirName("/Users/me/dev/project/code"))
	require.Equal(
		t,
		"-Users-me--claude-worktrees-api-fix-x",
		claudeProjectDirName("/Users/me/.claude-worktrees/api/fix_x"),
	)
}

func normalizeTimes(sessions []core.ProviderSessionSummary) []core.ProviderSessionSummary {
	for index := range sessions {
		sessions[index].LastActiveAt = sessions[index].LastActiveAt.UTC()
	}
	return sessions
}
