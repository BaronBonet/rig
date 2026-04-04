package cli

import "github.com/charmbracelet/lipgloss"

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
	iconRepo     = "📁"
	iconAgent    = "🤖"
	iconEditor   = "📝"
	iconBranch   = "🌿"
	iconTmux     = "💻"
	iconWorktree = "🌳"

	iconStatusActive   = "●"
	iconStatusIdle     = "○"
	iconStatusProgress = "◐"

	iconHeaderList    = "◈"
	iconHeaderCreate  = "✦"
	iconHeaderCleanup = "⚠"

	iconSelected = "▸"
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

	separatorStyle = lipgloss.NewStyle().
			Foreground(colorDimmed)

	detailBarStyle = lipgloss.NewStyle().
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

// healthStyle returns the styled string for a boolean health indicator.
func healthStyle(ok bool) string {
	if ok {
		return healthyStyle.Render("healthy")
	}
	return dimStyle.Render("missing")
}

// yesNoStyled returns a styled yes/no string.
func yesNoStyled(ok bool) string {
	if ok {
		return healthyStyle.Render("yes")
	}
	return dimStyle.Render("no")
}
