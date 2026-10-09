package core

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var (
	agentTestServer = TmuxServer{SocketPath: "/private/tmp/tmux-501/default", PID: 4722}
	agentTestStart  = time.Date(2026, time.October, 9, 10, 0, 0, 0, time.UTC)
)

// newAgentSessionHarness returns a harness with both providers configured and
// one Claude task, task-1, whose workspace is /tmp/repo-task.
func newAgentSessionHarness(t *testing.T) *testTaskServiceHarness {
	t.Helper()
	svc := newTestTaskService(t)
	svc.providerConfig.setup = &ProviderSetup{
		Configured: []Provider{ProviderCodex, ProviderClaude},
		Default:    ProviderClaude,
	}
	svc.taskRepo.listTasks = []*Task{{
		ID:           "task-1",
		WorktreePath: "/tmp/repo-task",
		TmuxSession:  "repo_task",
		Provider:     ProviderClaude,
	}}
	svc.sessionClient.locateServer = agentTestServer
	svc.sessionClient.locatePaneByPID = map[int]string{}
	return svc
}

// agentHook is a hook event from task-1's workspace whose forwarder ran as
// pid, reporting paneHint as its $TMUX_PANE.
func agentHook(provider Provider, eventName, sessionID string, pid int, paneHint string, minute int) HookEventInput {
	return HookEventInput{
		OccurredAt: agentTestStart.Add(time.Duration(minute) * time.Minute),
		Provider:   provider,
		Cwd:        "/tmp/repo-task",
		EventName:  eventName,
		SessionID:  sessionID,
		HookPID:    pid,
		TmuxPane:   paneHint,
		TmuxServer: agentTestServer,
	}
}

// handleAgentHook handles a hook event whose provider maps it to phase, or to
// no status when phase is empty.
func (h *testTaskServiceHarness) handleAgentHook(t *testing.T, input HookEventInput, phase TaskStatusPhase) {
	t.Helper()
	state := &h.providerRepo
	if input.Provider == ProviderClaude {
		state = &h.claudeRepo
	}
	state.hookUpdate = nil
	if phase != "" {
		state.hookUpdate = &TaskStatusUpdate{Phase: phase, RawEventName: input.EventName}
	}
	require.NoError(t, h.service.HandleHookEvent(t.Context(), input))
}

// agentSessionsWithoutIDs returns every recorded agent session, open or
// ended, with its generated ID cleared for comparison.
func (h *testTaskServiceHarness) agentSessionsWithoutIDs() []AgentSession {
	h.taskRepo.mu.Lock()
	defer h.taskRepo.mu.Unlock()
	sessions := make([]AgentSession, 0, len(h.taskRepo.agentSessions))
	for _, session := range h.taskRepo.agentSessions {
		session.ID = ""
		sessions = append(sessions, session)
	}
	return sessions
}

func (h *testTaskServiceHarness) locatedPIDs() []int {
	h.sessionClient.mu.Lock()
	defer h.sessionClient.mu.Unlock()
	return append([]int(nil), h.sessionClient.locatedPIDs...)
}

func agentStatus(phase TaskStatusPhase, eventName string, minute int) AgentSessionStatus {
	return AgentSessionStatus{
		ObservedAt:   agentTestStart.Add(time.Duration(minute) * time.Minute),
		RawEventName: eventName,
		Phase:        phase,
	}
}

func TestAgentSessions_TwoClaudePanesOpenTwoAgentSessions(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.sessionClient.locatePaneByPID = map[int]string{101: "%1", 202: "%9"}

	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventSessionStart, "sess-a", 101, "%1", 0),
		TaskStatusPhaseStarting)
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventUserPromptSubmit, "sess-b", 202, "%9", 1),
		TaskStatusPhaseWorking)

	require.Equal(t, []AgentSession{
		{
			TaskID:            "task-1",
			Provider:          ProviderClaude,
			TmuxServer:        agentTestServer,
			TmuxPane:          "%1",
			PaneTrusted:       true,
			ProviderSessionID: "sess-a",
			StartedAt:         agentTestStart,
			Status:            agentStatus(TaskStatusPhaseStarting, HookEventSessionStart, 0),
		},
		{
			TaskID:            "task-1",
			Provider:          ProviderClaude,
			TmuxServer:        agentTestServer,
			TmuxPane:          "%9",
			PaneTrusted:       true,
			ProviderSessionID: "sess-b",
			StartedAt:         agentTestStart.Add(time.Minute),
			Status:            agentStatus(TaskStatusPhaseWorking, HookEventUserPromptSubmit, 1),
		},
	}, svc.agentSessionsWithoutIDs())
}

func TestAgentSessions_ProviderChangeInAPaneEndsItsAgentSession(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.sessionClient.locatePaneByPID = map[int]string{101: "%1", 102: "%1"}

	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventSessionStart, "claude-sess", 101, "%1", 0),
		TaskStatusPhaseStarting)
	// The user quit Claude and started a Codex (without the daemon) in the
	// same pane.
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventSessionStart, "codex-sess", 102, "%1", 5),
		TaskStatusPhaseStarting)

	sessions := svc.agentSessionsWithoutIDs()
	require.Len(t, sessions, 2)
	require.Equal(t, ProviderClaude, sessions[0].Provider)
	require.Equal(t, agentTestStart.Add(5*time.Minute), sessions[0].EndedAt)
	require.Equal(t, AgentSession{
		TaskID:            "task-1",
		Provider:          ProviderCodex,
		TmuxServer:        agentTestServer,
		TmuxPane:          "%1",
		PaneTrusted:       true,
		ProviderSessionID: "codex-sess",
		StartedAt:         agentTestStart.Add(5 * time.Minute),
		Status:            agentStatus(TaskStatusPhaseStarting, HookEventSessionStart, 5),
	}, sessions[1])
}

