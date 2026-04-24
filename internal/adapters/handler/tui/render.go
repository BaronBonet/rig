package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"rig/internal/core"

	"charm.land/lipgloss/v2"
)

const (
	colWidthAgent   = 7
	colWidthStatus  = 14
	colWidthElapsed = 7
)

func (m model) listView() string {
	totalWidth := m.totalWidth()

	var builder strings.Builder
	builder.WriteString(renderHeader(
		headerLabelStyle.Render("RIG"),
		mutedStyle.Render("n new   r refresh   x clean   q quit"),
		totalWidth,
	) + "\n")
	builder.WriteString(dividerStyle.Render(strings.Repeat("─", totalWidth)) + "\n")

	if m.err != nil {
		builder.WriteString(errorStyle.Render("Error: "+m.err.Error()) + "\n\n")
	}

	switch {
	case m.loading:
		builder.WriteString(dimStyle.Render("Loading tasks..."))
		if status := m.listCreateStatusView(); status != "" {
			builder.WriteString("\n\n" + status)
		}
		return builder.String()
	case len(m.rows) == 0:
		builder.WriteString(dimStyle.Render("No tasks found.") + "\n")
		builder.WriteString(dimStyle.Render("Press n to create one."))
		if status := m.listCreateStatusView(); status != "" {
			builder.WriteString("\n\n" + status)
		}
		return builder.String()
	}

	previousRepoKey := ""
	for index, row := range m.rows {
		repoKey := repoGroupKey(row.task)
		if index == 0 || repoKey != previousRepoKey {
			if index > 0 {
				builder.WriteString("\n")
			}
			builder.WriteString(m.renderRepoHeader(row.task, totalWidth) + "\n")
			previousRepoKey = repoKey
		}

		line1, line2 := m.renderRow(index, row, totalWidth)
		if line1 == "" {
			continue
		}
		builder.WriteString(line1 + "\n")
		builder.WriteString(line2 + "\n")
		if index < len(m.rows)-1 && repoGroupKey(m.rows[index+1].task) == repoKey {
			builder.WriteString("\n")
		}
	}

	if detail := m.selectedTaskDetailView(); detail != "" {
		builder.WriteString(dividerStyle.Render(strings.Repeat("─", totalWidth)) + "\n")
		builder.WriteString(detail)
	}
	if status := m.listCreateStatusView(); status != "" {
		builder.WriteString("\n" + dividerStyle.Render(strings.Repeat("─", totalWidth)) + "\n")
		builder.WriteString(status)
	}

	return strings.TrimRight(builder.String(), "\n")
}

func (m model) renderRepoHeader(task *core.Task, totalWidth int) string {
	name := "unknown repo"
	if task != nil {
		name = emptyFallback(task.RepoName, name)
	}
	return headerLabelStyle.Render(truncateStr(name, totalWidth))
}

func (m model) renderRow(index int, row taskRow, totalWidth int) (string, string) {
	if row.task == nil {
		return "", ""
	}

	statusText, statusStyle := taskStatusText(row.status)
	statusCell := padRightVisible(statusText, colWidthStatus)
	timeCell := padLeftVisible(taskElapsed(row.task), colWidthElapsed)

	rightWidth := colWidthStatus + colWidthElapsed
	nameWidth := totalWidth - rightWidth - 4
	if nameWidth < 10 {
		nameWidth = 10
	}

	name := row.task.DisplayName
	if strings.TrimSpace(name) == "" {
		name = row.task.ID
	}
	nameCell := padRight(truncateStr(name, nameWidth), nameWidth)

	provider := emptyFallback(string(row.task.Provider), "-")
	agentCell := padRight(provider, colWidthAgent)
	prText := prStatusText(row.pullRequest)

	if index == m.selected {
		line1 := lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render(nameCell) +
			statusStyle.Render(statusCell) +
			primaryStyle.Render(timeCell)
		line2 := providerStyle(provider).Render(agentCell) + prText
		return selectedRowStyle.Render(line1), selectedRowStyle.Render(line2)
	}

	line1 := dimStyle.Render(nameCell) + statusStyle.Render(statusCell) + dimStyle.Render(timeCell)
	line2 := providerStyle(provider).Render(agentCell) + prText
	return normalRowStyle.Render(line1), normalRowStyle.Render(line2)
}

