package cli

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"

	"rig/internal/core"
)

// Colors
var (
	colorPrimary    = lipgloss.Color("#b8bcc8")
	colorDimmed     = lipgloss.Color("#5a5e70")
	colorMuted      = lipgloss.Color("#3d4050")
	colorDivider    = lipgloss.Color("#2a2d3a")
	colorAccent     = lipgloss.Color("#7c8af6")
	colorRepoHeader = lipgloss.Color("#5867d8")
	colorHealthy    = lipgloss.Color("#4aba7a")
	colorWarning    = lipgloss.Color("#c4a24e")
	colorError      = lipgloss.Color("#c05050")
	colorClaude     = lipgloss.Color("#d4956a")
	colorCodex      = lipgloss.Color("#5ac4a0")
	colorPRMerged   = lipgloss.Color("#9b7ce8")
	colorUserPrompt = lipgloss.Color("#7c8af6")
	colorLLMReply   = lipgloss.Color("#4aba7a")
)

// Icons
const (
	// Task status
	iconStatusActive   = "●"
	iconStatusIdle     = "○"
	iconStatusProgress = "◐"

	// PR status (GitHub-inspired, distinct from task status circles)
	iconPROpen   = "⊙"
	iconPRDraft  = "⊘"
	iconPRMerged = "⊕"
	iconPRClosed = "⊗"
	iconPRNone   = "—"

	// Activity feed
	iconUserPrompt = "▸"
	iconLLMReply   = "◂"

	// Progress
	iconCheckmark = "✔"
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

	claudeStyle = lipgloss.NewStyle().
			Foreground(colorClaude)

	codexStyle = lipgloss.NewStyle().
			Foreground(colorCodex)

	selectedRowStyle = lipgloss.NewStyle().
				BorderLeft(true).
				BorderStyle(lipgloss.Border{Left: "│"}).
				BorderForeground(colorAccent).
				PaddingLeft(2)

	normalRowStyle = lipgloss.NewStyle().
			PaddingLeft(3)

	keybindStyle = lipgloss.NewStyle().
			Foreground(colorAccent)

	mutedStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	dividerStyle = lipgloss.NewStyle().
			Foreground(colorDivider)

	prMergedStyle = lipgloss.NewStyle().
			Foreground(colorPRMerged)

	headerLabelStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#8b8fa3")).
				Bold(true)

	repoHeaderStyle = lipgloss.NewStyle().
			Foreground(colorRepoHeader).
			Bold(true)

	repoHeaderRowStyle = lipgloss.NewStyle().
				PaddingLeft(2)
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

func displayStateStyle(status string, activity string) (string, lipgloss.Style) {
	switch status {
	case "working":
		if activity == "command" {
			return iconStatusProgress, healthyStyle
		}
		return iconStatusActive, healthyStyle
	case "needs_input":
		return iconStatusProgress, warningStyle
	case "finished":
		return iconStatusIdle, dimStyle
	case "disconnected":
		return iconStatusIdle, dimStyle
	default:
		return "", dimStyle
	}
}

func prStateIconStyle(state core.PRState) (string, lipgloss.Style) {
	switch state {
	case core.PRStateOpen:
		return iconPROpen, healthyStyle
	case core.PRStateDraft:
		return iconPRDraft, warningStyle
	case core.PRStateMerged:
		return iconPRMerged, prMergedStyle
	case core.PRStateClosed:
		return iconPRClosed, errorStyle
	default:
		return iconPRNone, mutedStyle
	}
}

// shimmerWidth is the number of characters in the bright "wave" of the shimmer.
const shimmerWidth = 4

// renderShimmer renders text with a left-to-right shimmer highlight.
// Most characters use colorDimmed; a window of shimmerWidth characters near
// the tick position interpolates toward colorPrimary.
func renderShimmer(text string, tick int) string {
	runes := []rune(text)
	if len(runes) == 0 {
		return ""
	}

	// Wrap tick so the shimmer cycles continuously.
	cycle := len(runes) + shimmerWidth + 2
	pos := tick % cycle

	var b strings.Builder
	for i, r := range runes {
		dist := pos - i
		if dist >= 0 && dist < shimmerWidth {
			intensity := 1.0 - float64(dist)/float64(shimmerWidth)
			col := lerpColor(colorDimmed, colorPrimary, intensity)
			b.WriteString(lipgloss.NewStyle().Foreground(col).Render(string(r)))
		} else {
			b.WriteString(dimStyle.Render(string(r)))
		}
	}
	return b.String()
}

// lerpColor linearly interpolates between two colors.
func lerpColor(from, to color.Color, t float64) color.Color {
	fr, fg, fb, _ := from.RGBA()
	tr, tg, tb, _ := to.RGBA()
	// RGBA returns pre-multiplied values in [0, 0xffff]; shift to [0, 255].
	r := uint8(float64(fr>>8) + float64(int(tr>>8)-int(fr>>8))*t)
	g := uint8(float64(fg>>8) + float64(int(tg>>8)-int(fg>>8))*t)
	b := uint8(float64(fb>>8) + float64(int(tb>>8)-int(fb>>8))*t)
	return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", r, g, b))
}
