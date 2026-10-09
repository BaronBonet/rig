package core

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// agentPanes lays out task-1's Session with one pane per command, %1, %2 and
// so on, and traces PID 101 to %1, 102 to %2, and so on.
func (h *testTaskServiceHarness) agentPanes(commands ...string) {
	h.sessionClient.mu.Lock()
	defer h.sessionClient.mu.Unlock()
	h.sessionClient.locatePanes = nil
	h.sessionClient.locateTaskSessionPanes = map[string][]string{}
	h.sessionClient.locatePaneByPID = map[int]string{}
	for index, command := range commands {
		pane := fmt.Sprintf("%%%d", index+1)
		h.sessionClient.locatePanes = append(h.sessionClient.locatePanes, TmuxPane{
			ID:      pane,
			Session: "repo_task",
			Command: command,
		})
		h.sessionClient.locateTaskSessionPanes["task-1"] = append(
			h.sessionClient.locateTaskSessionPanes["task-1"],
			pane,
		)
		h.sessionClient.locatePaneByPID[101+index] = pane
	}
}

// manualStatusObserver replaces the harness's status observer with one that
// runs a cycle only when the test calls runCycle, which returns once the
// cycle has published.
func (h *testTaskServiceHarness) manualStatusObserver() *taskStatusObserver {
	observer := &taskStatusObserver{
		observation: h.observation,
		tasks:       make(map[string]*observedTaskStatus),
		wakeCycle:   make(chan struct{}, 1),
	}
	h.observation.statusObserver = observer
	return observer
}

// holdInspection makes tmux snapshots wait until release is closed, and
// signals started when one begins.
func (h *testTaskServiceHarness) holdInspection() (started chan struct{}, release chan struct{}) {
	started = make(chan struct{}, 1)
	release = make(chan struct{})
	h.sessionClient.mu.Lock()
	defer h.sessionClient.mu.Unlock()
	h.sessionClient.batchInspectStarted = started
	h.sessionClient.batchInspectRelease = release
	return started, release
}

func waitForSignal(t *testing.T, signal <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting: %s", what)
	}
}

// requireNoTaskStatus fails if a status was published to stream. A cycle run
// through runCycle publishes before it returns, so there is nothing to wait
// for.
func requireNoTaskStatus(t *testing.T, stream <-chan TaskStatusUpdate) {
	t.Helper()
	select {
	case update := <-stream:
		t.Fatalf("unexpected task status: %+v", update)
	default:
	}
}

func (h *testTaskServiceHarness) latestTaskStatus(t *testing.T) *TaskStatusUpdate {
	t.Helper()
	update, err := h.service.LatestTaskStatus(t.Context(), "task-1")
	require.NoError(t, err)
	return update
}

func TestAgentSessionStatus_TheTaskNeedsInputWhileAnotherAgentKeepsWorking(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.agentPanes("claude", "claude")

	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventUserPromptSubmit, "sess-main", 101, "%1", 0),
		TaskStatusPhaseWorking)
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventStop, "sess-side", 102, "%2", 1),
		TaskStatusPhaseWaitingForInput)

	require.Equal(t, TaskStatusUpdate{
		TaskID:       "task-1",
		Provider:     ProviderClaude,
		Phase:        TaskStatusPhaseWaitingForInput,
		RawEventName: HookEventStop,
		ObservedAt:   agentTestStart.Add(time.Minute),
	}, taskLevelStatus(*svc.latestTaskStatus(t)))
	sessions := svc.agentSessionsWithoutIDs()
	require.Len(t, sessions, 2)
	require.Equal(t, agentStatus(TaskStatusPhaseWorking, HookEventUserPromptSubmit, 0), sessions[0].Status)
	require.Equal(t, agentStatus(TaskStatusPhaseWaitingForInput, HookEventStop, 1), sessions[1].Status)
}

func TestAgentSessionStatus_ASideAgentsStopNeverChangesAnotherAgentsStatus(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.agentPanes("claude", "claude")
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventUserPromptSubmit, "sess-main", 101, "%1", 0),
		TaskStatusPhaseWorking)
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventUserPromptSubmit, "sess-side", 102, "%2", 1),
		TaskStatusPhaseWorking)

	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventStop, "sess-side", 102, "%2", 2),
		TaskStatusPhaseWaitingForInput)
	require.Equal(t, agentStatus(TaskStatusPhaseWorking, HookEventUserPromptSubmit, 0),
		svc.agentSessionsWithoutIDs()[0].Status)

	// When the main agent also needs input, the most recent one leads.
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventStop, "sess-main", 101, "%1", 3),
		TaskStatusPhaseWaitingForInput)
	update := svc.latestTaskStatus(t)
	require.Equal(t, TaskStatusPhaseWaitingForInput, update.Phase)
	require.Equal(t, agentTestStart.Add(3*time.Minute), update.ObservedAt)
}

