package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"rig/internal/core"
	"rig/internal/pkg/subprocess"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestRepositoryDetectRepo_ParsesTopLevelAndBranch(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	runner.EXPECT().
		Run(mock.Anything, "/tmp/repo", "git", "rev-parse", "--show-toplevel").
		Return(subprocess.Result{Stdout: "/tmp/repo\n"}, nil).
		Once()
	runner.EXPECT().
		Run(mock.Anything, "/tmp/repo", "git", "worktree", "list", "--porcelain").
		Return(subprocess.Result{
			Stdout: "worktree /tmp/repo\nHEAD abcdef\nbranch refs/heads/main\n",
		}, nil).
		Once()
	runner.EXPECT().
		Run(mock.Anything, "/tmp/repo", "git", "branch", "--show-current").
		Return(subprocess.Result{Stdout: "main\n"}, nil).
		Once()

	repoCtx, err := New(runner).DetectRepo(context.Background(), "/tmp/repo")
	require.NoError(t, err)
	require.Equal(t, "/tmp/repo", repoCtx.Root)
	require.Equal(t, "repo", repoCtx.Name)
	require.Equal(t, "main", repoCtx.BaseBranch)
}

func TestRepositoryDetectRepo_UsesPrimaryWorktreeWhenCalledFromLinkedWorktree(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	runner.EXPECT().
		Run(mock.Anything, "/tmp/repo-auth", "git", "rev-parse", "--show-toplevel").
		Return(subprocess.Result{Stdout: "/tmp/repo-auth\n"}, nil).
		Once()
	runner.EXPECT().
		Run(mock.Anything, "/tmp/repo-auth", "git", "worktree", "list", "--porcelain").
		Return(subprocess.Result{
			Stdout: "worktree /tmp/repo\nHEAD abcdef\nbranch refs/heads/main\n\n" +
				"worktree /tmp/repo-auth\nHEAD 123456\nbranch refs/heads/feat/auth\n",
		}, nil).
		Once()
	runner.EXPECT().
		Run(mock.Anything, "/tmp/repo", "git", "branch", "--show-current").
		Return(subprocess.Result{Stdout: "main\n"}, nil).
		Once()

	repoCtx, err := New(runner).DetectRepo(context.Background(), "/tmp/repo-auth")
	require.NoError(t, err)
	require.Equal(t, "/tmp/repo", repoCtx.Root)
	require.Equal(t, "repo", repoCtx.Name)
	require.Equal(t, "main", repoCtx.BaseBranch)
}

func TestRepositoryIsBranchUsedByWorktree_ReturnsTrueWhenBranchIsCheckedOutElsewhere(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	runner.EXPECT().
		Run(mock.Anything, "/tmp/repo", "git", "worktree", "list", "--porcelain").
		Return(subprocess.Result{
			Stdout: "worktree /tmp/repo\nHEAD abcdef\nbranch refs/heads/main\n\n" +
				"worktree /tmp/repo-auth\nHEAD 123456\nbranch refs/heads/feat/auth\n",
		}, nil).
		Once()

	used, err := New(runner).IsBranchUsedByWorktree(context.Background(), "/tmp/repo", "feat/auth")
	require.NoError(t, err)
	require.True(t, used)
}

func TestRepositoryIsBranchUsedByWorktree_IgnoresPrunableWorktrees(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	runner.EXPECT().
		Run(mock.Anything, "/tmp/repo", "git", "worktree", "list", "--porcelain").
		Return(subprocess.Result{
			Stdout: "worktree /tmp/repo\nHEAD abcdef\nbranch refs/heads/main\n\n" +
				"worktree /tmp/repo-auth\nHEAD 123456\nbranch refs/heads/feat/auth\n" +
				"prunable gitdir file points to non-existent location\n",
		}, nil).
		Once()

	used, err := New(runner).IsBranchUsedByWorktree(context.Background(), "/tmp/repo", "feat/auth")
	require.NoError(t, err)
	require.False(t, used)
}

func TestRepositoryCreateTaskWorkspace_UsesDetectedBaseBranch(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	runner.EXPECT().
		Run(mock.Anything, "/tmp/repo", "git", "rev-parse", "--show-toplevel").
		Return(subprocess.Result{Stdout: "/tmp/repo\n"}, nil).
		Once()
	runner.EXPECT().
		Run(mock.Anything, "/tmp/repo", "git", "worktree", "list", "--porcelain").
		Return(subprocess.Result{
			Stdout: "worktree /tmp/repo\nHEAD abcdef\nbranch refs/heads/main\n",
		}, nil).
		Once()
	runner.EXPECT().
		Run(mock.Anything, "/tmp/repo", "git", "branch", "--show-current").
		Return(subprocess.Result{Stdout: "main\n"}, nil).
		Once()
	runner.EXPECT().
		Run(mock.Anything, "/tmp/repo", "git", "worktree", "add", "/tmp/repo-billing-retry-flow", "-b", "feat/billing-retry-flow", "main").
		Return(subprocess.Result{}, nil).
		Once()

	err := New(runner).CreateTaskWorkspace(context.Background(), &core.Task{
		RepoRoot:     "/tmp/repo",
		BranchName:   "feat/billing-retry-flow",
		WorktreePath: "/tmp/repo-billing-retry-flow",
	})
	require.NoError(t, err)
}

func TestRepositoryCreateTaskWorkspaceFromBranch_UsesExistingBranch(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	runner.EXPECT().
		Run(mock.Anything, "/tmp/repo", "git", "worktree", "add", "/tmp/repo-auth-rewrite", "feat/auth-rewrite").
		Return(subprocess.Result{}, nil).
		Once()

	err := New(runner).CreateTaskWorkspaceFromBranch(context.Background(), &core.Task{
		RepoRoot:     "/tmp/repo",
		BranchName:   "feat/auth-rewrite",
		WorktreePath: "/tmp/repo-auth-rewrite",
	})
	require.NoError(t, err)
}

func TestRepositoryRemoveTaskWorkspace_RemovesWorktreePath(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	worktreePath := filepath.Join(t.TempDir(), "repo-auth-rewrite")
	require.NoError(t, os.Mkdir(worktreePath, 0o755))
	runner.EXPECT().
		Run(mock.Anything, "/tmp/repo", "git", "worktree", "remove", "--force", worktreePath).
		Return(subprocess.Result{}, nil).
		Once()

	err := New(runner).RemoveTaskWorkspace(context.Background(), &core.Task{
		RepoRoot:     "/tmp/repo",
		WorktreePath: worktreePath,
	})
	require.NoError(t, err)
}

func TestRepositoryRemoveTaskWorkspace_PrunesStaleWorktreeMetadataWhenPathIsMissing(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	worktreePath := filepath.Join(t.TempDir(), "repo-auth-rewrite")
	runner.EXPECT().
		Run(mock.Anything, "/tmp/repo", "git", "worktree", "prune").
		Return(subprocess.Result{}, nil).
		Once()

	err := New(runner).RemoveTaskWorkspace(context.Background(), &core.Task{
		RepoRoot:     "/tmp/repo",
		WorktreePath: worktreePath,
	})
	require.NoError(t, err)
}
