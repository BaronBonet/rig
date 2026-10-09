package core

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// observedTaskObservedAt is when a seeded task's agent was last seen working.
var observedTaskObservedAt = time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)

func TestTaskStatusObserver_DoesNotPollWithoutLiveInterest(t *testing.T) {
	svc := newTestTaskService(t)
	svc.observation.recoveryPollInterval = 5 * time.Millisecond
	seedObservedTask(t, svc, "task-1")

	time.Sleep(25 * time.Millisecond)

	svc.sessionClient.mu.Lock()
	require.Zero(t, svc.sessionClient.batchInspectCalls)
	svc.sessionClient.mu.Unlock()
	svc.taskRepo.mu.Lock()
	require.Empty(t, svc.taskRepo.agentSessionReads)
	svc.taskRepo.mu.Unlock()
}

func TestTaskStatusObserver_SixTasksShareOneBatchedSnapshotPerSerializedCycle(t *testing.T) {
	svc := newTestTaskService(t)
	svc.observation.recoveryPollInterval = time.Hour
	svc.providerRepo.statusRecoveryUpdate = &AgentSessionStatus{
		Phase:        TaskStatusPhaseWaitingForInput,
		RawEventName: "TranscriptTaskComplete",
		ObservedAt:   observedTaskObservedAt.Add(time.Minute),
	}

	const taskCount = 6
	for index := range taskCount {
		seedObservedTask(t, svc, fmt.Sprintf("task-%d", index))
	}
	contexts := make([]context.CancelFunc, 0, taskCount)
	streams := make([]<-chan TaskStatusUpdate, 0, taskCount)
	for index := range taskCount {
		taskID := fmt.Sprintf("task-%d", index)
		ctx, cancel := context.WithCancel(t.Context())
		contexts = append(contexts, cancel)
		stream, err := svc.service.SubscribeTaskStatus(ctx, taskID)
		require.NoError(t, err)
		streams = append(streams, stream)
	}
	t.Cleanup(func() {
		for _, cancel := range contexts {
			cancel()
		}
	})

	for index, stream := range streams {
		select {
		case update := <-stream:
			require.Equal(t, fmt.Sprintf("task-%d", index), update.TaskID)
			require.Equal(t, TaskStatusPhaseWaitingForInput, update.Phase)
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for task %d", index)
		}
	}

	svc.sessionClient.mu.Lock()
	cycles := svc.sessionClient.batchInspectCalls
	maxConcurrent := svc.sessionClient.batchInspectMax
	svc.sessionClient.mu.Unlock()
	require.Positive(t, cycles)
	require.Equal(t, 1, maxConcurrent)

	svc.providerRepo.mu.Lock()
	defer svc.providerRepo.mu.Unlock()
	for index := range taskCount {
		taskID := fmt.Sprintf("task-%d", index)
		require.Positive(t, svc.providerRepo.statusRecoveryCalls[taskID])
		require.LessOrEqual(t, svc.providerRepo.statusRecoveryCalls[taskID], cycles)
	}
}