func TestAgentSessionStatus_MixedProvidersNeitherAdoptNorFlipTheTask(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.agentPanes("claude", "codex")

	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventStop, "claude-a", 101, "%1", 0),
		TaskStatusPhaseWaitingForInput)
	// Codex started next to Claude, without the shared daemon.
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventSessionStart, "codex-a", 102, "%2", 1),
		TaskStatusPhaseStarting)
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventPostToolUse, "codex-a", 102, "%2", 2),
		TaskStatusPhaseWorking)

	update := svc.latestTaskStatus(t)
	require.Equal(t, ProviderClaude, update.Provider)
	require.Equal(t, TaskStatusPhaseWaitingForInput, update.Phase)

	// Once Claude works again, the most recent working agent leads.
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventPostToolUse, "codex-a", 102, "%2", 4),
		TaskStatusPhaseWorking)
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventUserPromptSubmit, "claude-a", 101, "%1", 3),
		TaskStatusPhaseWorking)
	update = svc.latestTaskStatus(t)
	require.Equal(t, ProviderCodex, update.Provider)
	require.Equal(t, TaskStatusPhaseWorking, update.Phase)

	// The task record never takes another provider.
	require.Nil(t, svc.taskRepo.updatedTask)
	require.Len(t, svc.agentSessionsWithoutIDs(), 2)
}

func TestAgentSessionStatus_ClosingAnAgentsPaneEndsItsAgentSession(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.agentPanes("claude", "claude")
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventUserPromptSubmit, "sess-main", 101, "%1", 0),
		TaskStatusPhaseWorking)
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventStop, "sess-side", 102, "%2", 1),
		TaskStatusPhaseWaitingForInput)

	// The side agent's pane closed; the Session lives on.
	svc.agentPanes("claude")

	update := svc.latestTaskStatus(t)
	require.Equal(t, TaskStatusPhaseWorking, update.Phase)
	sessions := svc.agentSessionsWithoutIDs()
	require.True(t, sessions[0].IsOpen())
	require.False(t, sessions[1].IsOpen())
}

func TestAgentSessionStatus_LosingTheSessionKeepsAgentSessionsForReconnect(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.agentPanes("claude")
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventUserPromptSubmit, "sess-main", 101, "%1", 0),
		TaskStatusPhaseWorking)

	// tmux is gone, and the Session with it.
	svc.agentPanes()

	require.Equal(t, &TaskStatusUpdate{
		TaskID:       "task-1",
		Provider:     ProviderClaude,
		Phase:        TaskStatusPhaseStopped,
		RawEventName: "TaskSessionStopped",
		ObservedAt:   agentTestStart,
	}, svc.latestTaskStatus(t))
	require.True(t, svc.agentSessionsWithoutIDs()[0].IsOpen())
}

func TestAgentSessionStatus_MissingProcessEvidenceChangesNothing(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.agentPanes("claude")
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventUserPromptSubmit, "sess-main", 101, "%1", 0),
		TaskStatusPhaseWorking)

	svc.sessionClient.mu.Lock()
	svc.sessionClient.locatePanes[0].Command = "zsh"
	svc.sessionClient.locatePanes[0].ChildProcessEvidenceUnavailable = true
	svc.sessionClient.mu.Unlock()

	require.Equal(t, TaskStatusPhaseWorking, svc.latestTaskStatus(t).Phase)
	require.True(t, svc.agentSessionsWithoutIDs()[0].IsOpen())
}

func TestAgentSessionStatus_ATmuxServerRestartEndsAgentSessions(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.agentPanes("claude")
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventUserPromptSubmit, "sess-main", 101, "%1", 0),
		TaskStatusPhaseWorking)

	// A new tmux server hosts the Session again, and its first pane is %1
	// once more, running another Claude.
	svc.sessionClient.mu.Lock()
	svc.sessionClient.locateServer = TmuxServer{SocketPath: agentTestServer.SocketPath, PID: 9001}
	svc.sessionClient.mu.Unlock()

	require.Equal(t, TaskStatusPhaseStopped, svc.latestTaskStatus(t).Phase)
	require.False(t, svc.agentSessionsWithoutIDs()[0].IsOpen())
}

func TestAgentSessionStatus_ALiveAgentWithoutAStatusIsStarting(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.agentPanes("claude")

	// A subagent starting maps to no status, but places the agent.
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventSubagentStart, "sess-main", 101, "%1", 2), "")

	require.Equal(t, TaskStatusUpdate{
		TaskID:     "task-1",
		Provider:   ProviderClaude,
		Phase:      TaskStatusPhaseStarting,
		ObservedAt: agentTestStart.Add(2 * time.Minute),
	}, taskLevelStatus(*svc.latestTaskStatus(t)))
}