func (m model) selectedTaskDetailView() string {
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
	if prLine := prStatusDetailText(row.pullRequest); prLine != "" {
		workspaceLines = append(workspaceLines, prLine)
	}

	sessionLines := []string{
		headerLabelStyle.Render("SESSION"),
	}
	if elapsed := taskElapsed(task); elapsed != "" {
		sessionLines = append(
			sessionLines,
			mutedStyle.Render("time")+"   "+primaryStyle.Bold(true).Render(elapsed)+mutedStyle.Render(" total"),
		)
	}
	if statusText, statusStyle := taskStatusText(row.status); statusText != "" {
		sessionLines = append(
			sessionLines,
			mutedStyle.Render("state")+"  "+statusStyle.Render(statusText),
		)
	}
	if provider := strings.TrimSpace(string(task.Provider)); provider != "" {
		sessionLines = append(
			sessionLines,
			mutedStyle.Render("provider")+"  "+providerStyle(provider).Render(provider),
		)
	}

	left := padLines(workspaceLines, detailColWidth)
	right := sessionLines

	maxLines := len(left)
	if len(right) > maxLines {
		maxLines = len(right)
	}

	var builder strings.Builder
	for i := range maxLines {
		leftLine := ""
		rightLine := ""
		if i < len(left) {
			leftLine = left[i]
		}
		if i < len(right) {
			rightLine = right[i]
		}
		builder.WriteString("   " + padRightVisible(leftLine, detailColWidth) + "   " + rightLine + "\n")
	}

	if strings.TrimSpace(task.Prompt) != "" {
		builder.WriteString("\n")
		builder.WriteString("   " + headerLabelStyle.Render("PROMPT") + "\n")
		for _, line := range wrapAndTruncate(task.Prompt, totalWidth-6, 3) {
			builder.WriteString("   " + primaryStyle.Render(line) + "\n")
		}
	}

	lastUserPrompt, assistantItems := taskActivityPreview(row.activity)
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

		rightLines := []string{providerStyle(string(task.Provider)).Render("assistant")}
		if len(assistantItems) == 0 {
			rightLines = append(rightLines, dimStyle.Render("No assistant activity yet."))
		} else {
			for idx, item := range assistantItems {
				textStyle := dimStyle
				if idx == 0 {
					textStyle = primaryStyle
				}
				for lineIndex, line := range wrapAndTruncate(item, rightWidth, 3) {
					if lineIndex == 0 {
						rightLines = append(rightLines, textStyle.Render(line))
						continue
					}
					rightLines = append(rightLines, textStyle.Render(line))
				}
				if idx < len(assistantItems)-1 {
					rightLines = append(rightLines, "")
				}
			}
		}

		maxActivityLines := len(leftLines)
		if len(rightLines) > maxActivityLines {
			maxActivityLines = len(rightLines)
		}

		for i := range maxActivityLines {
			leftLine := ""
			rightLine := ""
			if i < len(leftLines) {
				leftLine = leftLines[i]
			}
			if i < len(rightLines) {
				rightLine = rightLines[i]
			}
			builder.WriteString("   " + padRightVisible(leftLine, leftWidth) + "   " + rightLine + "\n")
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
		if events[i].Role == core.TaskActivityRoleUser && strings.TrimSpace(events[i].Text) != "" {
			start = i
			break
		}
	}

	lastUserPrompt := ""
	if start >= 0 {
		lastUserPrompt = strings.TrimSpace(events[start].Text)
	}

	assistantItems := make([]string, 0, taskActivityPreviewLimit)
	for i := len(events) - 1; i >= 0; i-- {
		if start >= 0 && i <= start {
			break
		}
		if events[i].Role != core.TaskActivityRoleAssistant {
			continue
		}
		text := strings.TrimSpace(events[i].Text)
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
	totalWidth := m.totalWidth()

	var builder strings.Builder
	builder.WriteString(renderHeader(
		headerLabelStyle.Render("RIG"),
		mutedStyle.Render("new task"),
		totalWidth,
	) + "\n")
	builder.WriteString(dividerStyle.Render(strings.Repeat("─", totalWidth)) + "\n")

	if m.createErr != nil {
		builder.WriteString(errorStyle.Render("Error: "+m.createErr.Error()) + "\n\n")
	}

	builder.WriteString(dimStyle.Render("Enter task prompt.") + "\n\n")
	builder.WriteString(
		mutedStyle.Render("provider  ") +
			providerStyle(string(defaultCreateProvider)).Render(string(defaultCreateProvider)) +
			"\n\n",
	)

	promptBoxWidth := totalWidth - 4
	if promptBoxWidth < 20 {
		promptBoxWidth = 20
	}
	m.ensurePromptInputInitialized()
	input := m.promptInput
	if input.Value() != m.prompt {
		input.SetValue(m.prompt)
	}
	contentWidth := promptBoxWidth - promptBoxStyle.GetHorizontalFrameSize()
	if contentWidth < 1 {
		contentWidth = 1
	}
	input.SetWidth(contentWidth)
	builder.WriteString(promptBoxStyle.Width(promptBoxWidth).Render(input.View()))

	if progress := m.renderCreateProgress(); progress != "" {
		builder.WriteString("\n\n" + progress)
	}

	builder.WriteString("\n\n")
	builder.WriteString(
		keybindStyle.Render("enter") + mutedStyle.Render(" submit · ") +
			keybindStyle.Render("ctrl+p") + mutedStyle.Render(" pull requests · ") +
			keybindStyle.Render("esc") + mutedStyle.Render(" cancel"),
	)

	return builder.String()
}

func (m model) prPickerView() string {
	totalWidth := m.totalWidth()

	headerSuffix := "PRs"
	if strings.TrimSpace(m.prRepoName) != "" {
		headerSuffix = "PRs: " + m.prRepoName
	}

	var builder strings.Builder
	builder.WriteString(renderHeader(
		headerLabelStyle.Render("RIG"),
		mutedStyle.Render(headerSuffix),
		totalWidth,
	) + "\n")
	builder.WriteString(dividerStyle.Render(strings.Repeat("─", totalWidth)) + "\n")

	if m.createErr != nil {
		builder.WriteString(errorStyle.Render("Error: "+m.createErr.Error()) + "\n\n")
	}

	if len(m.prRows) == 0 {
		builder.WriteString(dimStyle.Render("No pull requests found.") + "\n\n")
		builder.WriteString(keybindStyle.Render("esc") + mutedStyle.Render(" back"))
		return builder.String()
	}

	for i, pr := range m.prRows {
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
		if i == m.prSelected {
			builder.WriteString(selectedRowStyle.Render(block))
		} else {
			builder.WriteString(normalRowStyle.Render(block))
		}
		if i < len(m.prRows)-1 {
			builder.WriteString("\n\n")
		}
	}

	builder.WriteString("\n\n")
	builder.WriteString(
		keybindStyle.Render("enter") + mutedStyle.Render(" create · ") +
			keybindStyle.Render("esc") + mutedStyle.Render(" back"),
	)

	return builder.String()
}

func (m model) renderCreateProgress() string {
	if m.createFromPR {
		if !m.createPending && m.createErr == nil {
			return ""
		}
		label := "Creating task from pull request"
		switch {
		case m.createPending:
			return warningStyle.Render("●") + " " + renderShimmer(label, m.shimmerTick)
		case m.createErr != nil:
			return errorStyle.Render("●") + " " + errorStyle.Render(label)
		default:
			return ""
		}
	}

	if !m.createPending && len(m.createDone) == 0 && m.createActive == "" {
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
		case containsCreateStep(m.createDone, step):
			lines = append(lines, healthyStyle.Render("●")+" "+primaryStyle.Render(label))
		case step == m.createActive && m.createPending:
			lines = append(lines, warningStyle.Render("●")+" "+renderShimmer(label, m.shimmerTick))
		case step == m.createActive && m.createErr != nil:
			lines = append(lines, errorStyle.Render("●")+" "+errorStyle.Render(label))
		default:
			lines = append(lines, dimStyle.Render("○ "+label))
		}
	}

	return strings.Join(lines, "\n")
}

