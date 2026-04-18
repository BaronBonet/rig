package statusstream

import (
	"testing"
	"time"

	"rig/internal/core"

	"github.com/stretchr/testify/require"
)

func TestMapCodexHookToStatus(t *testing.T) {
	observedAt := time.Date(2026, time.April, 18, 11, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		eventName string
		phase     core.TaskStatusPhase
		ok        bool
	}{
		{name: "user prompt submit maps to working", eventName: "UserPromptSubmit", phase: core.TaskStatusPhaseWorking, ok: true},
		{name: "pre tool use maps to working", eventName: "PreToolUse", phase: core.TaskStatusPhaseWorking, ok: true},
		{name: "post tool use maps to working", eventName: "PostToolUse", phase: core.TaskStatusPhaseWorking, ok: true},
		{name: "stop maps to waiting for input", eventName: "Stop", phase: core.TaskStatusPhaseWaitingForInput, ok: true},
		{name: "unsupported event ignored", eventName: "SessionStart", ok: false},
		{name: "empty event ignored", eventName: "", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			summary := &core.HookSessionSummary{
				TaskID:         "task-1",
				Provider:       string(core.AgentProviderCodex),
				LastEventName:  tt.eventName,
				LastActivityAt: observedAt,
			}

			update, ok := MapCodexHookToStatus(summary, observedAt)
			require.Equal(t, tt.ok, ok)
			if !tt.ok {
				require.Equal(t, core.TaskStatusUpdate{}, update)
				return
			}

			require.Equal(t, "task-1", update.TaskID)
			require.Equal(t, core.AgentProviderCodex, update.Provider)
			require.Equal(t, tt.phase, update.Phase)
			require.Equal(t, tt.eventName, update.RawEventName)
			require.Equal(t, observedAt, update.ObservedAt)
		})
	}
}