// The user exits Claude in %1 and starts Claude again there. The cycle's
// snapshot saw only the shell, and the new Claude's SessionStart then moved
// the agent session to its conversation: the cycle must not end it.
func TestAgentSessionStatus_ACycleKeepsAnAgentSessionAHookChangedAfterItsSnapshot(t *testing.T) {
	svc := newAgentSessionHarness(t)
	observer := svc.manualStatusObserver()
	svc.agentPanes("claude")
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventUserPromptSubmit, "sess-old", 101, "%1", 0),
		TaskStatusPhaseWorking)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, err := svc.service.SubscribeTaskStatus(ctx, "task-1")
	require.NoError(t, err)

	// Claude exited: the pane runs only the shell when the cycle inspects tmux.
	svc.sessionClient.mu.Lock()
	svc.sessionClient.locatePanes[0].Command = "zsh"
	svc.sessionClient.locatePaneByPID[201] = "%1"
	svc.sessionClient.mu.Unlock()
	started, release := svc.holdInspection()
	cycleDone := make(chan struct{})
	go func() {
		observer.runCycle()
		close(cycleDone)
	}()
	waitForSignal(t, started, "the cycle never inspected tmux")

	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventSessionStart, "sess-new", 201, "%1", 1),
		TaskStatusPhaseStarting)
	require.Equal(t, TaskStatusPhaseStarting, receiveTaskStatus(t, stream).Phase)
	close(release)
	waitForSignal(t, cycleDone, "the cycle never finished")

	sessions := svc.agentSessionsWithoutIDs()
	require.Len(t, sessions, 1)
	require.Equal(t, "sess-new", sessions[0].ProviderSessionID)
	require.True(t, sessions[0].IsOpen())
	// The rebased view adds where the agent runs, which the hook's view could
	// not know before any cycle; the agent still counts.
	update := receiveTaskStatus(t, stream)
	require.Equal(t, TaskStatusPhaseStarting, update.Phase)
	require.Equal(t, &TmuxPaneLocation{}, update.AgentSessions[0].Location)
}

// closedPaneDuringAHook lays out Claude in %1 needing input and Claude in %2
// working; then %1 closes, and the next tmux snapshot is held.
func (h *testTaskServiceHarness) closedPaneDuringAHook(t *testing.T) (started chan struct{}, release chan struct{}) {
	t.Helper()
	h.observation.recoveryPollInterval = time.Hour
	h.agentPanes("claude", "claude")
	h.handleAgentHook(t, agentHook(ProviderClaude, HookEventStop, "sess-a", 101, "%1", 0),
		TaskStatusPhaseWaitingForInput)
	h.handleAgentHook(t, agentHook(ProviderClaude, HookEventUserPromptSubmit, "sess-b", 102, "%2", 1),
		TaskStatusPhaseWorking)

	h.sessionClient.mu.Lock()
	h.sessionClient.locatePanes = h.sessionClient.locatePanes[1:]
	h.sessionClient.locateTaskSessionPanes["task-1"] = []string{"%2"}
	h.sessionClient.mu.Unlock()
	return h.holdInspection()
}

// Nobody watches the task when a one-shot read runs a cycle, and the working
// agent's next hook lands while that cycle inspects tmux.
func TestAgentSessionStatus_AOneShotReadAHookOverlapsStillChecksLiveness(t *testing.T) {
	svc := newAgentSessionHarness(t)
	started, release := svc.closedPaneDuringAHook(t)

	type read struct {
		update *TaskStatusUpdate
		err    error
	}
	result := make(chan read, 1)
	go func() {
		update, err := svc.service.LatestTaskStatus(t.Context(), "task-1")
		result <- read{update: update, err: err}
	}()
	waitForSignal(t, started, "the cycle never inspected tmux")
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventPostToolUse, "sess-b", 102, "%2", 2),
		TaskStatusPhaseWorking)
	close(release)

	select {
	case got := <-result:
		require.NoError(t, got.err)
		require.NotNil(t, got.update)
		require.Equal(t, TaskStatusUpdate{
			TaskID:       "task-1",
			Provider:     ProviderClaude,
			Phase:        TaskStatusPhaseWorking,
			RawEventName: HookEventPostToolUse,
			ObservedAt:   agentTestStart.Add(2 * time.Minute),
		}, taskLevelStatus(*got.update))
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the one-shot read")
	}
}

func TestAgentSessionStatus_ACycleAHookOverlapsStillPublishesItsLiveness(t *testing.T) {
	svc := newAgentSessionHarness(t)
	started, release := svc.closedPaneDuringAHook(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, err := svc.service.SubscribeTaskStatus(ctx, "task-1")
	require.NoError(t, err)
	waitForSignal(t, started, "the cycle never inspected tmux")

	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventPostToolUse, "sess-b", 102, "%2", 2),
		TaskStatusPhaseWorking)
	// The hook cannot check liveness, so the closed pane's agent still leads.
	require.Equal(t, TaskStatusPhaseWaitingForInput, receiveTaskStatus(t, stream).Phase)
	close(release)

	// Once the cycle finishes, it no longer does.
	update := receiveTaskStatus(t, stream)
	require.Equal(t, TaskStatusPhaseWorking, update.Phase)
	require.Equal(t, agentTestStart.Add(2*time.Minute), update.ObservedAt)
}

func TestAgentSessionStatus_ReplacingAnotherTasksAgentSessionRepublishesThatTask(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.observation.recoveryPollInterval = time.Hour
	svc.taskRepo.listTasks = append(svc.taskRepo.listTasks, &Task{
		ID:           "task-2",
		WorktreePath: "/tmp/repo-other",
		TmuxSession:  "repo_other",
		Provider:     ProviderClaude,
	})
	svc.agentPanes("claude")
	svc.sessionClient.mu.Lock()
	svc.sessionClient.locatePanes = append(svc.sessionClient.locatePanes,
		TmuxPane{ID: "%2", Session: "repo_other", Command: "zsh"})
	svc.sessionClient.locateTaskSessionPanes["task-2"] = []string{"%2"}
	svc.sessionClient.locatePaneByPID[201] = "%1"
	svc.sessionClient.mu.Unlock()
	// A Claude in %1 works in task-2's workspace.
	other := agentHook(ProviderClaude, HookEventUserPromptSubmit, "claude-other", 201, "%1", 0)
	other.Cwd = "/tmp/repo-other"
	svc.handleAgentHook(t, other, TaskStatusPhaseWorking)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, err := svc.service.SubscribeTaskStatus(ctx, "task-2")
	require.NoError(t, err)
	require.Equal(t, TaskStatusPhaseWorking, receiveTaskStatus(t, stream).Phase)

	// The pane now runs an agent in task-1's workspace.
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventSessionStart, "claude-a", 101, "%1", 1),
		TaskStatusPhaseStarting)

	require.Equal(t, TaskStatusPhaseStopped, receiveTaskStatus(t, stream).Phase)
}

