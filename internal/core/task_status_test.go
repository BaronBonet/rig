package core_test

import (
	"testing"
	"time"

	"rig/internal/core"

	"github.com/stretchr/testify/require"
)

func TestTaskStatusPhase_FirstSliceValues(t *testing.T) {
	require.Equal(t, core.TaskStatusPhase("working"), core.TaskStatusPhaseWorking)
	require.Equal(t, core.TaskStatusPhase("waiting_for_input"), core.TaskStatusPhaseWaitingForInput)
}

func TestTaskStatusUpdate_HoldsFirstSliceFields(t *testing.T) {
	now := time.Date(2026, time.April, 18, 10, 30, 0, 0, time.UTC)

	update := core.TaskStatusUpdate{
		TaskID:       "task-123",
		Provider:     core.AgentProviderCodex,
		Phase:        core.TaskStatusPhaseWorking,
		RawEventName: "PreToolUse",
		ObservedAt:   now,
	}

	require.Equal(t, "task-123", update.TaskID)
	require.Equal(t, core.AgentProviderCodex, update.Provider)
	require.Equal(t, core.TaskStatusPhaseWorking, update.Phase)
	require.Equal(t, "PreToolUse", update.RawEventName)
	require.Equal(t, now, update.ObservedAt)
}