func (m model) listCreateStatusView() string {
	var lines []string
	if m.createErr != nil {
		lines = append(lines, errorStyle.Render("Error: "+m.createErr.Error()))
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
	totalWidth := m.totalWidth()

	var builder strings.Builder
	builder.WriteString(renderHeader(
		headerLabelStyle.Render("RIG"),
		errorStyle.Render("cleanup"),
		totalWidth,
	) + "\n")
	builder.WriteString(dividerStyle.Render(strings.Repeat("─", totalWidth)) + "\n")

	if row := m.selectedRow(); row != nil && row.task != nil {
		builder.WriteString(primaryStyle.Render(emptyFallback(row.task.DisplayName, row.task.ID)) + "\n\n")
	}

	builder.WriteString(dimStyle.Render("The tmux session and worktree will be deleted.") + "\n")
	builder.WriteString(dimStyle.Render("The branch will be kept.") + "\n\n")
	if m.deletePending {
		builder.WriteString(
			warningStyle.Render("●") + " " + renderShimmer("Cleaning up task...", m.shimmerTick) + "\n\n",
		)
	}
	builder.WriteString(
		keybindStyle.Render("y") + mutedStyle.Render(" confirm · ") +
			keybindStyle.Render("n") + mutedStyle.Render(" cancel"),
	)

	return builder.String()
}

func taskStatusText(update *core.TaskStatusUpdate) (string, lipgloss.Style) {
	if update == nil {
		return iconStatusIdle + " idle", dimStyle
	}

	switch update.Phase {
	case core.TaskStatusPhaseStarting:
		return iconStatusProgress + " starting", warningStyle
	case core.TaskStatusPhaseWorking:
		if indicatesCommandActivity(update.RawEventName) {
			return iconStatusProgress + " working · command", healthyStyle
		}
		return iconStatusActive + " working", healthyStyle
	case core.TaskStatusPhaseWaitingForInput:
		return iconStatusProgress + " needs input", warningStyle
	default:
		return iconStatusIdle + " idle", dimStyle
	}
}

func prStatusText(status *core.PRStatus) string {
	if status == nil || status.State == core.PRStateNone {
		return mutedStyle.Render(iconPRNone)
	}

	icon, style := prStateIconStyle(status.State)
	label := icon
	if status.Number > 0 {
		label += " #" + strconv.Itoa(status.Number)
	}
	label += " " + string(status.State)
	return style.Render(label)
}

func prStatusDetailText(status *core.PRStatus) string {
	if status == nil || status.State == core.PRStateNone {
		return ""
	}

	return mutedStyle.Render("pr") + "     " + prStatusText(status)
}

func prStateIconStyle(state core.PRState) (string, lipgloss.Style) {
	switch state {
	case core.PRStateOpen:
		return "●", healthyStyle
	case core.PRStateDraft:
		return "◐", warningStyle
	case core.PRStateMerged:
		return "✓", prMergedStyle
	case core.PRStateClosed:
		return "×", errorStyle
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

func truncateStr(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max-1]) + "…"
}

func padRight(value string, width int) string {
	runes := []rune(value)
	if len(runes) >= width {
		return value
	}
	return value + strings.Repeat(" ", width-len(runes))
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

func padLines(lines []string, width int) []string {
	padded := make([]string, 0, len(lines))
	for _, line := range lines {
		padded = append(padded, padRightVisible(line, width))
	}
	return padded
}

func wrapAndTruncate(text string, width int, maxLines int) []string {
	if text == "" {
		return nil
	}

	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}

	lines := []string{words[0]}
	for _, word := range words[1:] {
		current := lines[len(lines)-1]
		if len(current)+1+len(word) > width {
			lines = append(lines, word)
			continue
		}
		lines[len(lines)-1] = current + " " + word
	}

	if len(lines) > maxLines {
		lines = lines[:maxLines]
		last := lines[maxLines-1]
		if len(last) > 3 {
			lines[maxLines-1] = last[:len(last)-3] + "..."
		}
	}

	return lines
}

func emptyFallback(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