// A cycle that cannot judge an agent session keeps what the previous cycle
// learned about it: here, that the task's Session is gone.
func TestAgentSessionStatus_AnUnjudgedAgentSessionStaysLost(t *testing.T) {
	svc := newAgentSessionHarness(t)
	observer := svc.manualStatusObserver()
	svc.agentPanes("claude")
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventUserPromptSubmit, "sess-a", 101, "%1", 0),
		TaskStatusPhaseWorking)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, err := svc.service.SubscribeTaskStatus(ctx, "task-1")
	require.NoError(t, err)

	svc.sessionClient.mu.Lock()
	svc.sessionClient.locateTaskSessionPanes = map[string][]string{}
	svc.sessionClient.mu.Unlock()
	observer.runCycle()
	require.Equal(t, TaskStatusPhaseStopped, receiveTaskStatus(t, stream).Phase)

	// The Session is back, but the snapshot cannot identify the tmux server.
	svc.sessionClient.mu.Lock()
	svc.sessionClient.locateTaskSessionPanes = map[string][]string{"task-1": {"%1"}}
	svc.sessionClient.locateServer = TmuxServer{}
	svc.sessionClient.mu.Unlock()
	observer.runCycle()
	requireNoTaskStatus(t, stream)
	require.True(t, svc.agentSessionsWithoutIDs()[0].IsOpen())
}

// ...and here, the status recovered from its transcript.
func TestAgentSessionStatus_AnUnjudgedAgentSessionKeepsItsRecoveredStatus(t *testing.T) {
	svc := newAgentSessionHarness(t)
	observer := svc.manualStatusObserver()
	svc.agentPanes("claude")
	svc.claudeRepo.statusRecoveryUpdate = &AgentSessionStatus{
		Phase:        TaskStatusPhaseWaitingForInput,
		RawEventName: "TranscriptTurnInterrupted",
		ObservedAt:   agentTestStart.Add(time.Minute),
	}
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventUserPromptSubmit, "sess-a", 101, "%1", 0),
		TaskStatusPhaseWorking)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, err := svc.service.SubscribeTaskStatus(ctx, "task-1")
	require.NoError(t, err)

	observer.runCycle()
	require.Equal(t, "TranscriptTurnInterrupted", receiveTaskStatus(t, stream).RawEventName)

	// The next snapshot misses child process evidence, so it cannot tell
	// whether Claude still runs.
	svc.sessionClient.mu.Lock()
	svc.sessionClient.locatePanes[0].Command = "zsh"
	svc.sessionClient.locatePanes[0].ChildProcessEvidenceUnavailable = true
	svc.sessionClient.mu.Unlock()
	observer.runCycle()
	requireNoTaskStatus(t, stream)
}

func TestAgentSessionStatus_ExitingTheOnlyAgentStopsTheTaskAtOnce(t *testing.T) {
	svc := newAgentSessionHarness(t)
	observer := svc.manualStatusObserver()
	svc.agentPanes("claude")
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventUserPromptSubmit, "sess-a", 101, "%1", 0),
		TaskStatusPhaseWorking)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, err := svc.service.SubscribeTaskStatus(ctx, "task-1")
	require.NoError(t, err)
	observer.runCycle()
	require.Equal(t, TaskStatusPhaseWorking, receiveTaskStatus(t, stream).Phase)

	svc.handleAgentHook(t, sessionEndHook(ProviderClaude, "sess-a", 101, "%1", 1, "prompt_input_exit"), "")

	require.Equal(t, TaskStatusUpdate{
		TaskID:       "task-1",
		Provider:     ProviderClaude,
		Phase:        TaskStatusPhaseStopped,
		RawEventName: "TaskSessionStopped",
		ObservedAt:   agentTestStart,
	}, receiveTaskStatus(t, stream))
	// Claude may still be exiting when the next cycle looks at its pane.
	observer.runCycle()
	requireNoTaskStatus(t, stream)
	sessions := svc.agentSessionsWithoutIDs()
	require.Len(t, sessions, 1)
	require.Equal(t, agentTestStart.Add(time.Minute), sessions[0].EndedAt)
}

