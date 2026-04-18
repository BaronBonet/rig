package statusstream

import (
	"context"
	"testing"
	"time"

	"rig/internal/core"

	"github.com/stretchr/testify/require"
)

func TestHubPublish_DeliversUpdateToSubscriber(t *testing.T) {
	hub := NewHub()
	updates, cleanup := hub.Subscribe(context.Background())
	defer cleanup()

	expected := core.TaskStatusUpdate{
		TaskID:       "task-1",
		Provider:     core.AgentProviderCodex,
		Phase:        core.TaskStatusPhaseWorking,
		RawEventName: "PreToolUse",
		ObservedAt:   time.Now().UTC(),
	}

	hub.Publish(expected)

	select {
	case got := <-updates:
		require.Equal(t, expected, got)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for published update")
	}
}

func TestHubPublish_DeliversToMultipleSubscribers(t *testing.T) {
	hub := NewHub()
	first, firstCleanup := hub.Subscribe(context.Background())
	defer firstCleanup()
	second, secondCleanup := hub.Subscribe(context.Background())
	defer secondCleanup()

	expected := core.TaskStatusUpdate{
		TaskID:       "task-2",
		Provider:     core.AgentProviderCodex,
		Phase:        core.TaskStatusPhaseWaitingForInput,
		RawEventName: "Stop",
		ObservedAt:   time.Now().UTC(),
	}

	hub.Publish(expected)

	for _, ch := range []<-chan core.TaskStatusUpdate{first, second} {
		select {
		case got := <-ch:
			require.Equal(t, expected, got)
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for published update")
		}
	}
}

func TestHubCleanup_StopsDeliveringToClosedSubscriber(t *testing.T) {
	hub := NewHub()
	updates, cleanup := hub.Subscribe(context.Background())
	cleanup()

	_, stillOpen := <-updates
	require.False(t, stillOpen)

	hub.Publish(core.TaskStatusUpdate{
		TaskID:       "task-3",
		Provider:     core.AgentProviderCodex,
		Phase:        core.TaskStatusPhaseWorking,
		RawEventName: "UserPromptSubmit",
		ObservedAt:   time.Now().UTC(),
	})
}
