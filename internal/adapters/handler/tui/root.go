package tui

import (
	"rig/internal/core"

	tea "charm.land/bubbletea/v2"
)

// NewProgram creates the daemon-backed task TUI program backed by the task frontend.
func NewProgram(frontend core.TaskFrontend, opts ...tea.ProgramOption) *tea.Program {
	return tea.NewProgram(newModel(frontend), opts...)
}
