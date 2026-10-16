package tui

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/BaronBonet/rig/internal/core"

	"charm.land/bubbles/v2/textarea"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const (
	colWidthAgent = 7
	// colWidthContext fits how full an agent's context window is, from "0%"
	// to "100%", right-aligned between a gap after the provider column and a
	// gap before the compactions column.
	colWidthContext = 6
	// colWidthCompactions fits an agent's compaction marker, from "↻1" to
	// "↻99+".
	colWidthCompactions = 4
	colWidthElapsed     = 8
	// maxCompactionsMarker is the highest count a compaction marker spells
	// out.
	maxCompactionsMarker = 99

	// rowGutterWidth is a row style's left border and padding plus the
	// scrollbar column on the right.
	rowGutterWidth = 4
	// statusSymbolWidth is a row's status symbol and the space after it.
	statusSymbolWidth = 2
	// linkMarkerWidth is a child task row's link marker and the space after
	// it, which the row takes from its name.
	linkMarkerWidth = 2
	// nameGap separates a task's name from its pull request, and the pull
	// request from the columns after it.
	nameGap = "  "
	// minTaskNameArea is the narrowest name area a task row keeps: below it,
	// the row drops its context column, then its compactions column, then its
	// provider column.
	minTaskNameArea = 40
	// taskRowFixedWidth is a task row's width outside its name area.
	taskRowFixedWidth = rowGutterWidth + statusSymbolWidth +
		colWidthAgent + colWidthContext + colWidthCompactions + colWidthElapsed
	// maxEarlierPullRequestSymbols is how many of a task's earlier pull
	// requests its row's marker shows before the current one. Older ones
	// collapse into one leading hiddenPullRequestsMarker.
	maxEarlierPullRequestSymbols = 4
	// hiddenPullRequestsMarker leads a pull requests marker that leaves some
	// of a task's earlier pull requests out.
	hiddenPullRequestsMarker = "…"
	// minNameBesideMarker is how many cells of a task's name its row keeps
	// beside its pull requests marker, which leaves out more of the earlier
	// pull requests first.
	minNameBesideMarker = 8
	// unshippedWorkMarker follows the current pull request in a task row's
	// marker when the task has unshipped work.
	unshippedWorkMarker = "+"
	// maxUnshippedWorkDetailLines caps the lines the detail panel's words
	// about unshipped work wrap onto.
	maxUnshippedWorkDetailLines = 3
	// maxPullRequestDetailLines caps the detail panel's pull request lines.
	// Past it, one line counts the earlier pull requests and the newest
	// follow.
	maxPullRequestDetailLines = 5
	// detailGutter indents the detail panel from the screen's left edge, and
	// the panel leaves as wide a margin at its right.
	detailGutter = "   "
	// detailSummaryGap separates the parts of the detail panel's summary line.
	detailSummaryGap = "    "
	// detailLabelGap separates a detail panel label, such as tokens, from its
	// value.
	detailLabelGap = "  "
	// latestUserLabel leads the user's last prompt in the detail panel.
	latestUserLabel = "you"

	taskListScrollbarTrack = "│"
	taskListScrollbarThumb = "█"
)

// listView renders the browse screen. A totalHeight of zero or less (no
// window size seen yet) means an unbounded viewport: full detail and every
// task row.
func (m model) listView() string {
	totalWidth := m.totalWidth()
	totalHeight := m.totalHeight()

	lines := []string{
		renderHeader(m.renderHeaderLabel(), m.listKeybindText(), totalWidth),
		divider(totalWidth),
	}

	if m.err != nil {
		lines = append(lines, errorStyle.Render("Error: "+m.err.Error()), "")
	}

	switch {
	case m.loading:
		lines = append(lines, dimStyle.Render("Loading tasks..."))
		lines = append(lines, sectionLines(m.listCreateStatusView(), totalWidth)...)
	case len(m.rows) == 0:
		lines = append(lines, dimStyle.Render("No tasks found."), dimStyle.Render("Press n to create one."))
		lines = append(lines, sectionLines(m.listCreateStatusView(), totalWidth)...)
	default:
		createSection := sectionLines(m.listCreateStatusView(), totalWidth)
		detailSection := sectionLines(m.selectedTaskDetailView(), totalWidth)
		rowBudget := math.MaxInt
		if totalHeight > 0 {
			createSection, detailSection, rowBudget = m.constrainedListSections(totalWidth, totalHeight, len(lines))
			if rowBudget < 0 {
				rowBudget = 0
			}
		}
		lines = append(lines, m.visibleTaskList(totalWidth, rowBudget).lines...)
		lines = append(lines, detailSection...)
		lines = append(lines, createSection...)
	}

	return joinConstrainedLines(lines, totalHeight)
}

type taskListViewport struct {
	lines      []string
	startBlock int
	endBlock   int
}

type taskListBlock struct {
	rowIndex int
	lines    []string
	// selectedLine is the index in lines of the selected task row or agent
	// sub-row, or -1 when the selection is in another block.
	selectedLine int
}

func (m model) constrainedListSections(totalWidth int, totalHeight int, baseLineCount int) ([]string, []string, int) {
	createSection := sectionLines(m.listCreateStatusView(), totalWidth)
	detailSection := sectionLines(m.selectedTaskDetailView(), totalWidth)

	availableForDetail := totalHeight - baseLineCount - len(createSection)
	if availableForDetail < 0 {
		availableForDetail = 0
	}
	if len(detailSection) > 0 {
		rowReserve := 0
		if availableForDetail > 6 {
			rowReserve = 4
		}
		maxDetailLines := availableForDetail - rowReserve
		if maxDetailLines < 0 {
			maxDetailLines = 0
		}
		if len(detailSection) > maxDetailLines {
			detailSection = withoutDanglingEnd(detailSection[:maxDetailLines])
		}
	}

	return createSection, detailSection, totalHeight - baseLineCount - len(detailSection) - len(createSection)
}

// withoutDanglingEnd drops what a cut can leave at the end of the detail
// panel with nothing under it: blank lines, and a section's header.
func withoutDanglingEnd(lines []string) []string {
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if n := len(lines); n >= 2 && lines[n-2] == "" && isDetailHeader(lines[n-1]) {
		lines = lines[:n-2]
	}
	return lines
}

// isDetailHeader reports whether a detail panel line is a section's header,
// the only lines in capitals: the tokens line also follows a blank line.
func isDetailHeader(line string) bool {
	text := strings.TrimSpace(ansi.Strip(line))
	return text != "" && text == strings.ToUpper(text) && text != strings.ToLower(text)
}

func (m model) taskListRowBudget(totalWidth int, totalHeight int) int {
	if totalHeight <= 0 {
		return 0
	}

	baseLineCount := 2
	if m.err != nil {
		baseLineCount += 2
	}

	_, _, rowBudget := m.constrainedListSections(totalWidth, totalHeight, baseLineCount)
	return rowBudget
}

