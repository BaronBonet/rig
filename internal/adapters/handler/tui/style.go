package tui

import "charm.land/lipgloss/v2"

var (
	colorPrimary = lipgloss.Color("#c8d0da")
	colorMuted   = lipgloss.Color("#6b7280")
	colorAccent  = lipgloss.Color("#7dd3a7")
	colorError   = lipgloss.Color("#f87171")
)

var (
	titleStyle = lipgloss.NewStyle().
			Foreground(colorAccent).
			Bold(true)

	primaryStyle = lipgloss.NewStyle().
			Foreground(colorPrimary)

	mutedStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	errorStyle = lipgloss.NewStyle().
			Foreground(colorError)

	selectedRowStyle = lipgloss.NewStyle().
				BorderLeft(true).
				BorderForeground(colorAccent).
				BorderStyle(lipgloss.Border{Left: "│"}).
				PaddingLeft(1)

	promptBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorAccent).
			Padding(0, 1)

	rowStyle = lipgloss.NewStyle().
			PaddingLeft(2)
)
