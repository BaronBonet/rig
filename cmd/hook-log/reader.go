package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"

	"agent/internal/experimental/hooklog"
)

func renderSummary(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	reader := bufio.NewReader(file)

	sessionEvents := make(map[string]map[string]int)
	lastAssistant := make(map[string]assistantMessage)
	recordIndex := 0

	for {
		lineBytes, err := reader.ReadBytes('\n')
		if err != nil && err != io.EOF {
			return "", err
		}
		line := strings.TrimSpace(string(lineBytes))
		if line == "" {
			if err == io.EOF {
				break
			}
			continue
		}

		var record hooklog.Record
		if decodeErr := json.Unmarshal([]byte(line), &record); decodeErr != nil {
			return "", fmt.Errorf("decode %s: %w", path, decodeErr)
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
		if err == io.EOF {
			break
		}
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