func TestAgentSessionStatus_ExitingOneAgentShowsTheOthersStatus(t *testing.T) {
	svc := newAgentSessionHarness(t)
	observer := svc.manualStatusObserver()
	svc.agentPanes("claude", "codex")
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventUserPromptSubmit, "sess-main", 101, "%1", 0),
		TaskStatusPhaseWorking)
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventStop, "codex-side", 102, "%2", 1),
		TaskStatusPhaseWaitingForInput)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, err := svc.service.SubscribeTaskStatus(ctx, "task-1")
	require.NoError(t, err)
	observer.runCycle()
	require.Equal(t, TaskStatusPhaseWaitingForInput, receiveTaskStatus(t, stream).Phase)

	svc.handleAgentHook(t, sessionEndHook(ProviderCodex, "codex-side", 102, "%2", 2, "other"), "")

	update := receiveTaskStatus(t, stream)
	require.Equal(t, ProviderClaude, update.Provider)
	require.Equal(t, TaskStatusPhaseWorking, update.Phase)
	sessions := svc.agentSessionsWithoutIDs()
	require.True(t, sessions[0].IsOpen())
	require.False(t, sessions[1].IsOpen())
}

// The status cycle and a SessionEnd can both end an agent session: whichever
// comes first ends it, and the other changes nothing.
func TestAgentSessionStatus_ASessionEndAfterACycleEndedTheAgentSessionChangesNothing(t *testing.T) {
	svc := newAgentSessionHarness(t)
	observer := svc.manualStatusObserver()
	svc.agentPanes("claude")
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventUserPromptSubmit, "sess-a", 101, "%1", 0),
		TaskStatusPhaseWorking)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, err := svc.service.SubscribeTaskStatus(ctx, "task-1")
	require.NoError(t, err)
	observer.runCycle()
	require.Equal(t, TaskStatusPhaseWorking, receiveTaskStatus(t, stream).Phase)

	// Claude exited and the cycle saw it first.
	svc.sessionClient.mu.Lock()
	svc.sessionClient.locatePanes[0].Command = "zsh"
	svc.sessionClient.mu.Unlock()
	cycleAt := agentTestStart.Add(5 * time.Minute)
	svc.observation.now = func() time.Time { return cycleAt }
	observer.runCycle()
	require.Equal(t, TaskStatusPhaseStopped, receiveTaskStatus(t, stream).Phase)

	svc.handleAgentHook(t, sessionEndHook(ProviderClaude, "sess-a", 101, "%1", 1, "prompt_input_exit"), "")

	sessions := svc.agentSessionsWithoutIDs()
	require.Len(t, sessions, 1)
	require.Equal(t, cycleAt, sessions[0].EndedAt)
	requireNoTaskStatus(t, stream)
}

// liveAgentSessionsWithoutIDs returns the live agent sessions an update
// carries, with their generated IDs cleared for comparison.
func liveAgentSessionsWithoutIDs(update *TaskStatusUpdate) []LiveAgentSession {
	sessions := cloneLiveAgentSessions(update.AgentSessions)
	for i := range sessions {
		sessions[i].ID = ""
	}
	return sessions
}

// promptHook is a UserPromptSubmit hook event carrying prompt.
func promptHook(
	provider Provider,
	sessionID string,
	pid int,
	paneHint string,
	minute int,
	prompt string,
) HookEventInput {
	input := agentHook(provider, HookEventUserPromptSubmit, sessionID, pid, paneHint, minute)
	input.PromptText = prompt
	return input
}

func TestAgentSessionStatus_TheUpdateListsLiveAgentSessionsInTheOrderTheyStarted(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.agentPanes("claude", "codex")
	svc.sessionClient.mu.Lock()
	svc.sessionClient.locatePanes[0].WindowName = "task"
	svc.sessionClient.locatePanes[1].WindowName = "review"
	svc.sessionClient.locatePanes[1].WindowIndex = 1
	svc.sessionClient.locatePanes[1].PaneIndex = 2
	svc.sessionClient.mu.Unlock()

	svc.handleAgentHook(t, promptHook(ProviderClaude, "claude-a", 101, "%1", 0, "fix the  retry\nloop"),
		TaskStatusPhaseWorking)
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventSessionStart, "codex-a", 102, "%2", 1),
		TaskStatusPhaseStarting)

	// The one-shot read runs a cycle, which finds where the panes sit.
	update := svc.latestTaskStatus(t)
	require.Equal(t, []LiveAgentSession{
		{
			Provider:     ProviderClaude,
			Pane:         TmuxPaneRef{Server: agentTestServer, ID: "%1"},
			Location:     &TmuxPaneLocation{WindowName: "task"},
			Status:       agentStatus(TaskStatusPhaseWorking, HookEventUserPromptSubmit, 0),
			LatestPrompt: "fix the retry loop",
		},
		{
			Provider: ProviderCodex,
			Pane:     TmuxPaneRef{Server: agentTestServer, ID: "%2"},
			Location: &TmuxPaneLocation{WindowName: "review", WindowIndex: 1, PaneIndex: 2},
			Status:   agentStatus(TaskStatusPhaseStarting, HookEventSessionStart, 1),
		},
	}, liveAgentSessionsWithoutIDs(update))
	require.Equal(t, "%1", update.LeadAgentSession().Pane.ID)
}

func TestAgentSessionStatus_AnAgentWithoutAStatusIsListedAsStartingSinceItStarted(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.agentPanes("claude")

	// A subagent starting maps to no status, but places the agent.
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventSubagentStart, "sess-main", 101, "%1", 2), "")

	sessions := svc.latestTaskStatus(t).AgentSessions
	require.Len(t, sessions, 1)
	require.Equal(t, AgentSessionStatus{
		Phase:      TaskStatusPhaseStarting,
		ObservedAt: agentTestStart.Add(2 * time.Minute),
	}, sessions[0].Status)
}

