package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// reconnectedAt is when the reconnect tests reconnect, after their agents'
// hook events.
var reconnectedAt = agentTestStart.Add(30 * time.Minute)

// newLostSessionHarness is the agent session harness after task-1's Session
// was lost while Claude, in %1, and then Codex, in %2, worked in it, each in
// its own conversation. The observation's clock stands at reconnectedAt.
func newLostSessionHarness(t *testing.T) *testTaskServiceHarness {
	t.Helper()
	svc := newAgentSessionHarness(t)
	svc.observation.recoveryPollInterval = time.Hour
	svc.agentPanes("claude", "codex")
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventUserPromptSubmit, "claude-1", 101, "%1", 0),
		TaskStatusPhaseWorking)
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventUserPromptSubmit, "codex-1", 102, "%2", 1),
		TaskStatusPhaseWorking)
	svc.loseSession()
	svc.observation.now = func() time.Time { return reconnectedAt }
	return svc
}

// loseSession makes task-1's Session gone, with no pane left on the tmux
// server. A recreated Session's task window gets pane %50.
func (h *testTaskServiceHarness) loseSession() {
	h.sessionClient.mu.Lock()
	defer h.sessionClient.mu.Unlock()
	h.sessionClient.inspectState = TaskSessionRuntimeState{}
	h.sessionClient.locatePanes = nil
	h.sessionClient.locateTaskSessionPanes = map[string][]string{}
	h.sessionClient.startPane = TmuxPaneRef{Server: agentTestServer, ID: "%50"}
}

// recreatedSessionPanes lays out task-1's recreated Session with one pane per
// ID, running command.
func (h *testTaskServiceHarness) recreatedSessionPanes(panes map[string]string, order ...string) {
	h.sessionClient.mu.Lock()
	defer h.sessionClient.mu.Unlock()
	h.sessionClient.locatePanes = nil
	h.sessionClient.locateTaskSessionPanes = map[string][]string{}
	for _, id := range order {
		h.sessionClient.locatePanes = append(h.sessionClient.locatePanes, TmuxPane{
			ID:      id,
			Session: "repo_task",
			Command: panes[id],
		})
		h.sessionClient.locateTaskSessionPanes["task-1"] = append(h.sessionClient.locateTaskSessionPanes["task-1"], id)
	}
}

var restoredStarting = AgentSessionStatus{Phase: TaskStatusPhaseStarting, ObservedAt: reconnectedAt}

func TestReconnect_RestoresEveryAgentSessionInAPaneOfTheTaskWindow(t *testing.T) {
	svc := newLostSessionHarness(t)
	stream := svc.subscribeTaskStatus(t)

	require.NoError(t, svc.service.ReconnectTaskSession(t.Context(), "task-1"))

	// The oldest agent recreates the Session in its task window's pane, and
	// the other gets a pane split in beside it, so the Session keeps one
	// window for its agents. Each resumes its own conversation with its own
	// provider.
	require.Equal(t, "task-1", svc.sessionClient.startedTask.ID)
	require.Equal(t, []string{"claude", "resume", "claude-1"}, svc.sessionClient.startedLaunch.Command)
	require.Len(t, svc.sessionClient.agentPanes, 1)
	require.Equal(t, "%50", svc.sessionClient.agentPanes[0].beside)
	require.Equal(t, []string{"codex", "resume", "codex-1"}, svc.sessionClient.agentPanes[0].launch.Command)
	require.Less(t, indexOfEvent(svc.events, "start_task_session"), indexOfEvent(svc.events, "split_agent_pane"))

	// Each agent session moves to its agent's new pane, starting, and keeps
	// its conversation and start time.
	require.Equal(t, []AgentSession{
		{
			TaskID:            "task-1",
			Provider:          ProviderClaude,
			TmuxServer:        agentTestServer,
			TmuxPane:          "%50",
			PaneTrusted:       true,
			ProviderSessionID: "claude-1",
			StartedAt:         agentTestStart,
			LaunchedAt:        reconnectedAt,
		},
		{
			TaskID:            "task-1",
			Provider:          ProviderCodex,
			TmuxServer:        agentTestServer,
			TmuxPane:          "%100",
			PaneTrusted:       true,
			ProviderSessionID: "codex-1",
			StartedAt:         agentTestStart.Add(time.Minute),
			LaunchedAt:        reconnectedAt,
		},
	}, svc.agentSessionsWithoutIDs())
	update := receiveTaskStatus(t, stream)
	require.Equal(t, TaskStatusPhaseStarting, update.Phase)
	require.Equal(t, []LiveAgentSession{
		{Provider: ProviderClaude, Pane: TmuxPaneRef{Server: agentTestServer, ID: "%50"}, Status: restoredStarting},
		{Provider: ProviderCodex, Pane: TmuxPaneRef{Server: agentTestServer, ID: "%100"}, Status: restoredStarting},
	}, liveAgentSessionsWithoutIDs(&update))

	// Both providers get their files and session environment, the workspace
	// is never seeded again, and the task keeps its launch provider.
	require.Equal(t, 1, svc.claudeRepo.sessionEnvCalls)
	require.Equal(t, 1, svc.providerRepo.sessionEnvCalls)
	require.True(t, svc.workspace.bootstrapCalled)
	require.False(t, svc.workspace.setupCalled)
	require.Nil(t, svc.taskRepo.updatedTask)
}

