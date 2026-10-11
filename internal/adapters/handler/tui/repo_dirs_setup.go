package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// enterRepoDirsSetupMode opens the optional repo directories step that
// follows provider setup, and loads the directories it starts with.
func (m model) enterRepoDirsSetupMode() (tea.Model, tea.Cmd) {
	if !m.transition(modeRepoDirsSetup) {
		return m, nil
	}
	m.repoDirsLoad++
	m.repoDirsForm = repoDirsFormState{input: newRepoDirsInput(), loading: true}
	return m, loadRepoDirsCmd(m.statusContext, m.frontend, m.launchCwd, m.repoDirsLoad)
}

// applyLoadedRepoDirs fills the repo directories step with the directories
// it starts with, one per line, and lets the user edit them. It ignores the
// reply to an earlier load, such as one for a step the user has since left
// and reopened.
func (m model) applyLoadedRepoDirs(msg repoDirsLoadedMsg) (tea.Model, tea.Cmd) {
	if m.mode != modeRepoDirsSetup || !m.repoDirsForm.loading || msg.load != m.repoDirsLoad {
		return m, nil
	}
	m.repoDirsForm.loading = false
	m.repoDirsForm.loadFailed = msg.err != nil
	m.repoDirsForm.err = msg.err
	m.repoDirsForm.input.SetValue(strings.Join(msg.dirs, "\n"))
	return m, m.repoDirsForm.input.Focus()
}

// applySavedRepoDirs finishes setup once the repo directories are saved, or
// keeps the user on the step to fix what could not be saved.
func (m model) applySavedRepoDirs(msg repoDirsSavedMsg) (tea.Model, tea.Cmd) {
	if m.mode != modeRepoDirsSetup || !m.repoDirsForm.saving {
		return m, nil
	}
	m.repoDirsForm.saving = false
	if msg.err != nil {
		m.repoDirsForm.err = msg.err
		return m, nil
	}
	// Saving finishes setup the way skipping the step does.
	return m.handleBack()
}

// updateRepoDirsSetup handles the repo directories step. Enter saves the
// directories, one per line, and esc skips the step, leaving them unchanged;
// either finishes setup. Every other key, and a paste, edits the directories.
func (m model) updateRepoDirsSetup(msg tea.Msg) (tea.Model, tea.Cmd) {
	form := &m.repoDirsForm
	switch typed := msg.(type) {
	case tea.KeyPressMsg:
		if form.saving {
			return m, nil
		}
		switch typed.String() {
		case "esc":
			return m.handleBack()
		case "enter":
			if form.loading {
				return m, nil
			}
			if form.loadFailed && strings.TrimSpace(form.input.Value()) == "" {
				// The user's repo directories could not be shown, so saving
				// none would delete them unseen: skip the step instead.
				return m.handleBack()
			}
			form.saving = true
			form.err = nil
			dirs := strings.Split(form.input.Value(), "\n")
			return m, saveRepoDirsCmd(m.statusContext, m.frontend, dirs)
		}
		if form.loading {
			return m, nil
		}
	case tea.PasteMsg:
		if form.loading || form.saving {
			return m, nil
		}
	}

	previous := form.input.Value()
	form.input.SetWidth(m.textBoxContentWidth())
	var cmd tea.Cmd
	form.input, cmd = form.input.Update(msg)
	if form.input.Value() != previous {
		form.err = nil
	}
	return m, cmd
}