func TestAgentSessionStatus_TheUpdateLeavesOutEndedAndLostAgentSessions(t *testing.T) {
	svc := newAgentSessionHarness(t)
	// Every one-shot read runs its own cycle.
	svc.observation.statusCacheMaxAge = time.Nanosecond
	svc.agentPanes("claude", "claude")
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventUserPromptSubmit, "sess-main", 101, "%1", 0),
		TaskStatusPhaseWorking)
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventStop, "sess-side", 102, "%2", 1),
		TaskStatusPhaseWaitingForInput)

	// The side agent's pane closed.
	svc.agentPanes("claude")
	update := svc.latestTaskStatus(t)
	require.Len(t, update.AgentSessions, 1)
	require.Equal(t, "%1", update.AgentSessions[0].Pane.ID)
	require.Equal(t, "%1", update.LeadAgentSession().Pane.ID)

	// The Session is gone: its agent session stays open for Reconnect, but
	// it is not live.
	svc.agentPanes()
	update = svc.latestTaskStatus(t)
	require.Equal(t, TaskStatusPhaseStopped, update.Phase)
	require.Empty(t, update.AgentSessions)
	require.Nil(t, update.LeadAgentSession())
}

func TestAgentSessionStatus_TheMostUrgentAgentLeadsAndTheMostRecentOnATie(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.agentPanes("claude", "claude", "codex")
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventStop, "sess-a", 101, "%1", 0),
		TaskStatusPhaseWaitingForInput)
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventUserPromptSubmit, "sess-b", 102, "%2", 1),
		TaskStatusPhaseWorking)
	svc.handleAgentHook(t, agentHook(ProviderCodex, HookEventSessionStart, "codex-c", 103, "%3", 2),
		TaskStatusPhaseStarting)
	require.Equal(t, "%1", svc.latestTaskStatus(t).LeadAgentSession().Pane.ID)

	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventStop, "sess-b", 102, "%2", 3),
		TaskStatusPhaseWaitingForInput)
	require.Equal(t, "%2", svc.latestTaskStatus(t).LeadAgentSession().Pane.ID)
}

func TestAgentSessionStatus_ASideAgentsPromptRepublishesWhileTheTaskStatusStays(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.observation.recoveryPollInterval = time.Hour
	svc.agentPanes("claude", "claude")
	svc.handleAgentHook(t, promptHook(ProviderClaude, "sess-side", 102, "%2", 0, "explore the cache"),
		TaskStatusPhaseWorking)
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventStop, "sess-main", 101, "%1", 1),
		TaskStatusPhaseWaitingForInput)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, err := svc.service.SubscribeTaskStatus(ctx, "task-1")
	require.NoError(t, err)
	// The side agent started first, so it is listed first.
	first := receiveTaskStatus(t, stream)
	require.Equal(t, "explore the cache", first.AgentSessions[0].LatestPrompt)

	svc.handleAgentHook(t, promptHook(ProviderClaude, "sess-side", 102, "%2", 2, "now try the index"),
		TaskStatusPhaseWorking)

	// The main agent still needs input, so the task status is the same.
	update := receiveTaskStatus(t, stream)
	require.Equal(t, taskLevelStatus(first), taskLevelStatus(update))
	require.Equal(t, "now try the index", update.AgentSessions[0].LatestPrompt)
	require.Equal(t, agentStatus(TaskStatusPhaseWorking, HookEventUserPromptSubmit, 2),
		update.AgentSessions[0].Status)
}

func TestAgentSessionStatus_ThePublishedStatusOfAPromptCarriesIt(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.observation.recoveryPollInterval = time.Hour
	svc.agentPanes("claude")
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventSessionStart, "sess-a", 101, "%1", 0),
		TaskStatusPhaseStarting)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, err := svc.service.SubscribeTaskStatus(ctx, "task-1")
	require.NoError(t, err)
	require.Equal(t, TaskStatusPhaseStarting, receiveTaskStatus(t, stream).Phase)

	svc.handleAgentHook(t, promptHook(ProviderClaude, "sess-a", 101, "%1", 1, "add retries"),
		TaskStatusPhaseWorking)

	update := receiveTaskStatus(t, stream)
	require.Equal(t, TaskStatusPhaseWorking, update.Phase)
	require.Equal(t, "add retries", update.AgentSessions[0].LatestPrompt)
}

// The Claude adapter decodes the UserPromptSubmit of a turn Claude Code starts
// on its own, such as a task notification, with no prompt text.
func TestAgentSessionStatus_ATurnWithoutAPromptKeepsTheAgentsLatestPrompt(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.observation.recoveryPollInterval = time.Hour
	svc.agentPanes("claude")
	svc.handleAgentHook(t, promptHook(ProviderClaude, "sess-a", 101, "%1", 0, "run the tests in the background"),
		TaskStatusPhaseWorking)
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventStop, "sess-a", 101, "%1", 1),
		TaskStatusPhaseWaitingForInput)

	svc.handleAgentHook(t, promptHook(ProviderClaude, "sess-a", 101, "%1", 2, ""), TaskStatusPhaseWorking)

	update := svc.latestTaskStatus(t)
	require.Equal(t, TaskStatusPhaseWorking, update.Phase)
	require.Equal(t, agentStatus(TaskStatusPhaseWorking, HookEventUserPromptSubmit, 2),
		update.AgentSessions[0].Status)
	require.Equal(t, "run the tests in the background", update.AgentSessions[0].LatestPrompt)
	activity, err := svc.service.GetTaskActivity(t.Context(), "task-1", 0)
	require.NoError(t, err)
	require.Len(t, activity, 1)
	require.Equal(t, "run the tests in the background", activity[0].Text)
}

