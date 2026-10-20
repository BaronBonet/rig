package tui

import (
	"strconv"
	"strings"

	"github.com/BaronBonet/rig/internal/core"
)

const (
	// maxSpawnedTasks caps how many child tasks the detail panel lists under
	// spawned when it counts the rest.
	maxSpawnedTasks = 5
	// detailValueIndent lines a detail panel value up with the values after
	// its labels, such as the parent task after from.
	detailValueIndent = "       "
)

// parentTask returns a task's parent task when the task list shows it, or nil.
// Deleting a parent clears its children's links, so a link to a task missing
// from the list is only stale until the next reload.
func (m model) parentTask(task *core.Task) *core.Task {
	if task == nil || strings.TrimSpace(task.ParentTaskID) == "" {
		return nil
	}
	if row := m.taskRowByID(task.ParentTaskID); row != nil {
		return row.task
	}
	return nil
}

// linkMarker returns the marker a task's row carries when its parent is in the
// task list: one for a child in its parent's repo, which the list nests under
// its parent, and another for a child from another repo. Otherwise, it is "".
func (m model) linkMarker(task *core.Task) string {
	parent := m.parentTask(task)
	switch {
	case parent == nil:
		return ""
	case repoGroupKey(parent) != repoGroupKey(task):
		return iconCrossRepoChildTask
	default:
		return iconChildTask
	}
}

// childRows returns the rows of the tasks a task spawned, its child tasks, in
// the order the task list shows them.
func (m model) childRows(task *core.Task) []taskRow {
	if task == nil || strings.TrimSpace(task.ID) == "" {
		return nil
	}
	var children []taskRow
	for _, row := range m.rows {
		if row.task != nil && row.task.ParentTaskID == task.ID {
			children = append(children, row)
		}
	}
	return children
}

// taskLinkDetailLines describes a task's links in the detail panel's linked
// tasks section, within width columns: the parent task it came from, with its
// repo when that is another one, and the child tasks it spawned, each led by
// the status symbol its row shows.
func (m model) taskLinkDetailLines(task *core.Task, width int) []string {
	var lines []string
	if parent := m.parentTask(task); parent != nil {
		name := truncateStr(emptyFallback(parent.DisplayName, parent.ID), width-len(detailValueIndent))
		lines = append(lines, mutedStyle.Render("from")+"   "+primaryStyle.Render(name))
		if repoGroupKey(parent) != repoGroupKey(task) {
			repo := emptyFallback(parent.RepoName, parent.RepoRoot)
			repo = truncateStr(repo, width-len(detailValueIndent)-len("in "))
			lines = append(lines, detailValueIndent+mutedStyle.Render("in ")+primaryStyle.Render(repo))
		}
	}

	children := m.childRows(task)
	if len(children) == 0 {
		return lines
	}
	lines = append(lines, mutedStyle.Render("spawned"))
	// A "+1 more" line would take the line the last child needs, so one child
	// past the cap is still listed.
	shown := children
	if len(children) > maxSpawnedTasks+1 {
		shown = children[:maxSpawnedTasks]
	}
	nameWidth := width - len(detailValueIndent) - statusSymbolWidth
	for _, child := range shown {
		symbol, symbolStyle := m.taskRowSymbol(child)
		name := truncateStr(emptyFallback(child.task.DisplayName, child.task.ID), nameWidth)
		lines = append(lines, detailValueIndent+symbolStyle.Render(symbol)+" "+primaryStyle.Render(name))
	}
	if more := len(children) - len(shown); more > 0 {
		lines = append(lines, detailValueIndent+mutedStyle.Render("+"+strconv.Itoa(more)+" more"))
	}
	return lines
}
