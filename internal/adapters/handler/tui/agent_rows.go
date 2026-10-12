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
	// under its task's name.
	subRowIndent      = "  "
	subRowBranchWidth = 2
	// subRowProviderWidth leaves at least two spaces between the provider and
	// the free-form prompt after it.
	subRowProviderWidth = colWidthAgent + 1
	// subRowLead is what comes before a sub-row's text: its indent, branch
	// glyph, status symbol and provider.
	subRowLead = len(subRowIndent) + subRowBranchWidth + statusSymbolWidth + subRowProviderWidth
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

// agentSessionStatusUpdate presents an agent session's status in the shape a
// task's status takes, so a sub-row shows the same symbols.
func agentSessionStatusUpdate(session core.LiveAgentSession) *core.TaskStatusUpdate {
	phase := session.Status.Phase
	if phase == "" {
		phase = core.TaskStatusPhaseStarting
	}
	return &core.TaskStatusUpdate{
		ObservedAt: session.Status.ObservedAt,
		Provider:   session.Provider,
		Phase:      phase,
	}
}

// agentRowUsage is what an agent sub-row shows of its agent's usage: how full
// its context window is, or nil when that is not known, and its compactions.
type agentRowUsage struct {
	context     *core.ContextUsage
	compactions core.Compactions
}

// renderAgentSubRow renders one agent session as an indented sub-row under
// its task: its status symbol, its provider, and what agentSubRowText says
// about it, then how full its context window is and its compaction marker,
// where it has them.
func (m model) renderAgentSubRow(
	session core.LiveAgentSession,
	usage agentRowUsage,
	last bool,
	selected bool,
	layout taskRowLayout,
) string {
	branch := "├"
	if last {
		branch = "└"
	}

	status := agentSessionStatusUpdate(session)
	symbol, symbolStyle := m.statusSymbol(status)
	if m.removingAgents[session.ID] {
		symbol, symbolStyle = iconRemoving, mutedStyle
	}
	provider := emptyFallback(string(session.Provider), "-")
	providerCell := padRightVisible(provider, subRowProviderWidth)

	// The text ends where the task row's name area does, clear of its columns
	// on the right. Too narrow a terminal leaves no room for text rather than
	// overflowing.
	textWidth := max(0, statusSymbolWidth+layout.nameArea-subRowLead)
	text, placeholder := agentSubRowText(session)
	text = truncateStr(text, textWidth-len(nameGap))

	textStyle, markerStyle := dimStyle, mutedStyle
	if placeholder {
		textStyle = mutedStyle
	}
	if needsInput(status) {
		textStyle = primaryStyle
	}
	if selected {
		textStyle = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
		markerStyle = primaryStyle
	}

	line := dimStyle.Render(subRowIndent+branch+" ") +
		symbolStyle.Render(symbol) + " " +
		cursorLineStyle(providerStyle(provider), selected).Render(providerCell) +
		textStyle.Render(text)
	// The context and compaction markers line up with the task rows' context
	// and compactions columns, after their provider column, which a sub-row
	// leaves blank.
	_, hasPercent := contextPercent(usage.context)
	showContext := layout.context && hasPercent
	showCompactions := layout.compactions && usage.compactions.Total > 0
	if showContext || showCompactions {
		line = padRightVisible(line, subRowLead+textWidth) + strings.Repeat(" ", colWidthAgent)
		if layout.context {
			line += renderContextCell(usage.context, markerStyle)
		}
		if showCompactions {
			line += renderCompactionsCell(usage.compactions, markerStyle)
		}
	}
	if selected {
		return selectedRowStyle.Render(line)
	}
	return normalRowStyle.Render(line)
}

// agentSubRowText is what a sub-row says about its agent: its name, its
// latest prompt until it is named, or before the first prompt, where its pane
// runs. placeholder reports the last, which says nothing of the agent's work.
func agentSubRowText(session core.LiveAgentSession) (string, bool) {
	if name := terminalSafeText(session.Name); name != "" {
		return name, false
	}
	if prompt := terminalSafeText(session.LatestPrompt); prompt != "" {
		return prompt, false
	}
	if pane := agentPaneText(session); pane != "" {
		return pane, true
	}
	return "no prompt yet", true
}