func (m model) visibleTaskList(totalWidth int, budget int) taskListViewport {
	if budget <= 0 {
		return taskListViewport{}
	}

	blocks := m.taskListBlocks(totalWidth)
	if len(blocks) == 0 {
		return taskListViewport{}
	}

	selectedBlock := 0
	selectedRow := m.selected
	if selectedRow < 0 {
		selectedRow = 0
	}
	if selectedRow >= len(m.rows) {
		selectedRow = len(m.rows) - 1
	}
	for i, block := range blocks {
		if block.rowIndex == selectedRow {
			selectedBlock = i
			break
		}
	}

	start := selectedBlock
	end := selectedBlock
	if blockLineCount(blocks[start:end+1]) > budget {
		// Keep the selected line in view when a task's agent sub-rows make its
		// block taller than the budget.
		block := blocks[selectedBlock]
		offset := 0
		if block.selectedLine >= budget {
			offset = block.selectedLine - budget + 1
		}
		lines := trimBlankEdgeLines(block.lines[offset : offset+budget])
		return taskListViewport{
			lines:      withTaskListScrollbar(lines, totalWidth, selectedBlock, selectedBlock, len(blocks)),
			startBlock: selectedBlock,
			endBlock:   selectedBlock,
		}
	}

	for {
		expanded := false
		if start > 0 && blockLineCount(blocks[start-1:end+1]) <= budget {
			start--
			expanded = true
		}
		if end+1 < len(blocks) && blockLineCount(blocks[start:end+2]) <= budget {
			end++
			expanded = true
		}
		if !expanded {
			break
		}
	}

	var lines []string
	for _, block := range blocks[start : end+1] {
		lines = append(lines, block.lines...)
	}
	lines = trimBlankEdgeLines(lines)
	return taskListViewport{
		lines:      withTaskListScrollbar(lines, totalWidth, start, end, len(blocks)),
		startBlock: start,
		endBlock:   end,
	}
}

func (m model) taskListBlocks(totalWidth int) []taskListBlock {
	layout := taskRowLayoutFor(totalWidth)
	layout.names, _ = m.taskListColumns()
	blocks := make([]taskListBlock, 0, len(m.rows))
	previousRepoKey := ""
	for index, row := range m.rows {
		repoKey := repoGroupKey(row.task)
		var lines []string
		if index == 0 || repoKey != previousRepoKey {
			if index > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, m.renderRepoHeader(row.task, totalWidth))
			previousRepoKey = repoKey
		}

		rowLine := m.renderRow(index, row, layout)
		if rowLine == "" {
			continue
		}
		selectedLine := -1
		selectedAgent := ""
		if index == m.selected {
			if session := m.selectedAgentSession(); session != nil {
				selectedAgent = session.ID
			} else {
				selectedLine = len(lines)
			}
		}
		lines = append(lines, rowLine)
		subRows := agentSubRows(row)
		for i, session := range subRows {
			selected := selectedAgent != "" && session.ID == selectedAgent
			if selected {
				selectedLine = len(lines)
			}
			usage := agentRowUsage{
				context:     agentContext(row, session.ID),
				compactions: agentCompactions(row, session.ID),
			}
			lines = append(lines, m.renderAgentSubRow(session, usage, i == len(subRows)-1, selected, layout))
		}
		// A blank line sets a task with agent sub-rows apart from the tasks
		// next to it.
		if index < len(m.rows)-1 && repoGroupKey(m.rows[index+1].task) == repoKey &&
			(subRows != nil || agentSubRows(m.rows[index+1]) != nil) {
			lines = append(lines, "")
		}
		blocks = append(blocks, taskListBlock{rowIndex: index, lines: lines, selectedLine: selectedLine})
	}
	return blocks
}

func blockLineCount(blocks []taskListBlock) int {
	count := 0
	for _, block := range blocks {
		count += len(block.lines)
	}
	return count
}

func withTaskListScrollbar(lines []string, totalWidth int, startBlock int, endBlock int, totalBlocks int) []string {
	if len(lines) == 0 || totalWidth <= 1 || totalBlocks <= 0 || (startBlock <= 0 && endBlock >= totalBlocks-1) {
		return lines
	}

	visibleBlocks := endBlock - startBlock + 1
	if visibleBlocks < 1 {
		visibleBlocks = 1
	}

	thumbHeight := len(lines) * visibleBlocks / totalBlocks
	if thumbHeight < 1 {
		thumbHeight = 1
	}
	if thumbHeight > len(lines) {
		thumbHeight = len(lines)
	}

	thumbTop := 0
	scrollableBlocks := totalBlocks - visibleBlocks
	scrollableLines := len(lines) - thumbHeight
	if scrollableBlocks > 0 && scrollableLines > 0 {
		thumbTop = startBlock * scrollableLines / scrollableBlocks
	}

	rendered := make([]string, 0, len(lines))
	for i, line := range lines {
		marker := dimStyle.Render(taskListScrollbarTrack)
		if i >= thumbTop && i < thumbTop+thumbHeight {
			marker = primaryStyle.Render(taskListScrollbarThumb)
		}
		rendered = append(rendered, lineWithRightMarker(line, totalWidth, marker))
	}
	return rendered
}

func lineWithRightMarker(line string, totalWidth int, marker string) string {
	if totalWidth <= lipgloss.Width(marker) {
		return line
	}

	lineWidth := lipgloss.Width(line)
	targetWidth := totalWidth - lipgloss.Width(marker)
	if lineWidth > targetWidth {
		return line
	}

	return padRightVisible(line, targetWidth) + marker
}

func sectionLines(content string, totalWidth int) []string {
	content = strings.TrimRight(content, "\n")
	if content == "" {
		return nil
	}

	return append([]string{divider(totalWidth)}, strings.Split(content, "\n")...)
}

