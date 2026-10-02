package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// enterImportSessionMode opens the picker of provider sessions started in the
// launch folder outside Rig, so they can be resumed as tasks.
func (m model) enterImportSessionMode() (tea.Model, tea.Cmd) {
	if m.pending != opNone {
		return m, nil
	}
	if m.providerSetup == nil {
		return m.enterProviderSetupMode()
	}
	folder := m.currentCreateCwd()
	if !m.transition(modeImportSession) {
		return m, nil
	}
	m.err = nil
	m.sessionImport = importState{folder: folder, loading: true}
	return m, listImportableSessionsCmd(m.statusContext, m.frontend, folder)
}

func (m model) updateImportSession(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.pending != opNone {
		return m, nil
	}

	sessions := m.sessionImport.sessions
	switch msg.String() {
	case "q", "esc":
		return m.handleBack()
	case "j", "down":
		m.sessionImport.selected = clampIndex(m.sessionImport.selected+1, len(sessions))
		return m, nil
	case "k", "up":
		m.sessionImport.selected = clampIndex(m.sessionImport.selected-1, len(sessions))
		return m, nil
	case "enter":
		if m.sessionImport.loading || m.sessionImport.selected >= len(sessions) {
			return m, nil
		}
		m.beginOp(opImporting)
		return m, tea.Batch(
			importSessionCmd(m.statusContext, m.frontend, sessions[m.sessionImport.selected]),
			shimmerTickCmd(),
		)
	default:
		return m, nil
	}
}

func (m model) importSessionView() string {
	var builder strings.Builder
	builder.WriteString(m.screenHeader(mutedStyle.Render("import session")) + "\n")
	builder.WriteString(mutedStyle.Render("folder  ") + primaryStyle.Render(homeRelativePath(m.sessionImport.folder)) +
		"\n\n")

	switch {
	case m.pending == opImporting:
		builder.WriteString(renderShimmer("Resuming session...", m.shimmerTick) + "\n")
		return builder.String()
	case m.sessionImport.loading:
		builder.WriteString(dimStyle.Render("Looking for sessions...") + "\n")
		return builder.String()
	case m.sessionImport.err != nil:
		builder.WriteString(errorBlock(m.sessionImport.err))
	case len(m.sessionImport.sessions) == 0:
		builder.WriteString(dimStyle.Render("No sessions started in this folder are left to import.") + "\n")
	default:
		builder.WriteString(dimStyle.Render("Resume a session as a task. Close its old pane first: one "+
			"conversation must not run in two places.") + "\n\n")
		width := m.totalWidth() - 24
		sessions := m.sessionImport.sessions
		start, end := visibleRange(len(sessions), m.sessionImport.selected, m.importListRows())
		if start > 0 {
			builder.WriteString(mutedStyle.Render(fmt.Sprintf("  ↑ %d more", start)) + "\n")
		}
		for index := start; index < end; index++ {
			session := sessions[index]
			cursor := "  "
			titleStyle := dimStyle
			if index == m.sessionImport.selected {
				cursor = "> "
				titleStyle = primaryStyle
			}
			provider := padRightVisible(string(session.Provider), 8)
			builder.WriteString(cursor + providerStyle(string(session.Provider)).Render(provider) +
				titleStyle.Render(padRightVisible(truncateStr(session.Title, width), width)) +
				mutedStyle.Render(sessionAgeText(session.LastActiveAt)) + "\n")
		}
		if end < len(sessions) {
			builder.WriteString(mutedStyle.Render(fmt.Sprintf("  ↓ %d more", len(sessions)-end)) + "\n")
		}
	}

	builder.WriteString("\n")
	builder.WriteString(footerKeybinds(
		[2]string{"enter", "import"},
		[2]string{"esc", "cancel"},
	))
	return builder.String()
}

// importListRows is how many sessions fit between the picker's header and
// footer; 0 means the terminal height is not known yet and all are shown.
func (m model) importListRows() int {
	if m.height <= 0 {
		return 0
	}
	return max(m.height-10, 3)
}

// visibleRange returns the [start, end) slice of a list of total rows that
// keeps selected in view when only rows fit; rows <= 0 shows everything.
func visibleRange(total int, selected int, rows int) (int, int) {
	if rows <= 0 || total <= rows {
		return 0, total
	}
	start := min(max(selected-rows/2, 0), total-rows)
	return start, start + rows
}

// sessionAgeText says how long ago a session was last active; "active now"
// flags one that may still be open elsewhere.
func sessionAgeText(lastActive time.Time) string {
	if lastActive.IsZero() {
		return ""
	}
	age := time.Since(lastActive)
	if age < 2*time.Minute {
		return "active now"
	}
	if age >= 48*time.Hour {
		return lastActive.Format("Jan 02")
	}
	return formatElapsed(age) + " ago"
}
