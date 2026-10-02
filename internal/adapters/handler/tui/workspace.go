package tui

import (
	"os"
	"path/filepath"
	"strings"
)

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
