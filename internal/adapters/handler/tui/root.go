package tui

import (
	"rig/internal/core"

	tea "charm.land/bubbletea/v2"
)

// NewProgram creates the daemon-backed task TUI program backed by the task frontend.
func NewProgram(frontend core.TaskFrontend, launchCwd string, opts ...tea.ProgramOption) *tea.Program {
	return tea.NewProgram(newModelWithLaunchCwd(frontend, launchCwd), opts...)
}
