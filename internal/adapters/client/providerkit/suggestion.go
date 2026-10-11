package providerkit

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode"

	"github.com/BaronBonet/rig/internal/core"
)

// TicketLookupTimeout bounds the naming attempt that may look up the ticket a
// prompt refers to, so a slow or unreachable tracker never stalls task
// creation for long.
const TicketLookupTimeout = 45 * time.Second

// SuggestWithTicketLookup asks suggest for a task name that may look up the
// referenced ticket first. Ticket lookup is best effort: when that attempt
// fails or runs past TicketLookupTimeout, the task is named from the prompt
// alone instead.
func SuggestWithTicketLookup(
	ctx context.Context,
	suggest func(ctx context.Context, lookupTickets bool) (core.TaskSuggestion, error),
) (core.TaskSuggestion, error) {
	lookupCtx, cancel := context.WithTimeout(ctx, TicketLookupTimeout)
	suggestion, err := suggest(lookupCtx, true)
	cancel()
	if err == nil {
		return suggestion, nil
	}
	if ctx.Err() != nil {
		return core.TaskSuggestion{}, err
	}

	return suggest(ctx, false)
}

// baseTitleSkipPrefixes reject CLI noise every provider can emit in place of
// a task title. Providers append their own noise prefixes.
var baseTitleSkipPrefixes = []string{"usage:", "error:", "exit status"}

// ParseSuggestion scans raw CLI output bottom-up for a JSON task suggestion
// line, normalizing its title and ticket reference. A suggestion needs a
// title, a ticket reference, or both. skipPrefixes lists provider-specific
// noise line prefixes (lowercase) to reject in addition to the common CLI
// noise.
func ParseSuggestion(raw string, skipPrefixes []string) (core.TaskSuggestion, bool) {
	lines := strings.Split(raw, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "{") {
			continue
		}
		// A suggestion usually fits on one line, but may be pretty-printed
		// across the lines that follow.
		block := strings.Join(lines[i:], "\n")
		block = block[:strings.LastIndex(block, "}")+1]
		for _, candidate := range []string{line, block} {
			if suggestion, ok := parseSuggestionJSON(candidate, skipPrefixes); ok {
				return suggestion, true
			}
		}
	}

	return core.TaskSuggestion{}, false
}

func parseSuggestionJSON(candidate string, skipPrefixes []string) (core.TaskSuggestion, bool) {
	var suggestion core.TaskSuggestion
	if err := json.Unmarshal([]byte(candidate), &suggestion); err != nil {
		return core.TaskSuggestion{}, false
	}

	suggestion.Name = NormalizeTitle(suggestion.Name, skipPrefixes)
	suggestion.Ticket = normalizeTicket(suggestion.Ticket)
	if suggestion.Name == "" && suggestion.Ticket == "" {
		return core.TaskSuggestion{}, false
	}
	if suggestion.BranchType == "" {
		suggestion.BranchType = "feat"
	}

	return suggestion, true
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

// maxTicketLength bounds a ticket reference. Anything longer is a sentence
// or a URL, not a reference.
const maxTicketLength = 40

// normalizeTicket strips decoration from a ticket reference and rejects
// values that cannot be one, since the reference ends up in the branch name.
// Every tracker's references carry a number, which rules out placeholders
// such as "none" or "N/A". Unlike titles, references such as "#18" need not
// contain a letter.
func normalizeTicket(raw string) string {
	ticket := strings.ReplaceAll(raw, "`", "")
	ticket = strings.Trim(ticket, ` "'[]`)
	if len(ticket) > maxTicketLength ||
		len(strings.Fields(ticket)) > 2 ||
		strings.Contains(ticket, "://") ||
		!strings.ContainsFunc(ticket, unicode.IsDigit) {
		return ""
	}

	return ticket
}

func containsLetter(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}

	return false
}