// agentActivity is the part of a task's activity that came from one of its
// agent sessions.
func agentActivity(events []core.TaskActivityEvent, agentSessionID string) []core.TaskActivityEvent {
	var activity []core.TaskActivityEvent
	for _, event := range events {
		if event.AgentSessionID == agentSessionID {
			activity = append(activity, event)
		}
	}
	return activity
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

// agentCompactions is how many times an agent session's current conversation
// was compacted, as its task's usage reports it.
func agentCompactions(row taskRow, agentSessionID string) core.Compactions {
	if row.usage == nil {
		return core.Compactions{}
	}
	return row.usage.AgentCompactions[agentSessionID]
}

// taskRowCompactions is the compactions a task row shows: its agent's, while
// it has exactly one live agent session.
func taskRowCompactions(row taskRow) core.Compactions {
	if row.status == nil || len(row.status.AgentSessions) != 1 {
		return core.Compactions{}
	}
	return agentCompactions(row, row.status.AgentSessions[0].ID)
}

// agentContext is how full an agent session's context window is, as its
// task's usage reports it, or nil when the usage does not record it.
func agentContext(row taskRow, agentSessionID string) *core.ContextUsage {
	if row.usage == nil {
		return nil
	}
	contextUsage, ok := row.usage.AgentContext[agentSessionID]
	if !ok {
		return nil
	}
	return &contextUsage
}

// taskRowContext is the context usage a task row shows: its agent's, while it
// has exactly one live agent session, as with its compactions.
func taskRowContext(row taskRow) *core.ContextUsage {
	if row.status == nil || len(row.status.AgentSessions) != 1 {
		return nil
	}
	return agentContext(row, row.status.AgentSessions[0].ID)
}

// agentModel is the model and reasoning level an agent session runs on, as
// its task's usage reports them, and whether the usage reports them.
func agentModel(row taskRow, agentSessionID string) (core.AgentModel, bool) {
	if row.usage == nil {
		return core.AgentModel{}, false
	}
	running, ok := row.usage.AgentModels[agentSessionID]
	return running, ok
}

// contextPercent is how full a context window is, in whole percent rounded
// down. It is capped at 100, as Codex can count more tokens than its window
// holds. ok is false when the context or its window is unknown.
func contextPercent(contextUsage *core.ContextUsage) (int, bool) {
	if contextUsage == nil || contextUsage.Window <= 0 {
		return 0, false
	}
	return min(max(contextUsage.Tokens*100/contextUsage.Window, 0), 100), true
}

// renderContextCell renders how full an agent's context window is, such as
// "41%", padded to the context column, or a blank cell when that is not known.
func renderContextCell(contextUsage *core.ContextUsage, style lipgloss.Style) string {
	percent, ok := contextPercent(contextUsage)
	if !ok {
		return strings.Repeat(" ", colWidthContext)
	}
	return " " + padLeftVisible(style.Render(strconv.Itoa(percent)+"%"), colWidthContext-2) + " "
}

// contextStyle colors how full a context window is: plain while it has room,
// amber as it nears compaction, which may need the user, and red when nearly
// full.
func contextStyle(contextUsage *core.ContextUsage) lipgloss.Style {
	percent, ok := contextPercent(contextUsage)
	switch {
	case !ok || percent < 70:
		return primaryStyle
	case percent < 90:
		return warningStyle
	default:
		return errorStyle
	}
}

// contextDetailText describes an agent's context for the detail panel: how
// full its context window is and the tokens in it of the window's size, or
// only the tokens while the window is unknown.
func contextDetailText(contextUsage core.ContextUsage) string {
	tokens := formatTokenCount(contextUsage.Tokens)
	percent, ok := contextPercent(&contextUsage)
	if !ok {
		return tokens
	}
	return strconv.Itoa(percent) + "% · " + tokens + " of " + formatTokenCount(contextUsage.Window)
}

// renderCompactionsCell renders an agent's compaction marker, such as "↻2",
// padded to the compactions column, or a blank cell for an agent that has not
// compacted. Counts past maxCompactionsMarker fit the column as "↻99+".
func renderCompactionsCell(compactions core.Compactions, style lipgloss.Style) string {
	if compactions.Total <= 0 {
		return strings.Repeat(" ", colWidthCompactions)
	}
	count := strconv.Itoa(min(compactions.Total, maxCompactionsMarker))
	if compactions.Total > maxCompactionsMarker {
		count += "+"
	}
	return padRightVisible(style.Render(iconCompacted+count), colWidthCompactions)
}

// compactionsDetailText describes an agent's compactions for the detail
// panel: how many there were and, where the provider records it, what
// triggered them.
func compactionsDetailText(compactions core.Compactions) string {
	text := strconv.Itoa(compactions.Total) + "×"
	switch {
	case compactions.Auto == compactions.Total:
		return text + " (auto)"
	case compactions.Manual == compactions.Total:
		return text + " (manual)"
	}

	var triggers []string
	if compactions.Auto > 0 {
		triggers = append(triggers, strconv.Itoa(compactions.Auto)+" auto")
	}
	if compactions.Manual > 0 {
		triggers = append(triggers, strconv.Itoa(compactions.Manual)+" manual")
	}
	if len(triggers) == 0 {
		return text
	}
	return text + " (" + strings.Join(triggers, ", ") + ")"
}

// renderProviderCell renders a task row's provider cell, padded to the agent
// column, set for the cursor's line or not; see cursorLineStyle.
func renderProviderCell(name string, onCursor bool) string {
	return cursorLineStyle(providerStyle(name), onCursor).Render(padRightVisible(name, colWidthAgent))
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
