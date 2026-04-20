package gitworktree

import (
	"context"
	"path/filepath"
	"strings"

	"rig/internal/core"
	"rig/internal/pkg/subprocess"
)

type repository struct {
	runner subprocess.Runner
}

func New(runner subprocess.Runner) core.GitWorktreeClient {
	return &repository{runner: runner}
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
	for _, line := range strings.Split(result.Stdout, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "branch ") && strings.TrimSpace(strings.TrimPrefix(line, "branch ")) == target {
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
