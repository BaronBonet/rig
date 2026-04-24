package core_test

import (
	"testing"
	"time"

	"rig/internal/core"

	"github.com/stretchr/testify/require"
)

func TestTaskStatusPhase_FirstSliceValues(t *testing.T) {
	require.Equal(t, core.TaskStatusPhaseStarting, core.TaskStatusPhase("starting"))
	require.Equal(t, core.TaskStatusPhaseWorking, core.TaskStatusPhase("working"))
	require.Equal(t, core.TaskStatusPhaseWaitingForInput, core.TaskStatusPhase("waiting_for_input"))
	require.Equal(t, core.TaskStatusPhaseStopped, core.TaskStatusPhase("stopped"))
}

func TestTaskStatusUpdate_HoldsFirstSliceFields(t *testing.T) {
	now := time.Date(2026, time.April, 18, 10, 30, 0, 0, time.UTC)

	update := core.TaskStatusUpdate{
		TaskID:       "task-123",
		Provider:     core.ProviderCodex,
		Phase:        core.TaskStatusPhaseWorking,
		RawEventName: "PreToolUse",
		ObservedAt:   now,
	}

	require.Equal(t, "task-123", update.TaskID)
	require.Equal(t, core.ProviderCodex, update.Provider)
	require.Equal(t, core.TaskStatusPhaseWorking, update.Phase)
	require.Equal(t, "PreToolUse", update.RawEventName)
	require.Equal(t, now, update.ObservedAt)
}
