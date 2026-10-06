package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/BaronBonet/rig/internal/core"

	tea "charm.land/bubbletea/v2"
)

// enterImportSessionMode opens the picker of provider sessions started outside
// Rig in the launch folder or below it, so they can be resumed as tasks.
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
	return m, listImportableSessionsCmd(m.statusContext, m.frontend, folder, m.providerEnv)
}

// importHintCmd counts the importable sessions in the launch folder while the
// dashboard has no tasks, so a first run beside existing sessions points at
// them instead of looking empty. It never delays the task list.
func (m model) importHintCmd() tea.Cmd {
	folder := m.currentCreateCwd()
	if len(m.rows) > 0 || folder == "" {
		return nil
	}
	return importableCountCmd(m.statusContext, m.frontend, folder, m.providerEnv)
}

// importHintText invites importing the sessions an empty dashboard found.
func (m model) importHintText() string {
	noun := "sessions"
	if m.importableHere == 1 {
		noun = "session"
	}
	return fmt.Sprintf("%d %s started in %s can be imported. Press i to pick one.",
		m.importableHere, noun, homeRelativePath(m.currentCreateCwd()))
}

func (m model) updateImportSession(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.pending != opNone {
		return m, nil
	}
	if m.sessionImport.searching {
		return m.updateImportSearch(msg)
	}

	switch msg.String() {
	case "q", "esc":
		return m.handleBack()
	case "/":
		m.sessionImport.searching = true
		return m, nil
	case "j", "down":
		m.moveImportSelection(1)
		return m, nil
	case "k", "up":
		m.moveImportSelection(-1)
		return m, nil
	case "enter":
		return m.importSelectedSession()
	default:
		return m, nil
	}
}

// updateImportSearch takes typed text as the query; navigation moves to the
// arrow keys so letters such as j, k and q can be searched for.
func (m model) updateImportSearch(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.endImportSearch()
		return m, nil
	case "enter":
		return m.importSelectedSession()
	case "down", "ctrl+n":
		m.moveImportSelection(1)
		return m, nil
	case "up", "ctrl+p":
		m.moveImportSelection(-1)
		return m, nil
	case "backspace":
		if m.sessionImport.query == "" {
			m.endImportSearch()
			return m, nil
		}
		query := []rune(m.sessionImport.query)
		m.setImportQuery(string(query[:len(query)-1]))
		return m, nil
	case "ctrl+u":
		m.setImportQuery("")
		return m, nil
	default:
		if msg.Text != "" {
			m.setImportQuery(m.sessionImport.query + msg.Text)
		}
		return m, nil
	}
}

// pasteImportQuery appends pasted text to the search query.
func (m model) pasteImportQuery(msg tea.PasteMsg) (tea.Model, tea.Cmd) {
	if m.pending == opNone && m.sessionImport.searching {
		m.setImportQuery(m.sessionImport.query + strings.Join(strings.Fields(msg.Content), " "))
	}
	return m, nil
}

func (m *model) setImportQuery(query string) {
	m.sessionImport.query = query
	m.sessionImport.selected = 0
}

func (m *model) endImportSearch() {
	m.sessionImport.searching = false
	m.setImportQuery("")
}

func (m *model) moveImportSelection(delta int) {
	m.sessionImport.selected = clampIndex(m.sessionImport.selected+delta, len(m.importMatches()))
}

func (m model) importSelectedSession() (tea.Model, tea.Cmd) {
	matches := m.importMatches()
	if m.sessionImport.loading || m.sessionImport.selected >= len(matches) {
		return m, nil
	}
	m.beginOp(opImporting)
	return m, tea.Batch(
		importSessionCmd(m.statusContext, m.frontend, matches[m.sessionImport.selected], m.providerEnv),
		shimmerTickCmd(),
	)
}

// importMatches returns the sessions whose title, subfolder or provider
// contains every word of the search query, ignoring case.
func (m model) importMatches() []core.ProviderSessionSummary {
	terms := strings.Fields(strings.ToLower(m.sessionImport.query))
	if len(terms) == 0 {
		return m.sessionImport.sessions
	}
	var matches []core.ProviderSessionSummary
	for _, session := range m.sessionImport.sessions {
		text := strings.ToLower(strings.Join([]string{
			session.Title, sessionSubfolder(m.sessionImport.folder, session.Cwd), string(session.Provider),
		}, " "))
		if containsAll(text, terms) {
			matches = append(matches, session)
		}
	}
	return matches
}

