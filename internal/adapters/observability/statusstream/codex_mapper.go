package statusstream

import (
	"strings"
	"time"

	"rig/internal/core"
)

func MapCodexHookToStatus(summary *core.HookSessionSummary, observedAt time.Time) (core.TaskStatusUpdate, bool) {
	if summary == nil {
		return core.TaskStatusUpdate{}, false
	}

	eventName := strings.TrimSpace(summary.LastEventName)
	if strings.TrimSpace(summary.TaskID) == "" || eventName == "" {
		return core.TaskStatusUpdate{}, false
	}

	var phase core.TaskStatusPhase
	switch eventName {
	case "UserPromptSubmit", "PreToolUse", "PostToolUse":
		phase = core.TaskStatusPhaseWorking
	case "PermissionRequest", "Stop":
		phase = core.TaskStatusPhaseWaitingForInput
	default:
		return core.TaskStatusUpdate{}, false
	}

	if observedAt.IsZero() {
		observedAt = summary.LastActivityAt
	}

	return core.TaskStatusUpdate{
		TaskID:       summary.TaskID,
		Provider:     core.AgentProviderCodex,
		Phase:        phase,
		RawEventName: eventName,
		ObservedAt:   observedAt,
	}, true
}
