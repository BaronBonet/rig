package tui

import (
	tea "charm.land/bubbletea/v2"
)

// canEnter is the mode-transition guard: whether the TUI may enter the given
// mode with an operation possibly pending, in setup-only mode, and with or
// without a valid provider setup. Pure so the full matrix is testable.
func canEnter(to modelMode, pending pendingOp, setupOnly bool, hasSetup bool) bool {
	if setupOnly {
		// rig setup locks the TUI to the provider setup screen; the only way
		// out is quitting.
		return to == modeProviderSetup
	}
	if pending != opNone {
		// One operation at a time: while a create, delete, or switch is in
		// flight, only the browse list (where its progress renders) is
		// reachable.
		return to == modeBrowse
	}
	switch to {
	case modeBrowse, modeProviderSetup:
		return true
	default:
		// Creating, deleting, and switching all need a valid provider setup.
		return hasSetup
	}
}

// backDest is where esc/q leads from a mode: either another mode or quitting.
type backDest struct {
	quit bool
	to   modelMode
}

// backTarget declares the back-navigation table: where esc (and q, outside
// free-text entry) leads from each mode.
func backTarget(mode modelMode, hasSetup bool, setupOnly bool) backDest {
	switch mode {
	case modePromptInput:
		return backDest{to: modeBrowse}
	case modePRPicker:
		// Back to the prompt: both modes edit the same task draft.
		return backDest{to: modePromptInput}
	case modeCleanupConfirm, modeSwitchProvider:
		return backDest{to: modeBrowse}
	case modeProviderSetup:
		// Provider setup is mandatory: without a valid setup the only way out
		// is quitting rig entirely.
		if !hasSetup || setupOnly {
			return backDest{quit: true}
		}
		return backDest{to: modeBrowse}
	default:
		return backDest{quit: true}
	}
}

// modeFamily groups modes that share per-mode state: the prompt input and PR
// picker are two views over the same task draft.
type modeFamily int

const (
	familyBrowse modeFamily = iota
	familyDraft
	familySetup
	familySwitch
)

func familyOf(mode modelMode) modeFamily {
	switch mode {
	case modePromptInput, modePRPicker:
		return familyDraft
	case modeProviderSetup:
		return familySetup
	case modeSwitchProvider:
		return familySwitch
	default: // browse and cleanup confirm own no mode state
		return familyBrowse
	}
}

// transition is the only place m.mode is assigned after construction. It
// refuses transitions the guard forbids and discards the departing family's
// state when the mode family changes.
func (m *model) transition(to modelMode) bool {
	if !canEnter(to, m.pending, m.setupOnly, m.providerSetup != nil) {
		return false
	}
	if from := familyOf(m.mode); from != familyOf(to) {
		m.clearFamilyState(from)
	}
	m.mode = to
	return true
}

func (m *model) clearFamilyState(family modeFamily) {
	switch family {
	case familyDraft:
		m.draft = taskDraft{}
	case familySetup:
		m.setupForm = setupFormState{}
	case familySwitch:
		m.providerSwitch = switchState{}
	}
}

// handleBack applies the backTarget table for the current mode, including the
// departing mode's exit side effects.
func (m model) handleBack() (tea.Model, tea.Cmd) {
	dest := backTarget(m.mode, m.providerSetup != nil, m.setupOnly)
	if dest.quit {
		return m.quit()
	}

	from := m.mode
	if !m.transition(dest.to) {
		return m, nil
	}

	switch from {
	case modePromptInput:
		// Leaving the draft family discards the draft (transition above); the
		// abandoned create-flow leftovers go with it.
		m.create = createFlowState{}
		return m, nil
	case modePRPicker:
		// Same family: the draft survives, minus the picker's validation error.
		m.draft.err = nil
		return m, m.draft.input.Focus()
	default:
		return m, nil
	}
}
