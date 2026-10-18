package tui

import (
	"github.com/BaronBonet/rig/internal/core"

	tea "charm.land/bubbletea/v2"
)

// NewProgram creates the daemon-backed task TUI program backed by the task
// frontend. It backs the bare `rig` command. In a tmux popup, inPopup, the
// program quits after a jump.
func NewProgram(
	frontend core.TaskFrontend,
	launchCwd string,
	buildVersion string,
	inPopup bool,
	opts ...tea.ProgramOption,
) *tea.Program {
	m := newModel(frontend, launchCwd, buildVersion)
	m.inPopup = inPopup
	return tea.NewProgram(m, opts...)
}

// NewSetupProgram creates a TUI program that runs only the setup steps, the
// provider setup and the repo directories, and exits once setup finishes. It
// backs the `rig setup` command, run from launchCwd, which setup suggests a
// repo directory for.
func NewSetupProgram(
	frontend core.TaskFrontend,
	launchCwd string,
	buildVersion string,
	opts ...tea.ProgramOption,
) *tea.Program {
	m := newModel(frontend, launchCwd, buildVersion)
	m.setupOnly = true
	return tea.NewProgram(m, opts...)
}
