package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BaronBonet/rig/internal/core"
)

// scopeTasks returns the tasks to list, the launch folder's unless every
// folder is shown, and how many tasks belong to other folders.
func (m model) scopeTasks(tasks []*core.Task) ([]*core.Task, int) {
	folder := strings.TrimSpace(m.launchCwd)
	if !filepath.IsAbs(folder) {
		return tasks, 0
	}
	folder = filepath.Clean(folder)
	inFolder := make([]*core.Task, 0, len(tasks))
	for _, task := range tasks {
		if task != nil && taskInFolder(task, folder) {
			inFolder = append(inFolder, task)
		}
	}
	others := len(tasks) - len(inFolder)
	if m.allFolders {
		return tasks, others
	}
	return inFolder, others
}

// scopeLine says that tasks of other folders exist and how f shows or hides
// them; it is empty when every task belongs to the launch folder.
func (m model) scopeLine(totalWidth int) []string {
	if m.otherFolderTasks == 0 {
		return nil
	}
	noun := "tasks"
	if m.otherFolderTasks == 1 {
		noun = "task"
	}
	text := fmt.Sprintf("%d %s in other folders hidden. Press f to show all folders.", m.otherFolderTasks, noun)
	if m.allFolders {
		text = "Showing all folders. Press f to show only " + homeRelativePath(m.launchCwd) + "."
	}
	return []string{dimStyle.Render(truncateStr(text, totalWidth))}
}

// taskInFolder reports whether a task belongs to folder: its repository or
// worktree is the folder or lies below it, or the folder lies inside them. A
// task that names no path is never hidden.
func taskInFolder(task *core.Task, folder string) bool {
	named := false
	for _, path := range []string{task.RepoRoot, task.WorktreePath} {
		path = strings.TrimSpace(path)
		if !filepath.IsAbs(path) {
			continue
		}
		named = true
		path = filepath.Clean(path)
		if core.FolderContains(folder, path) || core.FolderContains(path, folder) {
			return true
		}
	}
	return !named
}

// insideGitWorktree reports whether dir is inside a Git worktree, by finding a
// .git entry in dir or one of its parents.
func insideGitWorktree(dir string) bool {
	dir = strings.TrimSpace(dir)
	if dir == "" || !filepath.IsAbs(dir) {
		return false
	}
	for current := filepath.Clean(dir); ; current = filepath.Dir(current) {
		if _, err := os.Lstat(filepath.Join(current, ".git")); err == nil {
			return true
		}
		if filepath.Dir(current) == current {
			return false
		}
	}
}

// homeRelativePath shortens a path under the home directory to ~/...
func homeRelativePath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if rest, ok := strings.CutPrefix(path, home+string(filepath.Separator)); ok {
		return "~" + string(filepath.Separator) + rest
	}
	return path
}

func (m model) draftWorkspaceLine() string {
	label := mutedStyle.Render("workspace ")
	switch {
	case m.draft.outsideGit:
		return label + primaryStyle.Render("this folder") + mutedStyle.Render("  ·  not a git repository")
	case m.draft.inFolder:
		return label + primaryStyle.Render("this folder") + mutedStyle.Render("  ·  ") +
			keybindStyle.Render("ctrl+o") + mutedStyle.Render(" new worktree")
	default:
		return label + primaryStyle.Render("new worktree") + mutedStyle.Render("  ·  ") +
			keybindStyle.Render("ctrl+o") + mutedStyle.Render(" this folder")
	}
}
