package observer

import (
	"context"
	"testing"
	"time"

	"rig/internal/core"

	"github.com/stretchr/testify/require"
)

func TestHub_PublishesToSubscribers(t *testing.T) {
	hub := NewHub()

	first, releaseFirst := hub.Subscribe(t.Context())
	defer releaseFirst()
	second, releaseSecond := hub.Subscribe(t.Context())
	defer releaseSecond()

	expected := core.ObserverTaskUpdate{
		TaskID:          "task-1",
		DisplayStatus:   core.DisplayStatusWorking,
		DisplayActivity: core.DisplayActivityCommand,
		LastActivityAt:  time.Date(2026, 4, 9, 12, 0, 0, 0, time.UTC),
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