// Three agents ran in the lost Session. Each one after the oldest is split in
// beside the oldest's pane in the order the agents started, which the tmux
// client lays out left to right.
func TestReconnect_SplitsTheOtherAgentsBesideTheOldestInTheOrderTheyStarted(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.observation.recoveryPollInterval = time.Hour
	svc.agentPanes("claude", "codex", "claude")
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventUserPromptSubmit, "claude-1", 101, "%1", 0),
		TaskStatusPhaseWorking)
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventUserPromptSubmit, "codex-1", 102, "%2", 1),
		TaskStatusPhaseWorking)
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventUserPromptSubmit, "claude-2", 103, "%3", 2),
		TaskStatusPhaseWorking)
	svc.loseSession()
	svc.observation.now = func() time.Time { return reconnectedAt }

	require.NoError(t, svc.service.ReconnectTaskSession(t.Context(), "task-1"))

	require.Equal(t, []string{"claude", "resume", "claude-1"}, svc.sessionClient.startedLaunch.Command)
	panes := svc.sessionClient.agentPanes
	require.Len(t, panes, 2)
	require.Equal(t, "%50", panes[0].beside)
	require.Equal(t, []string{"codex", "resume", "codex-1"}, panes[0].launch.Command)
	require.Equal(t, "%50", panes[1].beside)
	require.Equal(t, []string{"claude", "resume", "claude-2"}, panes[1].launch.Command)
	var placed []string
	for _, session := range svc.agentSessionsWithoutIDs() {
		placed = append(placed, session.ProviderSessionID+" in "+session.TmuxPane)
	}
	require.Equal(t, []string{"claude-1 in %50", "codex-1 in %100", "claude-2 in %101"}, placed)
}

// tmux recreated the Session without saying which pane its task window got,
// so the other agents are split into the task window itself.
func TestReconnect_SplitsTheTaskWindowWhenTheTaskPaneIsUnknown(t *testing.T) {
	svc := newLostSessionHarness(t)
	svc.sessionClient.startPane = TmuxPaneRef{}

	require.NoError(t, svc.service.ReconnectTaskSession(t.Context(), "task-1"))

	require.Equal(t, []string{"claude", "resume", "claude-1"}, svc.sessionClient.startedLaunch.Command)
	require.Len(t, svc.sessionClient.agentPanes, 1)
	require.Empty(t, svc.sessionClient.agentPanes[0].beside)
	require.Equal(t, []string{"codex", "resume", "codex-1"}, svc.sessionClient.agentPanes[0].launch.Command)
	// Codex moves to its pane. Claude's agent session waits without one for
	// its agent's first hook event, as with any launch tmux did not place.
	sessions := svc.agentSessionsWithoutIDs()
	require.Empty(t, sessions[0].TmuxPane)
	require.True(t, sessions[0].IsOpen())
	require.Equal(t, "%100", sessions[1].TmuxPane)
	require.Equal(t, "codex-1", sessions[1].ProviderSessionID)
}

// A status cycle saw the agents in their windows and then the Session gone,
// before the reconnect.
func TestReconnect_PublishesRestoredAgentsInTheirNewPanes(t *testing.T) {
	svc := newAgentSessionHarness(t)
	observer := svc.manualStatusObserver()
	svc.agentPanes("claude", "codex")
	svc.paneWindow(0, "task", 0, 0)
	svc.paneWindow(1, "codex", 1, 0)
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventUserPromptSubmit, "claude-1", 101, "%1", 0),
		TaskStatusPhaseWorking)
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventUserPromptSubmit, "codex-1", 102, "%2", 1),
		TaskStatusPhaseWorking)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, err := svc.service.SubscribeTaskStatus(ctx, "task-1")
	require.NoError(t, err)
	observer.runCycle()
	require.Equal(t, &TmuxPaneLocation{WindowName: "codex", WindowIndex: 1},
		receiveTaskStatus(t, stream).AgentSessions[1].Location)
	svc.loseSession()
	observer.runCycle()
	require.Equal(t, TaskStatusPhaseStopped, receiveTaskStatus(t, stream).Phase)
	svc.observation.now = func() time.Time { return reconnectedAt }

	require.NoError(t, svc.service.ReconnectTaskSession(t.Context(), "task-1"))

	// What the cycles found about the old panes, lost or where they sat, no
	// longer applies to the agents in their new panes.
	update := receiveTaskStatus(t, stream)
	require.Equal(t, TaskStatusPhaseStarting, update.Phase)
	require.Equal(t, []LiveAgentSession{
		{Provider: ProviderClaude, Pane: TmuxPaneRef{Server: agentTestServer, ID: "%50"}, Status: restoredStarting},
		{Provider: ProviderCodex, Pane: TmuxPaneRef{Server: agentTestServer, ID: "%100"}, Status: restoredStarting},
	}, liveAgentSessionsWithoutIDs(&update))
}

