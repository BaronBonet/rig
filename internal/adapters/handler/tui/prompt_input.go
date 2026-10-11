package tui

import (
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const (
	promptInputMinHeight = 1
	promptInputMaxHeight = 6
	// repoDirsInputMaxLines is how many lines of directories the repo
	// directories step holds. Its box still shows promptInputMaxHeight lines
	// at a time and scrolls through the rest.
	repoDirsInputMaxLines = 200
)

func newPromptInput() textarea.Model {
	return newTextBoxInput("Describe the task to create...")
}

// newRepoDirsInput returns the repo directories step's input, one directory
// per line. Enter saves there, so a new line takes ctrl+j, or shift+enter or
// alt+enter in a terminal that tells those apart from enter.
func newRepoDirsInput() textarea.Model {
	input := newTextBoxInput("~/code")
	input.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("ctrl+j", "shift+enter", "alt+enter"))
	// Without a content limit, the input would stop taking new lines once
	// they fill its box.
	input.MaxContentHeight = repoDirsInputMaxLines
	return input
}

// newTextBoxInput returns a text input styled for the box a screen draws it
// in, like the task prompt.
func newTextBoxInput(placeholder string) textarea.Model {
	input := textarea.New()
	input.ShowLineNumbers = false
	input.Prompt = "┃ "
	input.Placeholder = placeholder
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

	styles.Blurred = styles.Focused

	styles.Cursor.Color = colorAccent
	styles.Cursor.Shape = tea.CursorBar
	styles.Cursor.Blink = true
	styles.Cursor.BlinkSpeed = 530 * time.Millisecond

	input.SetStyles(styles)
	return input
}
