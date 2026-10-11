package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/BaronBonet/rig/internal/core"
)

// taskView is a task as the CLI prints it. Its JSON field names are a public
// contract that agents parse: fields may be added, but never renamed.
type taskView struct {
	ID             string `json:"id"`
	Slug           string `json:"slug"`
	Name           string `json:"name"`
	RepoName       string `json:"repo_name"`
	RepoRoot       string `json:"repo_root"`
	Branch         string `json:"branch"`
	Worktree       string `json:"worktree"`
	TmuxSession    string `json:"tmux_session"`
	Provider       string `json:"provider"`
	CreationStatus string `json:"creation_status"`
	CreationStep   string `json:"creation_step"`
	// Status is the task's live Runtime status phase, or empty when it has
	// none.
	Status string `json:"status"`
	// ParentID is the ID of the task's parent task, or empty when it has
	// none.
	ParentID string `json:"parent_id"`

	// creationError is why the task's creation failed, for people to read.
	creationError string
}

// taskDetailView is a task as rig task show prints it: its view, plus the
// views of its children.
type taskDetailView struct {
	taskView

	Children []taskView `json:"children"`
}

func newTaskView(task *core.Task, status *core.TaskStatusUpdate) taskView {
	view := taskView{
		ID:             task.ID,
		Slug:           task.Slug,
		Name:           task.DisplayName,
		RepoName:       task.RepoName,
		RepoRoot:       task.RepoRoot,
		Branch:         task.BranchName,
		Worktree:       task.WorktreePath,
		TmuxSession:    task.TmuxSession,
		Provider:       string(task.Provider),
		CreationStatus: string(task.CreationStatus),
		CreationStep:   string(task.CreationStep),
		ParentID:       task.ParentTaskID,
		creationError:  task.CreationError,
	}
	if status != nil {
		view.Status = string(status.Phase)
	}
	return view
}

// loadTaskView reads the task's live status and returns its view.
func loadTaskView(ctx context.Context, frontend core.TaskFrontend, task *core.Task) (taskView, error) {
	status, err := frontend.LatestTaskStatus(ctx, task.ID)
	if err != nil {
		return newTaskView(task, nil), fmt.Errorf("read the status of task %s: %w", task.ID, err)
	}
	return newTaskView(task, status), nil
}

// loadTaskViews reads each task's live status and returns their views, in
// the same order.
func loadTaskViews(ctx context.Context, frontend core.TaskFrontend, tasks []*core.Task) ([]taskView, error) {
	views := make([]taskView, 0, len(tasks))
	for _, task := range tasks {
		view, err := loadTaskView(ctx, frontend, task)
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	return views, nil
}

// labelledLine is one line of a task's description, such as its branch.
type labelledLine struct {
	label string
	value string
}

// writeLabelledLines prints each line's label and value, the values lined
// up.
func writeLabelledLines(w io.Writer, lines []labelledLine) error {
	for _, line := range lines {
		if _, err := fmt.Fprintf(w, "%-14s%s\n", line.label+":", line.value); err != nil {
			return err
		}
	}
	return nil
}

// writeTaskSummary prints the task's name, ID, repository, branch, worktree
// and tmux session, one labelled line each, and its parent's when parent is
// set.
func writeTaskSummary(w io.Writer, view taskView, parent string) error {
	lines := []labelledLine{
		{"Name", view.Name},
		{"ID", view.ID},
		{"Repository", fmt.Sprintf("%s (%s)", view.RepoName, view.RepoRoot)},
		{"Branch", view.Branch},
		{"Worktree", view.Worktree},
		{"Tmux session", view.TmuxSession},
	}
	if parent != "" {
		lines = append(lines, labelledLine{"Parent", parent})
	}
	return writeLabelledLines(w, lines)
}

// taskByID returns the task with the ID among tasks, or nil when none has
// it.
func taskByID(tasks []*core.Task, id string) *core.Task {
	for _, task := range tasks {
		if task != nil && task.ID == id {
			return task
		}
	}
	return nil
}

// parentLabel names a task's parent by name and ID, or by ID alone when the
// parent is not among tasks. It is empty for a task without a parent.
func parentLabel(tasks []*core.Task, parentID string) string {
	if parentID == "" {
		return ""
	}
	parent := taskByID(tasks, parentID)
	if parent == nil {
		return parentID
	}
	return fmt.Sprintf("%s (%s)", parent.DisplayName, parent.ID)
}
