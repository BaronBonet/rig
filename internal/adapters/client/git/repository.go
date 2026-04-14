package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"rig/internal/core"
	"rig/internal/pkg/execx"
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

func (r *Repository) IsBranchUsedByWorktree(ctx context.Context, repoRoot string, branchName string) (bool, error) {
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

func (r *Repository) CreateTaskWorkspace(ctx context.Context, task *core.Task) error {
	return r.CreateWorktree(ctx, core.CreateWorktreeInput{
		RepoRoot:     task.RepoRoot,
		BaseBranch:   task.BaseBranch,
		BranchName:   task.BranchName,
		WorktreePath: task.WorktreePath,
	})
}

func (r *Repository) CreateTaskWorkspaceFromBranch(ctx context.Context, task *core.Task) error {
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

func (r *Repository) InspectTaskWorkspace(ctx context.Context, task *core.Task) (core.RepoResources, error) {
	worktreeExists, err := worktreePresence(task.WorktreePath)
	if err != nil {
		return core.RepoResources{}, err
	}

	branchExists, err := r.BranchExists(ctx, task.RepoRoot, task.BranchName)
	if err != nil {
		return core.RepoResources{}, err
	}

	return core.RepoResources{
		WorktreeExists: worktreeExists,
		BranchExists:   branchExists,
	}, nil
}

func (r *Repository) RemoveTaskWorkspace(ctx context.Context, task *core.Task) error {
	return r.RemoveWorktree(ctx, task.RepoRoot, task.WorktreePath)
}

func (r *Repository) RemoveWorktree(ctx context.Context, repoRoot, path string) error {
	_, err := r.runner.Run(ctx, repoRoot, "git", "worktree", "remove", "--force", path)
	return err
}

func worktreePresence(path string) (bool, error) {
	if strings.TrimSpace(path) == "" {
		return false, nil
	}

	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}

		return false, err
	}

	return info.IsDir(), nil
}
