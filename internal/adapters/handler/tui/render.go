package tui

import (
	"fmt"
	"strings"

	"rig/internal/core"

	"charm.land/lipgloss/v2"
)

func (m model) listView() string {
	var sections []string
	sections = append(sections, titleStyle.Render("Tasks"))

	switch {
	case m.err != nil:
		sections = append(sections, errorStyle.Render(m.err.Error()))
	case m.loading:
		sections = append(sections, mutedStyle.Render("Loading tasks..."))
	case len(m.rows) == 0:
		sections = append(sections, mutedStyle.Render("No tasks found."))
	default:
		rows := make([]string, 0, len(m.rows))
		for i, row := range m.rows {
			rows = append(rows, m.renderRow(i, row))
		}
		sections = append(sections, lipgloss.JoinVertical(lipgloss.Left, rows...))
	}

	if m.mode == modePromptInput {
		sections = append(sections, m.promptInputView())
		if m.createErr != nil {
			sections = append(sections, errorStyle.Render(m.createErr.Error()))
		}
		sections = append(sections, mutedStyle.Render(m.promptHelpText()))
	} else {
		sections = append(sections, mutedStyle.Render("a: new  j/k: move  q: quit"))
	}
	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

func (m model) renderRow(index int, row taskRow) string {
	if row.task == nil {
		return ""
	}

	title := row.task.DisplayName
	if strings.TrimSpace(title) == "" {
		title = row.task.ID
	}

	meta := []string{
		emptyFallback(row.task.RepoName, "unknown repo"),
		string(row.task.Provider),
	}
	if phase := phaseText(row.status); phase != "" {
		meta = append(meta, phase)
	}

	body := lipgloss.JoinVertical(
		lipgloss.Left,
		primaryStyle.Render(title),
		mutedStyle.Render(strings.Join(meta, "  •  ")),
	)
	if index == m.selected {
		return selectedRowStyle.Render(body)
	}
	return rowStyle.Render(body)
}

func phaseText(update *core.TaskStatusUpdate) string {
	if update == nil || strings.TrimSpace(string(update.Phase)) == "" {
		return ""
	}
	return fmt.Sprintf("phase: %s", update.Phase)
}

func emptyFallback(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func (m model) promptInputView() string {
	prompt := m.prompt
	if prompt == "" {
		prompt = mutedStyle.Render("Describe the task...")
	}

	title := "New task prompt"
	if m.createPending {
		title = "Creating task..."
	}

	return promptBoxStyle.Render(lipgloss.JoinVertical(
		lipgloss.Left,
		primaryStyle.Render(title),
		prompt,
	))
}

func (m model) promptHelpText() string {
	if m.createPending {
		return "creating task..."
	}
	return "type prompt  enter: create  esc: cancel"
}
