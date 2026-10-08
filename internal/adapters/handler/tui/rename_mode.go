package tui

import (
	"errors"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/BaronBonet/rig/internal/core"
)

// renameState is the rename screen's working state: the task being renamed
// and the name typed for it.
type renameState struct {
	task  *core.Task
	input textinput.Model
}

// taskRenamedMsg reports a rename the daemon made, or refused.
type taskRenamedMsg struct {
	err error
}

func newRenameInput(name string, width int) textinput.Model {
	input := textinput.New()
	input.Prompt = "┃ "
	input.Placeholder = "New name for the task..."

	styles := textinput.DefaultDarkStyles()
	styles.Focused.Text = lipgloss.NewStyle().Foreground(colorPrimary)
	styles.Focused.Placeholder = lipgloss.NewStyle().Foreground(colorDimmed)
	styles.Focused.Prompt = lipgloss.NewStyle().Foreground(colorAccent)
	styles.Blurred = styles.Focused
	styles.Cursor.Color = colorAccent
	input.SetStyles(styles)

	if width > 0 {
		input.SetWidth(width)
	}
	input.SetValue(name)
	input.CursorEnd()
	return input
}

// enterRenameMode opens the rename screen for the selected task, with its
// current name ready to edit.
func (m model) enterRenameMode() (tea.Model, tea.Cmd) {
	if m.pending != opNone {
		return m, nil
	}
	row := m.selectedRow()
	if row == nil || row.task == nil {
		return m, nil
	}
	if !m.transition(modeRenameTask) {
		return m, nil
	}
	m.err = nil
	m.rename = renameState{task: row.task, input: newRenameInput(row.task.DisplayName, m.totalWidth()-4)}
	return m, m.rename.input.Focus()
}

func (m model) updateRename(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.pending != opNone {
		return m, nil
	}
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "esc":
			return m.handleBack()
		case "enter":
			return m.submitRename()
		}
	}
	m.err = nil
	var cmd tea.Cmd
	m.rename.input, cmd = m.rename.input.Update(msg)
	return m, cmd
}

func (m model) submitRename() (tea.Model, tea.Cmd) {
	name := strings.TrimSpace(m.rename.input.Value())
	if name == "" {
		m.err = errors.New("a task needs a name")
		return m, nil
	}
	if m.rename.task == nil || name == m.rename.task.DisplayName {
		m.transition(modeBrowse)
		return m, nil
	}
	m.beginOp(opRenaming)
	return m, tea.Batch(
		renameTaskCmd(m.statusContext, m.frontend, taskID(m.rename.task), name),
		shimmerTickCmd(),
	)
}

func (m model) renameTaskView() string {
	var builder strings.Builder
	builder.WriteString(m.screenHeader(mutedStyle.Render("rename task")) + "\n")

	task := m.rename.task
	if task != nil {
		builder.WriteString(mutedStyle.Render("now  ") +
			primaryStyle.Render(emptyFallback(task.DisplayName, task.ID)) + "\n\n")
	}
	if m.pending == opRenaming {
		builder.WriteString(renderShimmer("Renaming...", m.shimmerTick) + "\n")
		return builder.String()
	}

	builder.WriteString(m.rename.input.View() + "\n\n")
	builder.WriteString(errorBlock(m.err))
	if task != nil && strings.TrimSpace(task.Prompt) != "" {
		builder.WriteString(dimStyle.Render(
			"Renaming drops the task's original ask, so new sessions are not briefed on it.",
		) + "\n\n")
	}
	builder.WriteString(footerKeybinds(
		[2]string{"enter", "rename"},
		[2]string{"esc", "cancel"},
	))
	return builder.String()
}
