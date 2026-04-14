package core

import (
	"path/filepath"
	"regexp"
	"strings"
)

var providerANSIPattern = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
var claudeProgressPattern = regexp.MustCompile(`(?m)^\s*[·•]\s+.+…\s+\([^)]+\)\s*$`)

func NormalizeProvider(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "codex":
		return "codex"
	case "claude":
		return "claude"
	default:
		return ""
	}
}

func InferProviderFromModel(model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return ""
	}
	if strings.Contains(model, "claude") {
		return "claude"
	}
	if strings.Contains(model, "codex") ||
		strings.Contains(model, "gpt") ||
		strings.HasPrefix(model, "o1") ||
		strings.HasPrefix(model, "o3") ||
		strings.HasPrefix(model, "o4") {
		return "codex"
	}
	return ""
}

func InferProviderFromHookSession(summary *HookSessionSummary) string {
	if summary == nil {
		return ""
	}
	if provider := NormalizeProvider(summary.Provider); provider != "" {
		return provider
	}
	return InferProviderFromModel(summary.Model)
}

func InferProviderFromRuntimeSnapshot(snapshot RuntimeSnapshot) string {
	switch normalizeProviderCommand(snapshot.ForegroundCommand) {
	case "codex":
		return "codex"
	case "claude":
		return "claude"
	case "node", "deno":
		if contentLooksLikeClaude(snapshot.Content) {
			return "claude"
		}
	}

	content := stripProviderANSI(snapshot.Content)
	switch {
	case contentLooksLikeCodex(content) && !contentLooksLikeClaude(content):
		return "codex"
	case contentLooksLikeClaude(content) && !contentLooksLikeCodex(content):
		return "claude"
	default:
		return ""
	}
}

func normalizeProviderCommand(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	command = strings.ToLower(filepath.Base(command))
	switch {
	case command == "codex" || strings.HasPrefix(command, "codex-"):
		return "codex"
	case command == "claude" || strings.HasPrefix(command, "claude-"):
		return "claude"
	default:
		return command
	}
}

func stripProviderANSI(content string) string {
	return providerANSIPattern.ReplaceAllString(content, "")
}

func contentLooksLikeCodex(content string) bool {
	for _, line := range strings.Split(strings.ToLower(content), "\n") {
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

func contentLooksLikeClaude(content string) bool {
	lower := strings.ToLower(content)
	if strings.Contains(lower, "esc to cancel") ||
		strings.Contains(lower, "tab to amend") ||
		strings.Contains(lower, "enter to confirm") ||
		claudeProgressPattern.MatchString(content) {
		return true
	}

	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "❯" || strings.HasPrefix(trimmed, "❯ "):
			return true
		case strings.Contains(trimmed, "-- INSERT --"):
			return true
		case strings.Contains(strings.ToLower(trimmed), "(y/n)"):
			return true
		case strings.Contains(strings.ToLower(trimmed), "do you want to proceed"):
			return true
		}
	}

	return false
}
