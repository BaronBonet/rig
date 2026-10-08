package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"

	"github.com/BaronBonet/rig/internal/core"
)

func shelfTasksFixture() []*core.Task {
	return []*core.Task{
		{ID: "task-1", DisplayName: "search integration", RepoName: "code", RepoRoot: "/src/code",
			WorktreePath: "/src/code", WorkspaceKind: core.WorkspaceKindFolder, TmuxSession: "code_search"},
		{ID: "task-2", DisplayName: "fix router links", RepoName: "code", RepoRoot: "/src/code",
			WorktreePath: "/src/code", WorkspaceKind: core.WorkspaceKindFolder, TmuxSession: "code_router"},
	}
}

// press sends a key to the dashboard and applies every message its commands
// produce, the way the program loop would.
func press(t *testing.T, m model, key tea.KeyPressMsg) model {
	t.Helper()
	next, cmd := m.Update(key)
	m, ok := next.(model)
	require.True(t, ok)
	return settle(t, m, cmd)
}

func settle(t *testing.T, m model, cmd tea.Cmd) model {
	t.Helper()
	if cmd == nil {
		return m
	}
	msg := cmd()
	var msgs []tea.Msg
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, batchCmd := range batch {
			if batchCmd != nil {
				msgs = append(msgs, batchCmd())
			}
		}
	} else {
		msgs = append(msgs, msg)
	}
	for _, msg := range msgs {
		switch msg.(type) {
		case tasksLoadedMsg, taskShelvedMsg, taskOpenedMsg, taskRenamedMsg:
			next, follow := m.Update(msg)
			m, _ = next.(model)
			m = settle(t, m, follow)
		}
	}
	return m
}

func keyText(key string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: rune(key[0]), Text: key}
}

func TestShelf_DTakesTheSelectedTaskOffTheCurrentList(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = shelfTasksFixture()
	m := dashboardIn(t, frontend, "/src/code")

	m = press(t, m, keyText("d"))

	require.Equal(t, []string{"task-1"}, frontend.shelveTaskIDs)
	require.Equal(t, []string{"task-2"}, rowIDs(m))
	require.Contains(t, stripANSI(m.View().Content), "1 task shelved. Press tab to see the shelf.")
}

func TestShelf_TabShowsTheShelfAndEnterPutsATaskBackAndOpensIt(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = shelfTasksFixture()
	frontend.listTasks[0].ShelvedAt = time.Now().Add(-3 * time.Hour)
	m := dashboardIn(t, frontend, "/src/code")
	require.Equal(t, []string{"task-2"}, rowIDs(m))

	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	require.Equal(t, []string{"task-1"}, rowIDs(m))
	view := stripANSI(m.View().Content)
	require.Contains(t, view, "Shelf: tasks taken off the current list. Opening one puts it back.")
	require.Contains(t, view, "shelved 3h 0m ago")
	require.Contains(t, view, "enter open  d unshelve  e rename  tab current")

	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	require.Equal(t, []string{"task-1"}, frontend.unshelveTaskIDs)
	require.Equal(t, "task-1", frontend.attachedTask.ID, "opened, resuming its session")
	require.False(t, m.showShelf)
	require.ElementsMatch(t, []string{"task-1", "task-2"}, rowIDs(m))
	require.Equal(t, "task-1", taskID(m.selectedRow().task))
}

func TestShelf_DOnTheShelfPutsATaskBackWithoutOpeningIt(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = shelfTasksFixture()
	frontend.listTasks[1].ShelvedAt = time.Now()
	m := press(t, dashboardIn(t, frontend, "/src/code"), tea.KeyPressMsg{Code: tea.KeyTab})

	m = press(t, m, keyText("d"))

	require.Equal(t, []string{"task-2"}, frontend.unshelveTaskIDs)
	require.Zero(t, frontend.attachTaskSessionCalls)
	require.True(t, m.showShelf)
	require.Empty(t, m.rows)
	require.Contains(t, stripANSI(m.View().Content), "The shelf is empty.")
}

func TestShelf_StartingWorkHappensOnTheCurrentListAndEscGoesBackToIt(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = shelfTasksFixture()
	frontend.listTasks[0].ShelvedAt = time.Now()
	m := press(t, dashboardIn(t, frontend, "/src/code"), tea.KeyPressMsg{Code: tea.KeyTab})

	for _, key := range []string{"n", "N", "i", "p"} {
		next, cmd := m.Update(keyText(key))
		m, _ = next.(model)
		require.Nil(t, cmd, key)
		require.Equal(t, modeBrowse, m.mode, key)
	}

	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	require.False(t, m.showShelf, "esc leaves the shelf instead of quitting")
	require.Equal(t, []string{"task-2"}, rowIDs(m))
}

func TestShelf_ARefusalIsShownAndTheTaskStays(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = shelfTasksFixture()
	frontend.shelveTaskErr = errors.New(`provider session is still running: "search integration" is working`)
	m := dashboardIn(t, frontend, "/src/code")

	m = press(t, m, keyText("d"))

	require.ElementsMatch(t, []string{"task-1", "task-2"}, rowIDs(m))
	require.Contains(t, stripANSI(m.View().Content), `"search integration" is working`)
}

func TestShelf_TheKeyBarFitsANarrowScreen(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = shelfTasksFixture()
	m := dashboardIn(t, frontend, "/src/code")

	for _, width := range []int{60, 96} {
		m.width, m.height = width, 30
		for _, showShelf := range []bool{false, true} {
			m.showShelf = showShelf
			view := m.View().Content
			header := stripANSI(strings.Split(view, "\n")[0])
			require.LessOrEqual(t, lipgloss.Width(header), width, header)
			if showShelf {
				require.Contains(t, header, "d unshelve")
			} else {
				require.Contains(t, header, "d shelve")
			}
		}
	}
}
