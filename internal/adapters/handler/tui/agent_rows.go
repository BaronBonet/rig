package tui

import (
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/BaronBonet/rig/internal/core"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const (
	// subRowIndent and the branch glyph after it indent an agent sub-row
	// under its task's two lines.
	subRowIndent      = "  "
	subRowBranchWidth = 2
	// subRowProviderWidth leaves at least two spaces between the provider and
	// the free-form prompt after it.
	subRowProviderWidth = colWidthAgent + 1
	// subRowLead is what comes before a sub-row's text: its indent, branch
	// glyph and provider.
	subRowLead = len(subRowIndent) + subRowBranchWidth + subRowProviderWidth
)

// listCursor is one selectable line of the task list: a task row, or one of
// its agent sub-rows when agent is set.
type listCursor struct {
	row   int
	agent string
}

// agentSubRows returns the agent sessions a task row shows as sub-rows: its
// live agent sessions, oldest first, when it has two or more. A task with a
// single agent looks as it did before agent sessions existed.
func agentSubRows(row taskRow) []core.LiveAgentSession {
	if row.status == nil || len(row.status.AgentSessions) < 2 {
		return nil
	}
	return row.status.AgentSessions
}

// cursors lists every selectable line of the task list in display order.
func (m model) cursors() []listCursor {
	cursors := make([]listCursor, 0, len(m.rows))
	for index, row := range m.rows {
		cursors = append(cursors, listCursor{row: index})
		for _, session := range agentSubRows(row) {
			cursors = append(cursors, listCursor{row: index, agent: session.ID})
		}
	}
	return cursors
}

// moveCursor steps the selection through task rows and agent sub-rows.
func (m *model) moveCursor(delta int) {
	cursors := m.cursors()
	if len(cursors) == 0 {
		m.selected = 0
		m.selectedAgent = ""
		return
	}

	current := slices.Index(cursors, listCursor{row: clampIndex(m.selected, len(m.rows)), agent: m.selectedAgent})
	if current < 0 {
		current = slices.Index(cursors, listCursor{row: clampIndex(m.selected, len(m.rows))})
	}
	next := cursors[clampIndex(current+delta, len(cursors))]
	m.selected = next.row
	m.selectedAgent = next.agent
}

// selectLastLine selects the last line of the task list: the last task's
// last agent sub-row, or its task row when it shows none.
func (m *model) selectLastLine() {
	m.selected = len(m.rows) - 1
	m.selectedAgent = ""
	if cursors := m.cursors(); len(cursors) > 0 {
		last := cursors[len(cursors)-1]
		m.selected = last.row
		m.selectedAgent = last.agent
	}
}

// selectedAgentSession returns the agent session of the selected sub-row, or
// nil when a task row is selected.
func (m model) selectedAgentSession() *core.LiveAgentSession {
	if m.selectedAgent == "" || m.selected < 0 || m.selected >= len(m.rows) {
		return nil
	}
	subRows := agentSubRows(m.rows[m.selected])
	for i := range subRows {
		if subRows[i].ID == m.selectedAgent {
			return &subRows[i]
		}
	}
	return nil
}

// clampAgentSelection moves the cursor from a sub-row whose agent session is
// gone, or whose task no longer shows sub-rows, to the task's row.
func (m *model) clampAgentSelection() {
	if m.selectedAgentSession() == nil {
		m.selectedAgent = ""
	}
}

// agentSessionStatusUpdate presents an agent session's status in the shape
// the task status labels take, so a sub-row shows the same labels.
func agentSessionStatusUpdate(session core.LiveAgentSession) *core.TaskStatusUpdate {
	phase := session.Status.Phase
	if phase == "" {
		phase = core.TaskStatusPhaseStarting
	}
	return &core.TaskStatusUpdate{
		ObservedAt:     session.Status.ObservedAt,
		RawEventName:   session.Status.RawEventName,
		Provider:       session.Provider,
		Phase:          phase,
		BackgroundWork: session.Status.BackgroundWork,
	}
}

