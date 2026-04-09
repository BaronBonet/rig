package observer

import (
	"context"
	"testing"
	"time"

	"agent/internal/core"

	"github.com/stretchr/testify/require"
)

func TestHub_PublishesToSubscribers(t *testing.T) {
	hub := NewHub()

	first, releaseFirst := hub.Subscribe(t.Context())
	defer releaseFirst()
	second, releaseSecond := hub.Subscribe(t.Context())
	defer releaseSecond()

	expected := core.HookSessionSummary{
		TaskID:          "task-1",
		SessionID:       "sess-1",
		LastEventName:   "SessionStart",
		RuntimePhase:    core.HookRuntimePhaseReady,
		LastCommandText: "go test ./...",
	}

	hub.Publish(expected)

	require.Eventually(t, func() bool {
		select {
		case got := <-first:
			require.Equal(t, expected, got)
		default:
			return false
		}

		select {
		case got := <-second:
			require.Equal(t, expected, got)
		default:
			return false
		}

		return true
	}, time.Second, 10*time.Millisecond)
}

func TestHub_SubscriptionStopsOnContextCancel(t *testing.T) {
	hub := NewHub()
	ctx, cancel := context.WithCancel(t.Context())

	updates, release := hub.Subscribe(ctx)
	cancel()

	require.Eventually(t, func() bool {
		select {
		case _, ok := <-updates:
			return !ok
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)

	release()
}
