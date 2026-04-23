package tui

import (
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const (
	promptInputMinHeight = 1
	promptInputMaxHeight = 6
)

func newPromptInput() textarea.Model {
	input := textarea.New()
	input.ShowLineNumbers = false
	input.Prompt = "┃ "
	input.Placeholder = "Describe the task to create..."
	input.DynamicHeight = true
	input.MinHeight = promptInputMinHeight
	input.MaxHeight = promptInputMaxHeight
	input.SetHeight(3)

	styles := textarea.DefaultDarkStyles()
	styles.Focused.Base = lipgloss.NewStyle()
	styles.Focused.Text = lipgloss.NewStyle().Foreground(colorPrimary)
	styles.Focused.Placeholder = lipgloss.NewStyle().Foreground(colorDimmed)
	styles.Focused.Prompt = lipgloss.NewStyle().Foreground(colorAccent)
	styles.Focused.CursorLine = lipgloss.NewStyle()
	styles.Focused.CursorLineNumber = lipgloss.NewStyle()
	styles.Focused.EndOfBuffer = lipgloss.NewStyle()

	styles.Blurred.Base = lipgloss.NewStyle()
	styles.Blurred.Text = lipgloss.NewStyle().Foreground(colorPrimary)
	styles.Blurred.Placeholder = lipgloss.NewStyle().Foreground(colorDimmed)
	styles.Blurred.Prompt = lipgloss.NewStyle().Foreground(colorAccent)
	styles.Blurred.CursorLine = lipgloss.NewStyle()
	styles.Blurred.CursorLineNumber = lipgloss.NewStyle()
	styles.Blurred.EndOfBuffer = lipgloss.NewStyle()

	styles.Cursor.Color = colorAccent
	styles.Cursor.Shape = tea.CursorBar
	styles.Cursor.Blink = true
	styles.Cursor.BlinkSpeed = 530 * time.Millisecond

	input.SetStyles(styles)
	return input
}
