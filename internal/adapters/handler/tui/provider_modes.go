package tui

import (
	"errors"

	"github.com/BaronBonet/rig/internal/core"

	tea "charm.land/bubbletea/v2"
)

// enterProviderSetupMode opens the mandatory provider setup screen and kicks
// off provider detection.
func (m model) enterProviderSetupMode() (tea.Model, tea.Cmd) {
	m.mode = modeProviderSetup
	m.setupRows = nil
	m.setupSelected = 0
	m.setupDetecting = true
	m.setupSaving = false
	return m, detectProvidersCmd(m.statusContext, m.frontend)
}

// applyProviderDetections builds the setup rows, preserving the user's
// existing configured providers and default when setup is rerun.
func (m *model) applyProviderDetections(detections []core.ProviderDetection) {
	rows := make([]providerSetupRow, 0, len(detections))
	for _, detection := range detections {
		enabled := false
		if m.providerSetup != nil {
			enabled = m.providerSetup.IsConfigured(detection.Provider)
		} else {
			enabled = detection.Ready
		}
		rows = append(rows, providerSetupRow{
			provider: detection.Provider,
			ready:    detection.Ready,
			detail:   detection.Detail,
			enabled:  enabled && detection.Ready,
		})
	}
	m.setupRows = rows

	m.setupDefault = ""
	if m.providerSetup != nil {
		m.setupDefault = m.providerSetup.Default
	}
	m.normalizeSetupDefault()
}

// normalizeSetupDefault keeps the chosen default provider pointing at an
// enabled provider, falling back to the first enabled one.
func (m *model) normalizeSetupDefault() {
	var firstEnabled core.Provider
	for _, row := range m.setupRows {
		if !row.enabled {
			continue
		}
		if firstEnabled == "" {
			firstEnabled = row.provider
		}
		if row.provider == m.setupDefault {
			return
		}
	}
	m.setupDefault = firstEnabled
}

func (m model) updateProviderSetup(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.setupSaving {
		return m, nil
	}

	switch msg.String() {
	case "q", "esc":
		// Provider setup is mandatory: without a valid setup the only way out
		// is quitting rig entirely.
		if m.providerSetup == nil || m.setupOnly {
			if m.cancelStatus != nil {
				m.cancelStatus()
			}
			return m, tea.Quit
		}
		m.mode = modeBrowse
		return m, nil
	case "j", "down":
		if m.setupSelected < len(m.setupRows)-1 {
			m.setupSelected++
		}
		return m, nil
	case "k", "up":
		if m.setupSelected > 0 {
			m.setupSelected--
		}
		return m, nil
	case " ", "space":
		if m.setupSelected >= 0 && m.setupSelected < len(m.setupRows) {
			row := &m.setupRows[m.setupSelected]
			if row.ready {
				row.enabled = !row.enabled
				m.setupErr = nil
				m.normalizeSetupDefault()
			}
		}
		return m, nil
	case "d":
		if m.setupSelected >= 0 && m.setupSelected < len(m.setupRows) {
			row := m.setupRows[m.setupSelected]
			if row.enabled {
				m.setupDefault = row.provider
				m.setupErr = nil
			}
		}
		return m, nil
	case "enter":
		setup := m.setupSelection()
		if len(setup.Configured) == 0 {
			m.setupErr = errors.New("enable at least one provider to use rig")
			return m, nil
		}
		m.setupSaving = true
		m.setupErr = nil
		return m, saveProviderSetupCmd(m.statusContext, m.frontend, setup)
	default:
		return m, nil
	}
}

// setupSelection converts the setup rows into the provider setup to persist.
func (m model) setupSelection() core.ProviderSetup {
	setup := core.ProviderSetup{Default: m.setupDefault}
	for _, row := range m.setupRows {
		if row.enabled {
			setup.Configured = append(setup.Configured, row.provider)
		}
	}
	if setup.Default == "" && len(setup.Configured) > 0 {
		setup.Default = setup.Configured[0]
	}
	return setup
}

// enterSwitchProviderMode opens the provider switch picker for the selected
// task, listing only configured providers other than the active one.
func (m model) enterSwitchProviderMode() (tea.Model, tea.Cmd) {
	if m.createPending || m.deletePending || m.switchPending {
		return m, nil
	}
	row := m.selectedRow()
	if row == nil || row.task == nil {
		return m, nil
	}

	options := make([]core.Provider, 0, 1)
	for _, provider := range m.configuredProviders() {
		if provider != row.task.Provider {
			options = append(options, provider)
		}
	}
	if len(options) == 0 {
		m.err = errors.New("no configured alternative provider: run rig setup to enable another provider")
		return m, nil
	}

	m.err = nil
	m.mode = modeSwitchProvider
	m.switchOptions = options
	m.switchSelected = 0
	return m, nil
}

func (m model) updateSwitchProvider(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.switchPending {
		return m, nil
	}

	switch msg.String() {
	case "q", "esc":
		m.mode = modeBrowse
		return m, nil
	case "j", "down":
		if m.switchSelected < len(m.switchOptions)-1 {
			m.switchSelected++
		}
		return m, nil
	case "k", "up":
		if m.switchSelected > 0 {
			m.switchSelected--
		}
		return m, nil
	case "enter":
		row := m.selectedRow()
		if row == nil || row.task == nil ||
			m.switchSelected < 0 || m.switchSelected >= len(m.switchOptions) {
			m.mode = modeBrowse
			return m, nil
		}
		m.switchPending = true
		m.shimmerTick = 0
		return m, tea.Batch(
			switchTaskProviderCmd(m.statusContext, m.frontend, taskID(row.task), m.switchOptions[m.switchSelected]),
			shimmerTickCmd(),
		)
	default:
		return m, nil
	}
}
