package claude

import (
	"strings"
	"testing"
	"time"

	"github.com/BaronBonet/rig/internal/core"
	"github.com/BaronBonet/rig/internal/pkg/subprocess"

	"github.com/stretchr/testify/require"
)

// Transcript entries shaped like Claude Code 2.1.2xx output: a prompt whose
// turn runs a Bash command, then the user interrupts it with Esc.
const (
	transcriptPrompt = `{"type":"user","isSidechain":false,"timestamp":"2026-07-04T08:21:00.000Z",` +
		`"message":{"role":"user","content":"fix the flaky test"}}`
	transcriptToolUse = `{"type":"assistant","isSidechain":false,"timestamp":"2026-07-04T08:21:05.000Z",` +
		`"message":{"id":"msg_1","role":"assistant","content":[{"type":"tool_use","id":"toolu_1",` +
		`"name":"Bash","input":{"command":"go test ./..."}}]}}`
	transcriptInterrupt = `{"type":"user","isSidechain":false,"timestamp":"2026-07-04T08:21:10.000Z",` +
		`"message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user]"}]}}`
	// Bookkeeping Claude Code appends after a turn ends.
	transcriptTurnDuration = `{"type":"system","subtype":"turn_duration","isSidechain":false,` +
		`"timestamp":"2026-07-04T08:21:10.100Z"}`
	transcriptLastPrompt = `{"type":"last-prompt","lastPrompt":"fix the flaky test"}`
	// The next prompt and the assistant output of its turn.
	transcriptNextPrompt = `{"type":"user","isSidechain":false,"timestamp":"2026-07-04T08:21:20.000Z",` +
		`"message":{"role":"user","content":"run only the billing tests"}}`
	transcriptNextToolUse = `{"type":"assistant","isSidechain":false,"timestamp":"2026-07-04T08:21:25.000Z",` +
		`"message":{"id":"msg_2","role":"assistant","content":[{"type":"tool_use","id":"toolu_2",` +
		`"name":"Bash","input":{"command":"go test ./billing/..."}}]}}`
)

var (
	fixturePreToolUseAt  = time.Date(2026, 7, 4, 8, 21, 5, 500_000_000, time.UTC)
	fixtureInterruptedAt = time.Date(2026, 7, 4, 8, 21, 10, 0, time.UTC)
	fixtureNextPromptAt  = time.Date(2026, 7, 4, 8, 21, 20, 0, time.UTC)
)

func taskStatus(phase core.TaskStatusPhase, observedAt time.Time) core.TaskStatusUpdate {
	return core.TaskStatusUpdate{
		TaskID:       "task-1",
		Provider:     core.ProviderClaude,
		Phase:        phase,
		RawEventName: "PreToolUse",
		ObservedAt:   observedAt,
	}
}

func TestRecoverLatestTaskStatus_InterruptedTurnNeedsInput(t *testing.T) {
	repo, _ := newTestRepository(t, subprocess.NewMockRunner(t))
	interruptedToolUse := `{"type":"user","isSidechain":false,"timestamp":"2026-07-04T08:21:10.000Z",` +
		`"interruptedMessageId":"msg_1","message":{"role":"user",` +
		`"content":[{"type":"text","text":"[Request interrupted by user for tool use]"}]}}`
	rejectedToolResult := `{"type":"user","isSidechain":false,"timestamp":"2026-07-04T08:21:09.900Z",` +
		`"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1",` +
		`"is_error":true,"content":"The user doesn't want to proceed with this tool use."}]}}`

	for name, lines := range map[string][]string{
		"during a response": {
			transcriptPrompt, transcriptToolUse, transcriptInterrupt, transcriptTurnDuration, transcriptLastPrompt,
		},
		"during a tool call": {
			transcriptPrompt, transcriptToolUse, rejectedToolResult, interruptedToolUse, transcriptTurnDuration,
		},
	} {
		transcript := writeTranscript(t, lines...)

		recovered, err := repo.RecoverLatestTaskStatus(
			t.Context(),
			taskStatus(core.TaskStatusPhaseWorking, fixturePreToolUseAt),
			[]core.TaskProviderSession{claudeSession(transcript)},
			time.Time{},
		)

		require.NoError(t, err, name)
		require.Equal(t, &core.TaskStatusUpdate{
			TaskID:       "task-1",
			Provider:     core.ProviderClaude,
			Phase:        core.TaskStatusPhaseWaitingForInput,
			RawEventName: "TranscriptTurnInterrupted",
			ObservedAt:   fixtureInterruptedAt,
		}, recovered, name)
	}
}

