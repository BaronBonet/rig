package tui

import (
	"errors"
	"fmt"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/BaronBonet/rig/internal/core"
)

func TestImportMode_ImportsTheSelectedSessionAsATask(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.importableSessions = []core.ProviderSessionSummary{
		{
			LastActiveAt: time.Now().Add(-3 * time.Hour), Provider: core.ProviderClaude, SessionID: "sess-1",
			Title: "fix-date-format", Cwd: "/tmp/repo",
		},
		{
			LastActiveAt: time.Now(), Provider: core.ProviderCodex, SessionID: "sess-2",
			Title: "review the service endpoints", Cwd: "/tmp/repo",
		},
	}
	frontend.importTask = &core.Task{
		ID: "task-imported", DisplayName: "review the service endpoints", RepoRoot: "/tmp/repo",
		WorkspaceKind: core.WorkspaceKindFolder, Provider: core.ProviderCodex,
	}
	m := newLoadedModel(frontend)

	next, cmd := m.Update(tea.KeyPressMsg{Code: 'i', Text: "i"})
	m, ok := next.(model)
	require.True(t, ok)
	require.Equal(t, modeImportSession, m.mode)
	require.Contains(t, stripANSI(m.View().Content), "Looking for sessions...")

	next, _ = m.Update(runCmd(t, cmd))
	m, ok = next.(model)
	require.True(t, ok)
	require.Equal(t, "/tmp/repo", frontend.importableSessionsFolder)
	view := stripANSI(m.View().Content)
	require.Contains(t, view, "fix-date-format")
	require.Contains(t, view, "3h 0m ago")
	require.Contains(t, view, "active now")

	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m, ok = next.(model)
	require.True(t, ok)
	next, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m, ok = next.(model)
	require.True(t, ok)
	require.Equal(t, opImporting, m.pending)

	imported := requireMsgType[sessionImportedMsg](t, runBatchCmd(t, cmd))
	next, _ = m.Update(imported)
	m, ok = next.(model)
	require.True(t, ok)
	require.Equal(t, "sess-2", frontend.importedSession.SessionID)
	require.Equal(t, modeBrowse, m.mode)
	require.Equal(t, opNone, m.pending)
	require.Equal(t, "task-imported", taskID(m.rows[m.selected].task))
	require.NoError(t, m.err)
}

func TestImportMode_ShowsTheImportedTaskEvenWhenItsSessionDidNotStart(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.importableSessions = []core.ProviderSessionSummary{
		{Provider: core.ProviderClaude, SessionID: "sess-1", Title: "fix-date-format", Cwd: "/tmp/repo"},
	}
	frontend.importTask = &core.Task{ID: "task-imported", WorkspaceKind: core.WorkspaceKindFolder}
	frontend.importErr = errors.New("imported, but its session did not start (enter retries)")
	m := newLoadedModel(frontend)

	next, cmd := m.Update(tea.KeyPressMsg{Code: 'i', Text: "i"})
	m, _ = next.(model)
	next, _ = m.Update(runCmd(t, cmd))
	m, _ = next.(model)
	next, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m, _ = next.(model)
	next, _ = m.Update(requireMsgType[sessionImportedMsg](t, runBatchCmd(t, cmd)))
	m, ok := next.(model)
	require.True(t, ok)

	require.Equal(t, modeBrowse, m.mode)
	require.ErrorContains(t, m.err, "enter retries")
	require.Equal(t, "task-imported", taskID(m.rows[m.selected].task))
}

func TestImportMode_NamesTheSubfolderASessionWasStartedIn(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.importableSessions = []core.ProviderSessionSummary{
		{Provider: core.ProviderClaude, SessionID: "sess-1", Title: "review the service", Cwd: "/tmp/repo/service"},
		{Provider: core.ProviderClaude, SessionID: "sess-2", Title: "plan the quarter", Cwd: "/tmp/repo"},
	}
	m := newLoadedModel(frontend)

	next, cmd := m.Update(tea.KeyPressMsg{Code: 'i', Text: "i"})
	m, _ = next.(model)
	next, _ = m.Update(runCmd(t, cmd))
	m, ok := next.(model)
	require.True(t, ok)

	view := stripANSI(m.View().Content)
	require.Contains(t, view, "> claude  service · review the service")
	require.Contains(t, view, "  claude  plan the quarter")
}

func TestImportMode_EscReturnsToTheTaskList(t *testing.T) {
	frontend := newFrontendHarness()
	m := newLoadedModel(frontend)

	next, _ := m.Update(tea.KeyPressMsg{Code: 'i', Text: "i"})
	m, _ = next.(model)
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m, ok := next.(model)

	require.True(t, ok)
	require.Equal(t, modeBrowse, m.mode)
	require.Empty(t, m.sessionImport.sessions)
}