func joinConstrainedLines(lines []string, totalHeight int) string {
	if totalHeight > 0 && len(lines) > totalHeight {
		lines = lines[:totalHeight]
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

func trimBlankEdgeLines(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func (m model) renderRepoHeader(task *core.Task, totalWidth int) string {
	name := "unknown repo"
	if task != nil {
		name = emptyFallback(task.RepoName, name)
	}
	return headerLabelStyle.Render(truncateStr(name, totalWidth))
}

// renderRow renders a task row on one line: its status symbol, a child
// task's link marker, its name and pull requests, then its agent's provider,
// context and compactions and its elapsed time on the right.
func (m model) renderRow(index int, row taskRow, layout taskRowLayout) string {
	if row.task == nil {
		return ""
	}

	symbol, symbolStyle := m.taskRowSymbol(row)
	name := taskRowText(row)

	// A task that needs input stands out from the dimmed rest. The selected
	// task stands out further, also while the cursor is on one of its agent
	// sub-rows; the selection border, its bold name and its pull requests and
	// provider at full color mark the cursor's line. Its terse markers, a child task's link
	// marker and its agent's context and compactions, stay muted until it is
	// selected.
	nameStyle, markerStyle, timeStyle, rowStyle := dimStyle, mutedStyle, dimStyle, normalRowStyle
	if needsInput(row.status) {
		nameStyle = primaryStyle
	}
	onTaskRow := index == m.selected && m.selectedAgentSession() == nil
	if index == m.selected {
		nameStyle = lipgloss.NewStyle().Foreground(colorAccent).Bold(onTaskRow)
		markerStyle, timeStyle = primaryStyle, primaryStyle
		if onTaskRow {
			rowStyle = selectedRowStyle
		}
	}

	link := ""
	if m.parentTask(row.task) != nil {
		link = markerStyle.Render(iconChildTask)
	}

	marker := pullRequestsMarker{
		pullRequests: row.pullRequests,
		unshipped:    row.unshippedWork.Any(),
		onCursor:     onTaskRow,
	}
	line := symbolStyle.Render(symbol) + " " + renderNameAndPR(link, name, nameStyle, marker, layout)
	if layout.provider {
		// A task with agent sub-rows leaves its providers to them.
		providerCell := strings.Repeat(" ", colWidthAgent)
		if agentSubRows(row) == nil {
			providerCell = renderProviderCell(taskProviderNames(row)[0], onTaskRow)
		}
		line += providerCell
	}
	// As with the provider, a task with agent sub-rows leaves its agents'
	// context and compactions to them.
	if layout.context {
		line += renderContextCell(taskRowContext(row), markerStyle)
	}
	if layout.compactions {
		line += renderCompactionsCell(taskRowCompactions(row), markerStyle)
	}
	line += padLeftVisible(timeStyle.Render(taskElapsed(row.task)), colWidthElapsed)
	return rowStyle.Render(line)
}

// taskRowLayout is which columns a task row has room for, and how wide its
// name area is.
type taskRowLayout struct {
	nameArea int
	// names is the task list's name column, the widest of its rows' link
	// markers and names, after which each row's pull requests marker starts.
	names       int
	provider    bool
	context     bool
	compactions bool
}

// taskRowLayoutFor fits a task row's columns to the terminal: as it narrows,
// the context column goes first, the compactions column next and the provider
// column last, so the name area keeps minTaskNameArea columns for as long as
// it can.
func taskRowLayoutFor(totalWidth int) taskRowLayout {
	layout := taskRowLayout{
		nameArea:    totalWidth - taskRowFixedWidth,
		provider:    true,
		context:     true,
		compactions: true,
	}
	if layout.nameArea < minTaskNameArea {
		layout.nameArea += colWidthContext
		layout.context = false
	}
	if layout.nameArea < minTaskNameArea {
		layout.nameArea += colWidthCompactions
		layout.compactions = false
	}
	if layout.nameArea < minTaskNameArea {
		layout.nameArea += colWidthAgent
		layout.provider = false
	}
	layout.nameArea = max(layout.nameArea, 1)
	return layout
}

// taskListColumns is how wide the task list's name column is, the widest of
// its rows' link markers and names, and its pull requests column, the widest
// of their whole pull requests markers.
func (m model) taskListColumns() (names int, pullRequests int) {
	for _, row := range m.rows {
		if row.task == nil {
			continue
		}
		name := lipgloss.Width(taskRowName(row.task))
		if m.parentTask(row.task) != nil {
			name += linkMarkerWidth
		}
		names = max(names, name)
		marker := pullRequestsMarker{pullRequests: row.pullRequests, unshipped: row.unshippedWork.Any()}
		pullRequests = max(pullRequests, lipgloss.Width(pullRequestsRowText(marker, math.MaxInt)))
	}
	return names, pullRequests
}

// widestNameArea is the name area that fits the task list's name and pull
// requests columns and every agent sub-row's text, each with the gap after
// it.
func (m model) widestNameArea() int {
	names, pullRequests := m.taskListColumns()
	widest := names + len(nameGap)
	if pullRequests > 0 {
		widest += pullRequests + len(nameGap)
	}
	for _, row := range m.rows {
		// A creation's row adds its step to its name.
		if row.creating != nil {
			widest = max(widest, lipgloss.Width(taskRowText(row))+len(nameGap))
		}
		for _, session := range agentSubRows(row) {
			text, _ := agentSubRowText(session)
			widest = max(widest, subRowLead-statusSymbolWidth+lipgloss.Width(text)+len(nameGap))
		}
	}
	return widest
}

// taskRowText is what a task row shows in its name column: its name, then,
// while it is being created, its step, alone until the task is named.
func taskRowText(row taskRow) string {
	name := taskRowName(row.task)
	if row.creating == nil {
		return name
	}
	return strings.TrimSpace(name + "  " + lowerFirst(taskCreateProgressLabel(row.creating.step)))
}

// taskRowName is the name a task row shows: its display name, or its ID when
// it has none.
func taskRowName(task *core.Task) string {
	if strings.TrimSpace(task.DisplayName) == "" {
		return task.ID
	}
	return task.DisplayName
}

// pullRequestsMarker is what a task row's pull requests marker shows: the
// task's pull requests, oldest first, and whether it has unshipped work.
type pullRequestsMarker struct {
	pullRequests []core.PullRequest
	unshipped    bool
	// onCursor sets the marker for the cursor's line; see cursorLineStyle.
	onCursor bool
}

// renderNameAndPR fills a task row's name area: a child task's link marker,
// when it is given one, the name, then its pull requests' marker, which starts
// after the layout's name column where it fits, so the markers line up. The
// marker leaves out earlier pull requests rather than squeeze the name below
// minNameBesideMarker cells, and below that a long name is truncated so the
// markers and a gap after them still fit. The link marker takes its width
// from the name, so the columns on the right stay in line with the other rows,
// and it is left out when the name would keep no cell.
func renderNameAndPR(
	link string,
	name string,
	nameStyle lipgloss.Style,
	pullRequests pullRequestsMarker,
	layout taskRowLayout,
) string {
	width := layout.nameArea
	markerRoom := width - 2*len(nameGap) - min(lipgloss.Width(name), minNameBesideMarker)
	if link != "" {
		markerRoom -= linkMarkerWidth
	}
	marker := pullRequestsRowText(pullRequests, markerRoom)
	reserved := len(nameGap)
	if marker != "" {
		reserved += len(nameGap) + lipgloss.Width(marker)
	}

	text := ""
	if link != "" && width-reserved-linkMarkerWidth >= 1 {
		text = link + " "
		reserved += linkMarkerWidth
	}
	text += nameStyle.Render(truncateStr(name, width-reserved))
	if marker != "" {
		nameColumn := min(layout.names, width-2*len(nameGap)-lipgloss.Width(marker))
		text = padRightVisible(text, nameColumn) + nameGap + marker
	}
	return padRightVisible(text, width)
}

// pullRequestsRowText is a task row's pull requests marker, within room
// cells: the state symbol of each of its earlier pull requests, the ones but
// the current one, oldest first, then the current one's state symbol and
// number, each in its state's color, and unshippedWorkMarker when the task
// has unshipped work. Closed pull requests show only as the current one. It
// shows at most maxEarlierPullRequestSymbols earlier ones, and fewer when they
// do not fit, led by hiddenPullRequestsMarker when it leaves any out. The
// current one and unshippedWorkMarker always show, room or not. A task
// without pull requests has no marker.
func pullRequestsRowText(marker pullRequestsMarker, room int) string {
	pullRequests := marker.pullRequests
	current := currentPullRequest(pullRequests)
	if current < 0 {
		return ""
	}

	var earlier []string
	for index, pullRequest := range pullRequests {
		if index == current || pullRequest.State == core.PRStateClosed {
			continue
		}
		icon, style := prStateIconStyle(pullRequest.State)
		earlier = append(earlier, cursorLineStyle(style, marker.onCursor).Render(icon))
	}
	icon, style := prStateIconStyle(pullRequests[current].State)
	currentText := cursorLineStyle(style, marker.onCursor).Render(icon + pullRequestNumber(pullRequests[current]))
	if marker.unshipped {
		currentText += cursorLineStyle(unshippedWorkStyle, marker.onCursor).Render(unshippedWorkMarker)
	}

	for shown := min(len(earlier), maxEarlierPullRequestSymbols); ; shown-- {
		text := strings.Join(earlier[len(earlier)-shown:], "") + currentText
		if shown < len(earlier) {
			text = mutedStyle.Render(hiddenPullRequestsMarker) + text
		}
		if shown == 0 || lipgloss.Width(text) <= room {
			return text
		}
	}
}

// currentPullRequest returns the index of a task's current pull request among
// its pull requests, oldest first: its newest open or draft one, or else its
// newest one. It returns -1 when there are none.
func currentPullRequest(pullRequests []core.PullRequest) int {
	for index := len(pullRequests) - 1; index >= 0; index-- {
		if state := pullRequests[index].State; state == core.PRStateOpen || state == core.PRStateDraft {
			return index
		}
	}
	return len(pullRequests) - 1
}

// taskRowSymbol is the symbol a task's row leads with: a muted spinner frame
// while it is being created, the removing symbol while it is being deleted,
// the failed symbol when its creation failed, and its status symbol
// otherwise.
func (m model) taskRowSymbol(row taskRow) (string, lipgloss.Style) {
	if row.creating != nil {
		return spinnerFrames[m.spinnerFrame%len(spinnerFrames)], mutedStyle
	}
	if m.deleting[taskID(row.task)] {
		return iconRemoving, mutedStyle
	}
	if row.task != nil && row.task.CreationStatus == core.TaskCreationStatusFailed {
		return iconFailed, errorStyle
	}
	return m.statusSymbol(row.status)
}

// statusSymbol is the symbol a task or agent row shows for its status: a
// spinner frame while it works, and its status symbol otherwise.
func (m model) statusSymbol(update *core.TaskStatusUpdate) (string, lipgloss.Style) {
	symbol, style := taskStatusSymbol(update)
	if update != nil && update.Phase == core.TaskStatusPhaseWorking {
		symbol = spinnerFrames[m.spinnerFrame%len(spinnerFrames)]
	}
	return symbol, style
}

func needsInput(update *core.TaskStatusUpdate) bool {
	return update != nil && update.Phase == core.TaskStatusPhaseWaitingForInput
}

// selectedTaskDetailView describes the selected task, or the agent of the
// selected sub-row, full width: a summary line, then a section each for what
// it has of pull requests, linked tasks, its prompt and its latest exchange,
// and its token usage last.
func (m model) selectedTaskDetailView() string {
	if m.detailsHidden {
		return ""
	}
	row := m.selectedRow()
	if row == nil || row.task == nil {
		return ""
	}

	task := row.task
	width := m.totalWidth() - 2*len(detailGutter)
	agentSession := m.selectedAgentSession()

	var lines []string
	if row.creating != nil {
		creating := taskCreateProgressLabel(row.creating.step)
		lines = append(lines, fitDetailLine(mutedStyle.Render(creating), width))
	} else if task.CreationStatus == core.TaskCreationStatusFailed {
		failed := "Failed while " + lowerFirst(taskCreateProgressLabel(task.CreationStep))
		lines = append(lines, fitDetailLine(errorStyle.Render(failed), width))
		// A setup script's error goes on to its last output, which says why.
		for _, line := range wrapAndTruncate(terminalSafeText(task.CreationError), width, 4) {
			lines = append(lines, errorStyle.Render(line))
		}
	}
	lines = append(lines, packDetailParts("", m.detailSummaryParts(*row, agentSession), detailSummaryGap, width)...)

	pullRequestLines := pullRequestDetailLines(row.pullRequests, width)
	if len(row.pullRequests) > 0 && row.unshippedWork.Any() {
		pullRequestLines = append(pullRequestLines, unshippedWorkDetailLines(row.unshippedWork, width)...)
	}
	lines = appendDetailSection(lines, "PULL REQUESTS", pullRequestLines)
	lines = appendDetailSection(lines, "LINKED TASKS", m.taskLinkDetailLines(task, width))

	var promptLines []string
	for _, line := range wrapAndTruncate(task.Prompt, width, 3) {
		promptLines = append(promptLines, primaryStyle.Render(line))
	}
	lines = appendDetailSection(lines, "PROMPT", promptLines)

	if tokenLines := taskTokenUsageDetailLines(taskTokens(*row), width); len(tokenLines) > 0 {
		lines = append(append(lines, ""), tokenLines...)
	}

	// With an agent sub-row selected, the exchange is that agent's own. A task
	// row's mixes its agents', so it names a provider only when they share one.
	activity, assistantProvider := row.activity, core.Provider("")
	if providers := taskProviderNames(*row); len(providers) == 1 && providers[0] != "-" {
		assistantProvider = core.Provider(providers[0])
	}
	if agentSession != nil {
		activity, assistantProvider = agentActivity(row.activity, agentSession.ID), agentSession.Provider
	}
	lastUserPrompt, assistantItems := taskActivityPreview(activity)
	if lastUserPrompt == "" && agentSession != nil {
		lastUserPrompt = terminalSafeText(agentSession.LatestPrompt)
	}
	lines = appendDetailSection(
		lines,
		"LATEST",
		latestDetailLines(string(assistantProvider), lastUserPrompt, assistantItems, width),
	)

	lines = trimBlankEdgeLines(lines)
	for i, line := range lines {
		if line != "" {
			lines[i] = detailGutter + line
		}
	}
	return strings.Join(lines, "\n")
}

// detailSummaryParts are the parts of the detail panel's summary line: the
// model and reasoning level, context and compactions of the agent described,
// and on an agent sub-row where its pane runs.
func (m model) detailSummaryParts(row taskRow, agentSession *core.LiveAgentSession) []string {
	var parts []string
	if text := m.modelDetailText(row, agentSession); text != "" {
		parts = append(parts, text)
	}
	contextUsage, compactions := taskRowContext(row), taskRowCompactions(row)
	if agentSession != nil {
		contextUsage, compactions = agentContext(row, agentSession.ID), agentCompactions(row, agentSession.ID)
	}
	if contextUsage != nil {
		parts = append(
			parts,
			mutedStyle.Render("context")+" "+contextStyle(contextUsage).Render(contextDetailText(*contextUsage)),
		)
	}
	if compactions.Total > 0 {
		parts = append(
			parts,
			mutedStyle.Render("compacted")+" "+primaryStyle.Render(compactionsDetailText(compactions)),
		)
	}
	if agentSession != nil {
		if pane := agentPaneText(*agentSession); pane != "" {
			parts = append(parts, mutedStyle.Render(pane))
		}
	}
	return parts
}

// appendDetailSection appends a detail panel section, a blank line and its
// header over its lines, or nothing when it has no lines, as an empty
// header says nothing.
func appendDetailSection(lines []string, header string, sectionLines []string) []string {
	if len(sectionLines) == 0 {
		return lines
	}
	return append(append(lines, "", detailHeaderStyle.Render(header)), sectionLines...)
}

// packDetailParts lays parts out on lines of width columns with sep between
// the parts on a line: the first line after lead, the rest indented as far.
// A part too wide for what is left of a line moves to the next one whole,
// and one too wide even for a line of its own is truncated.
func packDetailParts(lead string, parts []string, sep string, width int) []string {
	indent := strings.Repeat(" ", lipgloss.Width(lead))
	var lines []string
	for _, part := range parts {
		if last := len(lines) - 1; last >= 0 && lipgloss.Width(lines[last]+sep+part) <= width {
			lines[last] += sep + part
			continue
		}
		prefix := indent
		if len(lines) == 0 {
			prefix = lead
		}
		lines = append(lines, fitDetailLine(prefix+part, width))
	}
	return lines
}

// latestDetailLines is the latest exchange with an agent, within width
// columns: the last user prompt after a muted "you", then the assistant's
// newest items after its provider's name, each value wrapping under its own
// start. The newest item reads brightest. An entry without text is left out.
func latestDetailLines(provider string, lastUserPrompt string, assistantItems []string, width int) []string {
	assistant := emptyFallback(provider, "assistant")
	labelWidth := max(len(latestUserLabel), len(assistant)) + len(detailLabelGap)
	valueWidth := width - labelWidth
	indent := strings.Repeat(" ", labelWidth)

	var lines []string
	label := padRightVisible(userLabelStyle.Render(latestUserLabel), labelWidth)
	for _, line := range wrapAndTruncate(lastUserPrompt, valueWidth, 3) {
		lines = append(lines, label+primaryStyle.Render(line))
		label = indent
	}

	label = padRightVisible(providerStyle(assistant).Render(assistant), labelWidth)
	for idx, item := range assistantItems {
		textStyle := dimStyle
		if idx == 0 {
			textStyle = primaryStyle
		}
		for _, line := range wrapAndTruncate(item, valueWidth, 3) {
			lines = append(lines, label+textStyle.Render(line))
			label = indent
		}
	}
	return lines
}

func taskActivityPreview(events []core.TaskActivityEvent) (string, []string) {
	if len(events) == 0 {
		return "", nil
	}

	start := -1
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Role == core.TaskActivityRoleUser && terminalSafeText(events[i].Text) != "" {
			start = i
			break
		}
	}

	lastUserPrompt := ""
	if start >= 0 {
		lastUserPrompt = terminalSafeText(events[start].Text)
	}

	assistantItems := make([]string, 0, taskActivityPreviewLimit)
	for i := len(events) - 1; i >= 0; i-- {
		if start >= 0 && i <= start {
			break
		}
		if events[i].Role != core.TaskActivityRoleAssistant {
			continue
		}
		text := terminalSafeText(events[i].Text)
		if text == "" {
			continue
		}
		assistantItems = append(assistantItems, text)
		if len(assistantItems) == taskActivityPreviewLimit {
			break
		}
	}

	return lastUserPrompt, assistantItems
}

func (m model) promptInputView() string {
	where := dimStyle.Render("no repo")
	if _, repoName, ok := m.currentRepoScope(); ok {
		where = primaryStyle.Render(repoName)
	}
	var builder strings.Builder
	builder.WriteString(m.screenHeader(mutedStyle.Render("new task")))
	builder.WriteString(errorBlock(m.draft.err))
	builder.WriteString(mutedStyle.Render("Creating in ") + where + "  " +
		keybindBar("", [2]string{"ctrl+n", "change repo"}) + "\n\n")
	builder.WriteString(m.launchChoiceView(m.totalWidth()) + "\n\n")

	m.ensurePromptInputInitialized()
	input := m.draft.input
	if input.Value() != m.draft.prompt {
		input.SetValue(m.draft.prompt)
	}
	builder.WriteString(m.renderTextBox(input))

	builder.WriteString("\n\n")
	builder.WriteString(footerKeybinds(
		[2]string{"enter", "submit"},
		[2]string{"ctrl+p", "pull requests"},
		[2]string{"esc", "cancel"},
	))

	return builder.String()
}

// textBoxWidth is the width of the box a screen draws a text input in, and
// textBoxContentWidth the width of the text inside it.
func (m model) textBoxWidth() int {
	return max(m.totalWidth()-4, 20)
}

func (m model) textBoxContentWidth() int {
	return max(m.textBoxWidth()-promptBoxStyle.GetHorizontalFrameSize(), 1)
}

// renderTextBox draws a text input in its box, sized to the screen.
func (m model) renderTextBox(input textarea.Model) string {
	input.SetWidth(m.textBoxContentWidth())
	return promptBoxStyle.Width(m.textBoxWidth()).Render(input.View())
}

func (m model) listKeybindText() string {
	if m.notice != "" {
		return healthyStyle.Render(m.notice)
	}
	binds := [][2]string{{"n", "new"}, {"m", "main"}, {"r", "refresh"}}
	if row := m.selectedRow(); row != nil && row.task != nil && row.creating == nil &&
		row.task.CreationStatus == core.TaskCreationStatusFailed {
		binds = append(binds, [2]string{"R", "retry"})
	}
	// On an agent sub-row, x removes only that agent.
	cleanup := "clean"
	if m.selectedAgentSession() != nil {
		cleanup = "remove"
	}
	binds = append(binds,
		[2]string{"c", "copy prompt"}, [2]string{"space", "details"}, [2]string{"x", cleanup}, [2]string{"q", "quit"})

	return keybindBar("   ", binds...)
}

func (m model) providerSetupView() string {
	totalWidth := m.totalWidth()

	var builder strings.Builder
	builder.WriteString(m.screenHeader(mutedStyle.Render("provider setup")) + "\n")

	builder.WriteString(primaryStyle.Bold(true).Render("Provider setup") + "\n")
	builder.WriteString(
		dimStyle.Render("Enable the AI coding providers rig may launch. At least one is required.") + "\n\n",
	)

	builder.WriteString(errorBlock(m.setupForm.err))

	if m.setupForm.detecting {
		builder.WriteString(renderShimmer("Checking provider availability...", m.shimmerTick) + "\n")
		return builder.String()
	}

	for index, row := range m.setupForm.rows {
		cursor := "  "
		if index == m.setupForm.selected {
			cursor = "> "
		}

		checkbox := "[ ]"
		if row.enabled {
			checkbox = "[x]"
		}

		name := providerStyle(string(row.provider)).Render(padRightVisible(string(row.provider), 8))
		state := healthyStyle.Render("ready")
		if !row.ready {
			checkbox = "[-]"
			state = errorStyle.Render("unavailable")
		}

		line := cursor + checkbox + " " + name + " " + state
		if row.provider == m.setupForm.defaultProvider && row.enabled {
			line += mutedStyle.Render("  (default)")
		}
		builder.WriteString(line + "\n")
		if row.showsLaunchDefault() {
			selected := index == m.setupForm.selected
			builder.WriteString(m.setupLaunchDefaultLine(row.provider, selected, totalWidth) + "\n")
		}
		if !row.ready && strings.TrimSpace(row.detail) != "" {
			for _, detailLine := range wrapAndTruncate(row.detail, totalWidth-len(setupDetailIndent), 2) {
				builder.WriteString(setupDetailIndent + dimStyle.Render(detailLine) + "\n")
			}
		}
	}

	if m.setupForm.saving {
		builder.WriteString("\n" + renderShimmer("Installing provider hooks...", m.shimmerTick) + "\n")
	}

	binds := [][2]string{{"space", "toggle"}, {"d", "default"}}
	if _, _, cycles := m.stepSetupLaunchChoice(model.nextLaunchModel); cycles {
		binds = append(binds, [2]string{"ctrl+o", "model"})
	}
	if _, _, cycles := m.stepSetupLaunchChoice(model.nextLaunchReasoningLevel); cycles {
		binds = append(binds, [2]string{"ctrl+r", "reasoning"})
	}
	binds = append(binds, [2]string{"enter", "save"}, [2]string{"q", "quit"})
	builder.WriteString("\n" + footerKeybindsWithin(totalWidth, binds...))

	return builder.String()
}

// setupDetailIndent indents the lines under a provider setup row past its
// cursor and checkbox.
const setupDetailIndent = "       "

// minSetupModelWidth is the fewest columns a model keeps on a provider setup
// line before the line drops its model list's state to make room.
const minSetupModelWidth = 5

// setupLaunchDefaultLine renders a provider's Launch default under its row
// on the provider setup screen, after a model label, or the Launch defaults'
// state while they are missing and the user has not edited it. The selected
// row's line ends with its model list's state while the list is loading or
// unavailable. Within width only the model is cut, as the reasoning level is
// what gets saved, and the state goes first when the model would have no
// room.
func (m model) setupLaunchDefaultLine(provider core.Provider, selected bool, width int) string {
	choice := m.setupLaunchChoice(provider)
	value := m.launchModelText(provider, choice.Model)
	level := ""
	if text := terminalSafeText(choice.ReasoningLevel); text != "" {
		level = mutedStyle.Render(" · ") + primaryStyle.Render(text)
	}
	if _, edited := m.setupForm.edits[provider]; !edited {
		if state := m.launchDefaultsStateText(); state != "" {
			value, level = mutedStyle.Render(state), ""
		}
	}
	suffix := ""
	if state := m.modelListStateText(provider); selected && state != "" {
		suffix = mutedStyle.Render("  (" + state + ")")
	}

	prefix := setupDetailIndent + mutedStyle.Render(launchModelLabel) + launchModelLabelGap
	room := func() int { return width - lipgloss.Width(prefix+level+suffix) }
	if room() < min(lipgloss.Width(value), minSetupModelWidth) {
		suffix = ""
	}
	if room() < 1 {
		return truncateStr(prefix+value+level, width)
	}
	return prefix + truncateStr(value, room()) + level + suffix
}

func (m model) repoDirsSetupView() string {
	form := m.repoDirsForm

	var builder strings.Builder
	builder.WriteString(m.screenHeader(mutedStyle.Render("repo directories")) + "\n")

	builder.WriteString(primaryStyle.Bold(true).Render("Repo directories") + "\n")
	explanation := "Optional. Rig lists the git repositories directly inside these directories, so agents can " +
		"find a repository by name. Worktrees are skipped. One path per line, absolute or starting with ~/."
	for _, line := range wrapAndTruncate(explanation, m.totalWidth(), 4) {
		builder.WriteString(dimStyle.Render(line) + "\n")
	}
	builder.WriteString("\n")

	builder.WriteString(errorBlock(form.err))

	if form.loading {
		builder.WriteString(renderShimmer("Loading repo directories...", m.shimmerTick) + "\n")
	} else {
		builder.WriteString(m.renderTextBox(form.input) + "\n")
	}

	if form.saving {
		builder.WriteString("\n" + renderShimmer("Saving repo directories...", m.shimmerTick) + "\n")
	}

	builder.WriteString("\n")
	builder.WriteString(footerKeybinds(
		[2]string{"enter", "save"},
		[2]string{"ctrl+j", "new line"},
		[2]string{"esc", "skip"},
	))

	return builder.String()
}

func (m model) prPickerView() string {
	totalWidth := m.totalWidth()

	headerSuffix := "PRs"
	if strings.TrimSpace(m.draft.repoName) != "" {
		headerSuffix = "PRs: " + m.draft.repoName
	}

	var builder strings.Builder
	builder.WriteString(m.screenHeader(mutedStyle.Render(headerSuffix)))
	builder.WriteString(errorBlock(m.draft.err))

	if len(m.draft.prs) == 0 {
		builder.WriteString(dimStyle.Render("No pull requests found.") + "\n\n")
		builder.WriteString(m.launchChoiceView(m.totalWidth()) + "\n\n")
		builder.WriteString(footerKeybinds([2]string{"esc", "back"}))
		return builder.String()
	}

	for i, pr := range m.draft.prs {
		title := strings.TrimSpace(pr.Title)
		if title == "" {
			title = pr.BranchName
		}

		state := string(pr.State)
		if state == "" {
			state = string(core.PRStateOpen)
		}

		meta := "#" + strconv.Itoa(pr.Number) + "  " + state + "  " + pr.BranchName
		if pr.HasExistingTask {
			meta += "  branch checked out"
		}

		titleLine := primaryStyle.Render(truncateStr(title, totalWidth-6))
		metaLine := mutedStyle.Render(truncateStr(meta, totalWidth-6))
		if pr.HasExistingTask {
			metaLine = errorStyle.Render(truncateStr(meta, totalWidth-6))
		}

		block := titleLine + "\n" + metaLine
		if i == m.draft.prSelected {
			builder.WriteString(selectedRowStyle.Render(block))
		} else {
			builder.WriteString(normalRowStyle.Render(block))
		}
		if i < len(m.draft.prs)-1 {
			builder.WriteString("\n\n")
		}
	}

	builder.WriteString("\n\n")
	builder.WriteString(m.launchChoiceView(m.totalWidth()) + "\n\n")
	builder.WriteString(footerKeybinds(
		[2]string{"enter", "create"},
		[2]string{"esc", "back"},
	))

	return builder.String()
}

func (m model) repoPickerView() string {
	var builder strings.Builder
	builder.WriteString(m.screenHeader(mutedStyle.Render("pick repo")))
	builder.WriteString(errorBlock(m.draft.err))

	switch {
	case !m.draft.reposLoaded && m.draft.err == nil:
		builder.WriteString(renderShimmer("Loading repos...", m.shimmerTick) + "\n\n")
	case m.draft.reposLoaded && len(m.draft.repos) == 0:
		builder.WriteString(dimStyle.Render("No repos found.") + "\n\n")
	}

	nameWidth := 0
	for _, repo := range m.draft.repos {
		nameWidth = max(nameWidth, lipgloss.Width(repo.Name))
	}
	// Repos that outgrow the screen scroll around the cursor, with the task
	// list's scrollbar. The 2 is the blank line and the footer below them.
	repos := m.draft.repos
	budget := len(repos)
	if m.height > 0 {
		budget = max(m.height-strings.Count(builder.String(), "\n")-2, 1)
	}
	start := min(max(m.draft.repoSelected-budget/2, 0), max(len(repos)-budget, 0))
	end := min(start+budget, len(repos))

	totalWidth := m.totalWidth()
	lines := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		line := padRightVisible(primaryStyle.Render(repos[i].Name), nameWidth) + "  " +
			mutedStyle.Render(truncateStr(repos[i].Root, totalWidth-nameWidth-8))
		if i == m.draft.repoSelected {
			lines = append(lines, selectedRowStyle.Render(line))
		} else {
			lines = append(lines, normalRowStyle.Render(line))
		}
	}
	for _, line := range withTaskListScrollbar(lines, totalWidth, start, end-1, len(repos)) {
		builder.WriteString(line + "\n")
	}

	builder.WriteString("\n")
	builder.WriteString(footerKeybinds(
		[2]string{"enter", "select"},
		[2]string{"esc", "back"},
	))
	return builder.String()
}

// listCreateStatusView is the status line under the task list while a
// session reconnects.
func (m model) listCreateStatusView() string {
	if !m.opening {
		return ""
	}
	return stepActiveLine("Reconnecting session...", m.shimmerTick)
}

func taskCreateProgressLabel(step core.TaskCreateProgressStep) string {
	switch step {
	case core.TaskCreateProgressSuggestingName:
		return "Suggesting name"
	case core.TaskCreateProgressCreatingWorktree:
		return "Creating worktree"
	case core.TaskCreateProgressPreparingWorkspace:
		return "Preparing workspace"
	case core.TaskCreateProgressStartingSession:
		return "Starting session"
	default:
		return "Creating task"
	}
}

func (m model) confirmationView() string {
	if m.cleanup.agent != nil {
		return m.removeAgentConfirmationView(*m.cleanup.agent)
	}

	var builder strings.Builder
	builder.WriteString(m.screenHeader(errorStyle.Render("cleanup")))

	if task := m.cleanup.task; task != nil {
		builder.WriteString(primaryStyle.Render(emptyFallback(task.DisplayName, task.ID)) + "\n\n")
	}

	builder.WriteString(dimStyle.Render("The tmux session and worktree will be deleted.") + "\n")
	builder.WriteString(dimStyle.Render("The branch will be kept.") + "\n\n")
	builder.WriteString(footerKeybinds(
		[2]string{"y", "confirm"},
		[2]string{"n", "cancel"},
	))

	return builder.String()
}

// removeAgentConfirmationView asks before removing one agent from its task: it
// names the task and the agent as its sub-row does, and says what goes and
// what stays.
func (m model) removeAgentConfirmationView(agent core.LiveAgentSession) string {
	var builder strings.Builder
	builder.WriteString(m.screenHeader(errorStyle.Render("remove agent")))

	if task := m.cleanup.task; task != nil {
		builder.WriteString(primaryStyle.Render(emptyFallback(task.DisplayName, task.ID)) + "\n")
	}
	provider := emptyFallback(string(agent.Provider), "-")
	lead := subRowIndent + "└ " + provider + "  "
	text, _ := agentSubRowText(agent)
	text = truncateStr(text, max(0, m.totalWidth()-lipgloss.Width(lead)))
	builder.WriteString(
		dimStyle.Render(subRowIndent+"└ ") + providerStyle(provider).Render(provider) + "  " +
			primaryStyle.Render(text) + "\n\n",
	)

	builder.WriteString(dimStyle.Render("The agent's tmux pane will be closed, which stops the agent.") + "\n")
	builder.WriteString(dimStyle.Render("The task, its worktree and its other agents will be kept.") + "\n\n")
	builder.WriteString(footerKeybinds(
		[2]string{"y", "confirm"},
		[2]string{"n", "cancel"},
	))

	return builder.String()
}

// taskStatusSymbol is the one-cell symbol task and agent rows show for a
// status, and its style. Each state has its own symbol, so a row reads
// without a label.
func taskStatusSymbol(update *core.TaskStatusUpdate) (string, lipgloss.Style) {
	if update == nil {
		return iconStatusNone, dimStyle
	}

	switch update.Phase {
	case core.TaskStatusPhaseStarting:
		return iconStatusStarting, warningStyle
	case core.TaskStatusPhaseWorking:
		return iconStatusActive, healthyStyle
	case core.TaskStatusPhaseWorkingInBackground:
		return iconStatusProgress, healthyStyle
	case core.TaskStatusPhaseWaitingForInput:
		return iconStatusNeedsInput, warningStyle
	case core.TaskStatusPhaseStopped:
		return iconStatusIdle, dimStyle
	default:
		return iconStatusIdle, dimStyle
	}
}

func countNoun(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(count) + " " + noun + "s"
}

// pullRequestDetailLines lists a task's pull requests, oldest first, in the
// detail panel's pull requests section, within width columns, one a line.
// Each line has the pull request's state symbol, number and state in its
// state's color, then its head branch, muted. Past maxPullRequestDetailLines
// pull requests, the first line counts the earlier ones, muted, and the
// newest follow.
func pullRequestDetailLines(pullRequests []core.PullRequest, width int) []string {
	if len(pullRequests) == 0 {
		return nil
	}

	var lines []string
	shown := pullRequests
	if len(pullRequests) > maxPullRequestDetailLines {
		shown = pullRequests[len(pullRequests)-maxPullRequestDetailLines+1:]
		earlier := strconv.Itoa(len(pullRequests)-len(shown)) + " earlier"
		lines = append(lines, fitDetailLine(mutedStyle.Render(earlier), width))
	}

	numberWidth, stateWidth := 0, 0
	for _, pullRequest := range shown {
		numberWidth = max(numberWidth, len(pullRequestNumber(pullRequest)))
		stateWidth = max(stateWidth, len(pullRequest.State))
	}
	for _, pullRequest := range shown {
		icon, style := prStateIconStyle(pullRequest.State)
		text := icon + " " + padRightVisible(pullRequestNumber(pullRequest), numberWidth) + " " +
			padRightVisible(string(pullRequest.State), stateWidth)
		line := style.Render(text) + nameGap + mutedStyle.Render(pullRequest.HeadBranch)
		lines = append(lines, fitDetailLine(line, width))
	}
	return lines
}

func pullRequestNumber(pullRequest core.PullRequest) string {
	return "#" + strconv.Itoa(pullRequest.Number)
}

// unshippedWorkDetailLines says in words, under the detail panel's pull
// requests, what unshipped work a task has, after the unshippedWorkMarker its
// row shows. Too wide for width columns, the words break before "not in a
// pull request", and wrap on lines lined up after the marker, as cutting them
// short would cut the sentence's point.
func unshippedWorkDetailLines(work core.UnshippedWork, width int) []string {
	var parts []string
	if work.Commits > 0 {
		parts = append(parts, countNoun(work.Commits, "commit"))
	}
	if work.UncommittedChanges {
		parts = append(parts, "uncommitted changes")
	}
	what := strings.Join(parts, " and ")
	const where = "not in a pull request"

	prefix := unshippedWorkMarker + " "
	room := width - len(prefix)
	wrapped := []string{what + " " + where}
	if width > 0 && lipgloss.Width(wrapped[0]) > room {
		wrapped = append(wrapAndTruncate(what, room, maxUnshippedWorkDetailLines-1), where)
	}
	lines := make([]string, 0, len(wrapped))
	for index, line := range wrapped {
		if index > 0 {
			prefix = strings.Repeat(" ", len(prefix))
		}
		lines = append(lines, fitDetailLine(unshippedWorkStyle.Render(prefix+line), width))
	}
	return lines
}

// taskTokens is a task's token usage, or nil before its usage has loaded.
func taskTokens(row taskRow) *core.TaskTokenUsage {
	if row.usage == nil {
		return nil
	}
	return row.usage.Tokens
}

// taskTokenUsageDetailLines is the detail panel's token usage: its fields
// after a muted tokens label, wrapping whole within width columns onto lines
// lined up under the first field.
func taskTokenUsageDetailLines(usage *core.TaskTokenUsage, width int) []string {
	if usage == nil || usage.TotalTokens <= 0 {
		return nil
	}

	sessionLabel := "session"
	if usage.SessionCount != 1 {
		sessionLabel = "sessions"
	}

	fields := []string{
		mutedStyle.Render("total ") + primaryStyle.Bold(true).Render(formatTokenCount(usage.TotalTokens)),
	}
	fields = appendPositiveTokenUsageField(fields, "input", usage.InputTokens)
	fields = appendPositiveTokenUsageField(fields, "output", usage.OutputTokens)
	fields = appendPositiveTokenUsageField(fields, "cached", usage.CachedInputTokens)
	fields = appendPositiveTokenUsageField(fields, "cache created", usage.CacheCreationInputTokens)
	fields = appendPositiveTokenUsageField(fields, "reasoning", usage.ReasoningOutputTokens)
	fields = append(fields, mutedStyle.Render(strconv.Itoa(usage.SessionCount)+" "+sessionLabel))

	return packDetailParts(mutedStyle.Render("tokens")+detailLabelGap, fields, mutedStyle.Render(" · "), width)
}

func appendPositiveTokenUsageField(fields []string, label string, tokens int) []string {
	if tokens <= 0 {
		return fields
	}
	return append(fields, tokenUsageField(label, tokens))
}

func tokenUsageField(label string, tokens int) string {
	return mutedStyle.Render(label+" ") + primaryStyle.Render(formatTokenCount(tokens))
}

func formatTokenCount(tokens int) string {
	if tokens < 1000 {
		return strconv.Itoa(tokens)
	}
	if tokens < 1000000 {
		return fmt.Sprintf("%.1fk", float64(tokens)/1000)
	}
	return fmt.Sprintf("%.1fm", float64(tokens)/1000000)
}

func prStateIconStyle(state core.PRState) (string, lipgloss.Style) {
	switch state {
	case core.PRStateOpen:
		return iconStatusActive, healthyStyle
	case core.PRStateDraft:
		return iconStatusProgress, warningStyle
	case core.PRStateMerged:
		return iconDone, prMergedStyle
	case core.PRStateClosed:
		return iconFailed, errorStyle
	default:
		return iconPRNone, mutedStyle
	}
}

func taskElapsed(task *core.Task) string {
	if task == nil || task.CreatedAt.IsZero() {
		return ""
	}
	return formatElapsed(time.Since(task.CreatedAt))
}

// formatElapsed shows a duration's two largest units, from days to minutes,
// with the second zero-padded so that, right-aligned, the units line up.
func formatElapsed(duration time.Duration) string {
	minutes := max(int(duration.Minutes()), 0)
	hours, days := minutes/60, minutes/(24*60)
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %02dh", days, hours%24)
	case hours > 0:
		return fmt.Sprintf("%dh %02dm", hours, minutes%60)
	}
	return fmt.Sprintf("%dm", minutes)
}