func TestRecoverLatestTaskStatus_NextPromptHookWinsOverTheInterrupt(t *testing.T) {
	repo, _ := newTestRepository(t, subprocess.NewMockRunner(t))
	// The next prompt's UserPromptSubmit hook is persisted before Claude Code
	// writes the prompt to the transcript, and the two race.
	for name, lines := range map[string][]string{
		"prompt not yet in transcript": {transcriptPrompt, transcriptToolUse, transcriptInterrupt},
		"prompt in transcript":         {transcriptPrompt, transcriptToolUse, transcriptInterrupt, transcriptNextPrompt},
	} {
		transcript := writeTranscript(t, lines...)

		recovered, err := repo.RecoverLatestTaskStatus(
			t.Context(),
			taskStatus(core.TaskStatusPhaseWorking, fixtureNextPromptAt),
			[]core.TaskProviderSession{claudeSession(transcript)},
			time.Time{},
		)

		require.NoError(t, err, name)
		require.Nil(t, recovered, name)
	}
}

func TestRecoverLatestTaskStatus_TurnAfterTheInterruptKeepsWorking(t *testing.T) {
	repo, _ := newTestRepository(t, subprocess.NewMockRunner(t))
	// The daemon missed the next prompt's hooks, but the transcript shows the
	// agent working on it.
	transcript := writeTranscript(t,
		transcriptPrompt, transcriptToolUse, transcriptInterrupt, transcriptNextPrompt, transcriptNextToolUse,
	)

	recovered, err := repo.RecoverLatestTaskStatus(
		t.Context(),
		taskStatus(core.TaskStatusPhaseWorking, fixturePreToolUseAt),
		[]core.TaskProviderSession{claudeSession(transcript)},
		time.Time{},
	)

	require.NoError(t, err)
	require.Nil(t, recovered)
}

func TestRecoverLatestTaskStatus_LocalCommandAfterTheInterruptStillNeedsInput(t *testing.T) {
	repo, _ := newTestRepository(t, subprocess.NewMockRunner(t))
	// Local slash commands are recorded as user entries but start no turn.
	transcript := writeTranscript(t,
		transcriptPrompt, transcriptToolUse, transcriptInterrupt,
		`{"type":"user","isSidechain":false,"timestamp":"2026-07-04T08:21:15.000Z",`+
			`"message":{"role":"user","content":"<command-name>/cost</command-name>"}}`,
		`{"type":"user","isSidechain":false,"timestamp":"2026-07-04T08:21:15.100Z",`+
			`"message":{"role":"user","content":"<local-command-stdout>Total cost: $0.12</local-command-stdout>"}}`,
	)

	recovered, err := repo.RecoverLatestTaskStatus(
		t.Context(),
		taskStatus(core.TaskStatusPhaseWorking, fixturePreToolUseAt),
		[]core.TaskProviderSession{claudeSession(transcript)},
		time.Time{},
	)

	require.NoError(t, err)
	require.NotNil(t, recovered)
	require.Equal(t, core.TaskStatusPhaseWaitingForInput, recovered.Phase)
	require.Equal(t, fixtureInterruptedAt, recovered.ObservedAt)
}

func TestRecoverLatestTaskStatus_IgnoresSubagentInterrupts(t *testing.T) {
	repo, _ := newTestRepository(t, subprocess.NewMockRunner(t))
	transcript := writeTranscript(t,
		transcriptPrompt, transcriptToolUse,
		`{"type":"user","isSidechain":true,"agentId":"a1","timestamp":"2026-07-04T08:21:10.000Z",`+
			`"message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user]"}]}}`,
	)

	recovered, err := repo.RecoverLatestTaskStatus(
		t.Context(),
		taskStatus(core.TaskStatusPhaseWorking, fixturePreToolUseAt),
		[]core.TaskProviderSession{claudeSession(transcript)},
		time.Time{},
	)

	require.NoError(t, err)
	require.Nil(t, recovered)
}

