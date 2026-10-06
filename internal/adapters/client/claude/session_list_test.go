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

func TestListFolderSessions_IncludesSessionsStartedBelowTheFolder(t *testing.T) {
	configDir := t.TempDir()
	projects := filepath.Join(configDir, "projects")
	promptIn := func(cwd string, text string) string {
		return `{"type":"user","cwd":"` + cwd + `","message":{"role":"user","content":"` + text + `"}}`
	}
	base := time.Date(2026, time.October, 2, 9, 0, 0, 0, time.UTC)
	write := func(project string, id string, minute int, lines ...string) {
		path := filepath.Join(projects, project, id+".jsonl")
		writeTranscriptAt(t, path, lines...)
		modTime := base.Add(time.Duration(minute) * time.Minute)
		require.NoError(t, os.Chtimes(path, modTime, modTime))
	}
	// Started in the folder, then moved into a worktree: it resumes from where
	// it started.
	write("-src-work-code", "moved", 0,
		promptIn("/src/work/code", "plan the service work"),
		promptIn("/src/work/code/service-2", "now edit it"))
	write("-src-work-code-service", "below", 1, promptIn("/src/work/code/service", "review the service"))
	// Sessions whose project dir shares the folder's encoding but which ran
	// elsewhere: a sibling, and a folder that differs only by "/" versus "-".
	write("-src-work-code-old", "sibling", 2, promptIn("/src/work/code-old", "old checkout"))
	write("-src-work-code", "lookalike", 3, promptIn("/src/work-code", "other repo"))
	// Without a recorded folder only an exact project dir places a session.
	write("-src-work-code-old", "unplaced", 5,
		`{"type":"user","message":{"role":"user","content":"no folder recorded"}}`)
	// A later line can name the project dir when the first does not.
	write("-src-work-code-api", "named", 4,
		promptIn("/src/work/code", "injected first"),
		promptIn("/src/work/code/api", "fix the api"))

	repo := &repository{claudeConfigDir: func() (string, error) { return configDir, nil }}
	found, err := repo.ListFolderSessions(t.Context(), "/src/work/code", 10)

	require.NoError(t, err)
	got := make(map[string]string, len(found))
	for _, session := range found {
		got[session.SessionID] = session.Cwd
	}
	require.Equal(t, map[string]string{
		"named": "/src/work/code/api",
		"below": "/src/work/code/service",
		"moved": "/src/work/code",
	}, got)
	require.Equal(t, "named", found[0].SessionID, "newest first across project dirs")
	require.Equal(t, filepath.Join(projects, "-src-work-code-service", "below.jsonl"), found[1].TranscriptPath)
}

func TestListFolderSessions_FromASubfolderSkipsItsParentsSessions(t *testing.T) {
	configDir := t.TempDir()
	writeTranscriptAt(t, filepath.Join(configDir, "projects", "-src-work-code", "parent.jsonl"),
		`{"type":"user","cwd":"/src/work/code","message":{"role":"user","content":"parent work"}}`)
	repo := &repository{claudeConfigDir: func() (string, error) { return configDir, nil }}

	found, err := repo.ListFolderSessions(t.Context(), "/src/work/code/service", 10)

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
