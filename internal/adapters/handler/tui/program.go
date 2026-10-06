package tui

import (
	"github.com/BaronBonet/rig/internal/core"

	tea "charm.land/bubbletea/v2"
)

// NewProgram creates the daemon-backed task TUI program backed by the task
// frontend. It backs the bare `rig` command. providerEnv is the provider
// configuration of the terminal rig runs in, which its tasks inherit.
func NewProgram(
	frontend core.TaskFrontend,
	launchCwd string,
	buildVersion string,
	providerEnv core.ProviderEnv,
	opts ...tea.ProgramOption,
) *tea.Program {
	m := newModel(frontend, launchCwd, buildVersion)
	m.providerEnv = providerEnv
	return tea.NewProgram(m, opts...)
}

// NewSetupProgram creates a TUI program that runs only the provider setup
// flow and exits once setup is saved. It backs the `rig setup` command.
func NewSetupProgram(
	frontend core.TaskFrontend,
	buildVersion string,
	opts ...tea.ProgramOption,
) *tea.Program {
	m := newModel(frontend, "", buildVersion)
	m.setupOnly = true
	return tea.NewProgram(m, opts...)
}
