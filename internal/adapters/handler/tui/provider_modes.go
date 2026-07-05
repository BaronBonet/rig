package tui

import (
	"errors"

	"github.com/BaronBonet/rig/internal/core"

	tea "charm.land/bubbletea/v2"
)

// enterProviderSetupMode opens the mandatory provider setup screen and kicks
// off provider detection.
func (m model) enterProviderSetupMode() (tea.Model, tea.Cmd) {
	if !m.transition(modeProviderSetup) {
		return m, nil
	}
	m.setupForm.rows = nil
	m.setupForm.selected = 0
	m.setupForm.detecting = true
	m.setupForm.saving = false
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
	m.setupForm.rows = rows

	m.setupForm.defaultProvider = ""
	if m.providerSetup != nil {
		m.setupForm.defaultProvider = m.providerSetup.Default
	}
	m.normalizeSetupDefault()
}

// normalizeSetupDefault keeps the chosen default provider pointing at an
// enabled provider, falling back to the first enabled one.
func (m *model) normalizeSetupDefault() {
	var firstEnabled core.Provider
	for _, row := range m.setupForm.rows {
		if !row.enabled {
			continue
		}
		if firstEnabled == "" {
			firstEnabled = row.provider
		}
		if row.provider == m.setupForm.defaultProvider {
			return
		}
	}
	m.setupForm.defaultProvider = firstEnabled
}

func (m model) updateProviderSetup(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.setupForm.saving {
		return m, nil
	}

	switch msg.String() {
	case "q", "esc":
		return m.handleBack()
	case "j", "down":
		m.setupForm.selected = clampIndex(m.setupForm.selected+1, len(m.setupForm.rows))
		return m, nil
	case "k", "up":
		m.setupForm.selected = clampIndex(m.setupForm.selected-1, len(m.setupForm.rows))
		return m, nil
	case " ", "space":
		if m.setupForm.selected >= 0 && m.setupForm.selected < len(m.setupForm.rows) {
			row := &m.setupForm.rows[m.setupForm.selected]
			if row.ready {
				row.enabled = !row.enabled
				m.setupForm.err = nil
				m.normalizeSetupDefault()
			}
		}
		return m, nil
	case "d":
		if m.setupForm.selected >= 0 && m.setupForm.selected < len(m.setupForm.rows) {
			row := m.setupForm.rows[m.setupForm.selected]
			if row.enabled {
				m.setupForm.defaultProvider = row.provider
				m.setupForm.err = nil
			}
		}
		return m, nil
	case "enter":
		setup := m.setupSelection()
		if len(setup.Configured) == 0 {
			m.setupForm.err = errors.New("enable at least one provider to use rig")
			return m, nil
		}
		m.setupForm.saving = true
		m.setupForm.err = nil
		return m, saveProviderSetupCmd(m.statusContext, m.frontend, setup)
	default:
		return m, nil
	}
}

// setupSelection converts the setup rows into the provider setup to persist.
func (m model) setupSelection() core.ProviderSetup {
	setup := core.ProviderSetup{Default: m.setupForm.defaultProvider}
	for _, row := range m.setupForm.rows {
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
	if m.pending != opNone {
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

	if !m.transition(modeSwitchProvider) {
		return m, nil
	}
	m.err = nil
	m.providerSwitch.options = options
	m.providerSwitch.selected = 0
	return m, nil
}

func (m model) updateSwitchProvider(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.pending != opNone {
		return m, nil
	}

	switch msg.String() {
	case "q", "esc":
		return m.handleBack()
	case "j", "down":
		m.providerSwitch.selected = clampIndex(m.providerSwitch.selected+1, len(m.providerSwitch.options))
		return m, nil
	case "k", "up":
		m.providerSwitch.selected = clampIndex(m.providerSwitch.selected-1, len(m.providerSwitch.options))
		return m, nil
	case "enter":
		row := m.selectedRow()
		if row == nil || row.task == nil ||
			m.providerSwitch.selected < 0 || m.providerSwitch.selected >= len(m.providerSwitch.options) {
			m.transition(modeBrowse)
			return m, nil
		}
		m.beginOp(opSwitching)
		target := m.providerSwitch.options[m.providerSwitch.selected]
		return m, tea.Batch(
			switchTaskProviderCmd(m.statusContext, m.frontend, taskID(row.task), target),
			shimmerTickCmd(),
		)
	default:
		return m, nil
	}
}