// paneWindow places the harness's pane at index in a window.
func (h *testTaskServiceHarness) paneWindow(index int, name string, windowIndex int, paneIndex int) {
	h.sessionClient.mu.Lock()
	defer h.sessionClient.mu.Unlock()
	h.sessionClient.locatePanes[index].WindowName = name
	h.sessionClient.locatePanes[index].WindowIndex = windowIndex
	h.sessionClient.locatePanes[index].PaneIndex = paneIndex
}

// A Codex subagent's instruction arrives as a UserPromptSubmit carrying the
// subagent's agent ID, under the agent's own Provider session.
func TestAgentSessionStatus_ASubagentsPromptIsNotTheAgentsLatestPrompt(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.observation.recoveryPollInterval = time.Hour
	svc.agentPanes("codex")
	svc.handleAgentHook(t, promptHook(ProviderCodex, "codex-a", 101, "%1", 0, "add retries to the client"),
		TaskStatusPhaseWorking)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, err := svc.service.SubscribeTaskStatus(ctx, "task-1")
	require.NoError(t, err)
	require.Equal(t, "add retries to the client", receiveTaskStatus(t, stream).AgentSessions[0].LatestPrompt)

	subagent := promptHook(ProviderCodex, "codex-a", 101, "%1", 1, "inspect the cache layer and report back")
	subagent.AgentID = "thread-2"
	subagent.AgentType = "explorer"
	svc.handleAgentHook(t, subagent, "")

	requireNoTaskStatus(t, stream)
	require.Equal(t, "add retries to the client", svc.latestTaskStatus(t).AgentSessions[0].LatestPrompt)
	activity, err := svc.service.GetTaskActivity(t.Context(), "task-1", 0)
	require.NoError(t, err)
	require.Len(t, activity, 2)
	require.Equal(t, "inspect the cache layer and report back", activity[1].Text)
	require.Empty(t, activity[1].AgentSessionID)
}

func TestAgentSessionStatus_AHookPublishKeepsTheLastCyclesLocations(t *testing.T) {
	svc := newAgentSessionHarness(t)
	observer := svc.manualStatusObserver()
	svc.agentPanes("claude", "claude")
	svc.paneWindow(0, "task", 0, 0)
	svc.paneWindow(1, "review", 1, 0)
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventStop, "sess-a", 101, "%1", 0),
		TaskStatusPhaseWaitingForInput)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, err := svc.service.SubscribeTaskStatus(ctx, "task-1")
	require.NoError(t, err)
	observer.runCycle()
	receiveTaskStatus(t, stream)

	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventUserPromptSubmit, "sess-b", 102, "%2", 1),
		TaskStatusPhaseWorking)

	update := receiveTaskStatus(t, stream)
	require.Len(t, update.AgentSessions, 2)
	require.Equal(t, &TmuxPaneLocation{WindowName: "task"}, update.AgentSessions[0].Location)
	// The new agent session's pane is unknown until the next cycle.
	require.Nil(t, update.AgentSessions[1].Location)
}

// A cycle that judged %1's agent exited is overlapped by a hook showing a new
// agent there, so the agent session stays open; the rebased view must still
// show where it runs.
func TestAgentSessionStatus_ARebasedViewKeepsTheLocationOfAnAgentSessionTheCycleDidNotEnd(t *testing.T) {
	svc := newAgentSessionHarness(t)
	observer := svc.manualStatusObserver()
	svc.agentPanes("claude")
	svc.paneWindow(0, "task", 0, 0)
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventUserPromptSubmit, "sess-old", 101, "%1", 0),
		TaskStatusPhaseWorking)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, err := svc.service.SubscribeTaskStatus(ctx, "task-1")
	require.NoError(t, err)
	observer.runCycle()
	require.NotNil(t, receiveTaskStatus(t, stream).AgentSessions[0].Location)

	// Claude exited: the cycle's snapshot sees only the shell.
	svc.sessionClient.mu.Lock()
	svc.sessionClient.locatePanes[0].Command = "zsh"
	svc.sessionClient.locatePaneByPID[201] = "%1"
	svc.sessionClient.mu.Unlock()
	started, release := svc.holdInspection()
	cycleDone := make(chan struct{})
	go func() {
		observer.runCycle()
		close(cycleDone)
	}()
	waitForSignal(t, started, "the cycle never inspected tmux")
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventSessionStart, "sess-new", 201, "%1", 1),
		TaskStatusPhaseStarting)
	require.Equal(t, &TmuxPaneLocation{WindowName: "task"}, receiveTaskStatus(t, stream).AgentSessions[0].Location)
	close(release)
	waitForSignal(t, cycleDone, "the cycle never finished")

	// The rebased view is the hook's, location included, so nothing changed.
	requireNoTaskStatus(t, stream)
}