func TestTaskStatusObserver_RecoveryWorkerLimitIsTwo(t *testing.T) {
	svc := newTestTaskService(t)
	const taskCount = 6
	for index := range taskCount {
		seedObservedTask(t, svc, fmt.Sprintf("task-%d", index))
	}

	recoveryStarted := make(chan struct{}, taskCount)
	recoveryRelease := make(chan struct{})
	svc.providerRepo.mu.Lock()
	svc.providerRepo.statusRecoveryStarted = recoveryStarted
	svc.providerRepo.statusRecoveryRelease = recoveryRelease
	svc.providerRepo.mu.Unlock()

	observation := &taskObservation{
		tasks:                svc.taskRepoMock,
		tmuxSession:          svc.sessionClientMock,
		providers:            svc.service.providers,
		recoveryPollInterval: time.Hour,
		recoveryWorkerLimit:  defaultTaskStatusRecoveryWorkerLimit,
		statusCacheMaxAge:    time.Hour,
		now:                  time.Now,
	}
	observer := &taskStatusObserver{
		observation: observation,
		tasks:       make(map[string]*observedTaskStatus),
		wakeCycle:   make(chan struct{}, 1),
	}
	observation.statusObserver = observer

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	streams := make([]<-chan TaskStatusUpdate, 0, taskCount)
	for index := range taskCount {
		stream, err := observer.SubscribeTaskStatus(ctx, fmt.Sprintf("task-%d", index))
		require.NoError(t, err)
		streams = append(streams, stream)
	}

	cycleDone := make(chan struct{})
	go func() {
		observer.runCycle()
		close(cycleDone)
	}()
	for range defaultTaskStatusRecoveryWorkerLimit {
		select {
		case <-recoveryStarted:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for recovery workers")
		}
	}
	time.Sleep(20 * time.Millisecond)
	svc.providerRepo.mu.Lock()
	require.Equal(t, defaultTaskStatusRecoveryWorkerLimit, svc.providerRepo.statusRecoveryActive)
	require.Equal(t, defaultTaskStatusRecoveryWorkerLimit, svc.providerRepo.statusRecoveryMax)
	svc.providerRepo.mu.Unlock()

	close(recoveryRelease)
	select {
	case <-cycleDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for recovery cycle")
	}
	for _, stream := range streams {
		_ = receiveTaskStatus(t, stream)
	}
}

func TestTaskStatusObserver_MultipleSubscribersShareRecoveryAndConverge(t *testing.T) {
	svc := newTestTaskService(t)
	svc.observation.recoveryPollInterval = time.Hour
	seedObservedTask(t, svc, "task-1")
	svc.providerRepo.statusRecoveryUpdate = &AgentSessionStatus{
		Phase:        TaskStatusPhaseWaitingForInput,
		RawEventName: "TranscriptTaskComplete",
		ObservedAt:   observedTaskObservedAt.Add(time.Minute),
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	first, err := svc.service.SubscribeTaskStatus(ctx, "task-1")
	require.NoError(t, err)
	second, err := svc.service.SubscribeTaskStatus(ctx, "task-1")
	require.NoError(t, err)

	firstUpdate := receiveTaskStatus(t, first)
	secondUpdate := receiveTaskStatus(t, second)
	require.Equal(t, firstUpdate, secondUpdate)

	svc.providerRepo.mu.Lock()
	require.Equal(t, 1, svc.providerRepo.statusRecoveryCalls["task-1"])
	svc.providerRepo.mu.Unlock()
}

func TestTaskStatusObserver_SuppressesEqualViewsAcrossRecoveryCycles(t *testing.T) {
	svc := newTestTaskService(t)
	svc.observation.recoveryPollInterval = 5 * time.Millisecond
	seedObservedTask(t, svc, "task-1")

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, err := svc.service.SubscribeTaskStatus(ctx, "task-1")
	require.NoError(t, err)
	_ = receiveTaskStatus(t, stream)

	select {
	case duplicate := <-stream:
		t.Fatalf("received equal status from a later cycle: %+v", duplicate)
	case <-time.After(25 * time.Millisecond):
	}
}

func TestTaskStatusObserver_NewInterestDuringFixedDelayIsObservedImmediately(t *testing.T) {
	svc := newTestTaskService(t)
	svc.observation.recoveryPollInterval = time.Hour
	seedObservedTask(t, svc, "task-1")
	seedObservedTask(t, svc, "task-2")

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	first, err := svc.service.SubscribeTaskStatus(ctx, "task-1")
	require.NoError(t, err)
	_ = receiveTaskStatus(t, first)

	second, err := svc.service.SubscribeTaskStatus(ctx, "task-2")
	require.NoError(t, err)
	select {
	case update := <-second:
		require.Equal(t, "task-2", update.TaskID)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("new task interest waited for the existing task's fixed delay")
	}
}

func TestTaskStatusObserver_SlowInspectionCannotOverlapCycles(t *testing.T) {
	svc := newTestTaskService(t)
	svc.observation.recoveryPollInterval = 5 * time.Millisecond
	seedObservedTask(t, svc, "task-1")
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	svc.sessionClient.mu.Lock()
	svc.sessionClient.batchInspectStarted = started
	svc.sessionClient.batchInspectRelease = release
	svc.sessionClient.mu.Unlock()

	ctx, cancel := context.WithCancel(t.Context())
	stream, err := svc.service.SubscribeTaskStatus(ctx, "task-1")
	require.NoError(t, err)
	_ = stream
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for inspection")
	}
	time.Sleep(25 * time.Millisecond)

	svc.sessionClient.mu.Lock()
	require.Equal(t, 1, svc.sessionClient.batchInspectCalls)
	require.Equal(t, 1, svc.sessionClient.batchInspectMax)
	svc.sessionClient.mu.Unlock()
	cancel()
	close(release)
}

