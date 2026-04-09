package core

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type stubHookObservabilityRepository struct {
	listHookSessionSummaries func(ctx context.Context, taskIDs []string) (map[string]*HookSessionSummary, error)
	listHookEvents           func(ctx context.Context, taskID string, limit int) ([]HookEvent, error)
	subscribeHookUpdates     func(ctx context.Context) (<-chan HookSessionSummary, func(), error)
}

type stubObserverRuntimeRepository struct {
	listObserverSummaries func(ctx context.Context, taskIDs []string) (map[string]*ObserverSummary, error)
	upsertObserverSummary func(ctx context.Context, summary *ObserverSummary) error
	subscribeTaskUpdates  func(ctx context.Context) (<-chan ObserverTaskUpdate, func(), error)
	lastUpsert            *ObserverSummary
}

func (s stubHookObservabilityRepository) ListHookSessionSummaries(
	ctx context.Context,
	taskIDs []string,
) (map[string]*HookSessionSummary, error) {
	if s.listHookSessionSummaries == nil {
		return map[string]*HookSessionSummary{}, nil
	}

	return s.listHookSessionSummaries(ctx, taskIDs)
}

func (s stubHookObservabilityRepository) ListHookEvents(
	ctx context.Context,
	taskID string,
	limit int,
) ([]HookEvent, error) {
	if s.listHookEvents == nil {
		return nil, nil
	}

	return s.listHookEvents(ctx, taskID, limit)
}

func (s stubHookObservabilityRepository) SubscribeHookSessionUpdates(
	ctx context.Context,
) (<-chan HookSessionSummary, func(), error) {
	if s.subscribeHookUpdates == nil {
		ch := make(chan HookSessionSummary)
		close(ch)
		return ch, func() {}, nil
	}

	return s.subscribeHookUpdates(ctx)
}

func (s *stubObserverRuntimeRepository) ListObserverSummaries(
	ctx context.Context,
	taskIDs []string,
) (map[string]*ObserverSummary, error) {
	if s == nil || s.listObserverSummaries == nil {
		return map[string]*ObserverSummary{}, nil
	}

	return s.listObserverSummaries(ctx, taskIDs)
}

func (s *stubObserverRuntimeRepository) UpsertObserverSummary(ctx context.Context, summary *ObserverSummary) error {
	if s == nil {
		return nil
	}
	if summary != nil {
		clone := *summary
		s.lastUpsert = &clone
	}
	if s.upsertObserverSummary == nil {
		return nil
	}

	return s.upsertObserverSummary(ctx, summary)
}

func (s *stubObserverRuntimeRepository) SubscribeObserverTaskUpdates(
	ctx context.Context,
) (<-chan ObserverTaskUpdate, func(), error) {
	if s == nil || s.subscribeTaskUpdates == nil {
		ch := make(chan ObserverTaskUpdate)
		close(ch)
		return ch, func() {}, nil
	}

	return s.subscribeTaskUpdates(ctx)
}

func TestServiceListTaskViews_UsesHookSummaryWhenAvailable(t *testing.T) {
	h := newTestService(t)
	task := h.existingTask("task-1")
	task.Slug = "billing-retry-flow"
	task.BranchName = "feat/billing-retry-flow"
	task.WorktreePath = t.TempDir()
	task.TmuxSession = "repo-billing-retry-flow"
	summary := &HookSessionSummary{
		TaskID:          task.ID,
		SessionID:       "sess-1",
		RuntimePhase:    HookRuntimePhaseRunningCommand,
		LastCommandText: "go test ./...",
	}

	h.taskRepo.listTasks = []*Task{task}
	h.repoClient.repoResources = RepoResources{WorktreeExists: true, BranchExists: true}
	h.sessionClient.sessionResources = SessionResources{
		SessionExists:      true,
		AgentWindowExists:  true,
		EditorWindowExists: true,
	}
	h.service.hooks = stubHookObservabilityRepository{
		listHookSessionSummaries: func(_ context.Context, taskIDs []string) (map[string]*HookSessionSummary, error) {
			require.Equal(t, []string{task.ID}, taskIDs)
			return map[string]*HookSessionSummary{task.ID: summary}, nil
		},
	}

	views, err := h.service.ListTaskViews(t.Context())
	require.NoError(t, err)
	require.Len(t, views, 1)
	require.NotNil(t, views[0].HookSession)
	require.Equal(t, HookRuntimePhaseRunningCommand, views[0].HookSession.RuntimePhase)
	require.Equal(t, "go test ./...", views[0].HookSession.LastCommandText)
}

