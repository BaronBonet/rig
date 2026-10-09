package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// subscribeTaskStatus subscribes to task-1's status and returns the stream
// after its first update, which shows the task as the test set it up.
func (h *testTaskServiceHarness) subscribeTaskStatus(t *testing.T) <-chan TaskStatusUpdate {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	stream, err := h.service.SubscribeTaskStatus(ctx, "task-1")
	require.NoError(t, err)
	receiveTaskStatus(t, stream)
	return stream
}

// launchProvider reads task-1 back and returns its launch provider.
func (h *testTaskServiceHarness) launchProvider(t *testing.T) Provider {
	t.Helper()
	tasks, err := h.service.ListTasks(t.Context())
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	return tasks[0].Provider
}

// newFreshLaunchHarness is the agent session harness with task-1's Session
// gone and nothing to restore, its prompt set, and time standing still at
// launchedAt for the observation. Reconnecting it launches Claude, the launch
// provider, fresh in the recreated Session's task pane, %50.
func newFreshLaunchHarness(t *testing.T, launchedAt time.Time) *testTaskServiceHarness {
	t.Helper()
	svc := newAgentSessionHarness(t)
	svc.taskRepo.listTasks[0].Prompt = "fix the flaky retry test"
	svc.observation.recoveryPollInterval = time.Hour
	svc.loseSession()
	svc.observation.now = func() time.Time { return launchedAt }
	return svc
}

// launchFresh reconnects task-1 so Claude launches fresh in %50, then lays the
// recreated Session out with %50 running command.
func (h *testTaskServiceHarness) launchFresh(t *testing.T, command string) {
	t.Helper()
	require.NoError(t, h.service.ReconnectTaskSession(t.Context(), "task-1"))
	h.recreatedSessionPanes(map[string]string{"%50": command}, "%50")
}

func TestLaunchedAgentSession_TheAgentsFirstHookGivesItsAgentSessionAConversation(t *testing.T) {
	launchedAt := agentTestStart.Add(5 * time.Minute)
	svc := newFreshLaunchHarness(t, launchedAt)
	svc.launchFresh(t, "claude")
	launched := svc.taskRepo.agentSessions[0].ID

	svc.sessionClient.mu.Lock()
	svc.sessionClient.locatePaneByPID[201] = "%50"
	svc.sessionClient.mu.Unlock()
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventSessionStart, "claude-1", 201, "%50", 6),
		TaskStatusPhaseStarting)

	require.Equal(t, []AgentSession{{
		TaskID:            "task-1",
		Provider:          ProviderClaude,
		TmuxServer:        agentTestServer,
		TmuxPane:          "%50",
		PaneTrusted:       true,
		ProviderSessionID: "claude-1",
		StartedAt:         launchedAt,
		Status:            agentStatus(TaskStatusPhaseStarting, HookEventSessionStart, 6),
	}}, svc.agentSessionsWithoutIDs())
	require.Equal(t, launched, svc.taskRepo.agentSessions[0].ID)
}

func TestLaunchedAgentSession_KeepsTheAgentSessionAHookFromTheAgentOpenedFirst(t *testing.T) {
	svc := newFreshLaunchHarness(t, agentTestStart.Add(5*time.Minute))
	svc.sessionClient.mu.Lock()
	svc.sessionClient.locatePaneByPID[201] = "%50"
	svc.sessionClient.mu.Unlock()
	svc.sessionClient.startOpened = func(TmuxPaneRef) {
		svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventSessionStart, "claude-1", 201, "%50", 6),
			TaskStatusPhaseStarting)
	}

	require.NoError(t, svc.service.ReconnectTaskSession(t.Context(), "task-1"))

	sessions := svc.agentSessionsWithoutIDs()
	require.Len(t, sessions, 1)
	require.Equal(t, "%50", sessions[0].TmuxPane)
	require.Equal(t, "claude-1", sessions[0].ProviderSessionID)
	require.True(t, sessions[0].LaunchedAt.IsZero())
	require.Equal(t, agentStatus(TaskStatusPhaseStarting, HookEventSessionStart, 6), sessions[0].Status)
}

// A SessionEnd before any other hook names no conversation the agent session
// has, so it neither ends the agent session nor gives it one.
func TestLaunchedAgentSession_ASessionEndBeforeTheFirstHookChangesNothing(t *testing.T) {
	svc := newFreshLaunchHarness(t, agentTestStart.Add(5*time.Minute))
	svc.launchFresh(t, "claude")

	svc.sessionClient.mu.Lock()
	svc.sessionClient.locatePaneByPID[201] = "%50"
	svc.sessionClient.mu.Unlock()
	svc.handleAgentHook(t, sessionEndHook(ProviderClaude, "claude-old", 201, "%50", 6, "other"), "")

	sessions := svc.agentSessionsWithoutIDs()
	require.Len(t, sessions, 1)
	require.True(t, sessions[0].IsOpen())
	require.Empty(t, sessions[0].ProviderSessionID)
}

