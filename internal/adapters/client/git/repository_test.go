package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"rig/internal/core"
	"rig/internal/pkg/execx"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestRepositoryDetectRepo_ParsesTopLevelAndBranch(t *testing.T) {
	runner := execx.NewMockRunner(t)
	runner.EXPECT().
		Run(mock.Anything, "/tmp/repo", "git", "rev-parse", "--show-toplevel").
		Return(execx.Result{Stdout: "/tmp/repo\n"}, nil).
		Once()
	runner.EXPECT().
		Run(mock.Anything, "/tmp/repo", "git", "branch", "--show-current").
		Return(execx.Result{Stdout: "main\n"}, nil).
		Once()
	repo := NewRepository(runner)

	repoCtx, err := repo.DetectRepo(context.Background(), "/tmp/repo")
	require.NoError(t, err)
	require.Equal(t, "/tmp/repo", repoCtx.Root)
	require.Equal(t, "main", repoCtx.BaseBranch)
	require.Equal(t, "repo", repoCtx.Name)
}

func TestRepositoryCreateWorktree_UsesExpectedGitCommand(t *testing.T) {
	runner := execx.NewMockRunner(t)
	runner.EXPECT().
		Run(mock.Anything, "/tmp/repo", "git", "worktree", "add", "/tmp/repo-billing-retry-flow", "-b", "feat/billing-retry-flow", "main").
		Return(execx.Result{}, nil).
		Once()
	repo := NewRepository(runner)

	err := repo.CreateWorktree(context.Background(), core.CreateWorktreeInput{
		RepoRoot:     "/tmp/repo",
		BaseBranch:   "main",
		BranchName:   "feat/billing-retry-flow",
		WorktreePath: "/tmp/repo-billing-retry-flow",
	})
	require.NoError(t, err)
}

func TestRepositoryCreateTaskWorkspace_UsesTaskFields(t *testing.T) {
	runner := execx.NewMockRunner(t)
	runner.EXPECT().
		Run(mock.Anything, "/tmp/repo", "git", "worktree", "add", "/tmp/repo-billing-retry-flow", "-b", "feat/billing-retry-flow", "main").
		Return(execx.Result{}, nil).
		Once()
	repo := NewRepository(runner)

	err := repo.CreateTaskWorkspace(context.Background(), &core.Task{
		RepoRoot:     "/tmp/repo",
		BaseBranch:   "main",
		BranchName:   "feat/billing-retry-flow",
		WorktreePath: "/tmp/repo-billing-retry-flow",
	})
	require.NoError(t, err)
}

func TestRepositoryCreateTaskWorkspaceFromBranch_UsesExistingBranch(t *testing.T) {
	runner := execx.NewMockRunner(t)
	runner.EXPECT().
		Run(
			mock.Anything,
			"/tmp/repo",
			"git",
			"worktree",
			"add",
			"/tmp/repo-auth-rewrite",
			"feat/auth-rewrite",
		).
		Return(execx.Result{}, nil).
		Once()
	repo := NewRepository(runner)

	err := repo.CreateTaskWorkspaceFromBranch(context.Background(), &core.Task{
		RepoRoot:     "/tmp/repo",
		BranchName:   "feat/auth-rewrite",
		WorktreePath: "/tmp/repo-auth-rewrite",
	})
	require.NoError(t, err)
}

func TestRepositoryIsBranchUsedByWorktree_ReturnsTrueWhenBranchIsCheckedOutElsewhere(t *testing.T) {
	runner := execx.NewMockRunner(t)
	runner.EXPECT().
		Run(mock.Anything, "/tmp/repo", "git", "worktree", "list", "--porcelain").
		Return(execx.Result{
			Stdout: "worktree /tmp/repo\nHEAD abcdef\nbranch refs/heads/main\n\n" +
				"worktree /tmp/repo-auth\nHEAD 123456\nbranch refs/heads/feat/auth\n",
		}, nil).
		Once()
	repo := NewRepository(runner)

	used, err := repo.IsBranchUsedByWorktree(context.Background(), "/tmp/repo", "feat/auth")
	require.NoError(t, err)
	require.True(t, used)
}

func TestRepositoryInspectTaskWorkspace_ReturnsWorktreeAndBranchPresence(t *testing.T) {
	worktreePath := filepath.Join(t.TempDir(), "repo-billing-retry-flow")
	require.NoError(t, os.Mkdir(worktreePath, 0o755))

	runner := execx.NewMockRunner(t)
	runner.EXPECT().
		Run(mock.Anything, "/tmp/repo", "git", "show-ref", "--verify", "--quiet", "refs/heads/feat/billing-retry-flow").
		Return(execx.Result{}, nil).
		Once()
	repo := NewRepository(runner)

	resources, err := repo.InspectTaskWorkspace(context.Background(), &core.Task{
		RepoRoot:     "/tmp/repo",
		BranchName:   "feat/billing-retry-flow",
		WorktreePath: worktreePath,
	})
	require.NoError(t, err)
	require.Equal(t, core.RepoResources{
		WorktreeExists: true,
		BranchExists:   true,
	}, resources)
}

func TestRepositoryRemoveTaskWorkspace_UsesTaskFields(t *testing.T) {
	runner := execx.NewMockRunner(t)
	runner.EXPECT().
		Run(mock.Anything, "/tmp/repo", "git", "worktree", "remove", "--force", "/tmp/repo-billing-retry-flow").
		Return(execx.Result{}, nil).
		Once()
	repo := NewRepository(runner)

	err := repo.RemoveTaskWorkspace(context.Background(), &core.Task{
		RepoRoot:     "/tmp/repo",
		WorktreePath: "/tmp/repo-billing-retry-flow",
	})
	require.NoError(t, err)
}

func TestRepositoryRemoveWorktree_UsesExpectedGitCommand(t *testing.T) {
	runner := execx.NewMockRunner(t)
	runner.EXPECT().
		Run(mock.Anything, "/tmp/repo", "git", "worktree", "remove", "--force", "/tmp/repo-billing-retry-flow").
		Return(execx.Result{}, nil).
		Once()
	repo := NewRepository(runner)

	err := repo.RemoveWorktree(context.Background(), "/tmp/repo", "/tmp/repo-billing-retry-flow")
	require.NoError(t, err)
}
