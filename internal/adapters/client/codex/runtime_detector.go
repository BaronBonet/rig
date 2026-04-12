package codex

import (
	"regexp"
	"strings"
	"time"

	"rig/internal/core"
)

type RuntimeDetector struct {
	activityWindow time.Duration
}

const defaultActivityWindow = 2 * time.Second

var ansiEscapePattern = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

func NewRuntimeDetector(activityWindow time.Duration) *RuntimeDetector {
	return &RuntimeDetector{activityWindow: activityWindow}
}

func (d *RuntimeDetector) Detect(snapshot core.RuntimeSnapshot) core.RuntimeState {
	return detectRuntimeStateWithWindow(snapshot, d.activityWindow)
}

func detectRuntimeState(snapshot core.RuntimeSnapshot) core.RuntimeState {
	return detectRuntimeStateWithWindow(snapshot, defaultActivityWindow)
}

func detectRuntimeStateWithWindow(snapshot core.RuntimeSnapshot, activityWindow time.Duration) core.RuntimeState {
	command := normalizeCommand(snapshot.ForegroundCommand)
	if command == "" {
		return core.RuntimeStateNone
	}

	if isShellCommand(command) {
		if strings.TrimSpace(snapshot.PaneID) != "" && snapshot.HadAgentBinding {
			return core.RuntimeStateFinished
		}
		return core.RuntimeStateNone
	}

	if command != "codex" {
		return core.RuntimeStateNone
	}

	content := strings.ToLower(stripANSI(snapshot.Content))
	if hasCodexBusyMarker(content) {
		return core.RuntimeStateRunning
	}
	if hasCodexPromptMarker(content) {
		return core.RuntimeStateNeedsInput
	}
	if hasRecentOutput(snapshot.ObservedAt, snapshot.LastOutputAt, activityWindow) {
		return core.RuntimeStateRunning
	}

	return core.RuntimeStateNone
}

func normalizeCommand(command string) string {
	command = strings.ToLower(strings.TrimSpace(command))
	if command == "codex" || strings.HasPrefix(command, "codex-") {
		return "codex"
	}
	return command
}

func stripANSI(content string) string {
	return ansiEscapePattern.ReplaceAllString(content, "")
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
	content = strings.ToLower(content)
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "›" || strings.HasPrefix(trimmed, "› ") {
			return true
		}
		if strings.Contains(trimmed, "continue?") {
			return true
		}
	}

	return false
}
