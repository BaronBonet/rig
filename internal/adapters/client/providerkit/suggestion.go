package providerkit

import (
	"encoding/json"
	"strings"
	"unicode"

	"github.com/BaronBonet/rig/internal/core"
)

// baseTitleSkipPrefixes reject CLI noise every provider can emit in place of
// a task title. Providers append their own noise prefixes.
var baseTitleSkipPrefixes = []string{"usage:", "error:", "exit status"}

// ParseSuggestion scans raw CLI output bottom-up for a JSON task suggestion
// line, normalizing its title. skipPrefixes lists provider-specific noise
// line prefixes (lowercase) to reject in addition to the common CLI noise.
func ParseSuggestion(raw string, skipPrefixes []string) (core.TaskSuggestion, bool) {
	lines := strings.Split(raw, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var suggestion core.TaskSuggestion
		if err := json.Unmarshal([]byte(line), &suggestion); err == nil && suggestion.Name != "" {
			suggestion.Name = NormalizeTitle(suggestion.Name, skipPrefixes)
			if suggestion.Name != "" {
				if suggestion.BranchType == "" {
					suggestion.BranchType = "feat"
				}
				return suggestion, true
			}
		}
	}

	return core.TaskSuggestion{}, false
}

// ExtractTitle scans raw CLI output bottom-up for the last plain-text line
// usable as a task title.
func ExtractTitle(raw string, skipPrefixes []string) string {
	lines := strings.Split(raw, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if title := NormalizeTitle(lines[i], skipPrefixes); title != "" {
			return title
		}
	}

	return ""
}

// NormalizeTitle strips CLI decoration from a candidate task title and
// rejects noise lines, returning "" when the line is unusable.
func NormalizeTitle(raw string, skipPrefixes []string) string {
	line := strings.TrimSpace(raw)
	line = strings.ReplaceAll(line, "`", "")
	line = strings.Trim(line, "[]")
	line = strings.Trim(line, ":")
	line = strings.Trim(line, `"'`)
	line = strings.TrimSpace(line)

	if line == "" {
		return ""
	}

	lower := strings.ToLower(line)
	for _, prefix := range baseTitleSkipPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return ""
		}
	}
	for _, prefix := range skipPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return ""
		}
	}
	if !containsLetter(line) {
		return ""
	}

	return line
}

func containsLetter(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}

	return false
}
