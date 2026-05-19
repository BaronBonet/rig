package tui

import (
	"github.com/BaronBonet/rig/internal/core"

	tea "charm.land/bubbletea/v2"
)

// NewProgram creates the daemon-backed task TUI program backed by the task frontend.
func NewProgram(frontend core.TaskFrontend, launchCwd string, opts ...tea.ProgramOption) *tea.Program {
	return NewProgramWithVersion(frontend, launchCwd, defaultBuildVersion, opts...)
}

// NewProgramWithVersion creates the daemon-backed task TUI program with a visible CLI build version.
func NewProgramWithVersion(
	frontend core.TaskFrontend,
	launchCwd string,
	buildVersion string,
	opts ...tea.ProgramOption,
) *tea.Program {
	return tea.NewProgram(newModelWithLaunchCwdAndVersion(frontend, launchCwd, buildVersion), opts...)
}