// An upgrade made the task's last conversation an agent session without a
// pane, after its Session was lost.
func TestReconnect_RestoresAnAgentSessionWithoutAPane(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.taskRepo.agentSessions = []AgentSession{{
		ID:                "task-1",
		TaskID:            "task-1",
		Provider:          ProviderClaude,
		ProviderSessionID: "claude-old",
		StartedAt:         agentTestStart,
	}}
	svc.loseSession()
	svc.observation.now = func() time.Time { return reconnectedAt }

	require.NoError(t, svc.service.ReconnectTaskSession(t.Context(), "task-1"))

	require.Equal(t, []string{"claude", "resume", "claude-old"}, svc.sessionClient.startedLaunch.Command)
	require.Equal(t, []AgentSession{{
		TaskID:            "task-1",
		Provider:          ProviderClaude,
		TmuxServer:        agentTestServer,
		TmuxPane:          "%50",
		PaneTrusted:       true,
		ProviderSessionID: "claude-old",
		StartedAt:         agentTestStart,
		LaunchedAt:        reconnectedAt,
	}}, svc.agentSessionsWithoutIDs())
}

func TestReconnect_LeavesAnAgentTheUserExitedGone(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.observation.recoveryPollInterval = time.Hour
	svc.agentPanes("claude", "codex")
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventUserPromptSubmit, "claude-1", 101, "%1", 0),
		TaskStatusPhaseWorking)
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventUserPromptSubmit, "codex-1", 102, "%2", 1),
		TaskStatusPhaseWorking)
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventSessionEnd, "codex-1", 102, "%2", 2), "")
	svc.loseSession()

	require.NoError(t, svc.service.ReconnectTaskSession(t.Context(), "task-1"))

	require.Equal(t, []string{"claude", "resume", "claude-1"}, svc.sessionClient.startedLaunch.Command)
	require.Empty(t, svc.sessionClient.agentPanes)
	sessions := svc.agentSessionsWithoutIDs()
	require.Len(t, sessions, 2)
	require.Equal(t, "%50", sessions[0].TmuxPane)
	require.False(t, sessions[1].IsOpen())
	require.Zero(t, svc.providerRepo.sessionEnvCalls)
	// Codex still gets its files, so a Codex started by hand reports hooks.
	require.NotNil(t, svc.providerRepo.bootstrapRequest)
}

func TestReconnect_SkipsAnAgentWhoseProviderIsNoLongerConfigured(t *testing.T) {
	svc := newLostSessionHarness(t)
	svc.providerConfig.setup = &ProviderSetup{Configured: []Provider{ProviderClaude}, Default: ProviderClaude}

	err := svc.service.ReconnectTaskSession(t.Context(), "task-1")

	// Claude is restored all the same, and the error names the conversation
	// left behind.
	require.EqualError(t, err, `reconnect: codex conversation codex-1 not restored: `+
		`provider "codex" is not configured: run rig setup to enable it`)
	require.Equal(t, []string{"claude", "resume", "claude-1"}, svc.sessionClient.startedLaunch.Command)
	require.Empty(t, svc.sessionClient.agentPanes)
	require.Zero(t, svc.providerRepo.sessionEnvCalls)
	sessions := svc.agentSessionsWithoutIDs()
	require.Equal(t, "%50", sessions[0].TmuxPane)
	require.True(t, sessions[1].IsOpen())
	require.Equal(t, "%2", sessions[1].TmuxPane)

	// The Session exists again, so the next status cycle ends Codex's agent
	// session like that of any agent whose pane is gone.
	svc.recreatedSessionPanes(map[string]string{"%50": "claude"}, "%50")
	update := svc.latestTaskStatus(t)
	require.Len(t, update.AgentSessions, 1)
	require.Equal(t, ProviderClaude, update.AgentSessions[0].Provider)
	require.False(t, svc.agentSessionsWithoutIDs()[1].IsOpen())
}

// recordLaunchedCodex records a Codex that Rig launched in pane of task-1 and
// that has sent no hook event yet, as Reconnect records each agent it
// launches.
func (h *testTaskServiceHarness) recordLaunchedCodex(t *testing.T, pane string) AgentSession {
	t.Helper()
	session, err := h.observation.recordLaunchedAgentSession(
		t.Context(),
		h.taskRepo.listTasks[0],
		ProviderCodex,
		TmuxPaneRef{Server: agentTestServer, ID: pane},
	)
	require.NoError(t, err)
	return session
}

