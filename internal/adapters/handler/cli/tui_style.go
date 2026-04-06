package cli

import "charm.land/lipgloss/v2"

// Colors
var (
	colorPrimary = lipgloss.Color("#c8c8d4")
	colorDimmed  = lipgloss.Color("#7b7b8e")
	colorAccent  = lipgloss.Color("#6c6ce0")
	colorHealthy = lipgloss.Color("#5a9e6f")
	colorWarning = lipgloss.Color("#c4a24e")
	colorError   = lipgloss.Color("#c05050")
)

// Icons
const (
	iconStatusActive   = "●"
	iconStatusIdle     = "○"
	iconStatusProgress = "◐"

	iconHeaderList    = "◈"
	iconHeaderCreate  = "✦"
	iconHeaderCleanup = "⚠"

	iconSelected = "▸"

	iconProviderCodex  = "⚡"
	iconProviderClaude = "✦"
)

// Styles
var (
	titleStyle = lipgloss.NewStyle().
			Foreground(colorAccent).
			Bold(true)

	dimStyle = lipgloss.NewStyle().
			Foreground(colorDimmed)

	primaryStyle = lipgloss.NewStyle().
			Foreground(colorPrimary)

	errorStyle = lipgloss.NewStyle().
			Foreground(colorError)

	warningStyle = lipgloss.NewStyle().
			Foreground(colorWarning)

	healthyStyle = lipgloss.NewStyle().
			Foreground(colorHealthy)

	selectedRowStyle = lipgloss.NewStyle().
				BorderLeft(true).
				BorderStyle(lipgloss.ThickBorder()).
				BorderForeground(colorAccent).
				PaddingLeft(1).
				Bold(true).
				Foreground(colorPrimary)

	normalRowStyle = lipgloss.NewStyle().
			PaddingLeft(3).
			Foreground(colorDimmed)
)

// statusStyle returns the icon and style for a given task status.
func statusStyle(status string) (string, lipgloss.Style) {
	switch status {
	case "running":
		return iconStatusActive, healthyStyle
	case "creating":
		return iconStatusProgress, warningStyle
	case "degraded":
		return iconStatusProgress, warningStyle
	case "broken":
		return iconStatusActive, errorStyle
	default:
		return iconStatusIdle, dimStyle
	}
}

// runtimeStateStyle returns the icon and style for a task runtime state badge.
func runtimeStateStyle(state string) (string, lipgloss.Style) {
	switch state {
	case "running":
		return iconStatusActive, healthyStyle
	case "needs_input":
		return iconStatusProgress, warningStyle
	case "finished":
		return iconStatusIdle, dimStyle
	default:
		return "", dimStyle
	}
}