func TestServiceListTaskViews_FallsBackToRuntimeStateWithoutHookSummary(t *testing.T) {
	h := newTestService(t)
	task := h.existingTask("task-1")
	now := time.Date(2026, 4, 8, 10, 0, 0, 0, time.UTC)

	h.repoClient.repoResources = RepoResources{WorktreeExists: true, BranchExists: true}
	h.sessionClient.sessionResources = SessionResources{
		SessionExists:      true,
		AgentWindowExists:  true,
		EditorWindowExists: true,
	}
	h.sessionClient.snapshot = RuntimeSnapshot{ObservedAt: now}
	h.providerRepo.runtimeState = RuntimeStateNeedsInput
	h.service.hooks = stubHookObservabilityRepository{
		listHookSessionSummaries: func(_ context.Context, taskIDs []string) (map[string]*HookSessionSummary, error) {
			require.Equal(t, []string{task.ID}, taskIDs)
			return map[string]*HookSessionSummary{}, nil
		},
	}

	views, err := h.service.ListTaskViews(t.Context())
	require.NoError(t, err)
	require.Len(t, views, 1)
	require.Equal(t, RuntimeStateNeedsInput, views[0].Task.RuntimeState)
	require.Equal(t, now, views[0].Task.RuntimeStateUpdatedAt)
	require.Nil(t, views[0].HookSession)
}

func TestServiceGetTaskHookEvents_ReturnsRepositoryEvents(t *testing.T) {
	h := newTestService(t)
	h.service.hooks = stubHookObservabilityRepository{
		listHookEvents: func(_ context.Context, taskID string, limit int) ([]HookEvent, error) {
			require.Equal(t, "task-1", taskID)
			require.Equal(t, 5, limit)
			return []HookEvent{{EventName: "Stop"}, {EventName: "PostToolUse"}}, nil
		},
	}

	events, err := h.service.GetTaskHookEvents(t.Context(), "task-1", 5)
	require.NoError(t, err)
	require.Len(t, events, 2)
	require.Equal(t, "Stop", events[0].EventName)
	require.Equal(t, "PostToolUse", events[1].EventName)
}

func TestServiceGetTaskHookEvents_ReturnsNilWhenHooksUnavailable(t *testing.T) {
	h := newTestService(t)
	h.service.hooks = nil

	events, err := h.service.GetTaskHookEvents(t.Context(), "task-1", 5)
	require.NoError(t, err)
	require.Nil(t, events)
}

func TestServiceSubscribeTaskHookUpdates_PassesThroughRepositoryStream(t *testing.T) {
	h := newTestService(t)
	updates := make(chan HookSessionSummary, 1)
	released := false
	h.service.hooks = stubHookObservabilityRepository{
		subscribeHookUpdates: func(_ context.Context) (<-chan HookSessionSummary, func(), error) {
			return updates, func() {
				released = true
				close(updates)
			}, nil
		},
	}

	stream, release, err := h.service.SubscribeTaskHookUpdates(t.Context())
	require.NoError(t, err)

	expected := HookSessionSummary{TaskID: "task-1", RuntimePhase: HookRuntimePhaseIdle}
	updates <- expected
	require.Equal(t, expected, <-stream)

	release()
	require.True(t, released)
}

func TestServiceSubscribeTaskHookUpdates_ReturnsClosedStreamWhenHooksUnavailable(t *testing.T) {
	h := newTestService(t)
	h.service.hooks = nil

	stream, release, err := h.service.SubscribeTaskHookUpdates(t.Context())
	require.NoError(t, err)

	select {
	case _, ok := <-stream:
		require.False(t, ok)
	default:
		t.Fatal("expected closed stream")
	}

	release()
}

func TestServiceSubscribeTaskUpdates_ReceivesObserverEvents(t *testing.T) {
	h := newTestService(t)
	updates := make(chan ObserverTaskUpdate, 1)
	released := false
	h.service.observers = &stubObserverRuntimeRepository{
		subscribeTaskUpdates: func(_ context.Context) (<-chan ObserverTaskUpdate, func(), error) {
			return updates, func() {
				released = true
				close(updates)
			}, nil
		},
	}

	stream, release, err := h.service.SubscribeTaskUpdates(t.Context())
	require.NoError(t, err)

	expected := ObserverTaskUpdate{
		TaskID:          "task-1",
		DisplayStatus:   DisplayStatusWorking,
		DisplayActivity: DisplayActivityCommand,
		LastActivityAt:  time.Date(2026, 4, 9, 12, 0, 0, 0, time.UTC),
	}
	updates <- expected
	require.Equal(t, expected, <-stream)

	release()
	require.True(t, released)
}