func TestAgentSessions_OnlyTheCurrentConversationDrivesStatus(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.sessionClient.locatePaneByPID = map[int]string{101: "%1", 102: "%1", 103: "%1", 104: "%1"}

	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventSessionStart, "sess-a", 101, "%1", 0),
		TaskStatusPhaseStarting)
	// /clear started a new conversation; its first prompt moves the agent
	// session to it.
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventUserPromptSubmit, "sess-b", 102, "%1", 1),
		TaskStatusPhaseWorking)
	// A late event from the previous conversation neither moves the agent
	// session back nor changes its status.
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventStop, "sess-a", 103, "%1", 2),
		TaskStatusPhaseWaitingForInput)
	sessions := svc.agentSessionsWithoutIDs()
	require.Len(t, sessions, 1)
	require.Equal(t, "sess-b", sessions[0].ProviderSessionID)
	require.Equal(t, agentStatus(TaskStatusPhaseWorking, HookEventUserPromptSubmit, 1), sessions[0].Status)

	// An event of the current conversation updates its status.
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventPostToolUse, "sess-b", 104, "%1", 3),
		TaskStatusPhaseWorking)

	sessions = svc.agentSessionsWithoutIDs()
	require.Len(t, sessions, 1)
	require.Equal(t, "sess-b", sessions[0].ProviderSessionID)
	require.Equal(t, agentStatus(TaskStatusPhaseWorking, HookEventPostToolUse, 3), sessions[0].Status)
}

func TestAgentSessions_ConversationChangeWithoutStatusClearsTheOldStatus(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.sessionClient.locatePaneByPID = map[int]string{101: "%1", 102: "%1"}

	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventStop, "sess-a", 101, "%1", 0),
		TaskStatusPhaseWaitingForInput)
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventSessionStart, "sess-b", 102, "%1", 1), "")

	sessions := svc.agentSessionsWithoutIDs()
	require.Len(t, sessions, 1)
	require.Equal(t, "sess-b", sessions[0].ProviderSessionID)
	require.Equal(t, AgentSessionStatus{}, sessions[0].Status)
}

func TestAgentSessions_ClaudeHookOutsideAnyPaneIsNotAttributed(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.sessionClient.locatePanes = []TmuxPane{{ID: "%1", Command: "claude"}}
	svc.sessionClient.locateTaskSessionPanes = map[string][]string{"task-1": {"%1"}}

	// Traced to no pane: a Claude in a terminal outside tmux.
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventSessionStart, "sess-a", 101, "", 0),
		TaskStatusPhaseStarting)
	// No forwarder PID, as from a forwarder script older than this release.
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventUserPromptSubmit, "sess-a", 0, "", 1),
		TaskStatusPhaseWorking)

	require.Empty(t, svc.agentSessionsWithoutIDs())
	require.Equal(t, []int{101}, svc.locatedPIDs())
}

func TestAgentSessions_TraceEachConversationAndPaneHintOnce(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.sessionClient.locatePaneByPID = map[int]string{101: "%1", 102: "%1", 103: "%1", 201: "%1", 301: "%9"}

	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventSessionStart, "sess-a", 101, "%1", 0),
		TaskStatusPhaseStarting)
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventPreToolUse, "sess-a", 102, "%1", 1),
		TaskStatusPhaseWorking)
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventPostToolUse, "sess-a", 103, "%1", 2),
		TaskStatusPhaseWorking)
	// A new conversation in the same pane is traced again.
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventSessionStart, "sess-b", 201, "%1", 3),
		TaskStatusPhaseStarting)
	// So is the same conversation resumed in another pane.
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventSessionStart, "sess-b", 301, "%9", 4),
		TaskStatusPhaseStarting)

	require.Equal(t, []int{101, 201, 301}, svc.locatedPIDs())
	sessions := svc.agentSessionsWithoutIDs()
	require.Len(t, sessions, 2)
	require.Equal(t, "%1", sessions[0].TmuxPane)
	require.Equal(t, "sess-b", sessions[0].ProviderSessionID)
	require.Equal(t, "%9", sessions[1].TmuxPane)
	require.Equal(t, "sess-b", sessions[1].ProviderSessionID)
}

// daemonCodexPanes lays out task-1's Session with a shell pane and the given
// panes running Codex. Hooks of a Codex using the shared daemon trace to no
// pane.
func (h *testTaskServiceHarness) daemonCodexPanes(codexPanes ...string) {
	h.sessionClient.locatePanes = []TmuxPane{{ID: "%1", Command: "zsh"}}
	h.sessionClient.locateTaskSessionPanes = map[string][]string{"task-1": {"%1"}}
	for _, pane := range codexPanes {
		h.sessionClient.locatePanes = append(h.sessionClient.locatePanes, TmuxPane{
			ID:       pane,
			Command:  "zsh",
			Children: []PaneProcess{{Command: "codex"}},
		})
		h.sessionClient.locateTaskSessionPanes["task-1"] = append(
			h.sessionClient.locateTaskSessionPanes["task-1"],
			pane,
		)
	}
}