// Rig launched Codex beside Claude, and the Session was lost before Codex sent
// a hook event.
func TestReconnect_EndsAnAgentSessionWithNoConversationToResume(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.observation.recoveryPollInterval = time.Hour
	svc.agentPanes("claude", "zsh")
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventUserPromptSubmit, "claude-1", 101, "%1", 0),
		TaskStatusPhaseWorking)
	svc.recordLaunchedCodex(t, "%2")
	svc.loseSession()
	svc.observation.now = func() time.Time { return reconnectedAt }

	require.NoError(t, svc.service.ReconnectTaskSession(t.Context(), "task-1"))

	require.Equal(t, []string{"claude", "resume", "claude-1"}, svc.sessionClient.startedLaunch.Command)
	require.Empty(t, svc.sessionClient.agentPanes)
	sessions := svc.agentSessionsWithoutIDs()
	require.Len(t, sessions, 2)
	require.Equal(t, "%50", sessions[0].TmuxPane)
	require.True(t, sessions[0].IsOpen())
	require.False(t, sessions[1].IsOpen())
}

func TestReconnect_LaunchesTheLaunchProviderFreshWhenNothingIsLeftToRestore(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.taskRepo.listTasks[0].Prompt = "fix billing retry flow"
	svc.loseSession()
	svc.observation.now = func() time.Time { return reconnectedAt }

	require.NoError(t, svc.service.ReconnectTaskSession(t.Context(), "task-1"))

	require.Equal(t, []string{"claude"}, svc.sessionClient.startedLaunch.Command)
	require.Empty(t, svc.sessionClient.startedLaunch.PrefillInput)
	require.Empty(t, svc.sessionClient.agentPanes)
	require.Equal(t, 1, svc.claudeRepo.sessionEnvCalls)
	// Codex gets its files too, so a Codex started by hand reports hooks.
	require.NotNil(t, svc.providerRepo.bootstrapRequest)
	require.Equal(t, []AgentSession{{
		TaskID:      "task-1",
		Provider:    ProviderClaude,
		TmuxServer:  agentTestServer,
		TmuxPane:    "%50",
		PaneTrusted: true,
		StartedAt:   reconnectedAt,
		LaunchedAt:  reconnectedAt,
	}}, svc.agentSessionsWithoutIDs())
}

func TestReconnect_LaunchesTheLaunchProviderFreshWhenItsOnlyAgentIsSkipped(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.agentPanes("codex")
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventUserPromptSubmit, "codex-1", 101, "%1", 0),
		TaskStatusPhaseWorking)
	svc.loseSession()
	svc.providerConfig.setup = &ProviderSetup{Configured: []Provider{ProviderClaude}, Default: ProviderClaude}

	err := svc.service.ReconnectTaskSession(t.Context(), "task-1")

	require.EqualError(t, err, `reconnect: codex conversation codex-1 not restored: `+
		`provider "codex" is not configured: run rig setup to enable it`)
	require.Equal(t, []string{"claude"}, svc.sessionClient.startedLaunch.Command)
	sessions := svc.agentSessionsWithoutIDs()
	require.Len(t, sessions, 2)
	require.Equal(t, "%1", sessions[0].TmuxPane)
	require.Equal(t, ProviderClaude, sessions[1].Provider)
	require.Equal(t, "%50", sessions[1].TmuxPane)
}

func TestReconnect_FailsClearlyWhenTheLaunchProviderIsNotConfigured(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.listTasks = []*Task{{
		ID:           "task-1",
		DisplayName:  "billing retry flow",
		RepoRoot:     "/tmp/repo",
		WorktreePath: "/tmp/repo-task",
		TmuxSession:  "repo_task",
		Provider:     ProviderClaude,
	}}
	svc.sessionClient.inspectState = TaskSessionRuntimeState{}

	err := svc.service.ReconnectTaskSession(t.Context(), "task-1")

	require.EqualError(t, err, `provider "claude" is not configured: run rig setup to enable it`)
	require.Nil(t, svc.sessionClient.startedTask)
}

func TestReconnect_FailsWithNothingLaunchedWhenItsOnlyAgentCannotBeRestored(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.agentPanes("codex")
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventUserPromptSubmit, "codex-1", 101, "%1", 0),
		TaskStatusPhaseWorking)
	svc.loseSession()
	svc.providerRepo.sessionEnvErr = errors.New("codex hooks install failed")

	err := svc.service.ReconnectTaskSession(t.Context(), "task-1")

	require.EqualError(t, err, "reconnect: codex conversation codex-1 not restored: "+
		"ensure task session environment: codex hooks install failed")
	require.Nil(t, svc.sessionClient.startedTask)
	// Nothing moved, so a later reconnect can still restore it.
	sessions := svc.agentSessionsWithoutIDs()
	require.True(t, sessions[0].IsOpen())
	require.Equal(t, "%1", sessions[0].TmuxPane)
}

