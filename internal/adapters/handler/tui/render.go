package tui

import (
	"fmt"
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
		mutedStyle.Render("enter open   n new   r refresh   x clean   q quit"),
		totalWidth,
	) + "\n")
	builder.WriteString(dividerStyle.Render(strings.Repeat("─", totalWidth)) + "\n")

	if m.err != nil {
		builder.WriteString(errorStyle.Render("Error: "+m.err.Error()) + "\n\n")
	}

	switch {
	case m.loading:
		builder.WriteString(dimStyle.Render("Loading tasks..."))
		return builder.String()
	case len(m.rows) == 0:
		builder.WriteString(dimStyle.Render("No tasks found.") + "\n")
		builder.WriteString(dimStyle.Render("Press n to create one."))
		return builder.String()
	}

	for index, row := range m.rows {
		line1, line2 := m.renderRow(index, row, totalWidth)
		if line1 == "" {
			continue
		}
		builder.WriteString(line1 + "\n")
		builder.WriteString(line2 + "\n")
		if index < len(m.rows)-1 {
			builder.WriteString("\n")
		}
	}

	if detail := m.selectedTaskDetailView(); detail != "" {
		builder.WriteString(dividerStyle.Render(strings.Repeat("─", totalWidth)) + "\n")
		builder.WriteString(detail)
	}

	return strings.TrimRight(builder.String(), "\n")
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
	prText := mutedStyle.Render(iconPRNone)

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

	sessionLines := []string{
		headerLabelStyle.Render("SESSION"),
	}
	if elapsed := taskElapsed(task); elapsed != "" {
		sessionLines = append(
			sessionLines,
			mutedStyle.Render("time")+"   "+primaryStyle.Bold(true).Render(elapsed)+mutedStyle.Render(" total"),
		)
	}
	if statusText, _ := taskStatusText(row.status); statusText != "" {
		sessionLines = append(
			sessionLines,
			mutedStyle.Render("state")+"  "+primaryStyle.Render(statusText),
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

	return strings.TrimRight(builder.String(), "\n")
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

	prompt := strings.TrimRight(m.prompt, "\n")
	if prompt == "" {
		prompt = dimStyle.Render("Describe the task to create...")
	} else {
		prompt = primaryStyle.Render(prompt)
	}
	builder.WriteString(prompt)

	if m.createPending {
		builder.WriteString("\n\n" + warningStyle.Render("●") + " " + renderShimmer("Creating task...", m.shimmerTick))
	}

	builder.WriteString("\n\n")
	builder.WriteString(
		keybindStyle.Render("enter") + mutedStyle.Render(" submit · ") +
			keybindStyle.Render("esc") + mutedStyle.Render(" cancel"),
	)

	return builder.String()
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
		builder.WriteString(warningStyle.Render("●") + " " + renderShimmer("Cleaning up task...", m.shimmerTick) + "\n\n")
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
