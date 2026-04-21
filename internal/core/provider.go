package core

import (
	"path/filepath"
	"regexp"
	"strings"
)

var providerANSIPattern = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

func NormalizeProvider(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "codex":
		return "codex"
	default:
		return ""
	}
}

func InferProviderFromModel(model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return ""
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
	}

	content := stripProviderANSI(snapshot.Content)
	switch {
	case contentLooksLikeCodex(content):
		return "codex"
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