func TestAgentSessions_CodexFallbackBindsTheOneUntrackedCodexPane(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.daemonCodexPanes("%9")

	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventSessionStart, "codex-a", 501, "%16", 0),
		TaskStatusPhaseStarting)
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventPostToolUse, "codex-a", 502, "%16", 1),
		TaskStatusPhaseWorking)

	require.Equal(t, []AgentSession{{
		TaskID:            "task-1",
		Provider:          ProviderCodex,
		TmuxServer:        agentTestServer,
		TmuxPane:          "%9",
		ProviderSessionID: "codex-a",
		StartedAt:         agentTestStart,
		Status:            agentStatus(TaskStatusPhaseWorking, HookEventPostToolUse, 1),
	}}, svc.agentSessionsWithoutIDs())
	// The daemon's stale pane hint is traced once; once bound, the
	// conversation needs no more tmux inspection.
	require.Equal(t, []int{501}, svc.locatedPIDs())
}

func TestAgentSessions_CodexFallbackLeavesAmbiguousPanesUnattributed(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.daemonCodexPanes("%9", "%10")
	clock := agentTestStart
	svc.observation.now = func() time.Time { return clock }

	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventPostToolUse, "codex-a", 0, "", 0),
		TaskStatusPhaseWorking)
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventPostToolUse, "codex-a", 0, "", 0),
		TaskStatusPhaseWorking)
	require.Empty(t, svc.agentSessionsWithoutIDs())
	// The fallback inspected tmux once, then waits before trying again.
	require.Equal(t, []int{0}, svc.locatedPIDs())

	// Once one of the Codex agents exits, the conversation can be placed.
	clock = clock.Add(attributionRetryDelay)
	svc.daemonCodexPanes("%10")
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventPostToolUse, "codex-a", 0, "", 1),
		TaskStatusPhaseWorking)

	sessions := svc.agentSessionsWithoutIDs()
	require.Len(t, sessions, 1)
	require.Equal(t, "%10", sessions[0].TmuxPane)
	require.False(t, sessions[0].PaneTrusted)
}

func TestAgentSessions_CodexFallbackMovesTheOnlyUntrustedCodexAgentSession(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.daemonCodexPanes("%9")
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventSessionStart, "codex-a", 0, "", 0),
		TaskStatusPhaseStarting)

	// /new in that Codex: the pane is already tracked, so the new
	// conversation's SessionStart moves the agent session.
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventSessionStart, "codex-b", 0, "", 1),
		TaskStatusPhaseStarting)
	// An event from yet another conversation that does not start one stays
	// unattributed.
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventPostToolUse, "codex-c", 0, "", 2),
		TaskStatusPhaseWorking)

	require.Equal(t, []AgentSession{{
		TaskID:            "task-1",
		Provider:          ProviderCodex,
		TmuxServer:        agentTestServer,
		TmuxPane:          "%9",
		ProviderSessionID: "codex-b",
		StartedAt:         agentTestStart,
		Status:            agentStatus(TaskStatusPhaseStarting, HookEventSessionStart, 1),
	}}, svc.agentSessionsWithoutIDs())
}

func TestAgentSessions_CodexFallbackDoesNotGuessBetweenUntrustedAgentSessions(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.daemonCodexPanes("%9")
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventSessionStart, "codex-a", 0, "", 0),
		TaskStatusPhaseStarting)
	svc.daemonCodexPanes("%9", "%10")
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventSessionStart, "codex-b", 0, "", 1),
		TaskStatusPhaseStarting)

	// /new in one of the two daemon-mode Codex agents cannot be placed.
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventSessionStart, "codex-c", 0, "", 2),
		TaskStatusPhaseStarting)

	sessions := svc.agentSessionsWithoutIDs()
	require.Len(t, sessions, 2)
	require.Equal(t, "codex-a", sessions[0].ProviderSessionID)
	require.Equal(t, "%9", sessions[0].TmuxPane)
	require.Equal(t, "codex-b", sessions[1].ProviderSessionID)
	require.Equal(t, "%10", sessions[1].TmuxPane)
}

func TestAgentSessions_CodexFallbackReplacesAnotherProvidersAgentSession(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.sessionClient.locatePaneByPID = map[int]string{101: "%9"}
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventSessionStart, "claude-a", 101, "%9", 0),
		TaskStatusPhaseStarting)

	// Claude exited and a daemon-mode Codex now runs in the same pane.
	svc.daemonCodexPanes("%9")
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventSessionStart, "codex-a", 0, "", 1),
		TaskStatusPhaseStarting)

	sessions := svc.agentSessionsWithoutIDs()
	require.Len(t, sessions, 2)
	require.False(t, sessions[0].IsOpen())
	require.Equal(t, ProviderCodex, sessions[1].Provider)
	require.Equal(t, "%9", sessions[1].TmuxPane)
	require.True(t, sessions[1].IsOpen())
}