func TestTaskStatusObserver_SlowSubscriberReceivesConflatedLatestView(t *testing.T) {
	svc := newTestTaskService(t)
	svc.observation.recoveryPollInterval = time.Hour
	seedObservedTask(t, svc, "task-1")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, err := svc.service.SubscribeTaskStatus(ctx, "task-1")
	require.NoError(t, err)

	var latest time.Time
	for index := range 20 {
		latest = observedTaskObservedAt.Add(time.Duration(index+1) * time.Second)
		svc.observedTaskHook(t, "task-1", HookEventPostToolUse, TaskStatusPhaseWorking, latest)
	}

	deadline := time.After(time.Second)
	for {
		select {
		case update := <-stream:
			if update.ObservedAt.Equal(latest) {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for conflated latest view")
		}
	}
}

func TestTaskStatusObserver_HookEvidenceWinsWhenRecoveryIsInFlight(t *testing.T) {
	svc := newTestTaskService(t)
	svc.observation.recoveryPollInterval = time.Hour
	seedObservedTask(t, svc, "task-1")
	recoveryStarted := make(chan struct{}, 1)
	recoveryRelease := make(chan struct{})
	svc.providerRepo.mu.Lock()
	svc.providerRepo.statusRecoveryStarted = recoveryStarted
	svc.providerRepo.statusRecoveryRelease = recoveryRelease
	svc.providerRepo.statusRecoveryUpdate = &AgentSessionStatus{
		Phase:        TaskStatusPhaseWaitingForInput,
		RawEventName: "TranscriptTaskComplete",
		ObservedAt:   observedTaskObservedAt.Add(time.Minute),
	}
	svc.providerRepo.mu.Unlock()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, err := svc.service.SubscribeTaskStatus(ctx, "task-1")
	require.NoError(t, err)
	select {
	case <-recoveryStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for recovery")
	}

	hookAt := observedTaskObservedAt.Add(2 * time.Minute)
	svc.observedTaskHook(t, "task-1", HookEventPostToolUse, TaskStatusPhaseWorking, hookAt)
	require.Equal(t, TaskStatusUpdate{
		TaskID:       "task-1",
		Provider:     ProviderCodex,
		Phase:        TaskStatusPhaseWorking,
		RawEventName: HookEventPostToolUse,
		ObservedAt:   hookAt,
	}, taskLevelStatus(receiveTaskStatus(t, stream)))
	close(recoveryRelease)

	select {
	case update := <-stream:
		require.NotEqual(t, "TranscriptTaskComplete", update.RawEventName)
	case <-time.After(40 * time.Millisecond):
	}
}

func TestTaskStatusObserver_HookEvidenceWinsWhenInspectionIsInFlight(t *testing.T) {
	svc := newTestTaskService(t)
	svc.observation.recoveryPollInterval = time.Hour
	seedObservedTask(t, svc, "task-1")
	inspectStarted := make(chan struct{}, 1)
	inspectRelease := make(chan struct{})
	svc.sessionClient.mu.Lock()
	svc.sessionClient.batchInspectStarted = inspectStarted
	svc.sessionClient.batchInspectRelease = inspectRelease
	svc.sessionClient.mu.Unlock()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, err := svc.service.SubscribeTaskStatus(ctx, "task-1")
	require.NoError(t, err)
	select {
	case <-inspectStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the tmux snapshot")
	}

	// The cycle read the agent working before this Stop arrived.
	stopAt := observedTaskObservedAt.Add(3 * time.Minute)
	svc.observedTaskHook(t, "task-1", HookEventStop, TaskStatusPhaseWaitingForInput, stopAt)
	require.Equal(t, TaskStatusPhaseWaitingForInput, receiveTaskStatus(t, stream).Phase)
	close(inspectRelease)

	select {
	case update := <-stream:
		require.NotEqual(t, TaskStatusPhaseWorking, update.Phase)
	case <-time.After(40 * time.Millisecond):
	}
}

func TestTaskStatusObserver_AHookKeepsAnotherAgentsRecoveredStatus(t *testing.T) {
	svc := newTestTaskService(t)
	svc.observation.recoveryPollInterval = time.Hour
	seedObservedTask(t, svc, "task-1")
	// A second Codex agent ended its turn, but its subagents are still
	// running.
	side := seedObservedAgent(t, svc, "task-1", "session-side", AgentSessionStatus{
		ObservedAt:   observedTaskObservedAt.Add(time.Minute),
		RawEventName: HookEventStop,
		Phase:        TaskStatusPhaseWaitingForInput,
	})
	svc.providerRepo.statusRecoveryByConversation = map[string]*AgentSessionStatus{
		side.ProviderSessionID: {
			ObservedAt:     observedTaskObservedAt.Add(time.Minute),
			RawEventName:   "TranscriptSubagentsRunning",
			Phase:          TaskStatusPhaseWorkingInBackground,
			BackgroundWork: TaskBackgroundWork{Subagents: 2},
		},
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, err := svc.service.SubscribeTaskStatus(ctx, "task-1")
	require.NoError(t, err)
	require.Equal(t, TaskStatusPhaseWorking, receiveTaskStatus(t, stream).Phase)

	// A hook from the first agent publishes at once; the side agent stays
	// working in the background rather than falling back to needs input.
	hookAt := observedTaskObservedAt.Add(2 * time.Minute)
	svc.observedTaskHook(t, "task-1", HookEventPostToolUse, TaskStatusPhaseWorking, hookAt)
	update := receiveTaskStatus(t, stream)
	require.Equal(t, TaskStatusPhaseWorking, update.Phase)
	require.Equal(t, hookAt, update.ObservedAt)
}

// A cycle recovered that the agent needs input, as after Claude's Esc
// interrupt. The agent's next prompt replaces the recovered status at once,
// not after the next cycle.
func TestTaskStatusObserver_ANewerHookReplacesTheSameAgentsRecoveredStatus(t *testing.T) {
	svc := newTestTaskService(t)
	svc.observation.recoveryPollInterval = time.Hour
	seedObservedTask(t, svc, "task-1")
	svc.providerRepo.statusRecoveryUpdate = &AgentSessionStatus{
		Phase:        TaskStatusPhaseWaitingForInput,
		RawEventName: "TranscriptTurnInterrupted",
		ObservedAt:   observedTaskObservedAt.Add(time.Minute),
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, err := svc.service.SubscribeTaskStatus(ctx, "task-1")
	require.NoError(t, err)
	require.Equal(t, "TranscriptTurnInterrupted", receiveTaskStatus(t, stream).RawEventName)

	promptAt := observedTaskObservedAt.Add(2 * time.Minute)
	svc.observedTaskHook(t, "task-1", HookEventUserPromptSubmit, TaskStatusPhaseWorking, promptAt)
	update := receiveTaskStatus(t, stream)
	require.Equal(t, TaskStatusPhaseWorking, update.Phase)
	require.Equal(t, promptAt, update.ObservedAt)
}

func TestTaskStatusObserver_FreshOneShotReusesCachedSharedView(t *testing.T) {
	svc := newTestTaskService(t)
	svc.observation.recoveryPollInterval = time.Hour
	svc.observation.statusCacheMaxAge = time.Hour
	seedObservedTask(t, svc, "task-1")

	first, err := svc.service.LatestTaskStatus(t.Context(), "task-1")
	require.NoError(t, err)
	second, err := svc.service.LatestTaskStatus(t.Context(), "task-1")
	require.NoError(t, err)
	require.Equal(t, first, second)

	svc.sessionClient.mu.Lock()
	require.Equal(t, 1, svc.sessionClient.batchInspectCalls)
	svc.sessionClient.mu.Unlock()
}

func TestTaskStatusObserver_HookEvidenceInvalidatesAnUnwatchedCachedView(t *testing.T) {
	svc := newTestTaskService(t)
	svc.observation.recoveryPollInterval = time.Hour
	svc.observation.statusCacheMaxAge = time.Hour
	seedObservedTask(t, svc, "task-1")

	first, err := svc.service.LatestTaskStatus(t.Context(), "task-1")
	require.NoError(t, err)
	require.Equal(t, TaskStatusPhaseWorking, first.Phase)

	stopAt := observedTaskObservedAt.Add(time.Minute)
	svc.observedTaskHook(t, "task-1", HookEventStop, TaskStatusPhaseWaitingForInput, stopAt)

	second, err := svc.service.LatestTaskStatus(t.Context(), "task-1")
	require.NoError(t, err)
	require.Equal(t, TaskStatusPhaseWaitingForInput, second.Phase)
	require.Equal(t, stopAt, second.ObservedAt)
}

func TestTaskStatusObserver_StopsPollingAfterOneShotInterestCompletes(t *testing.T) {
	svc := newTestTaskService(t)
	svc.observation.recoveryPollInterval = 5 * time.Millisecond
	svc.observation.statusCacheMaxAge = 5 * time.Millisecond
	seedObservedTask(t, svc, "task-1")

	update, err := svc.service.LatestTaskStatus(t.Context(), "task-1")
	require.NoError(t, err)
	require.NotNil(t, update)

	svc.sessionClient.mu.Lock()
	completedCycles := svc.sessionClient.batchInspectCalls
	svc.sessionClient.mu.Unlock()
	time.Sleep(30 * time.Millisecond)
	svc.sessionClient.mu.Lock()
	require.Equal(t, completedCycles, svc.sessionClient.batchInspectCalls)
	svc.sessionClient.mu.Unlock()
}

func TestTaskStatusObserver_TransientTaskListErrorDoesNotCloseStream(t *testing.T) {
	svc := newTestTaskService(t)
	svc.observation.recoveryPollInterval = time.Hour
	seedObservedTask(t, svc, "task-1")
	svc.taskRepo.listErr = errors.New("temporary read failure")

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, err := svc.service.SubscribeTaskStatus(ctx, "task-1")
	require.NoError(t, err)
	require.Equal(t, "task-1", receiveTaskStatus(t, stream).TaskID)

	select {
	case _, ok := <-stream:
		require.True(t, ok)
	case <-time.After(30 * time.Millisecond):
	}
}

func TestTaskStatusObserver_UnexpectedRuntimeSnapshotFailureKeepsPersistedView(t *testing.T) {
	svc := newTestTaskService(t)
	svc.observation.recoveryPollInterval = time.Hour
	seedObservedTask(t, svc, "task-1")
	svc.sessionClient.batchInspectErr = errors.New("tmux inventory unavailable")

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, err := svc.service.SubscribeTaskStatus(ctx, "task-1")
	require.NoError(t, err)
	update := receiveTaskStatus(t, stream)
	require.Equal(t, TaskStatusPhaseWorking, update.Phase)
	require.Equal(t, HookEventPostToolUse, update.RawEventName)
}

func TestTaskStatusObserver_ConfirmedDeletionClosesStreamsAndReleasesInterest(t *testing.T) {
	svc := newTestTaskService(t)
	svc.observation.recoveryPollInterval = time.Hour
	seedObservedTask(t, svc, "task-1")

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, err := svc.service.SubscribeTaskStatus(ctx, "task-1")
	require.NoError(t, err)
	_ = receiveTaskStatus(t, stream)

	require.NoError(t, svc.service.DeleteTask(t.Context(), "task-1"))
	select {
	case _, ok := <-stream:
		require.False(t, ok)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for deleted task stream to close")
	}
}

// seedObservedTask records a Codex task whose agent runs in a pane of the
// task's Session and was last seen working, with the conversation history
// status recovery reads.
func seedObservedTask(t *testing.T, svc *testTaskServiceHarness, taskID string) {
	t.Helper()
	svc.taskRepo.listTasks = append(svc.taskRepo.listTasks, &Task{
		ID:           taskID,
		Provider:     ProviderCodex,
		TmuxSession:  "repo_" + taskID,
		WorktreePath: "/tmp/" + taskID,
	})
	seedObservedAgent(t, svc, taskID, "session-"+taskID, AgentSessionStatus{
		ObservedAt:   observedTaskObservedAt,
		RawEventName: HookEventPostToolUse,
		Phase:        TaskStatusPhaseWorking,
	})
}

// seedObservedAgent records a Codex agent session for a seeded task in a new
// pane of the task's Session that runs Codex, with its conversation history.
func seedObservedAgent(
	t *testing.T,
	svc *testTaskServiceHarness,
	taskID string,
	providerSessionID string,
	status AgentSessionStatus,
) AgentSession {
	t.Helper()
	svc.sessionClient.mu.Lock()
	pane := fmt.Sprintf("%%%d", len(svc.sessionClient.locatePanes)+1)
	pid := 1000 + len(svc.sessionClient.locatePanes)
	svc.sessionClient.locateServer = agentTestServer
	svc.sessionClient.locatePanes = append(svc.sessionClient.locatePanes, TmuxPane{
		ID:       pane,
		Session:  "repo_" + taskID,
		Command:  "zsh",
		Children: []PaneProcess{{Command: "codex"}},
	})
	if svc.sessionClient.locateTaskSessionPanes == nil {
		svc.sessionClient.locateTaskSessionPanes = make(map[string][]string)
	}
	svc.sessionClient.locateTaskSessionPanes[taskID] = append(svc.sessionClient.locateTaskSessionPanes[taskID], pane)
	if svc.sessionClient.locatePaneByPID == nil {
		svc.sessionClient.locatePaneByPID = make(map[int]string)
	}
	svc.sessionClient.locatePaneByPID[pid] = pane
	svc.sessionClient.mu.Unlock()

	session := AgentSession{
		ID:                "agent-" + providerSessionID,
		TaskID:            taskID,
		Provider:          ProviderCodex,
		TmuxServer:        agentTestServer,
		TmuxPane:          pane,
		PaneTrusted:       true,
		ProviderSessionID: providerSessionID,
		StartedAt:         observedTaskObservedAt.Add(-time.Hour),
		Status:            status,
	}
	svc.taskRepo.mu.Lock()
	defer svc.taskRepo.mu.Unlock()
	svc.taskRepo.agentSessions = append(svc.taskRepo.agentSessions, session)
	svc.taskRepo.providerSessionsByTask[taskID] = append(svc.taskRepo.providerSessionsByTask[taskID],
		TaskProviderSession{
			TaskID:            taskID,
			Provider:          ProviderCodex,
			ProviderSessionID: providerSessionID,
			TranscriptPath:    "/tmp/" + providerSessionID + ".jsonl",
			StartSource:       "startup",
		},
	)
	return session
}

// observedTaskHook handles a hook event from a seeded task's first Codex
// agent, traced to its pane, that its provider maps to phase.
func (h *testTaskServiceHarness) observedTaskHook(
	t *testing.T,
	taskID string,
	eventName string,
	phase TaskStatusPhase,
	occurredAt time.Time,
) {
	t.Helper()
	h.sessionClient.mu.Lock()
	pane := h.sessionClient.locateTaskSessionPanes[taskID][0]
	pid := 0
	for candidate, candidatePane := range h.sessionClient.locatePaneByPID {
		if candidatePane == pane {
			pid = candidate
		}
	}
	h.sessionClient.mu.Unlock()

	h.providerRepo.hookUpdate = &TaskStatusUpdate{Phase: phase, RawEventName: eventName}
	require.NoError(t, h.service.HandleHookEvent(t.Context(), HookEventInput{
		OccurredAt: occurredAt,
		Provider:   ProviderCodex,
		Cwd:        "/tmp/" + taskID,
		EventName:  eventName,
		SessionID:  "session-" + taskID,
		HookPID:    pid,
		TmuxPane:   pane,
		TmuxServer: agentTestServer,
	}))
}

func receiveTaskStatus(t *testing.T, stream <-chan TaskStatusUpdate) TaskStatusUpdate {
	t.Helper()
	select {
	case update, ok := <-stream:
		require.True(t, ok)
		return update
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for task status")
		return TaskStatusUpdate{}
	}
}