func TestReconnect_RestoresTheOthersWhenOneAgentCannotBeRestored(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.observation.recoveryPollInterval = time.Hour
	svc.agentPanes("claude", "codex", "claude")
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventUserPromptSubmit, "claude-1", 101, "%1", 0),
		TaskStatusPhaseWorking)
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventUserPromptSubmit, "codex-1", 102, "%2", 1),
		TaskStatusPhaseWorking)
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventUserPromptSubmit, "claude-2", 103, "%3", 2),
		TaskStatusPhaseWorking)
	svc.loseSession()
	svc.observation.now = func() time.Time { return reconnectedAt }
	svc.sessionClient.agentPaneErrByCommand = map[string]error{"codex": errors.New("create pane failed")}

	err := svc.service.ReconnectTaskSession(t.Context(), "task-1")

	require.EqualError(t, err,
		"reconnect: codex conversation codex-1 not restored: split agent pane: create pane failed")
	require.Equal(t, []string{"claude", "resume", "claude-1"}, svc.sessionClient.startedLaunch.Command)
	// Claude's two agents share one preparation of the workspace for Claude.
	require.Equal(t, 1, svc.claudeRepo.sessionEnvCalls)
	require.Len(t, svc.sessionClient.agentPanes, 1)
	require.Equal(t, []string{"claude", "resume", "claude-2"}, svc.sessionClient.agentPanes[0].launch.Command)
	sessions := svc.agentSessionsWithoutIDs()
	require.Equal(t, "%50", sessions[0].TmuxPane)
	require.Empty(t, sessions[1].TmuxPane)
	require.Equal(t, "%100", sessions[2].TmuxPane)

	// Once its launch grace is over, the next status cycle ends Codex's agent
	// session.
	svc.observation.statusCacheMaxAge = time.Nanosecond
	svc.recreatedSessionPanes(map[string]string{"%50": "claude", "%100": "claude"}, "%50", "%100")
	svc.observation.now = func() time.Time { return reconnectedAt.Add(agentLaunchGrace) }
	update := svc.latestTaskStatus(t)
	require.Len(t, update.AgentSessions, 2)
	require.False(t, svc.agentSessionsWithoutIDs()[1].IsOpen())
}

func TestReconnect_LeavesASessionThatStillExistsAlone(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.agentPanes("claude")
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventUserPromptSubmit, "claude-1", 101, "%1", 0),
		TaskStatusPhaseWorking)

	require.NoError(t, svc.service.ReconnectTaskSession(t.Context(), "task-1"))

	require.Nil(t, svc.sessionClient.startedTask)
	require.Empty(t, svc.sessionClient.agentPanes)
	require.False(t, svc.workspace.bootstrapCalled)
	require.Zero(t, svc.claudeRepo.sessionEnvCalls)
	require.Equal(t, "%1", svc.agentSessionsWithoutIDs()[0].TmuxPane)
}

func TestReconnect_FailsWithoutLaunchingWhenTheSessionCannotBeInspected(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.sessionClient.inspectErr = errors.New("tmux protocol failure")

	err := svc.service.ReconnectTaskSession(t.Context(), "task-1")

	require.EqualError(t, err, "inspect task session: tmux protocol failure")
	require.Nil(t, svc.sessionClient.startedTask)
}

// The recreated Session's task window still runs the shell that Claude is
// resuming in when a status cycle looks at it.
func TestReconnect_ARestoredAgentStillStartingIsKeptUntilItsLaunchGraceEnds(t *testing.T) {
	svc := newLostSessionHarness(t)
	svc.observation.statusCacheMaxAge = time.Nanosecond
	require.NoError(t, svc.service.ReconnectTaskSession(t.Context(), "task-1"))
	svc.recreatedSessionPanes(map[string]string{"%50": "zsh", "%100": "codex"}, "%50", "%100")

	svc.observation.now = func() time.Time { return reconnectedAt.Add(agentLaunchGrace - time.Second) }
	update := svc.latestTaskStatus(t)
	require.Len(t, update.AgentSessions, 2)
	require.Equal(t, TaskStatusPhaseStarting, update.AgentSessions[0].Status.Phase)

	svc.observation.now = func() time.Time { return reconnectedAt.Add(agentLaunchGrace) }
	update = svc.latestTaskStatus(t)
	require.Len(t, update.AgentSessions, 1)
	require.Equal(t, ProviderCodex, update.AgentSessions[0].Provider)
}