func TestAgentSessions_TraceFailureIsBestEffortAndBacksOff(t *testing.T) {
	svc := newAgentSessionHarness(t)
	clock := agentTestStart
	svc.observation.now = func() time.Time { return clock }
	svc.sessionClient.locateErr = errors.New("ps unavailable")

	// The hook still succeeds; its event is left unattributed.
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventUserPromptSubmit, "sess-a", 101, "%1", 0),
		TaskStatusPhaseWorking)
	require.Empty(t, svc.agentSessionsWithoutIDs())

	// The same hook source is not traced again until the retry delay passes.
	svc.sessionClient.locateErr = nil
	svc.sessionClient.locatePaneByPID = map[int]string{102: "%1", 103: "%1"}
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventPostToolUse, "sess-a", 102, "%1", 1),
		TaskStatusPhaseWorking)
	require.Equal(t, []int{101}, svc.locatedPIDs())
	require.Empty(t, svc.agentSessionsWithoutIDs())

	clock = clock.Add(attributionRetryDelay)
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventPostToolUse, "sess-a", 103, "%1", 2),
		TaskStatusPhaseWorking)
	require.Equal(t, []int{101, 103}, svc.locatedPIDs())
	require.Len(t, svc.agentSessionsWithoutIDs(), 1)
}

func TestAgentSessions_CodexFallbackInspectionFailureBacksOff(t *testing.T) {
	svc := newAgentSessionHarness(t)
	clock := agentTestStart
	svc.observation.now = func() time.Time { return clock }
	svc.daemonCodexPanes("%9")
	svc.sessionClient.locateErr = errors.New("tmux unavailable")

	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventSessionStart, "codex-a", 0, "", 0),
		TaskStatusPhaseStarting)
	svc.sessionClient.locateErr = nil
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventPostToolUse, "codex-a", 0, "", 1),
		TaskStatusPhaseWorking)
	require.Equal(t, []int{0}, svc.locatedPIDs())
	require.Empty(t, svc.agentSessionsWithoutIDs())

	clock = clock.Add(attributionRetryDelay)
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventPostToolUse, "codex-a", 0, "", 2),
		TaskStatusPhaseWorking)
	require.Equal(t, []int{0, 0}, svc.locatedPIDs())
	require.Len(t, svc.agentSessionsWithoutIDs(), 1)
}

// claudeInPane traces pid to the pane %1 running Claude, and nestedPID to an
// agent that Claude's Bash tool runs in that pane.
func (h *testTaskServiceHarness) claudeInPane(pid int, nestedPID int, nestedCommand string) {
	h.sessionClient.locatePaneByPID = map[int]string{pid: "%1", nestedPID: "%1"}
	h.sessionClient.locateChainByPID = map[int][]string{
		pid:       {"sh", "claude", "-zsh"},
		nestedPID: {"sh", nestedCommand, "zsh", "claude", "-zsh"},
	}
}

func TestAgentSessions_NestedCodexExecLeavesTheClaudeAgentSessionAlone(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.claudeInPane(101, 301, "codex")
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventUserPromptSubmit, "claude-a", 101, "%1", 0),
		TaskStatusPhaseWorking)

	// codex exec runs its hooks in-process, so they trace to Claude's pane.
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventSessionStart, "codex-exec", 301, "%1", 1),
		TaskStatusPhaseStarting)

	require.Equal(t, []AgentSession{{
		TaskID:            "task-1",
		Provider:          ProviderClaude,
		TmuxServer:        agentTestServer,
		TmuxPane:          "%1",
		PaneTrusted:       true,
		ProviderSessionID: "claude-a",
		StartedAt:         agentTestStart,
		Status:            agentStatus(TaskStatusPhaseWorking, HookEventUserPromptSubmit, 0),
	}}, svc.agentSessionsWithoutIDs())
}

func TestAgentSessions_NestedClaudeDoesNotMoveTheOuterConversation(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.claudeInPane(101, 401, "claude")
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventUserPromptSubmit, "claude-a", 101, "%1", 0),
		TaskStatusPhaseWorking)

	// claude -p run by the outer Claude starts its own conversation.
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventSessionStart, "claude-nested", 401, "%1", 1),
		TaskStatusPhaseStarting)

	sessions := svc.agentSessionsWithoutIDs()
	require.Len(t, sessions, 1)
	require.Equal(t, "claude-a", sessions[0].ProviderSessionID)
	require.Equal(t, agentStatus(TaskStatusPhaseWorking, HookEventUserPromptSubmit, 0), sessions[0].Status)
}

func TestAgentSessions_NestedCodexDoesNotMoveADaemonModeCodexAgent(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.daemonCodexPanes("%9")
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventSessionStart, "codex-a", 0, "", 0),
		TaskStatusPhaseStarting)
	svc.claudeInPane(101, 301, "codex")
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventUserPromptSubmit, "claude-a", 101, "%1", 1),
		TaskStatusPhaseWorking)

	// The nested codex exec's SessionStart must not reach the Codex fallback,
	// which would move the daemon-mode Codex agent in %9.
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventSessionStart, "codex-exec", 301, "%1", 2),
		TaskStatusPhaseStarting)

	sessions := svc.agentSessionsWithoutIDs()
	require.Len(t, sessions, 2)
	require.Equal(t, "%9", sessions[0].TmuxPane)
	require.Equal(t, "codex-a", sessions[0].ProviderSessionID)
	require.Equal(t, "%1", sessions[1].TmuxPane)
	require.Equal(t, "claude-a", sessions[1].ProviderSessionID)
}

