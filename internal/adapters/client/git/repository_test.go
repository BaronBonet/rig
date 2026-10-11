package git

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/BaronBonet/rig/internal/core"
	"github.com/BaronBonet/rig/internal/pkg/subprocess"

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

func TestRepositoryCreateTaskWorkspaceFromPullRequest_FetchesPRHeadBeforeAddingWorktree(t *testing.T) {
	runner := subprocess.NewMockRunner(t)
	runner.EXPECT().
		Run(
			mock.Anything,
			"/tmp/repo",
			"git",
			"fetch",
			"origin",
			"+refs/pull/42/head:refs/heads/feat/auth-rewrite",
		).
		Return(subprocess.Result{}, nil).
		Once()
	runner.EXPECT().
		Run(mock.Anything, "/tmp/repo", "git", "worktree", "add", "/tmp/repo-auth-rewrite", "feat/auth-rewrite").
		Return(subprocess.Result{}, nil).
		Once()

	err := New(runner).CreateTaskWorkspaceFromPullRequest(context.Background(), &core.Task{
		RepoRoot:     "/tmp/repo",
		BranchName:   "feat/auth-rewrite",
		WorktreePath: "/tmp/repo-auth-rewrite",
	}, 42)
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

// runGit runs git in dir, isolated from the user's and the system's git
// config.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
}

// initRepo makes a git repository with one commit at dir.
func initRepo(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o750))
	runGit(t, dir, "init", "--quiet")
	runGit(t, dir, "-c", "user.name=rig", "-c", "user.email=rig@example.com",
		"commit", "--quiet", "--allow-empty", "-m", "init")
}

// physicalTempDir returns a temporary directory with its symlinks resolved,
// as /var is a symlink on macOS.
func physicalTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	return dir
}

func TestRepositoryListRepoCheckouts_ListsOnlyThePrimaryCheckouts(t *testing.T) {
	code := physicalTempDir(t)
	initRepo(t, filepath.Join(code, "frontend"))
	initRepo(t, filepath.Join(code, "backend"))
	runGit(t, filepath.Join(code, "backend"), "worktree", "add", "--quiet", "-b", "feat/retries",
		filepath.Join(code, "backend_add-retries"))
	initRepo(t, filepath.Join(code, ".dotfiles"))
	require.NoError(t, os.Mkdir(filepath.Join(code, "notes"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(code, "README.md"), []byte("code\n"), 0o600))
	initRepo(t, filepath.Join(code, "tools", "nested"))
	require.FileExists(t, filepath.Join(code, "backend_add-retries", ".git"))

	// The listing reads the filesystem alone, so the runner is never called.
	checkouts, err := New(subprocess.NewMockRunner(t)).ListRepoCheckouts(context.Background(), code)

	require.NoError(t, err)
	require.Equal(t, []string{filepath.Join(code, "backend"), filepath.Join(code, "frontend")}, checkouts)
}

func TestRepositoryListRepoCheckouts_ListsTheCheckoutsWithTheirSymlinksResolved(t *testing.T) {
	root := physicalTempDir(t)
	initRepo(t, filepath.Join(root, "disk", "frontend"))
	require.NoError(t, os.Symlink(filepath.Join(root, "disk"), filepath.Join(root, "code")))

	checkouts, err := New(subprocess.NewMockRunner(t)).ListRepoCheckouts(
		context.Background(),
		filepath.Join(root, "code"),
	)

	require.NoError(t, err)
	require.Equal(t, []string{filepath.Join(root, "disk", "frontend")}, checkouts)
}

// A symlink to a checkout lists the checkout at the place it links to, as
// git reports its root.
func TestRepositoryListRepoCheckouts_ListsSymlinkedCheckoutsWhereTheyLinkTo(t *testing.T) {
	root := physicalTempDir(t)
	code := filepath.Join(root, "code")
	initRepo(t, filepath.Join(code, "backend"))
	initRepo(t, filepath.Join(root, "ssd", "frontend"))
	require.NoError(t, os.Mkdir(filepath.Join(root, "ssd", "notes"), 0o750))
	require.NoError(t, os.Symlink(filepath.Join(root, "ssd", "frontend"), filepath.Join(code, "frontend")))
	require.NoError(t, os.Symlink(filepath.Join(root, "ssd", "notes"), filepath.Join(code, "notes")))
	require.NoError(t, os.Symlink(filepath.Join(code, "backend"), filepath.Join(code, "backend-link")))
	require.NoError(t, os.Symlink(filepath.Join(root, "moved"), filepath.Join(code, "broken")))

	checkouts, err := New(subprocess.NewMockRunner(t)).ListRepoCheckouts(context.Background(), code)

	require.NoError(t, err)
	require.Equal(t, []string{filepath.Join(code, "backend"), filepath.Join(root, "ssd", "frontend")}, checkouts)
}

// On a case-insensitive filesystem a repo directory may be typed in any case,
// but git reports a repository's root in the case on disk.
func TestRepositoryListRepoCheckouts_ListsTheCheckoutsInTheCaseOnDisk(t *testing.T) {
	root := physicalTempDir(t)
	initRepo(t, filepath.Join(root, "Code", "frontend"))
	if _, err := os.Stat(filepath.Join(root, "CODE")); err != nil {
		t.Skip("the temporary directory's filesystem is case-sensitive")
	}

	checkouts, err := New(subprocess.NewMockRunner(t)).ListRepoCheckouts(
		context.Background(),
		filepath.Join(root, "code"),
	)

	require.NoError(t, err)
	require.Equal(t, []string{filepath.Join(root, "Code", "frontend")}, checkouts)
}

func TestRepositoryListRepoCheckouts_ListsNothingInADirectoryWithoutCheckouts(t *testing.T) {
	checkouts, err := New(subprocess.NewMockRunner(t)).ListRepoCheckouts(context.Background(), t.TempDir())

	require.NoError(t, err)
	require.Empty(t, checkouts)
}

func TestRepositoryListRepoCheckouts_FailsWhenTheDirectoryDoesNotExist(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "moved")

	_, err := New(subprocess.NewMockRunner(t)).ListRepoCheckouts(context.Background(), missing)

	require.ErrorIs(t, err, fs.ErrNotExist)
}