// After the Session was lost and comes back with a new pane, the new agent's
// first hook publishes while the old agent session is still lost.
func TestAgentSessionStatus_AStillLostAgentSessionIsLeftOutNextToALiveOne(t *testing.T) {
	svc := newAgentSessionHarness(t)
	observer := svc.manualStatusObserver()
	svc.agentPanes("claude")
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventStop, "sess-old", 101, "%1", 0),
		TaskStatusPhaseWaitingForInput)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, err := svc.service.SubscribeTaskStatus(ctx, "task-1")
	require.NoError(t, err)
	svc.agentPanes()
	observer.runCycle()
	require.Equal(t, TaskStatusPhaseStopped, receiveTaskStatus(t, stream).Phase)

	// Reconnect recreated the Session; its agent runs in a new pane, %2.
	svc.sessionClient.mu.Lock()
	svc.sessionClient.locatePanes = []TmuxPane{{ID: "%2", Session: "repo_task", Command: "claude"}}
	svc.sessionClient.locateTaskSessionPanes = map[string][]string{"task-1": {"%2"}}
	svc.sessionClient.locatePaneByPID = map[int]string{102: "%2"}
	svc.sessionClient.mu.Unlock()
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventSessionStart, "sess-new", 102, "%2", 1),
		TaskStatusPhaseStarting)

	update := receiveTaskStatus(t, stream)
	require.Len(t, update.AgentSessions, 1)
	require.Equal(t, "%2", update.AgentSessions[0].Pane.ID)
	require.Equal(t, "%2", update.LeadAgentSession().Pane.ID)
}

func TestAgentSessionStatus_AMovedPaneRepublishes(t *testing.T) {
	svc := newAgentSessionHarness(t)
	observer := svc.manualStatusObserver()
	svc.agentPanes("claude")
	svc.paneWindow(0, "task", 0, 0)
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventStop, "sess-a", 101, "%1", 0),
		TaskStatusPhaseWaitingForInput)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, err := svc.service.SubscribeTaskStatus(ctx, "task-1")
	require.NoError(t, err)
	observer.runCycle()
	require.Equal(t, &TmuxPaneLocation{WindowName: "task"}, receiveTaskStatus(t, stream).AgentSessions[0].Location)

	// The user renames the window and moves it to index 3.
	svc.paneWindow(0, "renamed", 3, 0)
	observer.runCycle()
	moved := receiveTaskStatus(t, stream)
	require.Equal(t, &TmuxPaneLocation{WindowName: "renamed", WindowIndex: 3}, moved.AgentSessions[0].Location)

	observer.runCycle()
	requireNoTaskStatus(t, stream)
}

func TestAgentSessionStatus_ASideAgentsStatusRepublishesWhileTheTaskStatusStays(t *testing.T) {
	svc := newAgentSessionHarness(t)
	svc.observation.recoveryPollInterval = time.Hour
	svc.agentPanes("claude", "claude")
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventUserPromptSubmit, "sess-side", 102, "%2", 0),
		TaskStatusPhaseWorking)
	svc.handleAgentHook(t, agentHook(ProviderClaude, HookEventStop, "sess-main", 101, "%1", 1),
		TaskStatusPhaseWaitingForInput)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, err := svc.service.SubscribeTaskStatus(ctx, "task-1")
	require.NoError(t, err)
	first := receiveTaskStatus(t, stream)

	// The side agent ends its turn with background work running: only its
	// own entry changes, and its prompt stays the same.
	svc.claudeRepo.hookUpdate = &TaskStatusUpdate{
		Phase:          TaskStatusPhaseWorkingInBackground,
		RawEventName:   HookEventStop,
		BackgroundWork: TaskBackgroundWork{Subagents: 1},
	}
	require.NoError(t, svc.service.HandleHookEvent(t.Context(),
		agentHook(ProviderClaude, HookEventStop, "sess-side", 102, "%2", 2)))

	update := receiveTaskStatus(t, stream)
	require.Equal(t, taskLevelStatus(first), taskLevelStatus(update))
	require.Equal(t, TaskStatusPhaseWorkingInBackground, update.AgentSessions[0].Status.Phase)
	require.Equal(t, TaskBackgroundWork{Subagents: 1}, update.AgentSessions[0].Status.BackgroundWork)
}

func TestAgentSessionStatus_ChangingAReceivedUpdateLeavesTheObserverAlone(t *testing.T) {
	svc := newAgentSessionHarness(t)
	observer := svc.manualStatusObserver()
	svc.agentPanes("claude")
	svc.paneWindow(0, "task", 0, 0)
	svc.handleAgentHook(t, promptHook(ProviderClaude, "sess-a", 101, "%1", 0, "add retries"),
		TaskStatusPhaseWorking)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, err := svc.service.SubscribeTaskStatus(ctx, "task-1")
	require.NoError(t, err)
	observer.runCycle()
	got := receiveTaskStatus(t, stream)
	got.AgentSessions[0].LatestPrompt = "tampered"
	got.AgentSessions[0].Location.WindowName = "tampered"

	// Nothing changed for the observer, so the next cycle publishes nothing.
	observer.runCycle()
	requireNoTaskStatus(t, stream)
	latest := svc.latestTaskStatus(t)
	require.Equal(t, "add retries", latest.AgentSessions[0].LatestPrompt)
	require.Equal(t, "task", latest.AgentSessions[0].Location.WindowName)
}
