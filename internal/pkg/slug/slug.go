package slug

import (
	"regexp"
	"strconv"
	"strings"
)

var nonSlugCharPattern = regexp.MustCompile(`[^a-z0-9]+`)

func FromDisplayName(displayName string) string {
	normalized := strings.ToLower(strings.TrimSpace(displayName))
	normalized = nonSlugCharPattern.ReplaceAllString(normalized, "-")
	normalized = strings.Trim(normalized, "-")

	if normalized == "" {
		return "task"
	}

	return normalized
}

func EnsureUnique(base string, existing map[string]struct{}) string {
	if _, found := existing[base]; !found {
		return base
	}

	for i := 2; ; i++ {
		candidate := base + "-" + strconv.Itoa(i)
		if _, found := existing[candidate]; !found {
			return candidate
		}
	}
}