func TestAgentSessions_DaemonModeConversationDoesNotMoveATrustedCodexAgentSession(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.daemonCodexPanes("%9")
	svc.sessionClient.locatePaneByPID = map[int]string{201: "%9"}
	svc.sessionClient.locateChainByPID = map[int][]string{201: {"sh", "codex", "-zsh"}}
	// A Codex without the daemon, traced to its pane.
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventSessionStart, "codex-a", 201, "%9", 0),
		TaskStatusPhaseStarting)

	// A daemon-mode SessionStart elsewhere cannot be placed; the traced Codex
	// agent session is not a candidate for the move.
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventSessionStart, "codex-b", 0, "", 1),
		TaskStatusPhaseStarting)

	sessions := svc.agentSessionsWithoutIDs()
	require.Len(t, sessions, 1)
	require.Equal(t, "codex-a", sessions[0].ProviderSessionID)
	require.True(t, sessions[0].PaneTrusted)
}

func TestAgentSessions_TracedHookTrustsAFallbackBoundPane(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.daemonCodexPanes("%9")
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventSessionStart, "codex-a", 0, "", 0),
		TaskStatusPhaseStarting)
	require.False(t, svc.agentSessionsWithoutIDs()[0].PaneTrusted)

	svc.sessionClient.locatePaneByPID = map[int]string{202: "%9"}
	svc.sessionClient.locateChainByPID = map[int][]string{202: {"sh", "codex", "-zsh"}}
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventPostToolUse, "codex-a", 202, "%9", 1),
		TaskStatusPhaseWorking)

	sessions := svc.agentSessionsWithoutIDs()
	require.Len(t, sessions, 1)
	require.True(t, sessions[0].PaneTrusted)
	require.Equal(t, agentStatus(TaskStatusPhaseWorking, HookEventPostToolUse, 1), sessions[0].Status)
}

func TestAgentSessions_AnotherTasksAgentSessionOnThePaneEnds(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.taskRepo.listTasks = append(svc.taskRepo.listTasks, &Task{
		ID:           "task-2",
		WorktreePath: "/tmp/repo-other",
		TmuxSession:  "repo_other",
		Provider:     ProviderClaude,
	})
	svc.sessionClient.locatePaneByPID = map[int]string{101: "%1", 102: "%1"}
	other := agentHook(ProviderClaude, HookEventSessionStart, "claude-other", 101, "%1", 0)
	other.Cwd = "/tmp/repo-other"
	svc.handleAgentHook(t, other, TaskStatusPhaseStarting)

	// The pane now runs an agent in task-1's workspace.
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventSessionStart, "claude-a", 102, "%1", 1),
		TaskStatusPhaseStarting)

	sessions := svc.agentSessionsWithoutIDs()
	require.Len(t, sessions, 2)
	require.Equal(t, "task-2", sessions[0].TaskID)
	require.False(t, sessions[0].IsOpen())
	require.Equal(t, "task-1", sessions[1].TaskID)
	require.Equal(t, "claude-a", sessions[1].ProviderSessionID)
	require.True(t, sessions[1].IsOpen())
}

func TestAgentSessions_TraceAgainOnARestartedTmuxServer(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.sessionClient.locatePaneByPID = map[int]string{101: "%1", 102: "%1"}
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventSessionStart, "sess-a", 101, "%1", 0),
		TaskStatusPhaseStarting)

	// Pane IDs restart on a new server, so the same hint names another pane.
	restarted := agentHook(ProviderClaude, HookEventPostToolUse, "sess-a", 102, "%1", 1)
	restarted.TmuxServer = TmuxServer{SocketPath: agentTestServer.SocketPath, PID: 9001}
	svc.handleAgentHook(t, restarted, TaskStatusPhaseWorking)

	require.Equal(t, []int{101, 102}, svc.locatedPIDs())
}

func TestAgentSessions_AnAgentSessionWithoutAConversationTakesTheFirstOne(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.sessionClient.locatePaneByPID = map[int]string{101: "%1", 102: "%1"}
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventPostToolUse, "", 101, "%1", 0),
		TaskStatusPhaseWorking)

	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventStop, "sess-a", 102, "%1", 1),
		TaskStatusPhaseWaitingForInput)

	sessions := svc.agentSessionsWithoutIDs()
	require.Len(t, sessions, 1)
	require.Equal(t, "sess-a", sessions[0].ProviderSessionID)
	require.Equal(t, agentStatus(TaskStatusPhaseWaitingForInput, HookEventStop, 1), sessions[0].Status)
}

func TestAgentSessions_StatusMappingErrorFailsTheHookBeforeAttribution(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.sessionClient.locatePaneByPID = map[int]string{101: "%1"}
	svc.claudeRepo.hookErr = errors.New("unmappable claude event")

	err := svc.service.HandleHookEvent(t.Context(),
		agentHook(ProviderClaude, HookEventPostToolUse, "claude-a", 101, "%1", 0))

	require.ErrorContains(t, err, "unmappable claude event")
	require.Empty(t, svc.agentSessionsWithoutIDs())
	// Provider session history is recorded before the status mapping.
	require.Len(t, svc.taskRepo.savedProviderSessions, 1)
}

