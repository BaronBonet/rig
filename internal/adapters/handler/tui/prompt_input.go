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
	// promptInputMaxContentHeight lets a prompt run past the box, which then
	// scrolls; left at 0, the textarea stops new lines at MaxHeight.
	promptInputMaxContentHeight = 10000
)

func newPromptInput() textarea.Model {
	input := textarea.New()
	input.ShowLineNumbers = false
	input.Prompt = "┃ "
	input.Placeholder = "Describe the task to create..."
	input.DynamicHeight = true
	input.MinHeight = promptInputMinHeight
	input.MaxHeight = promptInputMaxHeight
	input.MaxContentHeight = promptInputMaxContentHeight
	input.SetHeight(3)
	// Enter submits, so a modified enter breaks the line. Shift+enter only
	// arrives inside tmux with extended-keys on; alt+enter always does.
	input.KeyMap.InsertNewline.SetKeys("alt+enter", "shift+enter")

	styles := textarea.DefaultDarkStyles()
	styles.Focused.Base = lipgloss.NewStyle()
	styles.Focused.Text = lipgloss.NewStyle().Foreground(colorPrimary)
	styles.Focused.Placeholder = lipgloss.NewStyle().Foreground(colorDimmed)
	styles.Focused.Prompt = lipgloss.NewStyle().Foreground(colorAccent)
	styles.Focused.CursorLine = lipgloss.NewStyle()
	styles.Focused.CursorLineNumber = lipgloss.NewStyle()
	styles.Focused.EndOfBuffer = lipgloss.NewStyle()

	styles.Blurred = styles.Focused

	styles.Cursor.Color = colorAccent
	styles.Cursor.Shape = tea.CursorBar
	styles.Cursor.Blink = true
	styles.Cursor.BlinkSpeed = 530 * time.Millisecond

	input.SetStyles(styles)
	return input
}
