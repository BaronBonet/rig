package claude

import (
	"testing"
	"time"

	"github.com/BaronBonet/rig/internal/core"
	"github.com/BaronBonet/rig/internal/pkg/subprocess"

	"github.com/stretchr/testify/require"
)

func claudeSession(transcriptPath string) core.TaskProviderSession {
	return core.TaskProviderSession{
		TaskID:            "task-1",
		Provider:          core.ProviderClaude,
		ProviderSessionID: "sess-1",
		TranscriptPath:    transcriptPath,
	}
}

func TestReadSessionActivity_RecoversUserPromptsAndAssistantMessages(t *testing.T) {
	repo, _ := newTestRepository(t, subprocess.NewMockRunner(t))
	transcript := writeTranscript(t,
		`{"type":"user","timestamp":"2026-07-04T08:21:23.479Z",`+
			`"message":{"role":"user","content":"test that switching works"}}`,
		`{"type":"assistant","timestamp":"2026-07-04T08:21:30.759Z",`+
			`"message":{"id":"msg_1","content":[{"type":"thinking","thinking":"..."}]}}`,
		`{"type":"assistant","timestamp":"2026-07-04T08:21:30.765Z",`+
			`"message":{"id":"msg_1","content":[{"type":"text","text":"Hello\nHello"}]}}`,
		`{"type":"assistant","timestamp":"2026-07-04T08:21:35.100Z",`+
			`"message":{"id":"msg_2","content":[{"type":"tool_use","name":"Bash",`+
			`"input":{"command":"echo hello"}}]}}`,
		`{"type":"user","timestamp":"2026-07-04T08:22:43.465Z",`+
			`"message":{"role":"user","content":"do it again"}}`,
	)

	events, err := repo.ReadSessionActivity(t.Context(), claudeSession(transcript), time.Time{})

	require.NoError(t, err)
	require.Len(t, events, 4)
	require.Equal(t, core.TaskActivityRoleUser, events[0].Role)
	require.Equal(t, "test that switching works", events[0].Text)
	require.Equal(t, core.TaskActivityRoleAssistant, events[1].Role)
	require.Equal(t, "Hello Hello", events[1].Text)
	require.Equal(t, core.TaskActivityRoleAssistant, events[2].Role)
	require.Equal(t, "echo hello", events[2].Text)
	require.Equal(t, core.TaskActivityRoleUser, events[3].Role)
	require.Equal(t, "do it again", events[3].Text)
}

func TestReadSessionActivity_ReturnsOnlyEventsAfterTheSuppliedTimestamp(t *testing.T) {
	repo, _ := newTestRepository(t, subprocess.NewMockRunner(t))
	transcript := writeTranscript(t,
		`{"type":"user","timestamp":"2026-07-04T08:21:23.479Z",`+
			`"message":{"role":"user","content":"already recorded by hooks"}}`,
		`{"type":"user","timestamp":"2026-07-04T08:22:43.465Z",`+
			`"message":{"role":"user","content":"lost while the daemon was down"}}`,
	)
	after := time.Date(2026, 7, 4, 8, 21, 30, 0, time.UTC)

	events, err := repo.ReadSessionActivity(t.Context(), claudeSession(transcript), after)

	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "lost while the daemon was down", events[0].Text)
}

func TestReadSessionActivity_SkipsToolResultsMetaAndSidechainEntries(t *testing.T) {
	repo, _ := newTestRepository(t, subprocess.NewMockRunner(t))
	transcript := writeTranscript(t,
		// Tool results come back as user entries with block content.
		`{"type":"user","timestamp":"2026-07-04T08:21:31.000Z",`+
			`"message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}`,
		`{"type":"user","isMeta":true,"timestamp":"2026-07-04T08:21:32.000Z",`+
			`"message":{"role":"user","content":"injected context"}}`,
		`{"type":"assistant","isSidechain":true,"timestamp":"2026-07-04T08:21:33.000Z",`+
			`"message":{"id":"msg_side","content":[{"type":"text","text":"subagent chatter"}]}}`,
		`{"type":"assistant","timestamp":"2026-07-04T08:21:34.000Z",`+
			`"message":{"id":"msg_3","content":[{"type":"tool_use","name":"Read",`+
			`"input":{"file_path":"/tmp/x"}}]}}`,
		`{"type":"system","timestamp":"2026-07-04T08:21:35.000Z"}`,
	)

	events, err := repo.ReadSessionActivity(t.Context(), claudeSession(transcript), time.Time{})

	require.NoError(t, err)
	require.Empty(t, events)
}

func TestReadSessionActivity_ToleratesMissingTranscriptAndTaskID(t *testing.T) {
	repo, _ := newTestRepository(t, subprocess.NewMockRunner(t))

	events, err := repo.ReadSessionActivity(t.Context(), claudeSession("/tmp/does-not-exist.jsonl"), time.Time{})
	require.NoError(t, err)
	require.Empty(t, events)

	session := claudeSession("/tmp/irrelevant.jsonl")
	session.TaskID = " "
	events, err = repo.ReadSessionActivity(t.Context(), session, time.Time{})
	require.NoError(t, err)
	require.Empty(t, events)
}