// sessionEndHook is a SessionEnd from task-1's workspace, as agentHook, ending
// sessionID for reason.
func sessionEndHook(
	provider Provider,
	sessionID string,
	pid int,
	paneHint string,
	minute int,
	reason string,
) HookEventInput {
	input := agentHook(provider, HookEventSessionEnd, sessionID, pid, paneHint, minute)
	input.EndReason = reason
	return input
}

func TestAgentSessions_ClaudeClearKeepsTheAgentSessionForItsNextConversation(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.agentPanes("claude")
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventStop, "sess-a", 101, "%1", 0),
		TaskStatusPhaseWaitingForInput)

	// /clear ends the conversation, not the agent; the SessionStart that
	// follows moves the agent session to the new conversation.
	svc.handleAgentHook(t, sessionEndHook(ProviderClaude, "sess-a", 101, "%1", 1, "clear"), "")
	require.True(t, svc.agentSessionsWithoutIDs()[0].IsOpen())
	clearStart := agentHook(ProviderClaude, HookEventSessionStart, "sess-b", 101, "%1", 1)
	clearStart.StartSource = "clear"
	svc.handleAgentHook(t, clearStart, TaskStatusPhaseStarting)

	require.Equal(t, []AgentSession{{
		TaskID:            "task-1",
		Provider:          ProviderClaude,
		TmuxServer:        agentTestServer,
		TmuxPane:          "%1",
		PaneTrusted:       true,
		ProviderSessionID: "sess-b",
		StartedAt:         agentTestStart,
		Status:            agentStatus(TaskStatusPhaseStarting, HookEventSessionStart, 1),
	}}, svc.agentSessionsWithoutIDs())
	require.Equal(t, TaskStatusPhaseStarting, svc.latestTaskStatus(t).Phase)
}

func TestAgentSessions_CodexSessionEndForAnEarlierConversationIsIgnored(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.agentPanes("codex")
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventSessionStart, "codex-a", 101, "%1", 0),
		TaskStatusPhaseStarting)
	// /new: the agent goes on with another conversation.
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventSessionStart, "codex-b", 101, "%1", 1),
		TaskStatusPhaseStarting)
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventUserPromptSubmit, "codex-b", 101, "%1", 2),
		TaskStatusPhaseWorking)

	// Codex ends the conversation it left once that has idled for 30 minutes.
	svc.handleAgentHook(t, sessionEndHook(ProviderCodex, "codex-a", 101, "%1", 32, "other"), "")

	require.Equal(t, []AgentSession{{
		TaskID:            "task-1",
		Provider:          ProviderCodex,
		TmuxServer:        agentTestServer,
		TmuxPane:          "%1",
		PaneTrusted:       true,
		ProviderSessionID: "codex-b",
		StartedAt:         agentTestStart,
		Status:            agentStatus(TaskStatusPhaseWorking, HookEventUserPromptSubmit, 2),
	}}, svc.agentSessionsWithoutIDs())
	// Once the Session is lost, Reconnect resumes the conversation the agent is
	// on.
	svc.loseSession()
	require.NoError(t, svc.service.ReconnectTaskSession(t.Context(), "task-1"))
	require.Equal(t, []string{"codex", "resume", "codex-b"}, svc.sessionClient.startedLaunch.Command)
}

func TestAgentSessions_SessionEndNeverOpensAnAgentSession(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.agentPanes("claude")

	svc.handleAgentHook(t, sessionEndHook(ProviderClaude, "sess-a", 101, "%1", 0, "prompt_input_exit"), "")
	require.Empty(t, svc.agentSessionsWithoutIDs())

	// Nor does it end the pane's agent session of another provider.
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventUserPromptSubmit, "sess-a", 101, "%1", 1),
		TaskStatusPhaseWorking)
	svc.sessionClient.mu.Lock()
	svc.sessionClient.locatePaneByPID[201] = "%1"
	svc.sessionClient.mu.Unlock()
	svc.handleAgentHook(t, sessionEndHook(ProviderCodex, "sess-a", 201, "%1", 2, "other"), "")

	sessions := svc.agentSessionsWithoutIDs()
	require.Len(t, sessions, 1)
	require.Equal(t, ProviderClaude, sessions[0].Provider)
	require.True(t, sessions[0].IsOpen())
}

// A Codex using the shared daemon runs SessionEnd outside its pane, so only
// the agent session bound to the ended conversation can take it.
func TestAgentSessions_DaemonModeCodexSessionEndEndsOnlyTheBoundAgentSession(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.daemonCodexPanes("%9")
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventSessionStart, "codex-a", 0, "", 0),
		TaskStatusPhaseStarting)
	svc.daemonCodexPanes("%9", "%10")
	inspections := len(svc.locatedPIDs())

	// Neither the untracked Codex in %10 nor the only daemon-mode agent
	// session takes the end of a conversation no agent session is on.
	svc.handleAgentHook(t, sessionEndHook(ProviderCodex, "codex-z", 0, "", 1, "other"), "")
	sessions := svc.agentSessionsWithoutIDs()
	require.Len(t, sessions, 1)
	require.Equal(t, "codex-a", sessions[0].ProviderSessionID)
	require.True(t, sessions[0].IsOpen())

	svc.handleAgentHook(t, sessionEndHook(ProviderCodex, "codex-a", 0, "", 2, "other"), "")
	sessions = svc.agentSessionsWithoutIDs()
	require.Len(t, sessions, 1)
	require.Equal(t, "%9", sessions[0].TmuxPane)
	require.Equal(t, agentTestStart.Add(2*time.Minute), sessions[0].EndedAt)
	// These hooks carry no PID, so nothing is traced, and unlike the Codex
	// fallback a SessionEnd never inspects tmux for an untracked Codex pane.
	require.Len(t, svc.locatedPIDs(), inspections)
}

