package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"agent/internal/core"
	"agent/internal/pkg/execx"

	"github.com/stretchr/testify/require"
)

func TestRepositoryDetectRepo_ParsesTopLevelAndBranch(t *testing.T) {
	runner := execx.NewFakeRunner([]execx.Result{
		{Stdout: "/tmp/repo\n"},
		{Stdout: "main\n"},
	})
	repo := NewRepository(runner)

	repoCtx, err := repo.DetectRepo(context.Background(), "/tmp/repo")
	require.NoError(t, err)
	require.Equal(t, "/tmp/repo", repoCtx.Root)
	require.Equal(t, "main", repoCtx.BaseBranch)
	require.Equal(t, "repo", repoCtx.Name)
}

func TestRepositoryCreateWorktree_UsesExpectedGitCommand(t *testing.T) {
	runner := execx.NewFakeRunner([]execx.Result{{}})
	repo := NewRepository(runner)

	err := repo.CreateWorktree(context.Background(), core.CreateWorktreeInput{
		RepoRoot:     "/tmp/repo",
		BaseBranch:   "main",
		BranchName:   "feat/billing-retry-flow",
		WorktreePath: "/tmp/repo-billing-retry-flow",
	})
	require.NoError(t, err)
	require.Len(t, runner.Calls, 1)
	require.Equal(t, "git", runner.Calls[0].Name)
	require.Equal(t, []string{
		"worktree",
		"add",
		"/tmp/repo-billing-retry-flow",
		"-b",
		"feat/billing-retry-flow",
		"main",
	}, runner.Calls[0].Args)
}

func TestRepositoryCreateTaskWorkspace_UsesTaskFields(t *testing.T) {
	runner := execx.NewFakeRunner([]execx.Result{{}})
	repo := NewRepository(runner)

	err := repo.CreateTaskWorkspace(context.Background(), &core.Task{
		RepoRoot:     "/tmp/repo",
		BaseBranch:   "main",
		BranchName:   "feat/billing-retry-flow",
		WorktreePath: "/tmp/repo-billing-retry-flow",
	})
	require.NoError(t, err)
	require.Equal(t, []string{
		"worktree",
		"add",
		"/tmp/repo-billing-retry-flow",
		"-b",
		"feat/billing-retry-flow",
		"main",
	}, runner.Calls[0].Args)
}

func TestRepositoryInspectTaskWorkspace_ReturnsWorktreeAndBranchPresence(t *testing.T) {
	worktreePath := filepath.Join(t.TempDir(), "repo-billing-retry-flow")
	require.NoError(t, os.Mkdir(worktreePath, 0o755))

	runner := execx.NewFakeRunner([]execx.Result{{}})
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
	require.Equal(t, []string{
		"show-ref",
		"--verify",
		"--quiet",
		"refs/heads/feat/billing-retry-flow",
	}, runner.Calls[0].Args)
}

func TestRepositoryRemoveWorktree_UsesExpectedGitCommand(t *testing.T) {
	runner := execx.NewFakeRunner([]execx.Result{{}})
	repo := NewRepository(runner)

	err := repo.RemoveWorktree(context.Background(), "/tmp/repo", "/tmp/repo-billing-retry-flow")
	require.NoError(t, err)
	require.Len(t, runner.Calls, 1)
	require.Equal(t, "git", runner.Calls[0].Name)
	require.Equal(t, "/tmp/repo", runner.Calls[0].Cwd)
	require.Equal(t, []string{
		"worktree",
		"remove",
		"--force",
		"/tmp/repo-billing-retry-flow",
	}, runner.Calls[0].Args)
}
