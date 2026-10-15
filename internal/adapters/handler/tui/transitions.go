package tui

import (
	tea "charm.land/bubbletea/v2"
)

// canEnter is the mode-transition guard: whether the TUI may enter the given
// mode in setup-only mode, and with or without a valid provider setup. Pure
// so the full matrix is testable.
func canEnter(to modelMode, setupOnly bool, hasSetup bool) bool {
	if setupOnly {
		// rig setup locks the TUI to the setup steps; the only way out is
		// quitting. The repo directories step follows a valid provider setup.
		return to == modeProviderSetup || (to == modeRepoDirsSetup && hasSetup)
	}
	switch to {
	case modeBrowse, modeProviderSetup:
		return true
	default:
		// Creating, deleting and the repo directories step all need a valid
		// provider setup.
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
	case modePRPicker, modeRepoPicker:
		// Back to the prompt: these modes edit the same task draft.
		return backDest{to: modePromptInput}
	case modeCleanupConfirm:
		return backDest{to: modeBrowse}
	case modeProviderSetup:
		// Provider setup is mandatory: without a valid setup the only way out
		// is quitting rig entirely.
		if !hasSetup || setupOnly {
			return backDest{quit: true}
		}
		return backDest{to: modeBrowse}
	case modeRepoDirsSetup:
		// The repo directories step is optional and the last of setup:
		// leaving it, skipped or saved, finishes setup. rig setup then quits,
		// and setup in the TUI goes on to the task list.
		if setupOnly {
			return backDest{quit: true}
		}
		return backDest{to: modeBrowse}
	default:
		return backDest{quit: true}
	}
}

// modeFamily groups modes that share per-mode state: the prompt input and its
// PR and repo pickers are views over the same task draft.
type modeFamily int

const (
	familyBrowse modeFamily = iota
	familyDraft
	familySetup
)

func familyOf(mode modelMode) modeFamily {
	switch mode {
	case modePromptInput, modePRPicker, modeRepoPicker:
		return familyDraft
	case modeProviderSetup, modeRepoDirsSetup:
		return familySetup
	default: // browse and cleanup confirm own no mode state
		return familyBrowse
	}
}

// transition is the only place m.mode is assigned after construction. It
// refuses transitions the guard forbids and discards the departing family's
// state when the mode family changes.
func (m *model) transition(to modelMode) bool {
	if !canEnter(to, m.setupOnly, m.providerSetup != nil) {
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
		m.repoDirsForm = repoDirsFormState{}
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
	case modeCleanupConfirm:
		m.cleanup = cleanupTarget{}
		return m, nil
	case modePRPicker, modeRepoPicker:
		// Same family: the draft survives, minus the picker's validation error.
		m.draft.err = nil
		return m, m.draft.input.Focus()
	case modeRepoDirsSetup:
		// Finishing setup reloads the task list it goes on to.
		m.loading = true
		return m, loadTasksCmd(m.statusContext, m.frontend)
	default:
		return m, nil
	}
}
