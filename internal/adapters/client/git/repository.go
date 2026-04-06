package git

import (
	"context"
	"errors"
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
		// git show-ref exits non-zero when the ref does not exist;
		// treat CommandError (expected) as "branch not found".
		var cmdErr execx.CommandError
		if errors.As(err, &cmdErr) {
			return false, nil
		}

		return false, err
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

func (r *Repository) RemoveWorktree(ctx context.Context, repoRoot, path string) error {
	_, err := r.runner.Run(ctx, repoRoot, "git", "worktree", "remove", "--force", path)
	return err
}