func renderHeader(left string, right string, totalWidth int) string {
	gap := totalWidth - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 2 {
		gap = 2
	}
	return left + strings.Repeat(" ", gap) + right
}

// screenHeader is the top of every screen: the RIG header with a
// right-aligned label, over a divider.
func (m model) screenHeader(right string) string {
	totalWidth := m.totalWidth()
	return renderHeader(m.renderHeaderLabel(), right, totalWidth) + "\n" + divider(totalWidth) + "\n"
}

func divider(totalWidth int) string {
	return dividerStyle.Render(strings.Repeat("─", totalWidth))
}

// errorBlock renders a user-facing error line followed by a blank line, or
// nothing when err is nil.
func errorBlock(err error) string {
	if err == nil {
		return ""
	}
	return errorStyle.Render("Error: "+err.Error()) + "\n\n"
}

// keybindBar renders key/action pairs in the shared keybind style, joined by
// sep.
func keybindBar(sep string, binds ...[2]string) string {
	parts := make([]string, 0, len(binds))
	for _, bind := range binds {
		parts = append(parts, keybindStyle.Render(bind[0])+" "+mutedStyle.Render(bind[1]))
	}
	return strings.Join(parts, mutedStyle.Render(sep))
}

// footerKeybinds is the keybind bar at the bottom of the modal screens.
func footerKeybinds(binds ...[2]string) string {
	return keybindBar(" · ", binds...)
}

