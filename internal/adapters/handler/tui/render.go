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
			detailSection = detailSection[:maxDetailLines]
		}
	}

	return createSection, detailSection, totalHeight - baseLineCount - len(detailSection) - len(createSection)
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

		rowLine := m.renderRow(index, row, totalWidth)
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
			lines = append(lines, m.renderAgentSubRow(session, usage, i == len(subRows)-1, selected, totalWidth))
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
func (m model) renderRow(index int, row taskRow, totalWidth int) string {
	if row.task == nil {
		return ""
	}

	symbol, symbolStyle := m.taskRowSymbol(row)

	name := row.task.DisplayName
	if strings.TrimSpace(name) == "" {
		name = row.task.ID
	}

	// A task that needs input stands out from the dimmed rest. The selected
	// task stands out further, also while the cursor is on one of its agent
	// sub-rows; the selection border and bold name mark the cursor's line.
	// Its terse markers, a child task's link marker and its agent's context
	// and compactions, stay muted until it is selected.
	nameStyle, markerStyle, timeStyle, rowStyle := dimStyle, mutedStyle, dimStyle, normalRowStyle
	if needsInput(row.status) {
		nameStyle = primaryStyle
	}
	if index == m.selected {
		onTaskRow := m.selectedAgentSession() == nil
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

	layout := taskRowLayoutFor(totalWidth)
	marker := pullRequestsMarker{pullRequests: row.pullRequests, unshipped: row.unshippedWork.Any()}
	line := symbolStyle.Render(symbol) + " " + renderNameAndPR(link, name, nameStyle, marker, layout.nameArea)
	if layout.provider {
		// A task with agent sub-rows leaves its providers to them.
		providerCell := strings.Repeat(" ", colWidthAgent)
		if agentSubRows(row) == nil {
			providerCell = renderProviderCell(taskProviderNames(row)[0])
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
	nameArea    int
	provider    bool
	context     bool
	compactions bool
}

// taskRowLayoutFor fits a task row's columns to the terminal: as it narrows,
// the context column goes first, the compactions column next and the provider
// column last, so the name area keeps minTaskNameArea columns for as long as
// it can.
func taskRowLayoutFor(totalWidth int) taskRowLayout {
	fixedWidth := rowGutterWidth + statusSymbolWidth +
		colWidthAgent + colWidthContext + colWidthCompactions + colWidthElapsed
	layout := taskRowLayout{
		nameArea:    totalWidth - fixedWidth,
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

// pullRequestsMarker is what a task row's pull requests marker shows: the
// task's pull requests, oldest first, and whether it has unshipped work.
type pullRequestsMarker struct {
	pullRequests []core.PullRequest
	unshipped    bool
}

// renderNameAndPR fills a task row's name area: a child task's link marker,
// when it is given one, the name, then its pull requests' marker. The marker
// leaves out earlier pull requests rather than squeeze the name below
// minNameBesideMarker cells, and below that a long name is truncated so the
// markers and a gap after them still fit. The link marker takes its width
// from the name, so the columns on the right stay in line with the other rows,
// and it is left out when the name would keep no cell.
func renderNameAndPR(
	link string,
	name string,
	nameStyle lipgloss.Style,
	pullRequests pullRequestsMarker,
	width int,
) string {
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
		text += nameGap + marker
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
		earlier = append(earlier, style.Render(icon))
	}
	icon, style := prStateIconStyle(pullRequests[current].State)
	currentText := style.Render(icon + pullRequestNumber(pullRequests[current]))
	if marker.unshipped {
		currentText += unshippedWorkStyle.Render(unshippedWorkMarker)
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

// taskRowSymbol is the symbol a task's row leads with: the failed symbol when
// its creation failed, and its status symbol otherwise.
func (m model) taskRowSymbol(row taskRow) (string, lipgloss.Style) {
	if row.task != nil && row.task.CreationStatus == core.TaskCreationStatusFailed {
		return iconFailed, errorStyle
	}
	return m.statusSymbol(row.status)
}

// statusSymbol is the symbol a task or agent row shows for its status: a
// spinner frame while it works, and its status symbol otherwise.
func (m model) statusSymbol(update *core.TaskStatusUpdate) (string, lipgloss.Style) {
	symbol, _, style := taskStatusParts(update)
	if update != nil && update.Phase == core.TaskStatusPhaseWorking {
		symbol = spinnerFrames[m.spinnerFrame%len(spinnerFrames)]
	}
	return symbol, style
}

func needsInput(update *core.TaskStatusUpdate) bool {
	return update != nil && update.Phase == core.TaskStatusPhaseWaitingForInput
}

func (m model) selectedTaskDetailView() string {
	if m.detailsHidden {
		return ""
	}
	row := m.selectedRow()
	if row == nil || row.task == nil {
		return ""
	}

	task := row.task
	totalWidth := m.totalWidth()
	detailColWidth := totalWidth * 45 / 100
	if detailColWidth < 42 {
		detailColWidth = 42
	}

	workspaceLines := []string{
		headerLabelStyle.Render("WORKSPACE"),
	}
	if strings.TrimSpace(task.BranchName) != "" {
		workspaceLines = append(
			workspaceLines,
			mutedStyle.Render("branch")+" "+primaryStyle.Render(task.BranchName),
		)
	}
	if strings.TrimSpace(task.RepoName) != "" {
		workspaceLines = append(
			workspaceLines,
			mutedStyle.Render("repo")+"   "+primaryStyle.Render(task.RepoName),
		)
	}
	if strings.TrimSpace(task.WorktreePath) != "" {
		workspaceLines = append(
			workspaceLines,
			mutedStyle.Render("path")+"   "+dimStyle.Render(task.WorktreePath),
		)
	}
	workspaceLines = append(workspaceLines, pullRequestDetailLines(row.pullRequests, detailColWidth)...)
	if len(row.pullRequests) > 0 && row.unshippedWork.Any() {
		workspaceLines = append(workspaceLines, unshippedWorkDetailLines(row.unshippedWork, detailColWidth)...)
	}
	workspaceLines = append(workspaceLines, m.taskLinkDetailLines(task, detailColWidth)...)

	sessionLines := []string{
		headerLabelStyle.Render("SESSION"),
	}
	if task.CreationStatus == core.TaskCreationStatusFailed {
		sessionLines = append(
			sessionLines,
			errorStyle.Render("Failed while "+lowerFirst(taskCreateProgressLabel(task.CreationStep))),
		)
		if creationErr := strings.TrimSpace(task.CreationError); creationErr != "" {
			sessionLines = append(sessionLines, errorStyle.Render(creationErr))
		}
	}
	if elapsed := taskElapsed(task); elapsed != "" {
		sessionLines = append(
			sessionLines,
			mutedStyle.Render("time")+"   "+primaryStyle.Bold(true).Render(elapsed)+mutedStyle.Render(" total"),
		)
	}
	// With an agent sub-row selected, the session column describes that agent;
	// on the task row, it names the providers of the task's agents and the
	// task's Launch choice.
	sessionWidth := totalWidth - detailColWidth - 6
	status := row.status
	providers := taskProviderNames(*row)
	agentSession := m.selectedAgentSession()
	if agentSession != nil {
		status = agentSessionStatusUpdate(*agentSession)
		providers = []string{emptyFallback(string(agentSession.Provider), "-")}
	}
	if statusText, statusStyle := taskStatusDetailText(status); statusText != "" {
		sessionLines = append(sessionLines, detailFieldLines("state", "  ", statusText, statusStyle, sessionWidth)...)
	}
	if providers[0] != "-" {
		sessionLines = append(
			sessionLines,
			fitDetailLine(mutedStyle.Render("provider")+"  "+renderProviderNames(providers), sessionWidth),
		)
	}
	contextUsage, compactions := taskRowContext(*row), taskRowCompactions(*row)
	if agentSession != nil {
		if pane := agentPaneText(*agentSession); pane != "" {
			sessionLines = append(sessionLines, detailFieldLines("pane", "   ", pane, dimStyle, sessionWidth)...)
		}
		contextUsage, compactions = agentContext(*row, agentSession.ID), agentCompactions(*row, agentSession.ID)
	} else {
		sessionLines = append(sessionLines, m.launchChoiceDetailLines(task, sessionWidth)...)
	}
	if contextUsage != nil {
		sessionLines = append(
			sessionLines,
			detailFieldLines("context", "  ", contextDetailText(*contextUsage), primaryStyle, sessionWidth)...,
		)
	}
	if compactions.Total > 0 {
		sessionLines = append(
			sessionLines,
			detailFieldLines("compacted", "  ", compactionsDetailText(compactions), primaryStyle, sessionWidth)...,
		)
	}

	var builder strings.Builder
	for _, line := range zipColumns(workspaceLines, sessionLines, detailColWidth) {
		builder.WriteString(line + "\n")
	}

	if tokenLines := taskTokenUsageDetailLines(taskTokens(*row)); len(tokenLines) > 0 {
		builder.WriteString("\n")
		for _, line := range tokenLines {
			builder.WriteString("   " + line + "\n")
		}
	}

	if strings.TrimSpace(task.Prompt) != "" {
		builder.WriteString("\n")
		builder.WriteString("   " + headerLabelStyle.Render("INITIAL PROMPT") + "\n")
		for _, line := range wrapAndTruncate(task.Prompt, totalWidth-6, 3) {
			builder.WriteString("   " + primaryStyle.Render(line) + "\n")
		}
	}

	// With an agent sub-row selected, the activity is that agent's own.
	activity, assistantProvider := row.activity, task.Provider
	if agentSession != nil {
		activity, assistantProvider = agentActivity(row.activity, agentSession.ID), agentSession.Provider
	}
	lastUserPrompt, assistantItems := taskActivityPreview(activity)
	if lastUserPrompt == "" && agentSession != nil {
		lastUserPrompt = terminalSafeText(agentSession.LatestPrompt)
	}
	if lastUserPrompt != "" || len(assistantItems) > 0 {
		builder.WriteString("\n")
		builder.WriteString("   " + headerLabelStyle.Render("ACTIVITY") + "\n")

		leftWidth := totalWidth * 35 / 100
		if leftWidth < 24 {
			leftWidth = 24
		}
		rightWidth := totalWidth - leftWidth - 9
		if rightWidth < 24 {
			rightWidth = 24
			leftWidth = totalWidth - rightWidth - 9
		}

		leftLines := []string{mutedStyle.Render("last user prompt")}
		if lastUserPrompt != "" {
			for _, line := range wrapAndTruncate(lastUserPrompt, leftWidth, 5) {
				leftLines = append(leftLines, primaryStyle.Render(line))
			}
		} else {
			leftLines = append(leftLines, dimStyle.Render("No prompt yet."))
		}

		rightLines := []string{providerStyle(string(assistantProvider)).Render("assistant")}
		if len(assistantItems) == 0 {
			rightLines = append(rightLines, dimStyle.Render("No assistant activity yet."))
		} else {
			for idx, item := range assistantItems {
				textStyle := dimStyle
				if idx == 0 {
					textStyle = primaryStyle
				}
				for _, line := range wrapAndTruncate(item, rightWidth, 3) {
					rightLines = append(rightLines, textStyle.Render(line))
				}
				if idx < len(assistantItems)-1 {
					rightLines = append(rightLines, "")
				}
			}
		}

		for _, line := range zipColumns(leftLines, rightLines, leftWidth) {
			builder.WriteString(line + "\n")
		}
	}

	return strings.TrimRight(builder.String(), "\n")
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
	var builder strings.Builder
	builder.WriteString(m.screenHeader(mutedStyle.Render("new task")))
	builder.WriteString(errorBlock(m.draft.err))
	builder.WriteString(dimStyle.Render("Enter task prompt.") + "\n\n")
	builder.WriteString(m.launchChoiceView(m.totalWidth()) + "\n\n")

	m.ensurePromptInputInitialized()
	input := m.draft.input
	if input.Value() != m.draft.prompt {
		input.SetValue(m.draft.prompt)
	}
	builder.WriteString(m.renderTextBox(input))

	if progress := m.renderCreateProgress(); progress != "" {
		builder.WriteString("\n\n" + progress)
	}

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
	binds := [][2]string{{"n", "new"}, {"r", "refresh"}}
	if row := m.selectedRow(); row != nil && row.task != nil &&
		row.task.CreationStatus == core.TaskCreationStatusFailed {
		binds = append(binds, [2]string{"R", "retry"})
	}
	binds = append(binds, [2]string{"space", "details"}, [2]string{"x", "clean"}, [2]string{"q", "quit"})

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
// on the provider setup screen, labeled as the detail panel's model line, or
// the Launch defaults' state while they are missing and the user has not
// edited it. The selected row's line ends with its model list's state while
// the list is loading or unavailable. Within width only the model is cut, as
// the reasoning level is what gets saved, and the state goes first when the
// model would have no room.
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

func (m model) renderCreateProgress() string {
	if m.create.fromPR {
		if m.pending != opCreating && m.create.err == nil {
			return ""
		}
		label := "Creating task from pull request"
		switch {
		case m.pending == opCreating:
			return stepActiveLine(label, m.shimmerTick)
		case m.create.err != nil:
			return stepFailedLine(label)
		default:
			return ""
		}
	}

	if m.pending != opCreating && len(m.create.done) == 0 && m.create.active == "" {
		return ""
	}

	steps := []core.TaskCreateProgressStep{
		core.TaskCreateProgressSuggestingName,
		core.TaskCreateProgressCreatingWorktree,
		core.TaskCreateProgressPreparingWorkspace,
		core.TaskCreateProgressStartingSession,
	}

	var lines []string
	for _, step := range steps {
		label := taskCreateProgressLabel(step)
		switch {
		case containsCreateStep(m.create.done, step):
			lines = append(lines, stepDoneLine(label))
		case step == m.create.active && m.pending == opCreating:
			lines = append(lines, stepActiveLine(label, m.shimmerTick))
		case step == m.create.active && m.create.err != nil:
			lines = append(lines, stepFailedLine(label))
		default:
			lines = append(lines, stepPendingLine(label))
		}
	}

	return strings.Join(lines, "\n")
}

func (m model) listCreateStatusView() string {
	var lines []string
	if m.opening {
		lines = append(lines, stepActiveLine("Reconnecting session...", m.shimmerTick))
	}
	if m.create.err != nil {
		lines = append(lines, errorStyle.Render("Error: "+m.create.err.Error()))
	}
	if progress := m.renderCreateProgress(); progress != "" {
		lines = append(lines, progress)
	}
	return strings.Join(lines, "\n\n")
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
	var builder strings.Builder
	builder.WriteString(m.screenHeader(errorStyle.Render("cleanup")))

	if row := m.selectedRow(); row != nil && row.task != nil {
		builder.WriteString(primaryStyle.Render(emptyFallback(row.task.DisplayName, row.task.ID)) + "\n\n")
	}

	builder.WriteString(dimStyle.Render("The tmux session and worktree will be deleted.") + "\n")
	builder.WriteString(dimStyle.Render("The branch will be kept.") + "\n\n")
	if m.pending == opDeleting {
		builder.WriteString(stepActiveLine("Cleaning up task...", m.shimmerTick) + "\n\n")
	}
	builder.WriteString(footerKeybinds(
		[2]string{"y", "confirm"},
		[2]string{"n", "cancel"},
	))

	return builder.String()
}

// taskStatusParts is how a status reads: the one-cell symbol task and agent
// rows show for it, the label the detail panel adds, and their style. Each
// state has its own symbol, so a row reads without its label.
func taskStatusParts(update *core.TaskStatusUpdate) (string, string, lipgloss.Style) {
	if update == nil {
		return iconStatusNone, "no status", dimStyle
	}

	switch update.Phase {
	case core.TaskStatusPhaseStarting:
		return iconStatusStarting, "starting", warningStyle
	case core.TaskStatusPhaseWorking:
		if indicatesCommandActivity(update.RawEventName) {
			return iconStatusActive, "working · command", healthyStyle
		}
		return iconStatusActive, "working", healthyStyle
	case core.TaskStatusPhaseWorkingInBackground:
		return iconStatusProgress, "working in background", healthyStyle
	case core.TaskStatusPhaseWaitingForInput:
		return iconStatusNeedsInput, "needs input", warningStyle
	case core.TaskStatusPhaseStopped:
		return iconStatusIdle, "stopped", dimStyle
	default:
		return iconStatusIdle, "idle", dimStyle
	}
}

// taskStatusDetailText is the status as the detail panel states it: its
// symbol and label, with background work broken down by kind.
func taskStatusDetailText(update *core.TaskStatusUpdate) (string, lipgloss.Style) {
	symbol, label, style := taskStatusParts(update)
	text := symbol + " " + label
	if update != nil && update.Phase == core.TaskStatusPhaseWorkingInBackground {
		if parts := backgroundWorkParts(update.BackgroundWork); len(parts) > 0 {
			text += " · " + strings.Join(parts, ", ")
		}
	}
	return text, style
}

func backgroundWorkParts(work core.TaskBackgroundWork) []string {
	kinds := []struct {
		noun  string
		count int
	}{
		{"subagent", work.Subagents},
		{"monitor", work.Monitors},
		{"workflow", work.Workflows},
		{"shell", work.Shells},
		{"bg task", work.Other},
	}

	var parts []string
	for _, kind := range kinds {
		if kind.count > 0 {
			parts = append(parts, countNoun(kind.count, kind.noun))
		}
	}
	return parts
}

func countNoun(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(count) + " " + noun + "s"
}

// pullRequestDetailLines lists a task's pull requests, oldest first, in the
// detail panel's workspace column, within width columns: one a line, the
// first after the pr label and the rest lined up under it. Each line has the
// pull request's state symbol, number and state in its state's color, then
// its head branch, muted. Past maxPullRequestDetailLines pull requests, the
// first line counts the earlier ones, muted, and the newest follow.
func pullRequestDetailLines(pullRequests []core.PullRequest, width int) []string {
	if len(pullRequests) == 0 {
		return nil
	}

	label := mutedStyle.Render("pr") + "     "
	var lines []string
	shown := pullRequests
	if len(pullRequests) > maxPullRequestDetailLines {
		shown = pullRequests[len(pullRequests)-maxPullRequestDetailLines+1:]
		earlier := strconv.Itoa(len(pullRequests)-len(shown)) + " earlier"
		lines = append(lines, fitDetailLine(label+mutedStyle.Render(earlier), width))
		label = detailValueIndent
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
		line := label + style.Render(text) + nameGap + mutedStyle.Render(pullRequest.HeadBranch)
		lines = append(lines, fitDetailLine(line, width))
		label = detailValueIndent
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
	room := width - len(detailValueIndent) - len(prefix)
	wrapped := []string{what + " " + where}
	if width > 0 && lipgloss.Width(wrapped[0]) > room {
		wrapped = append(wrapAndTruncate(what, room, maxUnshippedWorkDetailLines-1), where)
	}
	lines := make([]string, 0, len(wrapped))
	for index, line := range wrapped {
		if index > 0 {
			prefix = strings.Repeat(" ", len(prefix))
		}
		lines = append(lines, fitDetailLine(detailValueIndent+unshippedWorkStyle.Render(prefix+line), width))
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

func taskTokenUsageDetailLines(usage *core.TaskTokenUsage) []string {
	if usage == nil || usage.TotalTokens <= 0 {
		return nil
	}

	sessionLabel := "session"
	if usage.SessionCount != 1 {
		sessionLabel = "sessions"
	}

	fields := []string{
		tokenUsageField("total", usage.TotalTokens),
	}
	fields = appendPositiveTokenUsageField(fields, "input", usage.InputTokens)
	fields = appendPositiveTokenUsageField(fields, "output", usage.OutputTokens)
	fields = appendPositiveTokenUsageField(fields, "cached", usage.CachedInputTokens)
	fields = appendPositiveTokenUsageField(fields, "cache created", usage.CacheCreationInputTokens)
	fields = appendPositiveTokenUsageField(fields, "reasoning", usage.ReasoningOutputTokens)
	fields = append(fields, mutedStyle.Render(strconv.Itoa(usage.SessionCount)+" "+sessionLabel))

	return []string{
		headerLabelStyle.Render("TOKENS"),
		strings.Join(fields, "   "),
	}
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

func indicatesCommandActivity(rawEventName string) bool {
	switch rawEventName {
	case "PreToolUse", "PostToolUse":
		return true
	default:
		return false
	}
}

func taskElapsed(task *core.Task) string {
	if task == nil || task.CreatedAt.IsZero() {
		return ""
	}
	return formatElapsed(time.Since(task.CreatedAt))
}

func formatElapsed(duration time.Duration) string {
	if duration < 0 {
		duration = 0
	}
	hours := int(duration.Hours())
	minutes := int(duration.Minutes()) % 60
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
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

// zipColumns lays two line columns side by side: a three-space indent, the
// left column padded to leftWidth, a three-space gutter, then the right.
func zipColumns(left []string, right []string, leftWidth int) []string {
	lines := make([]string, 0, max(len(left), len(right)))
	for i := range max(len(left), len(right)) {
		leftLine, rightLine := "", ""
		if i < len(left) {
			leftLine = left[i]
		}
		if i < len(right) {
			rightLine = right[i]
		}
		lines = append(lines, "   "+padRightVisible(leftLine, leftWidth)+"   "+rightLine)
	}
	return lines
}

// Status-dot lines: a colored dot followed by a styled label, the shared
// shape for progress steps and long-running-operation status rows.
func stepDoneLine(label string) string {
	return healthyStyle.Render(iconStatusActive) + " " + primaryStyle.Render(label)
}

func stepActiveLine(label string, tick int) string {
	return warningStyle.Render(iconStatusActive) + " " + renderShimmer(label, tick)
}

func stepFailedLine(label string) string {
	return errorStyle.Render(iconStatusActive) + " " + errorStyle.Render(label)
}

func stepPendingLine(label string) string {
	return dimStyle.Render(iconStatusIdle + " " + label)
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