// The shared daemon may still hold a conversation that a Codex without the
// daemon resumed, as Reconnect does. When the daemon idles it out, its
// SessionEnd comes from outside any pane and is not the trusted agent's.
func TestAgentSessions_APanelessSessionEndLeavesATrustedAgentSessionOpen(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.agentPanes("codex")
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventSessionStart, "codex-a", 101, "%1", 0),
		TaskStatusPhaseStarting)
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventUserPromptSubmit, "codex-a", 101, "%1", 1),
		TaskStatusPhaseWorking)

	// PID 900 traces to no pane.
	svc.handleAgentHook(t, sessionEndHook(ProviderCodex, "codex-a", 900, "%16", 31, "other"), "")

	require.Equal(t, []AgentSession{{
		TaskID:            "task-1",
		Provider:          ProviderCodex,
		TmuxServer:        agentTestServer,
		TmuxPane:          "%1",
		PaneTrusted:       true,
		ProviderSessionID: "codex-a",
		StartedAt:         agentTestStart,
		Status:            agentStatus(TaskStatusPhaseWorking, HookEventUserPromptSubmit, 1),
	}}, svc.agentSessionsWithoutIDs())
}

// An upgrade made the task's last Codex conversation an agent session without
// a pane, and a Codex on the shared daemon still holds that conversation.
func TestAgentSessions_ACodexOnTheSharedDaemonLeavesAnAgentSessionWithoutAPaneAlone(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.taskRepo.agentSessions = []AgentSession{{
		ID:                "migrated",
		TaskID:            "task-1",
		Provider:          ProviderCodex,
		ProviderSessionID: "codex-old",
		StartedAt:         agentTestStart,
	}}
	svc.daemonCodexPanes()

	// It works in the old conversation, starts a new one, and ends the old.
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventUserPromptSubmit, "codex-old", 501, "%16", 1),
		TaskStatusPhaseWorking)
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventSessionStart, "codex-new", 501, "%16", 2),
		TaskStatusPhaseStarting)
	svc.handleAgentHook(t, sessionEndHook(ProviderCodex, "codex-old", 501, "%16", 3, "other"), "")

	// None of it reaches the agent session, which Reconnect can still restore.
	require.Equal(t, []AgentSession{{
		TaskID:            "task-1",
		Provider:          ProviderCodex,
		ProviderSessionID: "codex-old",
		StartedAt:         agentTestStart,
	}}, svc.agentSessionsWithoutIDs())
}

// A Codex you started yourself, on the shared daemon, is lost with its
// Session. Half an hour later the daemon ends the conversation it still holds.
func TestAgentSessions_ALateDaemonSessionEndLeavesALostCodexAgentSessionToReconnect(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.daemonCodexPanes("%9")
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventUserPromptSubmit, "codex-a", 501, "%16", 0),
		TaskStatusPhaseWorking)
	svc.loseSession()

	svc.handleAgentHook(t, sessionEndHook(ProviderCodex, "codex-a", 501, "%16", 31, "other"), "")

	sessions := svc.agentSessionsWithoutIDs()
	require.Len(t, sessions, 1)
	require.True(t, sessions[0].IsOpen())
	svc.observation.now = func() time.Time { return reconnectedAt }
	require.NoError(t, svc.service.ReconnectTaskSession(t.Context(), "task-1"))
	require.Equal(t, []string{"codex", "resume", "codex-a"}, svc.sessionClient.startedLaunch.Command)
}

func TestAgentSessions_APanelessSessionEndLeavesTheAgentSessionOpenWhileTmuxCannotSay(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.daemonCodexPanes("%9")
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventUserPromptSubmit, "codex-a", 501, "%16", 0),
		TaskStatusPhaseWorking)
	svc.sessionClient.inspectErr = errors.New("tmux protocol failure")

	svc.handleAgentHook(t, sessionEndHook(ProviderCodex, "codex-a", 501, "%16", 31, "other"), "")

	require.True(t, svc.agentSessionsWithoutIDs()[0].IsOpen())
}

// Claude runs hooks inside the agent, so a SessionEnd from outside tmux, as
// from the same conversation resumed in a terminal, is not a pane's agent's.
func TestAgentSessions_ClaudeSessionEndOutsideTmuxIsIgnored(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.agentPanes("claude")
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventUserPromptSubmit, "sess-a", 101, "%1", 0),
		TaskStatusPhaseWorking)

	// PID 777 traces to no pane.
	svc.handleAgentHook(t, sessionEndHook(ProviderClaude, "sess-a", 777, "", 1, "prompt_input_exit"), "")

	require.True(t, svc.agentSessionsWithoutIDs()[0].IsOpen())
}

