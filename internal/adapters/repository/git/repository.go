package git

import (
	"context"
	"path/filepath"
	"strings"

	"agent/internal/core"
	"agent/internal/pkg/execx"
)

type Repository struct {
	runner execx.Runner
}

func NewRepository(runner execx.Runner) *Repository {
	return &Repository{runner: runner}
}

func (r *Repository) IsAvailable(ctx context.Context) error {
	_, err := r.runner.Run(ctx, "", "git", "--version")
	return err
}

func (r *Repository) DetectRepo(ctx context.Context, cwd string) (core.RepoContext, error) {
	rootResult, err := r.runner.Run(ctx, cwd, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return core.RepoContext{}, err
	}

	root := strings.TrimSpace(rootResult.Stdout)

	branchResult, err := r.runner.Run(ctx, cwd, "git", "branch", "--show-current")
	if err != nil {
		return core.RepoContext{}, err
	}

	return core.RepoContext{
		Root:       root,
		Name:       filepath.Base(root),
		BaseBranch: strings.TrimSpace(branchResult.Stdout),
	}, nil
}

func (r *Repository) BranchExists(ctx context.Context, repoRoot, branch string) (bool, error) {
	_, err := r.runner.Run(ctx, repoRoot, "git", "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	if err != nil {
		return false, nil
	}

	return true, nil
}

func (r *Repository) CreateWorktree(ctx context.Context, in core.CreateWorktreeInput) error {
	_, err := r.runner.Run(
		ctx,
		in.RepoRoot,
		"git",
		"worktree",
		"add",
		in.WorktreePath,
		"-b",
		in.BranchName,
		in.BaseBranch,
	)
	return err
}

func (r *Repository) RemoveWorktree(ctx context.Context, path string) error {
	_, err := r.runner.Run(ctx, "", "git", "worktree", "remove", path)
	return err
}