// After Claude's first hook its launch is over: when it exits a moment later,
// the next cycle ends its agent session without waiting for the grace.
func TestLaunchedAgentSession_AnAgentsFirstHookEndsItsLaunchGrace(t *testing.T) {
	launchedAt := agentTestStart.Add(5 * time.Minute)
	svc := newFreshLaunchHarness(t, launchedAt)
	svc.observation.statusCacheMaxAge = time.Nanosecond
	svc.launchFresh(t, "claude")
	svc.sessionClient.mu.Lock()
	svc.sessionClient.locatePaneByPID[201] = "%50"
	svc.sessionClient.mu.Unlock()
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventSessionStart, "claude-1", 201, "%50", 5),
		TaskStatusPhaseStarting)

	// Claude exited back to the shell, inside what was its launch grace.
	svc.recreatedSessionPanes(map[string]string{"%50": "zsh"}, "%50")
	svc.observation.now = func() time.Time { return launchedAt.Add(3 * time.Second) }

	update := svc.latestTaskStatus(t)
	require.Empty(t, update.AgentSessions)
	require.False(t, svc.agentSessionsWithoutIDs()[0].IsOpen())
}

// The recreated task pane's shell has not started Claude yet when status
// cycles look at it.
func TestLaunchedAgentSession_AnAgentStillStartingIsKeptUntilItsLaunchGraceEnds(t *testing.T) {
	launchedAt := agentTestStart.Add(5 * time.Minute)
	svc := newFreshLaunchHarness(t, launchedAt)
	svc.observation.statusCacheMaxAge = time.Nanosecond
	svc.launchFresh(t, "zsh")

	svc.observation.now = func() time.Time { return launchedAt.Add(agentLaunchGrace - time.Second) }
	update := svc.latestTaskStatus(t)
	require.Len(t, update.AgentSessions, 1)
	require.Equal(t, TaskStatusPhaseStarting, update.AgentSessions[0].Status.Phase)

	svc.observation.now = func() time.Time { return launchedAt.Add(agentLaunchGrace) }
	update = svc.latestTaskStatus(t)
	require.Empty(t, update.AgentSessions)
	require.False(t, svc.agentSessionsWithoutIDs()[0].IsOpen())
}

func TestLaunchedAgentSession_AnAgentRigCouldNotRecordStillLaunches(t *testing.T) {
	svc := newFreshLaunchHarness(t, agentTestStart.Add(5*time.Minute))
	svc.taskRepo.createAgentSessionErr = errors.New("database is locked")

	// The agent runs, so Reconnect succeeds; its first hook event records its
	// agent session.
	require.NoError(t, svc.service.ReconnectTaskSession(t.Context(), "task-1"))
	require.Equal(t, []string{"claude"}, svc.sessionClient.startedLaunch.Command)
	require.Empty(t, svc.agentSessionsWithoutIDs())
	require.Equal(t, ProviderClaude, svc.launchProvider(t))
}

func TestTaskServiceCreateTask_RecordsNoAgentSessionWithoutAReportedPane(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerRepo.suggestedName = "billing retry flow"

	_, err := svc.service.CreateTaskWithProgress(t.Context(), CreateTaskInput{
		Cwd:    "/tmp/repo",
		Prompt: "add billing retry flow",
	}, nil)

	// tmux did not say which pane the agent runs in, so its first hook event
	// opens its agent session instead.
	require.NoError(t, err)
	require.Empty(t, svc.agentSessionsWithoutIDs())
}

func TestTaskServiceCreateTask_ShowsItsAgentStartingInTheTaskPane(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerRepo.suggestedName = "billing retry flow"
	svc.sessionClient.locateServer = agentTestServer
	svc.sessionClient.startPane = TmuxPaneRef{Server: agentTestServer, ID: "%0"}

	task, err := svc.service.CreateTaskWithProgress(t.Context(), CreateTaskInput{
		Cwd:    "/tmp/repo",
		Prompt: "add billing retry flow",
	}, nil)

	require.NoError(t, err)
	sessions := svc.agentSessionsWithoutIDs()
	require.Len(t, sessions, 1)
	require.Equal(t, task.ID, sessions[0].TaskID)
	require.Equal(t, ProviderCodex, sessions[0].Provider)
	require.Equal(t, agentTestServer, sessions[0].TmuxServer)
	require.Equal(t, "%0", sessions[0].TmuxPane)
	require.True(t, sessions[0].PaneTrusted)
	require.False(t, sessions[0].LaunchedAt.IsZero())

	// Before Codex's first hook, while its pane still runs the shell, the task
	// shows its agent starting.
	svc.taskRepo.listTasks = []*Task{task}
	svc.sessionClient.locatePanes = []TmuxPane{{ID: "%0", Session: task.TmuxSession, Command: "zsh"}}
	svc.sessionClient.locateTaskSessionPanes = map[string][]string{task.ID: {"%0"}}
	update, err := svc.service.LatestTaskStatus(t.Context(), task.ID)
	require.NoError(t, err)
	require.Equal(t, TaskStatusPhaseStarting, update.Phase)
	require.Len(t, update.AgentSessions, 1)
	require.Equal(t, TmuxPaneRef{Server: agentTestServer, ID: "%0"}, update.AgentSessions[0].Pane)
}
