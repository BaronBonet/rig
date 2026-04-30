package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BaronBonet/rig/internal/core"
	"github.com/BaronBonet/rig/internal/pkg/subprocess"
)

type repository struct {
	runner subprocess.Runner
}

func New(runner subprocess.Runner) core.GitWorktreeClient {
	return &repository{runner: runner}
}

func (r *repository) HealthCheck(ctx context.Context) error {
	_, err := r.runner.Run(ctx, "", "git", "--version")
	return err
}

func (r *repository) DetectRepo(ctx context.Context, cwd string) (core.RepoContext, error) {
	rootResult, err := r.runner.Run(ctx, cwd, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return core.RepoContext{}, err
	}

	root := strings.TrimSpace(rootResult.Stdout)
	if primaryRoot, ok := primaryWorktreeRoot(root, r.loadWorktreeList(ctx, cwd)); ok {
		root = primaryRoot
	}

	branchResult, err := r.runner.Run(ctx, root, "git", "branch", "--show-current")
	if err != nil {
		return core.RepoContext{}, err
	}

	return core.RepoContext{
		Root:       root,
		Name:       filepath.Base(root),
		BaseBranch: strings.TrimSpace(branchResult.Stdout),
	}, nil
}

func (r *repository) IsBranchUsedByWorktree(ctx context.Context, repoRoot string, branchName string) (bool, error) {
	result, err := r.runner.Run(ctx, repoRoot, "git", "worktree", "list", "--porcelain")
	if err != nil {
		return false, err
	}

	target := "refs/heads/" + strings.TrimSpace(branchName)
	for _, entry := range parseWorktreeEntries(result.Stdout) {
		if entry.prunable {
			continue
		}
		if entry.branch == target {
			return true, nil
		}
	}

	return false, nil
}

func (r *repository) CreateTaskWorkspace(ctx context.Context, task *core.Task) error {
	repoCtx, err := r.DetectRepo(ctx, task.RepoRoot)
	if err != nil {
		return err
	}

	_, err = r.runner.Run(
		ctx,
		task.RepoRoot,
		"git",
		"worktree",
		"add",
		task.WorktreePath,
		"-b",
		task.BranchName,
		repoCtx.BaseBranch,
	)
	return err
}

func (r *repository) CreateTaskWorkspaceFromBranch(ctx context.Context, task *core.Task) error {
	_, err := r.runner.Run(
		ctx,
		task.RepoRoot,
		"git",
		"worktree",
		"add",
		task.WorktreePath,
		task.BranchName,
	)
	return err
}

func (r *repository) CreateTaskWorkspaceFromPullRequest(
	ctx context.Context,
	task *core.Task,
	pullRequestNumber int,
) error {
	refspec := fmt.Sprintf("+refs/pull/%d/head:refs/heads/%s", pullRequestNumber, task.BranchName)
	if _, err := r.runner.Run(ctx, task.RepoRoot, "git", "fetch", "origin", refspec); err != nil {
		return err
	}

	return r.CreateTaskWorkspaceFromBranch(ctx, task)
}

func (r *repository) RemoveTaskWorkspace(ctx context.Context, task *core.Task) error {
	if task == nil || strings.TrimSpace(task.WorktreePath) == "" {
		return nil
	}
	if _, err := os.Stat(task.WorktreePath); err != nil {
		if os.IsNotExist(err) {
			_, pruneErr := r.runner.Run(ctx, task.RepoRoot, "git", "worktree", "prune")
			return pruneErr
		}
		return err
	}

	_, err := r.runner.Run(
		ctx,
		task.RepoRoot,
		"git",
		"worktree",
		"remove",
		"--force",
		task.WorktreePath,
	)
	return err
}

type worktreeEntry struct {
	branch   string
	prunable bool
}

func parseWorktreeEntries(worktreeList string) []worktreeEntry {
	entries := []worktreeEntry{{}}
	for _, line := range strings.Split(worktreeList, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			if entries[len(entries)-1] != (worktreeEntry{}) {
				entries = append(entries, worktreeEntry{})
			}
			continue
		}

		current := &entries[len(entries)-1]
		switch {
		case strings.HasPrefix(line, "branch "):
			current.branch = strings.TrimSpace(strings.TrimPrefix(line, "branch "))
		case strings.HasPrefix(line, "prunable "):
			current.prunable = true
		}
	}

	if len(entries) > 0 && entries[len(entries)-1] == (worktreeEntry{}) {
		entries = entries[:len(entries)-1]
	}
	return entries
}

func (r *repository) loadWorktreeList(ctx context.Context, cwd string) string {
	result, err := r.runner.Run(ctx, cwd, "git", "worktree", "list", "--porcelain")
	if err != nil {
		return ""
	}

	return result.Stdout
}

func primaryWorktreeRoot(currentRoot string, worktreeList string) (string, bool) {
	for _, line := range strings.Split(worktreeList, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "worktree ") {
			continue
		}

		root := strings.TrimSpace(strings.TrimPrefix(line, "worktree "))
		if root == "" || root == currentRoot {
			return "", false
		}

		return root, true
	}

	return "", false
}