func TestVisibleRange_KeepsTheSelectionInView(t *testing.T) {
	start, end := visibleRange(30, 0, 10)
	require.Equal(t, [2]int{0, 10}, [2]int{start, end})

	start, end = visibleRange(30, 15, 10)
	require.Equal(t, [2]int{10, 20}, [2]int{start, end})

	start, end = visibleRange(30, 29, 10)
	require.Equal(t, [2]int{20, 30}, [2]int{start, end})

	start, end = visibleRange(5, 4, 10)
	require.Equal(t, [2]int{0, 5}, [2]int{start, end})

	start, end = visibleRange(30, 7, 0)
	require.Equal(t, [2]int{0, 30}, [2]int{start, end}, "unknown height shows everything")
}

func TestImportMode_ScrollsLongListsInShortTerminals(t *testing.T) {
	frontend := newFrontendHarness()
	for index := range 30 {
		frontend.importableSessions = append(frontend.importableSessions, core.ProviderSessionSummary{
			Provider: core.ProviderClaude, SessionID: "sess", Title: fmt.Sprintf("session %02d", index), Cwd: "/tmp/repo",
		})
	}
	m := newLoadedModel(frontend)
	m.height = 20

	next, cmd := m.Update(tea.KeyPressMsg{Code: 'i', Text: "i"})
	m, _ = next.(model)
	next, _ = m.Update(runCmd(t, cmd))
	m, _ = next.(model)
	for range 25 {
		next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		m, _ = next.(model)
	}

	view := stripANSI(m.View().Content)
	require.Contains(t, view, "> claude  session 25")
	require.NotContains(t, view, "session 00")
	require.Contains(t, view, "↑ 20 more")
	require.NotContains(t, view, "↓", "no marker below the last page")
}

// loadDashboard starts the TUI in /tmp/repo, lets the task list load, and
// returns the model plus every message the load triggered.
func loadDashboard(t *testing.T, frontend *frontendHarness) (model, []tea.Msg) {
	t.Helper()
	m, loadMsg := initModel(t, newModel(frontend.mock, "/tmp/repo", ""))
	next, cmd := m.Update(loadMsg)
	m, ok := next.(model)
	require.True(t, ok)
	if cmd == nil {
		return m, nil
	}
	msg := cmd()
	if batch, isBatch := msg.(tea.BatchMsg); isBatch {
		msgs := make([]tea.Msg, 0, len(batch))
		for _, batchCmd := range batch {
			msgs = append(msgs, runCmd(t, batchCmd))
		}
		return m, msgs
	}
	return m, []tea.Msg{msg}
}

func TestEmptyDashboard_PointsToSessionsThatCanBeImported(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.importableSessions = []core.ProviderSessionSummary{
		{Provider: core.ProviderClaude, SessionID: "sess-1", Cwd: "/tmp/repo"},
		{Provider: core.ProviderClaude, SessionID: "sess-2", Cwd: "/tmp/repo"},
		{Provider: core.ProviderCodex, SessionID: "sess-3", Cwd: "/tmp/repo"},
	}

	m, msgs := loadDashboard(t, frontend)
	view := stripANSI(m.View().Content)
	require.Contains(t, view, "No tasks found.", "the list renders before the count arrives")
	require.NotContains(t, view, "can be imported")

	next, _ := m.Update(requireMsgType[importableCountLoadedMsg](t, msgs))
	m, ok := next.(model)
	require.True(t, ok)

	require.Equal(t, "/tmp/repo", frontend.importableSessionsFolder)
	view = stripANSI(m.View().Content)
	require.Contains(t, view, "3 sessions started in /tmp/repo can be imported. Press i to pick one.")
	require.Contains(t, view, "Press n to create one.")
}

func TestEmptyDashboard_KeepsTheCreateHintWhenNothingCanBeImported(t *testing.T) {
	frontend := newFrontendHarness()

	m, msgs := loadDashboard(t, frontend)
	next, _ := m.Update(requireMsgType[importableCountLoadedMsg](t, msgs))
	m, ok := next.(model)
	require.True(t, ok)

	view := stripANSI(m.View().Content)
	require.NotContains(t, view, "can be imported")
	require.Contains(t, view, "No tasks found.\nPress n to create one.")
}

func TestEmptyDashboard_HidesTheHintWhenTheCountFails(t *testing.T) {
	frontend := newFrontendHarness()
	m := newLoadedModel(frontend)
	m.importableHere = 2

	next, _ := m.Update(importableCountLoadedMsg{err: errors.New("daemon busy"), count: 0})
	m, ok := next.(model)
	require.True(t, ok)

	require.NotContains(t, stripANSI(m.View().Content), "can be imported")
}

func TestDashboard_DoesNotLookForSessionsWhileItHasTasks(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{{ID: "task-1", RepoName: "repo", DisplayName: "first task"}}
	frontend.importableSessions = []core.ProviderSessionSummary{
		{Provider: core.ProviderClaude, SessionID: "sess-1", Cwd: "/tmp/repo"},
	}

	_, msgs := loadDashboard(t, frontend)

	require.Empty(t, frontend.importableSessionsFolder)
	for _, msg := range msgs {
		_, counted := msg.(importableCountLoadedMsg)
		require.False(t, counted)
	}
}

