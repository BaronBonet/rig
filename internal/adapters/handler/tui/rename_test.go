package tui

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

// edit applies a key without running its commands: in the rename input
// they only blink the cursor, on a timer settle would wait out.
func edit(t *testing.T, m model, key tea.KeyPressMsg) model {
	t.Helper()
	next, _ := m.Update(key)
	m, ok := next.(model)
	require.True(t, ok)
	return m
}

func typeText(t *testing.T, m model, text string) model {
	t.Helper()
	for _, r := range text {
		m = edit(t, m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return m
}

func clearInput(t *testing.T, m model) model {
	t.Helper()
	return edit(t, m, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
}

func TestRename_EEditsTheSelectedTasksNameInPlace(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = shelfTasksFixture()
	m := dashboardIn(t, frontend, "/src/code")

	m = edit(t, m, keyText("e"))
	require.Equal(t, modeRenameTask, m.mode)
	require.Equal(t, "search integration", m.rename.input.Value())

	m = typeText(t, m, " v2")
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	require.Equal(t, map[string]string{"task-1": "search integration v2"}, frontend.renamedTasks)
	require.Equal(t, modeBrowse, m.mode)
	require.Contains(t, stripANSI(m.View().Content), "search integration v2")
}

func TestRename_TypedQDoesNotQuit(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = shelfTasksFixture()
	m := dashboardIn(t, frontend, "/src/code")

	m = edit(t, m, keyText("e"))
	m = clearInput(t, m)
	m = typeText(t, m, "quarterly review")
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	require.Equal(t, map[string]string{"task-1": "quarterly review"}, frontend.renamedTasks)
	require.Equal(t, modeBrowse, m.mode)
}

func TestRename_EscLeavesTheNameAsItWas(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = shelfTasksFixture()
	m := dashboardIn(t, frontend, "/src/code")

	m = edit(t, m, keyText("e"))
	m = typeText(t, m, " v2")
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})

	require.Equal(t, modeBrowse, m.mode)
	require.Empty(t, frontend.renamedTasks)
}

func TestRename_AnEmptyNameIsNotSent(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = shelfTasksFixture()
	m := dashboardIn(t, frontend, "/src/code")

	m = edit(t, m, keyText("e"))
	m = clearInput(t, m)
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	require.Equal(t, modeRenameTask, m.mode)
	require.Empty(t, frontend.renamedTasks)
	require.Contains(t, stripANSI(m.View().Content), "a task needs a name")
}

func TestRename_ARefusalKeepsTheTypedNameToTryAgain(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = shelfTasksFixture()
	frontend.renameTaskErr = errors.New(`"search integration" is still being created; rename it once it is ready`)
	m := dashboardIn(t, frontend, "/src/code")

	m = edit(t, m, keyText("e"))
	m = typeText(t, m, " v2")
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	require.Equal(t, modeRenameTask, m.mode)
	require.Equal(t, "search integration v2", m.rename.input.Value())
	require.Contains(t, stripANSI(m.View().Content), "still being created")
}
