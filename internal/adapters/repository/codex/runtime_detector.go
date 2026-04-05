package codex

import (
	"strings"
	"time"

	"agent/internal/core"
)

type RuntimeDetector struct {
	activityWindow time.Duration
}

func NewRuntimeDetector(activityWindow time.Duration) *RuntimeDetector {
	return &RuntimeDetector{activityWindow: activityWindow}
}

func (d *RuntimeDetector) Detect(snapshot core.RuntimeSnapshot) core.RuntimeState {
	command := normalizeCommand(snapshot.ForegroundCommand)
	if command == "" {
		return core.RuntimeStateNone
	}

	if isShellCommand(command) {
		if strings.TrimSpace(snapshot.PaneID) != "" {
			return core.RuntimeStateFinished
		}
		return core.RuntimeStateNone
	}

	if command != "codex" {
		return core.RuntimeStateNone
	}

	content := strings.ToLower(snapshot.Content)
	if hasCodexBusyMarker(content) {
		return core.RuntimeStateRunning
	}
	if hasRecentOutput(snapshot.ObservedAt, snapshot.LastOutputAt, d.activityWindow) {
		return core.RuntimeStateRunning
	}
	if hasCodexPromptMarker(content) {
		return core.RuntimeStateNeedsInput
	}

	return core.RuntimeStateNone
}

func normalizeCommand(command string) string {
	return strings.ToLower(strings.TrimSpace(command))
}

func isShellCommand(command string) bool {
	switch command {
	case "sh", "bash", "zsh", "fish", "dash", "ksh":
		return true
	default:
		return false
	}
}

func hasRecentOutput(observedAt, lastOutputAt time.Time, activityWindow time.Duration) bool {
	if observedAt.IsZero() || lastOutputAt.IsZero() || activityWindow <= 0 {
		return false
	}

	if observedAt.Before(lastOutputAt) {
		return false
	}

	return observedAt.Sub(lastOutputAt) <= activityWindow
}

func hasCodexBusyMarker(content string) bool {
	return strings.Contains(content, "esc to interrupt") ||
		strings.Contains(content, "ctrl+c to interrupt") ||
		strings.Contains(content, "working (")
}

func hasCodexPromptMarker(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "›" || strings.HasPrefix(trimmed, "› ") {
			return true
		}
		if strings.Contains(trimmed, "Continue?") {
			return true
		}
	}

	return false
}
