package tui

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

var (
	colorPrimary = lipgloss.Color("#b8bcc8")
	colorDimmed  = lipgloss.Color("#5a5e70")
	colorMuted   = lipgloss.Color("#3d4050")
	colorDivider = lipgloss.Color("#2a2d3a")
	colorAccent  = lipgloss.Color("#7c8af6")
	colorHealthy = lipgloss.Color("#4aba7a")
	colorWarning = lipgloss.Color("#c4a24e")
	colorError   = lipgloss.Color("#c05050")
	colorClaude  = lipgloss.Color("#d4956a")
	colorCodex   = lipgloss.Color("#5ac4a0")
)

const (
	iconStatusActive   = "●"
	iconStatusIdle     = "○"
	iconStatusProgress = "◐"
	iconPRNone         = "—"
)

var (
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

	headerLabelStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#8b8fa3")).
				Bold(true)
)

func providerStyle(provider string) lipgloss.Style {
	switch provider {
	case "claude":
		return claudeStyle
	default:
		return codexStyle
	}
}

const shimmerWidth = 4

func renderShimmer(text string, tick int) string {
	runes := []rune(text)
	if len(runes) == 0 {
		return ""
	}

	cycle := len(runes) + shimmerWidth + 2
	pos := tick % cycle

	var builder strings.Builder
	for i, r := range runes {
		dist := pos - i
		if dist >= 0 && dist < shimmerWidth {
			intensity := 1.0 - float64(dist)/float64(shimmerWidth)
			col := lerpColor(colorDimmed, colorPrimary, intensity)
			builder.WriteString(lipgloss.NewStyle().Foreground(col).Render(string(r)))
			continue
		}
		builder.WriteString(dimStyle.Render(string(r)))
	}

	return builder.String()
}

func lerpColor(from, to color.Color, t float64) color.Color {
	fr, fg, fb, _ := from.RGBA()
	tr, tg, tb, _ := to.RGBA()

	r := uint8(float64(fr>>8) + float64(int(tr>>8)-int(fr>>8))*t)
	g := uint8(float64(fg>>8) + float64(int(tg>>8)-int(fg>>8))*t)
	b := uint8(float64(fb>>8) + float64(int(tb>>8)-int(fb>>8))*t)

	return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", r, g, b))
}
