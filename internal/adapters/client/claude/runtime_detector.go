package claude

import (
	"regexp"
	"strings"
	"time"

	"agent/internal/core"
)

type RuntimeDetector struct {
	activityWindow time.Duration
}

const defaultActivityWindow = 2 * time.Second

var ansiEscapePattern = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
var claudeLiveProgressPattern = regexp.MustCompile(`(?m)^\s*[·•]\s+.+…\s+\([^)]+\)\s*$`)

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

	content := stripANSI(snapshot.Content)
	tail := lastNLines(content, 5)
	tailLower := strings.ToLower(tail)

	// When Claude is actively running a tool, tmux may report the tool
	// process (for example bash or go) instead of claude itself. The busy
	// footer is still authoritative in the captured pane content.
	if snapshot.HadAgentBinding && (hasClaudeBusyMarker(tailLower) || hasClaudeLiveProgressMarker(tail)) {
		return core.RuntimeStateRunning
	}

	if isShellCommand(command) {
		if strings.TrimSpace(snapshot.PaneID) != "" && snapshot.HadAgentBinding {
			return core.RuntimeStateFinished
		}
		return core.RuntimeStateNone
	}

	if command != "claude" {
		return core.RuntimeStateNone
	}

	if hasClaudePromptMarker(tail) {
		return core.RuntimeStateNeedsInput
	}
	if hasClaudeBusyMarker(tailLower) {
		return core.RuntimeStateRunning
	}
	if hasRecentOutput(snapshot.ObservedAt, snapshot.LastOutputAt, activityWindow) {
		return core.RuntimeStateRunning
	}

	return core.RuntimeStateNone
}

func normalizeCommand(command string) string {
	command = strings.ToLower(strings.TrimSpace(command))
	if command == "claude" || strings.HasPrefix(command, "claude-") {
		return "claude"
	}
	// Claude Code runs as a Node.js process; tmux reports the interpreter
	// rather than the script name. This detector is only called for
	// claude-provider tasks, so treating node/deno as claude is safe.
	if command == "node" || command == "deno" {
		return "claude"
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

func hasClaudeBusyMarker(content string) bool {
	return strings.Contains(content, "esc to interrupt") ||
		strings.Contains(content, "ctrl+c to interrupt")
}

func hasClaudeLiveProgressMarker(content string) bool {
	return claudeLiveProgressPattern.MatchString(content)
}

func hasClaudePromptMarker(content string) bool {
	lower := strings.ToLower(content)
	// Claude Code permission prompt footers — these always appear at the
	// bottom of interactive permission dialogs (tool approval, workspace
	// trust, external imports). Checking the full content avoids dependence
	// on a specific line-window size.
	if strings.Contains(lower, "esc to cancel") ||
		strings.Contains(lower, "tab to amend") ||
		strings.Contains(lower, "enter to confirm") {
		return true
	}

	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		// Claude Code input prompt: ❯ (U+276F)
		if trimmed == "❯" || strings.HasPrefix(trimmed, "❯ ") {
			return true
		}
		// Claude Code vim-style insert mode indicator
		if strings.Contains(trimmed, "-- INSERT --") {
			return true
		}
		if strings.Contains(strings.ToLower(trimmed), "(y/n)") {
			return true
		}
		if strings.Contains(strings.ToLower(trimmed), "do you want to proceed") {
			return true
		}
	}

	return false
}

func lastNLines(content string, n int) string {
	lines := strings.Split(content, "\n")
	// Trim trailing empty lines
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) <= n {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}
