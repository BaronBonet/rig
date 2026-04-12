package observer

import (
	"context"
	"testing"
	"time"

	"rig/internal/core"

	"github.com/stretchr/testify/require"
)

type stubPublishingHookRepo struct {
	summary         *core.HookSessionSummary
	observerSummary *core.ObserverSummary
}

func (s *stubPublishingHookRepo) IngestHookEvent(
	_ context.Context,
	_ core.HookEventInput,
) (*core.HookSessionSummary, error) {
	return s.summary, nil
}

func (s *stubPublishingHookRepo) ListObserverSummaries(
	_ context.Context,
	taskIDs []string,
) (map[string]*core.ObserverSummary, error) {
	summaries := make(map[string]*core.ObserverSummary, len(taskIDs))
	if s.observerSummary != nil {
		summaries[s.observerSummary.TaskID] = s.observerSummary
	}
	return summaries, nil
}

func (s *stubPublishingHookRepo) UpsertObserverSummary(context.Context, *core.ObserverSummary) error {
	return nil
}

func (s *stubPublishingHookRepo) SubscribeObserverTaskUpdates(
	context.Context,
) (<-chan core.ObserverTaskUpdate, func(), error) {
	ch := make(chan core.ObserverTaskUpdate)
	close(ch)
	return ch, func() {}, nil
}

func TestPublishingHookIngestor_PublishesHookSessionWithObserverUpdate(t *testing.T) {
	hub := NewHub()
	updates, release := hub.Subscribe(t.Context())
	defer release()

	repo := &stubPublishingHookRepo{
		summary: &core.HookSessionSummary{
			TaskID:               "task-1",
			LastEventName:        "Stop",
			LastPromptText:       "fix the billing retry flow",
			LastAssistantMessage: "Updated the retry loop and tests",
		},
		observerSummary: &core.ObserverSummary{
			TaskID:                "task-1",
			DisplayStatus:         core.DisplayStatusWorking,
			DisplayActivity:       core.DisplayActivityCommand,
			ProcessAlive:          true,
			LastRuntimeObservedAt: time.Date(2026, 4, 10, 9, 0, 0, 0, time.UTC),
		},
	}

	ingestor := newPublishingHookIngestor(repo, hub, nil)

	summary, err := ingestor.IngestHookEvent(t.Context(), core.HookEventInput{
		TaskID:    "task-1",
		EventName: "Stop",
	})
	require.NoError(t, err)
	require.NotNil(t, summary)

	select {
	case update := <-updates:
		require.Equal(t, "task-1", update.TaskID)
		require.NotNil(t, update.HookSession)
		require.Equal(t, "fix the billing retry flow", update.HookSession.LastPromptText)
		require.Equal(t, "Updated the retry loop and tests", update.HookSession.LastAssistantMessage)
		require.Equal(t, "Stop", update.HookSession.LastEventName)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for published update")
	}
}
