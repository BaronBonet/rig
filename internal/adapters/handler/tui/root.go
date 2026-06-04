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
	return NewProgramWithVersionAndProvider(frontend, launchCwd, buildVersion, core.ProviderCodex, opts...)
}

// NewProgramWithVersionAndProvider creates the task TUI with a configured task creation provider.
func NewProgramWithVersionAndProvider(
	frontend core.TaskFrontend,
	launchCwd string,
	buildVersion string,
	createProvider core.Provider,
	opts ...tea.ProgramOption,
) *tea.Program {
	return tea.NewProgram(
		newModelWithLaunchCwdVersionAndProvider(frontend, launchCwd, buildVersion, createProvider),
		opts...,
	)
}