// footerKeybindsWithin is footerKeybinds broken between keys onto more lines
// where one line would run wider than width.
func footerKeybindsWithin(width int, binds ...[2]string) string {
	var lines []string
	var line [][2]string
	for _, bind := range binds {
		if len(line) > 0 && lipgloss.Width(footerKeybinds(append(line, bind)...)) > width {
			lines = append(lines, footerKeybinds(line...))
			line = nil
		}
		line = append(line, bind)
	}
	return strings.Join(append(lines, footerKeybinds(line...)), "\n")
}

// stepActiveLine is a long-running operation's status line: a colored dot
// followed by its shimmering label.
func stepActiveLine(label string, tick int) string {
	return warningStyle.Render(iconStatusActive) + " " + renderShimmer(label, tick)
}

func (m model) renderHeaderLabel() string {
	return headerLabelStyle.Render("RIG") + " " + mutedStyle.Render(m.buildVersion)
}

// truncateStr caps value at max visible columns, ending with a one-column
// ellipsis when it truncates.
func truncateStr(value string, max int) string {
	if max <= 0 {
		return ""
	}
	if lipgloss.Width(value) <= max {
		return value
	}
	return clipVisible(value, max-1) + "…"
}

func padRightVisible(value string, width int) string {
	visible := lipgloss.Width(value)
	if visible >= width {
		return value
	}
	return value + strings.Repeat(" ", width-visible)
}