func TestEmptyDashboard_RecountsAfterTheLastTaskIsDeleted(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{{ID: "task-1", RepoName: "repo", DisplayName: "first task"}}
	m := newLoadedModel(frontend)

	next, cmd := m.Update(taskDeletedMsg{taskID: "task-1"})
	_, ok := next.(model)
	require.True(t, ok)

	requireMsgType[importableCountLoadedMsg](t, []tea.Msg{runCmd(t, cmd)})
	require.Equal(t, "/tmp/repo", frontend.importableSessionsFolder)
}

// openPicker loads the import picker over the harness's sessions.
func openPicker(t *testing.T, frontend *frontendHarness) model {
	t.Helper()
	m := newLoadedModel(frontend)
	next, cmd := m.Update(tea.KeyPressMsg{Code: 'i', Text: "i"})
	m, _ = next.(model)
	next, _ = m.Update(runCmd(t, cmd))
	m, ok := next.(model)
	require.True(t, ok)
	return m
}

func pressKeys(t *testing.T, m model, keys ...tea.KeyPressMsg) model {
	t.Helper()
	for _, key := range keys {
		next, _ := m.Update(key)
		var ok bool
		m, ok = next.(model)
		require.True(t, ok)
	}
	return m
}

func typed(text string) []tea.KeyPressMsg {
	keys := make([]tea.KeyPressMsg, 0, len(text))
	for _, r := range text {
		keys = append(keys, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return keys
}

func olderSessionsFixture() []core.ProviderSessionSummary {
	var sessions []core.ProviderSessionSummary
	for index := range 40 {
		sessions = append(sessions, core.ProviderSessionSummary{
			Provider: core.ProviderClaude, SessionID: fmt.Sprintf("recent-%02d", index),
			Title: fmt.Sprintf("session %02d", index), Cwd: "/tmp/repo",
		})
	}
	return append(sessions,
		core.ProviderSessionSummary{
			Provider: core.ProviderClaude, SessionID: "search-follow-up", Title: "Search follow-up", Cwd: "/tmp/repo",
		},
		core.ProviderSessionSummary{
			Provider: core.ProviderCodex, SessionID: "search-api", Title: "search integration", Cwd: "/tmp/repo/api-server",
		},
	)
}

func TestImportMode_SearchFindsOlderSessionsByTitleAndSubfolder(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.importableSessions = olderSessionsFixture()
	m := openPicker(t, frontend)
	m.height = 20

	m = pressKeys(t, m, typed("/SEARCH")...)
	view := stripANSI(m.View().Content)
	require.Contains(t, view, "search  SEARCH▏  2 of 42")
	require.Contains(t, view, "> claude  Search follow-up")
	require.Contains(t, view, "  codex   api-server · search integration")
	require.NotContains(t, view, "session 00")

	m = pressKeys(t, m, typed(" api")...)
	view = stripANSI(m.View().Content)
	require.Contains(t, view, "1 of 42", "every word must match, in the title or the subfolder")
	require.Contains(t, view, "> codex   api-server · search integration")

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m, _ = next.(model)
	require.Equal(t, opImporting, m.pending)
	runBatchCmd(t, cmd)
	require.Equal(t, "search-api", frontend.importedSession.SessionID, "enter imports the highlighted match")
}

func TestImportMode_SearchTakesLettersThatAreKeysElsewhere(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.importableSessions = olderSessionsFixture()
	m := openPicker(t, frontend)

	m = pressKeys(t, m, typed("/jq")...)

	require.Equal(t, modeImportSession, m.mode)
	require.Equal(t, "jq", m.sessionImport.query)
	require.Contains(t, stripANSI(m.View().Content), `No session matches "jq".`)
}

func TestImportMode_EscClearsTheSearchBeforeLeaving(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.importableSessions = olderSessionsFixture()
	m := openPicker(t, frontend)
	m = pressKeys(t, m, typed("/search")...)
	m = pressKeys(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	require.Equal(t, 1, m.sessionImport.selected)

	m = pressKeys(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	require.Equal(t, modeImportSession, m.mode)
	require.False(t, m.sessionImport.searching)
	require.Empty(t, m.sessionImport.query)
	require.Zero(t, m.sessionImport.selected)
	require.Contains(t, stripANSI(m.View().Content), "session 00")

	m = pressKeys(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	require.Equal(t, modeBrowse, m.mode)
}

func TestImportMode_BackspaceEditsTheQueryThenEndsTheSearch(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.importableSessions = olderSessionsFixture()
	m := openPicker(t, frontend)
	m = pressKeys(t, m, typed("/pé")...)

	m = pressKeys(t, m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	require.Equal(t, "p", m.sessionImport.query, "a multi-byte character goes in one keystroke")
	m = pressKeys(t, m, tea.KeyPressMsg{Code: tea.KeyBackspace}, tea.KeyPressMsg{Code: tea.KeyBackspace})

	require.False(t, m.sessionImport.searching)
	require.Equal(t, modeImportSession, m.mode)
}

func TestImportMode_PasteGoesIntoTheSearch(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.importableSessions = olderSessionsFixture()
	m := openPicker(t, frontend)
	m = pressKeys(t, m, typed("/")...)

	next, _ := m.Update(tea.PasteMsg{Content: "search\nintegration"})
	m, ok := next.(model)

	require.True(t, ok)
	require.Equal(t, "search integration", m.sessionImport.query)
}
