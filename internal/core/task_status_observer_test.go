package core

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTaskStatusObserver_DoesNotPollWithoutLiveInterest(t *testing.T) {
	svc := newTestTaskService(t)
	svc.observation.recoveryPollInterval = 5 * time.Millisecond

	time.Sleep(25 * time.Millisecond)

	svc.sessionClient.mu.Lock()
	require.Zero(t, svc.sessionClient.batchInspectCalls)
	svc.sessionClient.mu.Unlock()
	svc.taskRepo.mu.Lock()
	require.Empty(t, svc.taskRepo.subscribeCalls)
	svc.taskRepo.mu.Unlock()
}

func TestTaskStatusObserver_SixTasksShareOneBatchedSnapshotPerSerializedCycle(t *testing.T) {
	svc := newTestTaskService(t)
	svc.observation.recoveryPollInterval = time.Hour
	svc.providerRepo.statusRecoveryUpdate = &TaskStatusUpdate{
		Provider:     ProviderCodex,
		Phase:        TaskStatusPhaseWaitingForInput,
		RawEventName: "TranscriptTaskComplete",
		ObservedAt:   time.Date(2026, time.July, 29, 12, 1, 0, 0, time.UTC),
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
	svc.taskRepo.mu.Lock()
	defer svc.taskRepo.mu.Unlock()
	for index := range taskCount {
		require.Equal(t, 1, svc.taskRepo.subscribeCalls[fmt.Sprintf("task-%d", index)])
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
	svc.providerRepo.statusRecoveryUpdate = &TaskStatusUpdate{
		Provider:     ProviderCodex,
		Phase:        TaskStatusPhaseWaitingForInput,
		RawEventName: "TranscriptTaskComplete",
		ObservedAt:   time.Date(2026, time.July, 29, 12, 1, 0, 0, time.UTC),
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

	svc.taskRepo.mu.Lock()
	require.Equal(t, 1, svc.taskRepo.subscribeCalls["task-1"])
	svc.taskRepo.mu.Unlock()
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
	svc.taskRepo.listTasks = []*Task{{
		ID:          "task-1",
		Provider:    ProviderCodex,
		TmuxSession: "repo_task-1",
	}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, err := svc.service.SubscribeTaskStatus(ctx, "task-1")
	require.NoError(t, err)

	var latest TaskStatusUpdate
	for index := range 20 {
		latest = TaskStatusUpdate{
			TaskID:       "task-1",
			Provider:     ProviderCodex,
			Phase:        TaskStatusPhaseWorking,
			RawEventName: fmt.Sprintf("event-%d", index),
			ObservedAt:   time.Date(2026, time.July, 29, 12, 0, index, 0, time.UTC),
		}
		require.NoError(t, svc.taskRepoMock.UpsertTaskStatus(t.Context(), latest))
	}

	deadline := time.After(time.Second)
	for {
		select {
		case update := <-stream:
			if update == latest {
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
	svc.providerRepo.statusRecoveryUpdate = &TaskStatusUpdate{
		Provider:     ProviderCodex,
		Phase:        TaskStatusPhaseWaitingForInput,
		RawEventName: "TranscriptTaskComplete",
		ObservedAt:   time.Date(2026, time.July, 29, 12, 1, 0, 0, time.UTC),
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

	hook := TaskStatusUpdate{
		TaskID:       "task-1",
		Provider:     ProviderCodex,
		Phase:        TaskStatusPhaseWorking,
		RawEventName: "PostToolUse",
		ObservedAt:   time.Date(2026, time.July, 29, 12, 2, 0, 0, time.UTC),
	}
	require.NoError(t, svc.taskRepoMock.UpsertTaskStatus(t.Context(), hook))
	require.Equal(t, hook, receiveTaskStatus(t, stream))
	close(recoveryRelease)

	select {
	case update := <-stream:
		require.NotEqual(t, "TranscriptTaskComplete", update.RawEventName)
	case <-time.After(40 * time.Millisecond):
	}
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
	svc.taskRepo.mu.Lock()
	require.Equal(t, 1, svc.taskRepo.subscribeCalls["task-1"])
	svc.taskRepo.mu.Unlock()
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
	require.Equal(t, "PostToolUse", update.RawEventName)
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

func seedObservedTask(t *testing.T, svc *testTaskServiceHarness, taskID string) {
	t.Helper()
	task := &Task{
		ID:          taskID,
		Provider:    ProviderCodex,
		TmuxSession: "repo_" + taskID,
	}
	svc.taskRepo.listTasks = append(svc.taskRepo.listTasks, task)
	svc.taskRepo.providerSessionsByTask[taskID] = []TaskProviderSession{{
		TaskID:            taskID,
		Provider:          ProviderCodex,
		ProviderSessionID: "session-" + taskID,
		TranscriptPath:    "/tmp/" + taskID + ".jsonl",
	}}
	require.NoError(t, svc.taskRepoMock.UpsertTaskStatus(t.Context(), TaskStatusUpdate{
		TaskID:       taskID,
		Provider:     ProviderCodex,
		Phase:        TaskStatusPhaseWorking,
		RawEventName: "PostToolUse",
		ObservedAt:   time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC),
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