func padLeftVisible(value string, width int) string {
	visible := lipgloss.Width(value)
	if visible >= width {
		return value
	}
	return strings.Repeat(" ", width-visible) + value
}

func wrapAndTruncate(text string, width int, maxLines int) []string {
	if text == "" {
		return nil
	}
	if width <= 0 {
		return nil
	}

	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}

	lines := []string{truncateVisible(words[0], width)}
	for _, word := range words[1:] {
		current := lines[len(lines)-1]
		if lipgloss.Width(current)+1+lipgloss.Width(word) > width {
			lines = append(lines, truncateVisible(word, width))
			continue
		}
		lines[len(lines)-1] = current + " " + word
	}

	if len(lines) > maxLines {
		lines = lines[:maxLines]
		lines[maxLines-1] = truncateVisible(lines[maxLines-1]+"...", width)
	}

	return lines
}

// truncateVisible caps value at max visible columns, ending with "..." when
// it truncates.
func truncateVisible(value string, max int) string {
	if max <= 0 {
		return ""
	}
	if lipgloss.Width(value) <= max {
		return value
	}
	if max <= 3 {
		return strings.Repeat(".", max)
	}
	return clipVisible(value, max-3) + "..."
}

// clipVisible cuts value to at most max visible columns. It measures
// graphemes as lipgloss.Width does, so an emoji with a variation selector
// counts as wide, and it keeps styling escape sequences whole.
func clipVisible(value string, max int) string {
	if max <= 0 {
		return ""
	}
	return ansi.Truncate(value, max, "")
}

func emptyFallback(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func lowerFirst(value string) string {
	if value == "" {
		return ""
	}
	return strings.ToLower(value[:1]) + value[1:]
}
