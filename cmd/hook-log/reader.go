package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"agent/internal/experimental/hooklog"
)

func renderSummary(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	sessionEvents := make(map[string]map[string]int)
	lastAssistant := make(map[string]string)

	scanner := bufio.NewScanner(file)
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

		if msg := strings.TrimSpace(record.LastAssistantMessage()); msg != "" {
			lastAssistant[sessionID] = msg
		}
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

		if msg := lastAssistant[sessionID]; msg != "" {
			fmt.Fprintf(&b, "  last assistant message: %s\n", msg)
		}
	}

	return strings.TrimRight(b.String(), "\n"), nil
}
