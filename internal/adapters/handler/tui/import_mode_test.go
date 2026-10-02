package tui

import (
	"errors"
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