func containsAll(text string, terms []string) bool {
	for _, term := range terms {
		if !strings.Contains(text, term) {
			return false
		}
	}
	return true
}

func (m model) importSessionView() string {
	var builder strings.Builder
	builder.WriteString(m.screenHeader(mutedStyle.Render("import session")) + "\n")
	builder.WriteString(mutedStyle.Render("folder  ") + primaryStyle.Render(homeRelativePath(m.sessionImport.folder)) +
		"\n")
	matches := m.importMatches()
	if m.sessionImport.searching {
		builder.WriteString(mutedStyle.Render("search  ") + primaryStyle.Render(m.sessionImport.query) +
			keybindStyle.Render("▏") + "  " +
			mutedStyle.Render(fmt.Sprintf("%d of %d", len(matches), len(m.sessionImport.sessions))) + "\n")
	}
	builder.WriteString("\n")

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
		builder.WriteString(dimStyle.Render("No sessions started in this folder or below it are left to import.") +
			"\n")
	case len(matches) == 0:
		builder.WriteString(dimStyle.Render(fmt.Sprintf("No session matches %q.", m.sessionImport.query)) + "\n")
	default:
		builder.WriteString(dimStyle.Render("Resume a session as a task. Close its old pane first: one "+
			"conversation must not run in two places.") + "\n\n")
		width := m.totalWidth() - 24
		start, end := visibleRange(len(matches), m.sessionImport.selected, m.importListRows())
		if start > 0 {
			builder.WriteString(mutedStyle.Render(fmt.Sprintf("  ↑ %d more", start)) + "\n")
		}
		for index := start; index < end; index++ {
			session := matches[index]
			cursor := "  "
			titleStyle := dimStyle
			if index == m.sessionImport.selected {
				cursor = "> "
				titleStyle = primaryStyle
			}
			provider := padRightVisible(string(session.Provider), 8)
			location := ""
			if subfolder := sessionSubfolder(m.sessionImport.folder, session.Cwd); subfolder != "" {
				location = truncateStr(subfolder, width/2) + " · "
			}
			titleWidth := max(width-lipgloss.Width(location), 0)
			builder.WriteString(cursor + providerStyle(string(session.Provider)).Render(provider) +
				dimStyle.Render(location) +
				titleStyle.Render(padRightVisible(truncateStr(session.Title, titleWidth), titleWidth)) +
				mutedStyle.Render(sessionAgeText(session.LastActiveAt)) + "\n")
		}
		if end < len(matches) {
			builder.WriteString(mutedStyle.Render(fmt.Sprintf("  ↓ %d more", len(matches)-end)) + "\n")
		}
	}

	builder.WriteString("\n")
	if m.sessionImport.searching {
		builder.WriteString(footerKeybinds(
			[2]string{"enter", "import"},
			[2]string{"↑↓", "move"},
			[2]string{"esc", "clear search"},
		))
		return builder.String()
	}
	builder.WriteString(footerKeybinds(
		[2]string{"enter", "import"},
		[2]string{"/", "search"},
		[2]string{"esc", "cancel"},
	))
	return builder.String()
}

// sessionSubfolder names the folder below the launch folder a session was
// started in, where importing resumes it; "" for the launch folder itself.
func sessionSubfolder(folder string, cwd string) string {
	if cwd == "" {
		return ""
	}
	rel, err := filepath.Rel(folder, cwd)
	if err != nil || rel == "." {
		return ""
	}
	return rel
}

// importListRows is how many sessions fit between the picker's header and
// footer; 0 means the terminal height is not known yet and all are shown.
func (m model) importListRows() int {
	if m.height <= 0 {
		return 0
	}
	reserved := 10
	if m.sessionImport.searching {
		reserved++ // the search line
	}
	return max(m.height-reserved, 3)
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