// A status cycle runs while Reconnect is still launching Codex, and sees the
// recreated Session without Codex's agent session in a pane yet.
func TestReconnect_KeepsAnAgentSessionNotMovedYetThroughAStatusCycle(t *testing.T) {
	svc := newLostSessionHarness(t)
	svc.observation.statusCacheMaxAge = time.Nanosecond
	codexID := svc.taskRepo.agentSessions[1].ID
	svc.sessionClient.mu.Lock()
	svc.sessionClient.agentPaneOpened = func(TmuxPaneRef) {
		svc.recreatedSessionPanes(map[string]string{"%50": "claude", "%100": "zsh"}, "%50", "%100")
		svc.latestTaskStatus(t)
	}
	svc.sessionClient.mu.Unlock()

	require.NoError(t, svc.service.ReconnectTaskSession(t.Context(), "task-1"))

	codex := svc.taskRepo.agentSessions[1]
	require.Equal(t, codexID, codex.ID)
	require.True(t, codex.IsOpen())
	require.Equal(t, "%100", codex.TmuxPane)
	require.Equal(t, "codex-1", codex.ProviderSessionID)
}

// The launch grace runs out while Reconnect is still launching Codex, and a
// status cycle ends Codex's agent session, which has no pane yet.
func TestReconnect_TracksAnAgentWhoseAgentSessionEndedBeforeItsMove(t *testing.T) {
	svc := newLostSessionHarness(t)
	svc.observation.statusCacheMaxAge = time.Nanosecond
	graceOver := reconnectedAt.Add(agentLaunchGrace)
	svc.sessionClient.mu.Lock()
	svc.sessionClient.agentPaneOpened = func(TmuxPaneRef) {
		svc.recreatedSessionPanes(map[string]string{"%50": "claude", "%100": "zsh"}, "%50", "%100")
		svc.observation.now = func() time.Time { return graceOver }
		svc.latestTaskStatus(t)
	}
	svc.sessionClient.mu.Unlock()

	require.NoError(t, svc.service.ReconnectTaskSession(t.Context(), "task-1"))

	// Codex runs in %100, so it gets a new agent session there, which its
	// first hook event gives its conversation.
	sessions := svc.agentSessionsWithoutIDs()
	require.Len(t, sessions, 3)
	require.False(t, sessions[1].IsOpen())
	require.Equal(t, AgentSession{
		TaskID:      "task-1",
		Provider:    ProviderCodex,
		TmuxServer:  agentTestServer,
		TmuxPane:    "%100",
		PaneTrusted: true,
		StartedAt:   graceOver,
		LaunchedAt:  graceOver,
	}, sessions[2])
}

// A status cycle recovered Claude's status from its transcript before the
// Session was lost, while no hook event had given it one.
func TestReconnect_ARestoredAgentStartsRatherThanShowingTheStatusRecoveredBefore(t *testing.T) {
	svc := newAgentSessionHarness(t)
	observer := svc.manualStatusObserver()
	svc.agentPanes("claude")
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventSessionStart, "claude-1", 101, "%1", 0), "")
	working := agentStatus(TaskStatusPhaseWorking, "PostToolUse", 5)
	svc.claudeRepo.statusRecoveryUpdate = &working
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, err := svc.service.SubscribeTaskStatus(ctx, "task-1")
	require.NoError(t, err)
	observer.runCycle()
	require.Equal(t, TaskStatusPhaseWorking, receiveTaskStatus(t, stream).Phase)
	svc.loseSession()
	svc.observation.now = func() time.Time { return reconnectedAt }

	require.NoError(t, svc.service.ReconnectTaskSession(t.Context(), "task-1"))

	update := receiveTaskStatus(t, stream)
	require.Equal(t, TaskStatusPhaseStarting, update.Phase)
	require.Equal(t, restoredStarting, update.AgentSessions[0].Status)
}

func TestReconnect_KeepsTheAgentSessionARestoredAgentsFirstHookOpened(t *testing.T) {
	svc := newLostSessionHarness(t)
	svc.sessionClient.mu.Lock()
	svc.sessionClient.locatePaneByPID[202] = "%100"
	svc.sessionClient.agentPaneOpened = func(TmuxPaneRef) {
		// Codex resumes and reports its conversation before Rig moves its
		// agent session to the new pane.
		svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventSessionStart, "codex-1", 202, "%100", 31),
			TaskStatusPhaseStarting)
	}
	svc.sessionClient.mu.Unlock()

	require.NoError(t, svc.service.ReconnectTaskSession(t.Context(), "task-1"))

	sessions := svc.agentSessionsWithoutIDs()
	require.Len(t, sessions, 3)
	require.Equal(t, "%50", sessions[0].TmuxPane)
	require.False(t, sessions[1].IsOpen())
	require.Equal(t, AgentSession{
		TaskID:            "task-1",
		Provider:          ProviderCodex,
		TmuxServer:        agentTestServer,
		TmuxPane:          "%100",
		PaneTrusted:       true,
		ProviderSessionID: "codex-1",
		StartedAt:         agentTestStart.Add(31 * time.Minute),
		Status:            agentStatus(TaskStatusPhaseStarting, HookEventSessionStart, 31),
	}, sessions[2])
}

