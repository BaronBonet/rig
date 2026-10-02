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
