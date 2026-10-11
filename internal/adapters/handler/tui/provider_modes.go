package tui

import (
	"errors"

	"github.com/BaronBonet/rig/internal/core"

	tea "charm.land/bubbletea/v2"
)

// enterProviderSetupMode opens the mandatory provider setup screen and kicks
// off a numbered provider detection, and a fetch of the Launch defaults the
// screen shows, which may have changed since they loaded.
func (m model) enterProviderSetupMode() (tea.Model, tea.Cmd) {
	if !m.transition(modeProviderSetup) {
		return m, nil
	}
	m.setupForm.rows = nil
	m.setupForm.selected = 0
	m.setupForm.detecting = true
	m.setupForm.saving = false
	m.setupForm.edits = nil
	m.setupDetections++
	return m, tea.Batch(
		detectProvidersCmd(m.statusContext, m.frontend, m.setupDetections),
		m.fetchLaunchDefaults(),
	)
}

// setProviderDetections builds the setup rows from the latest provider
// detection's reply. It drops a reply that is not the latest's, or that
// arrives once the setup screen has closed, which would rebuild rows the user
// may have changed.
func (m *model) setProviderDetections(msg providerDetectionsMsg) tea.Cmd {
	if familyOf(m.mode) != familySetup || msg.detection != m.setupDetections {
		return nil
	}
	m.setupForm.detecting = false
	if msg.err != nil {
		m.setupForm.err = msg.err
		return nil
	}
	m.applyProviderDetections(msg.detections)
	return tea.Batch(m.setupModelListCmds()...)
}

// setupModelListCmds fetches the model list of each ready provider on the
// setup screen that has none yet: never fetched, failed, or empty. It fetches
// them whether or not the provider is configured, as before the first
// provider setup none is.
func (m *model) setupModelListCmds() []tea.Cmd {
	var cmds []tea.Cmd
	for _, row := range m.setupForm.rows {
		state, fetched := m.providerModels[row.provider]
		if row.ready && (!fetched || state.retryable()) {
			cmds = append(cmds, m.fetchProviderModels(row.provider))
		}
	}
	return cmds
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
	case "ctrl+o":
		m.cycleSetupLaunchChoice(model.nextLaunchModel)
		return m, nil
	case "ctrl+r":
		m.cycleSetupLaunchChoice(model.nextLaunchReasoningLevel)
		return m, nil
	case "enter":
		setup := m.setupSelection()
		if len(setup.Configured) == 0 {
			m.setupForm.err = errors.New("enable at least one provider to use rig")
			return m, nil
		}
		m.setupForm.saving = true
		m.setupForm.err = nil
		return m, saveProviderSetupCmd(m.statusContext, m.frontend, setup, m.setupLaunchDefaultEdits())
	default:
		return m, nil
	}
}

// selectedSetupRow is the setup row under the cursor, if there is one.
func (m model) selectedSetupRow() (providerSetupRow, bool) {
	if m.setupForm.selected < 0 || m.setupForm.selected >= len(m.setupForm.rows) {
		return providerSetupRow{}, false
	}
	return m.setupForm.rows[m.setupForm.selected], true
}

// setupLaunchChoice is the Launch default the setup screen shows and saves
// for provider: as the user edited it, else as fetched.
func (m model) setupLaunchChoice(provider core.Provider) core.LaunchChoice {
	choice, edited := m.setupForm.edits[provider]
	if !edited {
		choice = m.launchDefaults.choices[provider]
	}
	return m.normalizedLaunchChoice(provider, choice)
}

// stepSetupLaunchChoice is the selected row's Launch default moved on by
// step, as the task draft's keys move its Launch choice, and whether the key
// does anything for it. It does nothing for a row that shows no Launch
// default, nor for one the user has not edited while the Launch defaults
// load: an edit would start from no choice and be saved over the stored one.
func (m model) stepSetupLaunchChoice(step launchChoiceStep) (core.Provider, core.LaunchChoice, bool) {
	row, ok := m.selectedSetupRow()
	if !ok || !row.showsLaunchDefault() {
		return "", core.LaunchChoice{}, false
	}
	_, edited := m.setupForm.edits[row.provider]
	if !edited && !m.launchDefaults.loaded && m.launchDefaults.err == nil {
		return "", core.LaunchChoice{}, false
	}
	choice, ok := step(m, row.provider, m.setupLaunchChoice(row.provider))
	return row.provider, choice, ok
}

// cycleSetupLaunchChoice edits the selected row's Launch default by step when
// the key does anything for it.
func (m *model) cycleSetupLaunchChoice(step launchChoiceStep) {
	provider, choice, ok := m.stepSetupLaunchChoice(step)
	if !ok {
		return
	}
	if m.setupForm.edits == nil {
		m.setupForm.edits = make(map[core.Provider]core.LaunchChoice)
	}
	m.setupForm.edits[provider] = choice
	m.setupForm.err = nil
}

// setupLaunchDefaultEdits is the Launch default of each provider the user
// edited and left enabled, in the rows' order. The rest are not saved.
func (m model) setupLaunchDefaultEdits() []launchDefaultEdit {
	var edits []launchDefaultEdit
	for _, row := range m.setupForm.rows {
		if _, edited := m.setupForm.edits[row.provider]; edited && row.enabled {
			edits = append(edits, launchDefaultEdit{provider: row.provider, choice: m.setupLaunchChoice(row.provider)})
		}
	}
	return edits
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
