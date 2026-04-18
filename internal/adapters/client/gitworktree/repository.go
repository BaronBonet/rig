package gitworktree

import (
	"context"

	gitclient "rig/internal/adapters/client/git"
	"rig/internal/core"
	"rig/internal/pkg/execx"
)

type Repository struct {
	repo *gitclient.Repository
}

func NewRepository(runner execx.Runner) *Repository {
	return &Repository{repo: gitclient.NewRepository(runner)}
}

func (r *Repository) DetectRepo(ctx context.Context, cwd string) (core.RepoContext, error) {
	return r.repo.DetectRepo(ctx, cwd)
}

func (r *Repository) IsBranchUsedByWorktree(ctx context.Context, repoRoot string, branchName string) (bool, error) {
	return r.repo.IsBranchUsedByWorktree(ctx, repoRoot, branchName)
}

func (r *Repository) CreateTaskWorkspace(ctx context.Context, task *core.Task) error {
	return r.repo.CreateTaskWorkspace(ctx, task)
}

func (r *Repository) CreateTaskWorkspaceFromBranch(ctx context.Context, task *core.Task) error {
	return r.repo.CreateTaskWorkspaceFromBranch(ctx, task)
}
