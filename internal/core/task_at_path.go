package core

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// TaskAtPath returns the task whose worktree contains path, or nil when no
// task's worktree does.
func (s *service) TaskAtPath(ctx context.Context, path string) (*Task, error) {
	tasks, err := s.tasks.ListTasks(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	return FindTaskAtPath(tasks, path), nil
}

// FindTaskAtPath returns the task whose worktree contains path: its worktree root
// or anything below it. Paths are compared clean and absolute, a whole
// component at a time, so a worktree /work/app_fix does not contain
// /work/app_fix-ui. Should worktrees nest, the deepest one holding path wins.
// It returns nil when no task's worktree contains path, or path is not
// absolute.
func FindTaskAtPath(tasks []*Task, path string) *Task {
	path = strings.TrimSpace(path)
	if !filepath.IsAbs(path) {
		return nil
	}
	path = filepath.Clean(path)

	var found *Task
	var foundRoot string
	for _, task := range tasks {
		if task == nil {
			continue
		}
		root := strings.TrimSpace(task.WorktreePath)
		if !filepath.IsAbs(root) {
			continue
		}
		root = filepath.Clean(root)
		if pathWithin(path, root) && len(root) > len(foundRoot) {
			found, foundRoot = task, root
		}
	}
	return found
}

// pathWithin reports whether the clean absolute path is root or lies below
// it.
func pathWithin(path string, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// parentTaskID returns the ID of a new task's parent task: when the input
// asks to link to the calling task, the task whose worktree contains the
// caller's working directory. The parent may be in another repository than
// the new task. It is empty when the new task has no parent.
func parentTaskID(input CreateTaskInput, tasks []*Task) string {
	if !input.LinkToCallingTask {
		return ""
	}
	if parent := FindTaskAtPath(tasks, input.Cwd); parent != nil {
		return parent.ID
	}
	return ""
}