func TestAgentSessions_SessionEndLeavesAnotherTasksAgentSessionAlone(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.taskRepo.listTasks = append(svc.taskRepo.listTasks, &Task{
		ID:           "task-2",
		WorktreePath: "/tmp/repo-other",
		TmuxSession:  "repo_other",
		Provider:     ProviderClaude,
	})
	svc.agentPanes("claude")
	other := agentHook(ProviderClaude, HookEventUserPromptSubmit, "sess-a", 101, "%1", 0)
	other.Cwd = "/tmp/repo-other"
	svc.handleAgentHook(t, other, TaskStatusPhaseWorking)

	// The agent changed directory into task-1's workspace, then exited.
	svc.handleAgentHook(t, sessionEndHook(ProviderClaude, "sess-a", 101, "%1", 1, "prompt_input_exit"), "")

	sessions := svc.agentSessionsWithoutIDs()
	require.Len(t, sessions, 1)
	require.Equal(t, "task-2", sessions[0].TaskID)
	require.True(t, sessions[0].IsOpen())
}

// Hooks of one /clear can be handled in either order.
func TestAgentSessions_ClearHandledOutOfOrderKeepsTheAgentSession(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.agentPanes("claude")
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventStop, "sess-a", 101, "%1", 0),
		TaskStatusPhaseWaitingForInput)
	clearStart := agentHook(ProviderClaude, HookEventSessionStart, "sess-b", 101, "%1", 1)
	clearStart.StartSource = "clear"
	svc.handleAgentHook(t, clearStart, TaskStatusPhaseStarting)

	svc.handleAgentHook(t, sessionEndHook(ProviderClaude, "sess-a", 101, "%1", 1, "clear"), "")
	// Nor does a late exit of the old conversation end the agent session.
	svc.handleAgentHook(t, sessionEndHook(ProviderClaude, "sess-a", 101, "%1", 2, "other"), "")

	sessions := svc.agentSessionsWithoutIDs()
	require.Len(t, sessions, 1)
	require.True(t, sessions[0].IsOpen())
	require.Equal(t, "sess-b", sessions[0].ProviderSessionID)
	require.Equal(t, agentStatus(TaskStatusPhaseStarting, HookEventSessionStart, 1), sessions[0].Status)
}

// Codex fires a fork's SessionStart only with its first prompt, and Rig does
// not register the fork source (see the Codex hook catalog), so a fork the
// user makes is placed by that prompt. Until then the agent session stays on
// the conversation it left.
func TestAgentSessions_ACodexForkIsPlacedByItsFirstPrompt(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.agentPanes("codex")
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventUserPromptSubmit, "codex-a", 101, "%1", 0),
		TaskStatusPhaseWorking)
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventStop, "codex-a", 101, "%1", 1),
		TaskStatusPhaseWaitingForInput)

	// The user forks codex-a into codex-f, sends no prompt, and codex-a idles
	// out.
	svc.handleAgentHook(t, sessionEndHook(ProviderCodex, "codex-a", 101, "%1", 32, "other"), "")
	require.False(t, svc.agentSessionsWithoutIDs()[0].IsOpen())

	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventUserPromptSubmit, "codex-f", 101, "%1", 40),
		TaskStatusPhaseWorking)

	sessions := svc.agentSessionsWithoutIDs()
	require.Len(t, sessions, 2)
	require.Equal(t, "codex-f", sessions[1].ProviderSessionID)
	require.True(t, sessions[1].IsOpen())
	require.Equal(t, TaskStatusPhaseWorking, svc.latestTaskStatus(t).Phase)
}

func TestBoundedCache_EvictsTheOldestEntryOnceFull(t *testing.T) {
	cache := newBoundedCache[string, int](2)
	cache.put("a", 1)
	cache.put("b", 2)
	cache.put("a", 3)
	cache.put("c", 4)

	_, hasA := cache.get("a")
	b, hasB := cache.get("b")
	c, hasC := cache.get("c")
	require.False(t, hasA)
	require.True(t, hasB)
	require.Equal(t, 2, b)
	require.True(t, hasC)
	require.Equal(t, 4, c)
	require.Equal(t, 2, cache.len())
}

func TestAgentSessions_ActivityRecordsTheAgentSessionItCameFrom(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.agentPanes("claude")

	svc.handleAgentHook(t, promptHook(ProviderClaude, "sess-a", 101, "%1", 0, "add retries"),
		TaskStatusPhaseWorking)
	// A Claude in a terminal outside tmux, and one whose forwarder sent no
	// PID: neither is placed, but both prompts are kept.
	svc.handleAgentHook(t, promptHook(ProviderClaude, "sess-b", 999, "", 1, "outside tmux"),
		TaskStatusPhaseWorking)
	svc.handleAgentHook(t, promptHook(ProviderClaude, "sess-c", 0, "", 2, "old forwarder"),
		TaskStatusPhaseWorking)

	agentSessions := svc.latestTaskStatus(t).AgentSessions
	require.Len(t, agentSessions, 1)
	activity, err := svc.service.GetTaskActivity(t.Context(), "task-1", 0)
	require.NoError(t, err)
	require.Len(t, activity, 3)
	require.Equal(t, agentSessions[0].ID, activity[0].AgentSessionID)
	require.Equal(t, "add retries", activity[0].Text)
	require.Empty(t, activity[1].AgentSessionID)
	require.Equal(t, "outside tmux", activity[1].Text)
	require.Empty(t, activity[2].AgentSessionID)
	require.Equal(t, "old forwarder", activity[2].Text)
}