func TestRecoverLatestTaskStatus_RepairsOnlyInProgressStatus(t *testing.T) {
	repo, _ := newTestRepository(t, subprocess.NewMockRunner(t))
	transcript := writeTranscript(t, transcriptPrompt, transcriptToolUse, transcriptInterrupt)
	sessions := []core.TaskProviderSession{claudeSession(transcript)}

	for _, phase := range []core.TaskStatusPhase{
		core.TaskStatusPhaseStarting,
		core.TaskStatusPhaseWorking,
		core.TaskStatusPhaseWorkingInBackground,
	} {
		recovered, err := repo.RecoverLatestTaskStatus(
			t.Context(), taskStatus(phase, fixturePreToolUseAt), sessions, time.Time{},
		)
		require.NoError(t, err, phase)
		require.NotNil(t, recovered, phase)
		require.Equal(t, core.TaskStatusPhaseWaitingForInput, recovered.Phase, phase)
	}

	for _, phase := range []core.TaskStatusPhase{core.TaskStatusPhaseWaitingForInput, core.TaskStatusPhaseStopped} {
		recovered, err := repo.RecoverLatestTaskStatus(
			t.Context(), taskStatus(phase, fixturePreToolUseAt), sessions, time.Time{},
		)
		require.NoError(t, err, phase)
		require.Nil(t, recovered, phase)
	}
}

func TestRecoverLatestTaskStatus_ReadsTheNewestClaudeSessionTranscript(t *testing.T) {
	repo, _ := newTestRepository(t, subprocess.NewMockRunner(t))
	interrupted := writeTranscript(t, transcriptPrompt, transcriptToolUse, transcriptInterrupt)
	working := writeTranscript(t, transcriptPrompt, transcriptToolUse)
	session := func(provider core.Provider, transcriptPath string, lastObservedAt time.Time) core.TaskProviderSession {
		s := claudeSession(transcriptPath)
		s.Provider = provider
		s.LastObservedAt = lastObservedAt
		return s
	}
	older := time.Date(2026, 7, 4, 8, 0, 0, 0, time.UTC)
	newer := older.Add(time.Minute)
	newest := newer.Add(time.Minute)
	current := taskStatus(core.TaskStatusPhaseWorking, fixturePreToolUseAt)

	// A conversation started with /clear replaces the interrupted one.
	recovered, err := repo.RecoverLatestTaskStatus(t.Context(), current, []core.TaskProviderSession{
		session(core.ProviderClaude, interrupted, older),
		session(core.ProviderClaude, working, newer),
		session(core.ProviderCodex, interrupted, newest),
	}, time.Time{})
	require.NoError(t, err)
	require.Nil(t, recovered)

	recovered, err = repo.RecoverLatestTaskStatus(t.Context(), current, []core.TaskProviderSession{
		session(core.ProviderClaude, working, older),
		session(core.ProviderClaude, interrupted, newer),
	}, time.Time{})
	require.NoError(t, err)
	require.NotNil(t, recovered)
	require.Equal(t, core.TaskStatusPhaseWaitingForInput, recovered.Phase)
}

func TestRecoverLatestTaskStatus_ToleratesMissingTranscripts(t *testing.T) {
	repo, _ := newTestRepository(t, subprocess.NewMockRunner(t))
	current := taskStatus(core.TaskStatusPhaseWorking, fixturePreToolUseAt)

	recovered, err := repo.RecoverLatestTaskStatus(t.Context(), current, nil, time.Time{})
	require.NoError(t, err)
	require.Nil(t, recovered)

	recovered, err = repo.RecoverLatestTaskStatus(t.Context(), current, []core.TaskProviderSession{
		claudeSession("/tmp/does-not-exist.jsonl"),
	}, time.Time{})
	require.NoError(t, err)
	require.Nil(t, recovered)
}

func TestTranscriptInterruptedAt_ReadsOnlyTheTranscriptTail(t *testing.T) {
	attachment := `{"type":"attachment","isSidechain":false,"timestamp":"2026-07-04T08:21:11.000Z",` +
		`"attachment":{"content":"` + strings.Repeat("x", 4096) + `"}}`
	transcript := writeTranscript(t, transcriptPrompt, transcriptToolUse, transcriptInterrupt, attachment)

	// A window that starts inside the trailing attachment holds no entry.
	interruptedAt, err := transcriptInterruptedAt(transcript, 1024)
	require.NoError(t, err)
	require.True(t, interruptedAt.IsZero())

	interruptedAt, err = transcriptInterruptedAt(transcript, int64(len(transcriptInterrupt)+len(attachment)+2+10))
	require.NoError(t, err)
	require.Equal(t, fixtureInterruptedAt, interruptedAt)
}