// renderAgentSubRow renders one agent session as an indented sub-row under
// its task: the provider, the latest prompt or else where the agent runs, and
// the status in the task row's status column.
func renderAgentSubRow(session core.LiveAgentSession, last bool, selected bool, totalWidth int) string {
	branch := "├"
	if last {
		branch = "└"
	}

	provider := emptyFallback(string(session.Provider), "-")
	providerCell := padRightVisible(provider, subRowProviderWidth)
	statusText, statusStyle := taskStatusText(agentSessionStatusUpdate(session))
	statusCell := padRightVisible(statusText, colWidthStatus)

	// The text column ends where the task row's status column starts. Too
	// narrow a terminal leaves no room for text rather than overflowing.
	textWidth := max(0, taskNameWidth(totalWidth)-subRowLead)
	text, isPrompt := agentSubRowText(session)
	// A truncated prompt stops a column short of the status.
	textCell := padRightVisible(truncateStr(text, textWidth-1), textWidth)

	textStyle := dimStyle
	if !isPrompt {
		textStyle = mutedStyle
	}
	if selected {
		textStyle = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	}

	line := dimStyle.Render(subRowIndent+branch+" ") +
		providerStyle(provider).Render(providerCell) +
		textStyle.Render(textCell) +
		statusStyle.Render(statusCell)
	if selected {
		return selectedRowStyle.Render(line)
	}
	return normalRowStyle.Render(line)
}

// agentSubRowText is what a sub-row says about its agent: its latest prompt,
// or before the first prompt, where its pane runs. isPrompt reports which.
func agentSubRowText(session core.LiveAgentSession) (string, bool) {
	if prompt := terminalSafeText(session.LatestPrompt); prompt != "" {
		return prompt, true
	}
	if pane := agentPaneText(session); pane != "" {
		return pane, false
	}
	return "no prompt yet", false
}

// agentPaneText describes where an agent session's pane runs: its window and
// pane position from the last tmux inspection, or else the pane's ID.
func agentPaneText(session core.LiveAgentSession) string {
	if location := session.Location; location != nil {
		window := "window " + strconv.Itoa(location.WindowIndex)
		if name := strings.TrimSpace(location.WindowName); name != "" {
			window += " (" + name + ")"
		}
		return window + " · pane " + strconv.Itoa(location.PaneIndex)
	}
	if id := strings.TrimSpace(session.Pane.ID); id != "" {
		return "pane " + id
	}
	return ""
}

// taskProviderNames lists the distinct providers of a task's live agent
// sessions in name order, or the task's own provider while none is live.
func taskProviderNames(row taskRow) []string {
	if row.status != nil && len(row.status.AgentSessions) > 0 {
		var names []string
		for _, session := range row.status.AgentSessions {
			name := strings.TrimSpace(string(session.Provider))
			if name != "" && !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
		if len(names) > 0 {
			slices.Sort(names)
			return names
		}
	}
	if row.task == nil {
		return []string{"-"}
	}
	return []string{emptyFallback(string(row.task.Provider), "-")}
}

// renderProviderCell renders a task row's provider cell, padded to the agent
// column.
func renderProviderCell(name string) string {
	return providerStyle(name).Render(padRightVisible(name, colWidthAgent))
}

// renderProviderNames renders providers each in its color, joined by "+".
func renderProviderNames(names []string) string {
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, providerStyle(name).Render(name))
	}
	return strings.Join(parts, mutedStyle.Render("+"))
}

// detailFieldLines renders a labelled detail panel field within width
// columns. A value too wide for one line continues after each " · " on lines
// indented under the value, and a part still too wide is truncated.
func detailFieldLines(label string, gap string, value string, style lipgloss.Style, width int) []string {
	prefix := mutedStyle.Render(label) + gap
	if width <= 0 || lipgloss.Width(label+gap+value) <= width {
		return []string{prefix + style.Render(value)}
	}

	indent := strings.Repeat(" ", lipgloss.Width(label+gap))
	parts := strings.Split(value, " · ")
	lines := []string{fitDetailLine(prefix+style.Render(parts[0]), width)}
	for _, part := range parts[1:] {
		lines = append(lines, fitDetailLine(indent+style.Render(part), width))
	}
	return lines
}

// fitDetailLine truncates a detail panel line to width columns, when the
// panel has any room at all.
func fitDetailLine(line string, width int) string {
	if width <= 0 {
		return line
	}
	return truncateStr(line, width)
}

// terminalSafeText makes text an agent or its transcript supplied safe to
// print on one line: it drops terminal escape sequences, turns other control
// characters into spaces, and collapses whitespace.
func terminalSafeText(text string) string {
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, ansi.Strip(text))
	return strings.Join(strings.Fields(text), " ")
}