// lostDaemonCodexHarness is the agent session harness after task-1's Session
// was lost while a Codex you started yourself, on the shared daemon, worked in
// %9 on conversation codex-a. Its hooks come from PID 501, in no pane.
func lostDaemonCodexHarness(t *testing.T) *testTaskServiceHarness {
	t.Helper()
	svc := newAgentSessionHarness(t)
	svc.observation.recoveryPollInterval = time.Hour
	svc.daemonCodexPanes("%9")
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventUserPromptSubmit, "codex-a", 501, "%16", 0),
		TaskStatusPhaseWorking)
	svc.loseSession()
	svc.observation.now = func() time.Time { return reconnectedAt }
	return svc
}

// onFirstSessionEnvironment runs hook once, while Reconnect prepares a
// provider: after it read the agent sessions and before it detaches them.
func onFirstSessionEnvironment(state *providerClientState, hook func()) {
	fired := false
	state.sessionEnvRan = func() {
		if !fired {
			fired = true
			hook()
		}
	}
}

// The daemon reports Codex's turn ending while Reconnect prepares Codex.
func TestReconnect_RestoresAnAgentWhoseStatusAloneChangedMeanwhile(t *testing.T) {
	svc := lostDaemonCodexHarness(t)
	onFirstSessionEnvironment(&svc.providerRepo, func() {
		svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventStop, "codex-a", 501, "%16", 20),
			TaskStatusPhaseWaitingForInput)
	})

	require.NoError(t, svc.service.ReconnectTaskSession(t.Context(), "task-1"))

	require.Equal(t, []string{"codex", "resume", "codex-a"}, svc.sessionClient.startedLaunch.Command)
	sessions := svc.agentSessionsWithoutIDs()
	require.Len(t, sessions, 1)
	require.Equal(t, "%50", sessions[0].TmuxPane)
	require.Equal(t, AgentSessionStatus{}, sessions[0].Status)
}

// The daemon reports a new conversation while Reconnect prepares Codex, and
// moves Codex's agent session to it.
func TestReconnect_NamesAnAgentAHookMovedMeanwhile(t *testing.T) {
	svc := lostDaemonCodexHarness(t)
	onFirstSessionEnvironment(&svc.providerRepo, func() {
		svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventSessionStart, "codex-b", 501, "%16", 20),
			TaskStatusPhaseStarting)
	})

	err := svc.service.ReconnectTaskSession(t.Context(), "task-1")

	require.EqualError(t, err,
		"reconnect: codex conversation codex-a not restored: its agent session changed during the reconnect")
	// Nothing was left to restore, so the launch provider starts fresh, and
	// the moved agent session is left as it is.
	require.Equal(t, []string{"claude"}, svc.sessionClient.startedLaunch.Command)
	sessions := svc.agentSessionsWithoutIDs()
	require.True(t, sessions[0].IsOpen())
	require.Equal(t, "%9", sessions[0].TmuxPane)
	require.Equal(t, "codex-b", sessions[0].ProviderSessionID)
}

// Rig launched Codex beside Claude, and the Session was lost; Codex's first
// hook event arrives while Reconnect prepares Claude.
func TestReconnect_KeepsAnAgentSessionThatGotAConversationMeanwhile(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.observation.recoveryPollInterval = time.Hour
	svc.agentPanes("claude")
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventUserPromptSubmit, "claude-1", 101, "%1", 0),
		TaskStatusPhaseWorking)
	svc.recordLaunchedCodex(t, "%100")
	svc.loseSession()
	svc.observation.now = func() time.Time { return reconnectedAt }
	svc.sessionClient.mu.Lock()
	svc.sessionClient.locatePaneByPID[201] = "%100"
	svc.sessionClient.mu.Unlock()
	onFirstSessionEnvironment(&svc.claudeRepo, func() {
		svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventSessionStart, "codex-1", 201, "%100", 31),
			TaskStatusPhaseStarting)
	})

	require.NoError(t, svc.service.ReconnectTaskSession(t.Context(), "task-1"))

	codex := svc.agentSessionsWithoutIDs()[1]
	require.True(t, codex.IsOpen())
	require.Equal(t, "codex-1", codex.ProviderSessionID)
}

func TestReconnect_MovesEachAgentSessionWithItsOwnLaunchTime(t *testing.T) {
	svc := newLostSessionHarness(t)
	codexLaunchedAt := reconnectedAt.Add(5 * time.Second)
	svc.sessionClient.mu.Lock()
	svc.sessionClient.agentPaneOpened = func(TmuxPaneRef) {
		svc.observation.now = func() time.Time { return codexLaunchedAt }
	}
	svc.sessionClient.mu.Unlock()

	require.NoError(t, svc.service.ReconnectTaskSession(t.Context(), "task-1"))

	sessions := svc.agentSessionsWithoutIDs()
	require.Equal(t, reconnectedAt, sessions[0].LaunchedAt)
	require.Equal(t, codexLaunchedAt, sessions[1].LaunchedAt)
}

