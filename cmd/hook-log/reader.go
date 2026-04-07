package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"

	"agent/internal/experimental/hooklog"
)

const maxLogLineSize = 1024 * 1024

func renderSummary(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLogLineSize)

	sessionEvents := make(map[string]map[string]int)
	lastAssistant := make(map[string]assistantMessage)
	recordIndex := 0

	for scanner.Scan() {
		var record hooklog.Record
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return "", fmt.Errorf("decode %s: %w", path, err)
		}

		sessionID := record.SessionID()
		if sessionID == "" {
			sessionID = "(unknown session)"
		}

		if sessionEvents[sessionID] == nil {
			sessionEvents[sessionID] = make(map[string]int)
		}
		sessionEvents[sessionID][record.EventName]++

		if msg := sanitizeSummaryText(record.LastAssistantMessage()); msg != "" {
			candidate := assistantMessage{
				text:       msg,
				receivedAt: record.ReceivedAt,
				order:      recordIndex,
			}
			if shouldReplaceAssistant(lastAssistant[sessionID], candidate) {
				lastAssistant[sessionID] = candidate
			}
		}

		recordIndex++
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}

	sessions := make([]string, 0, len(sessionEvents))
	for sessionID := range sessionEvents {
		sessions = append(sessions, sessionID)
	}
	sort.Strings(sessions)

	var b strings.Builder
	for _, sessionID := range sessions {
		fmt.Fprintf(&b, "session %s\n", sessionID)

		events := make([]string, 0, len(sessionEvents[sessionID]))
		for eventName := range sessionEvents[sessionID] {
			events = append(events, eventName)
		}
		sort.Strings(events)

		for _, eventName := range events {
			fmt.Fprintf(&b, "  %s: %d\n", eventName, sessionEvents[sessionID][eventName])
		}

		if msg := lastAssistant[sessionID].text; msg != "" {
			fmt.Fprintf(&b, "  last assistant message: %s\n", msg)
		}
	}

	return strings.TrimRight(b.String(), "\n"), nil
}

type assistantMessage struct {
	text       string
	receivedAt time.Time
	order      int
}

func shouldReplaceAssistant(existing, candidate assistantMessage) bool {
	if candidate.text == "" {
		return false
	}
	if existing.text == "" {
		return true
	}
	if candidate.receivedAt.IsZero() {
		if existing.receivedAt.IsZero() {
			return candidate.order > existing.order
		}
		return false
	}
	if existing.receivedAt.IsZero() {
		return true
	}
	if candidate.receivedAt.After(existing.receivedAt) {
		return true
	}
	if candidate.receivedAt.Before(existing.receivedAt) {
		return false
	}
	return candidate.order > existing.order
}

func sanitizeSummaryText(s string) string {
	if s == "" {
		return ""
	}

	normalized := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)

	return strings.Join(strings.Fields(normalized), " ")
}
