package providerkit

import (
	"testing"
	"time"

	"github.com/BaronBonet/rig/internal/core"

	"github.com/stretchr/testify/require"
)

func TestCatalogStatusUpdate_BindingWithoutPhaseYieldsNoStatusUpdate(t *testing.T) {
	catalog := Catalog{
		{Event: core.HookEventStop, Phase: core.TaskStatusPhaseWaitingForInput},
		{Event: "SubagentStart"},
	}

	update, err := catalog.StatusUpdate(core.ProviderCodex, core.HookEventInput{
		TaskID:     "task-123",
		OccurredAt: time.Date(2026, time.October, 1, 10, 0, 0, 0, time.UTC),
		EventName:  "SubagentStart",
	})

	require.NoError(t, err)
	require.Nil(t, update)
	require.Equal(t, []string{core.HookEventStop, "SubagentStart"}, catalog.EventNames())
}

func TestCatalogStatusUpdate_BindingWithPhaseYieldsStatusUpdate(t *testing.T) {
	catalog := Catalog{
		{Event: core.HookEventStop, Phase: core.TaskStatusPhaseWaitingForInput},
		{Event: "SubagentStart"},
	}
	observedAt := time.Date(2026, time.October, 1, 10, 0, 0, 0, time.UTC)

	update, err := catalog.StatusUpdate(core.ProviderCodex, core.HookEventInput{
		TaskID:     "task-123",
		OccurredAt: observedAt,
		EventName:  core.HookEventStop,
	})

	require.NoError(t, err)
	require.Equal(t, &core.TaskStatusUpdate{
		TaskID:       "task-123",
		Provider:     core.ProviderCodex,
		Phase:        core.TaskStatusPhaseWaitingForInput,
		RawEventName: core.HookEventStop,
		ObservedAt:   observedAt,
	}, update)
}
