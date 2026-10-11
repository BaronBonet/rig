package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// targetRepo resolves the repository a new task is created in: the one
// containing the caller's working directory when the input names none, else
// the one the input names by path or by name. Repository detection maps a
// worktree to its main repository, so a task created from inside another
// task's worktree branches off the main checkout like any other.
func (c *taskCreation) targetRepo(ctx context.Context, input CreateTaskInput) (RepoContext, error) {
	target := strings.TrimSpace(input.TargetRepo)
	if target == "" {
		return c.gitWorktree.DetectRepo(ctx, input.Cwd)
	}
	if !isRepoPath(target) {
		return c.repoByName(ctx, target)
	}

	path, err := repoPath(target, input.Cwd)
	if err != nil {
		return RepoContext{}, err
	}
	repoCtx, err := c.gitWorktree.DetectRepo(ctx, path)
	if err != nil {
		return RepoContext{}, fmt.Errorf("repository path %q: %w", target, err)
	}
	return repoCtx, nil
}

// repoByName resolves a target repository given by name: the one known
// repository with that name, ignoring case. A name no known repository has,
// or that several have, is an error that says how to name the repository by
// path instead.
func (c *taskCreation) repoByName(ctx context.Context, name string) (RepoContext, error) {
	matches, err := c.repos.named(ctx, name)
	if err != nil {
		return RepoContext{}, fmt.Errorf("find the repository named %q: %w", name, err)
	}

	switch len(matches) {
	case 0:
		return RepoContext{}, fmt.Errorf(
			"no known repository is named %q: `rig repo list` shows the known repositories, "+
				"or pass the repository's path instead",
			name,
		)
	case 1:
		// A repository known from its tasks alone may have been moved or
		// deleted since.
		repoCtx, err := c.gitWorktree.DetectRepo(ctx, matches[0].Root)
		if err != nil {
			return RepoContext{}, fmt.Errorf(
				"repository %q at %s cannot be used (%w): if it has moved, pass the repository's path instead",
				name, matches[0].Root, err,
			)
		}
		return repoCtx, nil
	default:
		paths := make([]string, 0, len(matches))
		for _, repo := range matches {
			paths = append(paths, repo.Root)
		}
		return RepoContext{}, fmt.Errorf(
			"%d known repositories are named %q, so pass the path of the one you mean with --repo <path>: %s",
			len(matches), name, strings.Join(paths, ", "),
		)
	}
}

// isRepoPath reports whether a target repository is given as a path rather
// than a name: a path contains a path separator, or starts with ~ or a dot.
func isRepoPath(target string) bool {
	return strings.ContainsRune(target, '/') ||
		strings.ContainsRune(target, filepath.Separator) ||
		strings.HasPrefix(target, "~") ||
		strings.HasPrefix(target, ".")
}

// repoPath turns a target repository path into a clean absolute path. A
// leading ~ is the user's home directory, and a relative path is relative to
// the caller's working directory, never to the daemon's.
func repoPath(target string, cwd string) (string, error) {
	path, err := expandHome(target)
	if err != nil {
		return "", fmt.Errorf("repository path %q: %w", target, err)
	}

	if !filepath.IsAbs(path) {
		if !filepath.IsAbs(cwd) {
			return "", fmt.Errorf("repository path %q is relative, but the working directory is unknown", target)
		}
		path = filepath.Join(cwd, path)
	}
	return filepath.Clean(path), nil
}

// expandHome expands a leading ~ or ~/ in path to the user's home directory,
// and returns any other path as it is. Another user's home directory, such
// as ~alice, is an error.
func expandHome(path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		if strings.HasPrefix(path, "~") {
			return "", errors.New("only ~ and ~/ expand to the home directory")
		}
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("expand ~: %w", err)
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~")), nil
}