func TestReconnect_ASessionTmuxCannotCreateLeavesItsAgentsToTheNextReconnect(t *testing.T) {
	svc := newLostSessionHarness(t)
	svc.sessionClient.startErr = errors.New("tmux new-session failed")

	err := svc.service.ReconnectTaskSession(t.Context(), "task-1")

	require.EqualError(t, err, "reconnect task session: tmux new-session failed")
	require.Empty(t, svc.sessionClient.agentPanes)
	for _, session := range svc.agentSessionsWithoutIDs() {
		require.True(t, session.IsOpen())
		require.NotEmpty(t, session.ProviderSessionID)
	}
	// They are lost, so the task shows stopped rather than agents starting.
	update := svc.latestTaskStatus(t)
	require.Equal(t, TaskStatusPhaseStopped, update.Phase)
	require.Empty(t, update.AgentSessions)

	svc.sessionClient.startErr = nil
	require.NoError(t, svc.service.ReconnectTaskSession(t.Context(), "task-1"))
	require.Equal(t, []string{"claude", "resume", "claude-1"}, svc.sessionClient.startedLaunch.Command)
	require.Len(t, svc.sessionClient.agentPanes, 1)
	require.Equal(t, []string{"codex", "resume", "codex-1"}, svc.sessionClient.agentPanes[0].launch.Command)
}

func TestReconnect_NamesEveryAgentItLeavesBehind(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.observation.recoveryPollInterval = time.Hour
	svc.agentPanes("claude", "codex", "claude")
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventUserPromptSubmit, "claude-1", 101, "%1", 0),
		TaskStatusPhaseWorking)
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventUserPromptSubmit, "codex-1", 102, "%2", 1),
		TaskStatusPhaseWorking)
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventUserPromptSubmit, "claude-2", 103, "%3", 2),
		TaskStatusPhaseWorking)
	svc.loseSession()
	svc.providerConfig.setup = &ProviderSetup{Configured: []Provider{ProviderClaude}, Default: ProviderClaude}
	svc.sessionClient.agentPaneErrByCommand = map[string]error{"claude": errors.New("create pane failed")}

	err := svc.service.ReconnectTaskSession(t.Context(), "task-1")

	require.EqualError(t, err, `reconnect: codex conversation codex-1 not restored: `+
		`provider "codex" is not configured: run rig setup to enable it; `+
		`claude conversation claude-2 not restored: split agent pane: create pane failed`)
}

func TestReconnect_ASessionTmuxCannotCreateStillNamesTheSkippedAgents(t *testing.T) {
	for name, setup := range map[string]func(*testTaskServiceHarness){
		// Claude is restored in the task window.
		"with an agent to restore": func(*testTaskServiceHarness) {},
		// Nothing is left to restore, so Claude, the launch provider, starts
		// fresh.
		"with nothing to restore": func(svc *testTaskServiceHarness) {
			svc.taskRepo.mu.Lock()
			defer svc.taskRepo.mu.Unlock()
			svc.taskRepo.agentSessions = svc.taskRepo.agentSessions[1:]
		},
	} {
		t.Run(name, func(t *testing.T) {
			svc := newLostSessionHarness(t)
			setup(svc)
			svc.providerConfig.setup = &ProviderSetup{Configured: []Provider{ProviderClaude}, Default: ProviderClaude}
			svc.sessionClient.startErr = errors.New("tmux new-session failed")

			err := svc.service.ReconnectTaskSession(t.Context(), "task-1")

			require.EqualError(t, err, `reconnect task session: tmux new-session failed; `+
				`codex conversation codex-1 not restored: `+
				`provider "codex" is not configured: run rig setup to enable it`)
		})
	}
}

// An upgrade made Claude's last conversation an agent session without a pane
// while the Session was alive, and the running Claude's next hook event opened
// its own before any status cycle ended the migrated one. Then the Session was
// lost.
func TestReconnect_ResumesAConversationTwoAgentSessionsHoldOnce(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.taskRepo.agentSessions = []AgentSession{{
		ID:                "task-1",
		TaskID:            "task-1",
		Provider:          ProviderClaude,
		ProviderSessionID: "claude-1",
		StartedAt:         agentTestStart,
	}}
	svc.agentPanes("claude")
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventUserPromptSubmit, "claude-1", 101, "%1", 5),
		TaskStatusPhaseWorking)
	svc.loseSession()
	svc.observation.now = func() time.Time { return reconnectedAt }

	require.NoError(t, svc.service.ReconnectTaskSession(t.Context(), "task-1"))

	require.Equal(t, []string{"claude", "resume", "claude-1"}, svc.sessionClient.startedLaunch.Command)
	require.Empty(t, svc.sessionClient.agentPanes)
	sessions := svc.agentSessionsWithoutIDs()
	require.Len(t, sessions, 2)
	require.False(t, sessions[0].IsOpen())
	require.True(t, sessions[1].IsOpen())
	require.Equal(t, "%50", sessions[1].TmuxPane)
}
