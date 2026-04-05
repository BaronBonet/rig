package tmux

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"agent/internal/core"
)

type RuntimeMonitor struct {
	factory    controlPipeFactory
	now        func() time.Time
	mu         sync.Mutex
	pipes      map[string]controlPipe
	boundPanes map[string]string
}

func NewRuntimeMonitor() *RuntimeMonitor {
	return NewRuntimeMonitorWithFactory(execControlPipeFactory{}, time.Now)
}

func NewRuntimeMonitorWithFactory(factory controlPipeFactory, now func() time.Time) *RuntimeMonitor {
	if now == nil {
		now = time.Now
	}
	return &RuntimeMonitor{
		factory:    factory,
		now:        now,
		pipes:      make(map[string]controlPipe),
		boundPanes: make(map[string]string),
	}
}

func (m *RuntimeMonitor) Snapshot(ctx context.Context, task *core.Task) (core.RuntimeSnapshot, error) {
	if task == nil || strings.TrimSpace(task.TmuxSession) == "" {
		return core.RuntimeSnapshot{}, nil
	}
	if ctx != nil {
		select {
		case <-ctx.Done():
			return core.RuntimeSnapshot{}, ctx.Err()
		default:
		}
	}

	pipe, err := m.pipeForSession(task.TmuxSession)
	if err != nil {
		return core.RuntimeSnapshot{}, err
	}

	paneID, command, err := m.resolvePaneBinding(task, pipe)
	if err != nil || strings.TrimSpace(paneID) == "" {
		return core.RuntimeSnapshot{}, err
	}

	content, err := pipe.SendCommand(fmt.Sprintf("capture-pane -t %s -p -e", paneID))
	if err != nil {
		return core.RuntimeSnapshot{}, err
	}

	return core.RuntimeSnapshot{
		SessionName:       task.TmuxSession,
		WindowName:        windowOrDefault(task.AgentWindowName, "agent"),
		PaneID:            paneID,
		ForegroundCommand: command,
		Content:           content,
		ObservedAt:        m.now().UTC(),
		LastOutputAt:      pipe.LastOutputAt().UTC(),
	}, nil
}

func (m *RuntimeMonitor) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var err error
	for session, pipe := range m.pipes {
		if closeErr := pipe.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
		delete(m.pipes, session)
		delete(m.boundPanes, session)
	}

	return err
}

func (m *RuntimeMonitor) pipeForSession(session string) (controlPipe, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if pipe, ok := m.pipes[session]; ok {
		return pipe, nil
	}
	if m.factory == nil {
		return nil, fmt.Errorf("tmux runtime monitor factory is nil")
	}

	pipe, err := m.factory.Attach(session)
	if err != nil {
		return nil, err
	}
	m.pipes[session] = pipe
	return pipe, nil
}

func (m *RuntimeMonitor) resolvePaneBinding(task *core.Task, pipe controlPipe) (string, string, error) {
	sessionKey := task.TmuxSession
	windowName := windowOrDefault(task.AgentWindowName, "agent")
	listCommand := fmt.Sprintf(
		"list-panes -t %s:%s -F #{pane_id}\t#{pane_current_command}",
		exactSessionTarget(task.TmuxSession),
		windowName,
	)

	m.mu.Lock()
	bound := m.boundPanes[sessionKey]
	m.mu.Unlock()
	if strings.TrimSpace(bound) != "" {
		output, err := pipe.SendCommand(listCommand)
		if err != nil {
			return "", "", err
		}
		if paneID, command, ok := findPane(output, bound); ok {
			return paneID, command, nil
		}

		m.mu.Lock()
		delete(m.boundPanes, sessionKey)
		m.mu.Unlock()
	}

	output, err := pipe.SendCommand(listCommand)
	if err != nil {
		return "", "", err
	}

	panes, codexPanes := parsePanes(output)
	switch {
	case len(codexPanes) == 1:
		m.mu.Lock()
		m.boundPanes[sessionKey] = codexPanes[0].id
		m.mu.Unlock()
		return codexPanes[0].id, codexPanes[0].command, nil
	case len(panes) == 1:
		m.mu.Lock()
		m.boundPanes[sessionKey] = panes[0].id
		m.mu.Unlock()
		return panes[0].id, panes[0].command, nil
	default:
		return "", "", nil
	}
}

type paneInfo struct {
	id      string
	command string
}

func parsePanes(output string) ([]paneInfo, []paneInfo) {
	var panes []paneInfo
	var codexPanes []paneInfo

	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		info := paneInfo{
			id:      strings.TrimSpace(parts[0]),
			command: normalizeForegroundCommand(parts[1]),
		}
		panes = append(panes, info)
		if info.command == "codex" {
			codexPanes = append(codexPanes, info)
		}
	}

	return panes, codexPanes
}

func findPane(output, paneID string) (string, string, bool) {
	for _, pane := range strings.Split(strings.TrimSpace(output), "\n") {
		if strings.TrimSpace(pane) == "" {
			continue
		}
		parts := strings.SplitN(pane, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		id := strings.TrimSpace(parts[0])
		if id != paneID {
			continue
		}
		return id, normalizeForegroundCommand(parts[1]), true
	}

	return "", "", false
}

func normalizeForegroundCommand(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	command = filepath.Base(command)
	return strings.ToLower(command)
}
