package git

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
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

// A checkout's from side is the branch HEAD was on, or a whole commit ID. Its
// to side is what the checkout was given, whatever that was.
func TestReflogBranchNames_ReadsBranchesCheckoutsMovedFromAndBranchRenames(t *testing.T) {
	reflog := "checkout: moving from feat/c2 to part-2\n" +
		"Branch: renamed refs/heads/feat/c to refs/heads/feat/c2\n" +
		"checkout: moving from 51f8929aeb890858b60fe3bcdabf5e73d8c98aa9 to feat/a\n" +
		"checkout: moving from feat/a to 51F8929A\n" +
		"checkout: moving from feat/a to refs/heads/side\n" +
		"checkout: moving from feat/a to origin/main\n" +
		"checkout: moving from side to feat/a\n" +
		"commit: s1\n" +
		"rebase (finish): returning to refs/heads/side\n" +
		"reset: moving to HEAD\n"

	require.Equal(
		t,
		[]string{"feat/c2", "feat/c", "feat/c2", "feat/a", "feat/a", "feat/a", "side"},
		reflogBranchNames(reflog),
	)
}

func TestRepositoryListWorktreeBranches_FailsWhenTheWorktreeDoesNotExist(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "repo_billing")

	// The runner is never called.
	_, err := New(subprocess.NewMockRunner(t)).ListWorktreeBranches(context.Background(), missing)

	require.ErrorIs(t, err, fs.ErrNotExist)
}

// gitCommit makes an empty commit in dir.
func gitCommit(t *testing.T, dir string, message string) {
	t.Helper()
	runGit(t, dir, "-c", "user.name=rig", "-c", "user.email=rig@example.com",
		"commit", "--quiet", "--allow-empty", "-m", message)
}

// gitOutput runs git in dir, as runGit does, and returns its output trimmed.
func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.Output()
	require.NoError(t, err, "git %v", args)
	return strings.TrimSpace(string(out))
}

// A worktree's reflog logs whatever a checkout was given on the side it moves
// to: a branch's full ref name, a commit ID in upper case, a revision, a
// remote-tracking branch or a tag. Only the branches HEAD was on are the
// worktree's, including those deleted since.
func TestRepositoryListWorktreeBranches_ListsTheBranchesTheWorktreeHasHadCheckedOut(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	root := physicalTempDir(t)
	repo := filepath.Join(root, "repo")
	worktree := filepath.Join(root, "repo_billing")
	initRepo(t, repo)
	runGit(t, repo, "branch", "-M", "main")
	runGit(t, repo, "tag", "v1")
	runGit(t, repo, "update-ref", "refs/remotes/origin/main", "HEAD")
	runGit(t, repo, "worktree", "add", "--quiet", "-b", "feat/a", worktree, "main")

	gitCommit(t, worktree, "a1")
	runGit(t, worktree, "checkout", "--quiet", "-b", "side", "main")
	gitCommit(t, worktree, "s1")
	upperCaseID := strings.ToUpper(gitOutput(t, worktree, "rev-parse", "--short=8", "side"))
	// Each detaching checkout starts from feat/a, on another commit, as git
	// logs only a checkout that moves HEAD.
	for _, detached := range []string{"refs/heads/side", upperCaseID, "side~0", "origin/main", "v1"} {
		runGit(t, worktree, "checkout", "--quiet", "feat/a")
		runGit(t, worktree, "checkout", "--quiet", detached)
	}
	runGit(t, worktree, "checkout", "--quiet", "feat/a")
	runGit(t, worktree, "switch", "--quiet", "-c", "feat/c")
	runGit(t, worktree, "branch", "-m", "feat/c", "feat/c2")
	runGit(t, worktree, "switch", "--quiet", "-c", "part-2", "main")
	// gh pr merge --delete-branch deletes a merged pull request's branch.
	runGit(t, worktree, "branch", "--quiet", "-D", "feat/c2", "side")

	reflog := gitOutput(t, worktree, "reflog", "show", "HEAD", "--format=%gs")
	for _, detached := range []string{"refs/heads/side", upperCaseID, "side~0", "origin/main", "v1"} {
		require.Contains(t, reflog, "checkout: moving from feat/a to "+detached+"\n")
	}

	branches, err := New(subprocess.ExecRunner{}).ListWorktreeBranches(context.Background(), worktree)

	require.NoError(t, err)
	require.Equal(t, []string{"feat/a", "feat/c", "feat/c2", "part-2", "side"}, branches)
}

// A worktree whose branch was created by git worktree add has nothing about it
// in its reflog, so its current branch counts too.
func TestRepositoryListWorktreeBranches_IncludesTheBranchCheckedOutNow(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	root := physicalTempDir(t)
	repo := filepath.Join(root, "repo")
	worktree := filepath.Join(root, "repo_billing")
	initRepo(t, repo)
	runGit(t, repo, "worktree", "add", "--quiet", "-b", "feat/billing-export", worktree)

	branches, err := New(subprocess.ExecRunner{}).ListWorktreeBranches(context.Background(), worktree)

	require.NoError(t, err)
	require.Equal(t, []string{"feat/billing-export"}, branches)
}

const (
	shippedHeadA = "a7d3f0c2b8e14f6a9c5d2e7b1f0a3c8d6e4b2a90"
	shippedHeadB = "b2c4e6f8a0b2c4e6f8a0b2c4e6f8a0b2c4e6f8a0"
)

// expectGit expects one git run in dir with args and stdin, never fetching
// objects a partial clone lacks.
func expectGit(
	runner *subprocess.MockRunner,
	dir string,
	stdin string,
	result subprocess.Result,
	err error,
	args ...string,
) {
	runner.EXPECT().
		RunWithStdin(mock.Anything, subprocess.RunWithStdinOptions{
			Cwd:   dir,
			Stdin: stdin,
			Name:  "git",
			Args:  args,
			Env:   []string{"GIT_NO_LAZY_FETCH=1"},
		}).
		Return(result, err).
		Once()
}

var (
	unshippedStatusArgs = []string{
		"--no-optional-locks", "status", "--porcelain", "--untracked-files=normal", "--ignore-submodules=dirty",
		"--", ".",
	}
	unshippedRevListArgs = []string{
		"rev-list", "--count", "--no-merges", "--ignore-missing", "--stdin", "HEAD", "--",
	}
)

func TestRepositoryUnshippedWork_CountsCommitsNothingShippedReaches(t *testing.T) {
	worktree := t.TempDir()
	runner := subprocess.NewMockRunner(t)
	statusArgs := append(slices.Clone(unshippedStatusArgs),
		":(exclude,literal).claude/settings.local.json", ":(exclude,literal)rig files/a.json")
	expectGit(runner, worktree, "", subprocess.Result{Stdout: " M main.go\n?? notes.md\n"}, nil, statusArgs...)
	expectGit(runner, worktree, "", subprocess.Result{Stdout: "origin\nupstream\n"}, nil, "remote")
	expectGit(
		runner,
		worktree,
		"--not\n"+
			"refs/heads/develop\nrefs/remotes/origin/develop\nrefs/remotes/upstream/develop\n"+
			"refs/heads/main\nrefs/remotes/origin/main\nrefs/remotes/upstream/main\n"+
			shippedHeadA+"\n"+shippedHeadB+"\n"+
			"refs/remotes/origin/feat/billing-2\nrefs/remotes/upstream/feat/billing-2\n",
		subprocess.Result{Stdout: "3\n"},
		nil,
		unshippedRevListArgs...,
	)

	work, err := New(runner).UnshippedWork(context.Background(), worktree, core.ShippedWork{
		BaseBranches: []string{"develop", "main"},
		// A commit that is not a whole commit ID is never passed to git.
		PullRequestCommits: []string{shippedHeadA, "--all", shippedHeadB},
		OpenHeadBranches:   []string{"feat/billing-2"},
	}, []string{".claude/settings.local.json", "rig files/a.json"})

	require.NoError(t, err)
	require.Equal(t, core.UnshippedWork{Commits: 3, UncommittedChanges: true}, work)
}

func TestRepositoryUnshippedWork_FindsACleanWorktree(t *testing.T) {
	worktree := t.TempDir()
	runner := subprocess.NewMockRunner(t)
	expectGit(runner, worktree, "", subprocess.Result{}, nil, unshippedStatusArgs...)
	expectGit(runner, worktree, "", subprocess.Result{}, nil, "remote")
	expectGit(
		runner,
		worktree,
		"--not\nrefs/heads/main\n",
		subprocess.Result{Stdout: "0\n"},
		nil,
		unshippedRevListArgs...)

	work, err := New(runner).UnshippedWork(
		context.Background(),
		worktree,
		core.ShippedWork{BaseBranches: []string{"main"}},
		nil,
	)

	require.NoError(t, err)
	require.Equal(t, core.UnshippedWork{}, work)
}

func TestRepositoryUnshippedWork_FailsWhenGitFails(t *testing.T) {
	worktree := t.TempDir()
	runner := subprocess.NewMockRunner(t)
	expectGit(runner, worktree, "", subprocess.Result{}, nil, unshippedStatusArgs...)
	expectGit(runner, worktree, "", subprocess.Result{}, nil, "remote")
	expectGit(
		runner,
		worktree,
		"--not\nrefs/heads/main\n",
		subprocess.Result{},
		errors.New("fatal: not a git repository"),
		unshippedRevListArgs...,
	)

	_, err := New(runner).UnshippedWork(
		context.Background(),
		worktree,
		core.ShippedWork{BaseBranches: []string{"main"}},
		nil,
	)

	require.ErrorContains(t, err, "not a git repository")
}

func TestRepositoryUnshippedWork_FailsWhenTheWorktreeDoesNotExist(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "repo_billing")

	// The runner is never called.
	_, err := New(subprocess.NewMockRunner(t)).UnshippedWork(context.Background(), missing, core.ShippedWork{}, nil)

	require.ErrorIs(t, err, fs.ErrNotExist)
}

// shippingRepo is a repository pushing to a bare origin, with a task worktree
// on feat/a made from origin/main.
type shippingRepo struct {
	root     string
	repo     string
	worktree string
	git      core.GitWorktreeClient
}

// newShippingRepo makes a shippingRepo, isolated from the user's and the
// system's git configuration, the global ignore file included.
func newShippingRepo(t *testing.T) shippingRepo {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := physicalTempDir(t)
	origin := filepath.Join(root, "origin.git")
	repo := filepath.Join(root, "repo")
	worktree := filepath.Join(root, "repo_billing")
	runGit(t, root, "init", "--quiet", "--bare", origin)
	initRepo(t, repo)
	runGit(t, repo, "branch", "-M", "main")
	runGit(t, repo, "remote", "add", "origin", origin)
	runGit(t, repo, "push", "--quiet", "origin", "main")
	runGit(t, repo, "worktree", "add", "--quiet", "-b", "feat/a", worktree, "origin/main")
	return shippingRepo{root: root, repo: repo, worktree: worktree, git: New(subprocess.ExecRunner{})}
}

// unshipped finds the worktree's unshipped work against shipped, whose base
// branch is main unless it names others.
func (r shippingRepo) unshipped(t *testing.T, shipped core.ShippedWork, rigFiles ...string) core.UnshippedWork {
	t.Helper()
	if shipped.BaseBranches == nil {
		shipped.BaseBranches = []string{"main"}
	}
	work, err := r.git.UnshippedWork(context.Background(), r.worktree, shipped, rigFiles)
	require.NoError(t, err)
	return work
}

func (r shippingRepo) head(t *testing.T) string {
	t.Helper()
	return gitOutput(t, r.worktree, "rev-parse", "HEAD")
}

// squashMerge squash merges branch into base in the main checkout, as GitHub
// would, and pushes base.
func (r shippingRepo) squashMerge(t *testing.T, branch string, base string) {
	t.Helper()
	runGit(t, r.repo, "switch", "--quiet", base)
	runGit(t, r.repo, "merge", "--quiet", "--squash", branch)
	runGit(t, r.repo, "-c", "user.name=rig", "-c", "user.email=rig@example.com",
		"commit", "--quiet", "--allow-empty", "-m", "squash "+branch)
	runGit(t, r.repo, "push", "--quiet", "origin", base)
}

// A task's work ships in a pull request, whose head commit holds it even once
// a squash merge leaves the branch's commits off the default branch, or on
// the default branch. Pushing alone ships nothing.
func TestRepositoryUnshippedWork_FindsTheWorkNoPullRequestContains(t *testing.T) {
	r := newShippingRepo(t)

	// feat/a is pushed, which ships nothing without a pull request. Open pull
	// request #1's head is its pushed commit.
	gitCommit(t, r.worktree, "a1")
	runGit(t, r.worktree, "push", "--quiet", "-u", "origin", "feat/a")
	require.Equal(t, core.UnshippedWork{Commits: 1}, r.unshipped(t, core.ShippedWork{}))
	openPR := core.ShippedWork{PullRequestCommits: []string{r.head(t)}, OpenHeadBranches: []string{"feat/a"}}
	require.Equal(t, core.UnshippedWork{}, r.unshipped(t, openPR))

	// A commit on top is unshipped, and so is an untracked file.
	gitCommit(t, r.worktree, "a2")
	require.Equal(t, core.UnshippedWork{Commits: 1}, r.unshipped(t, openPR))
	require.NoError(t, os.WriteFile(filepath.Join(r.worktree, "notes.md"), []byte("notes\n"), 0o600))
	require.Equal(t, core.UnshippedWork{Commits: 1, UncommittedChanges: true}, r.unshipped(t, openPR))
	require.NoError(t, os.Remove(filepath.Join(r.worktree, "notes.md")))

	// Pushed, a2 is in the open pull request even while GitHub has moved its
	// head to a commit never fetched, as its Update branch button does.
	runGit(t, r.worktree, "push", "--quiet")
	updatedOnGitHub := core.ShippedWork{
		PullRequestCommits: []string{shippedHeadA},
		OpenHeadBranches:   []string{"feat/a"},
	}
	require.Equal(t, core.UnshippedWork{}, r.unshipped(t, updatedOnGitHub))
	updatedOnGitHub.OpenHeadBranches = nil
	require.Equal(t, core.UnshippedWork{Commits: 2}, r.unshipped(t, updatedOnGitHub))

	// Squash merged, #1's commits are not on main, but its head holds them.
	mergedHead := r.head(t)
	r.squashMerge(t, "feat/a", "main")
	require.Equal(t, core.UnshippedWork{Commits: 2}, r.unshipped(t, core.ShippedWork{}))
	merged := core.ShippedWork{PullRequestCommits: []string{mergedHead}}
	require.Equal(t, core.UnshippedWork{}, r.unshipped(t, merged))

	// Merging main in brings nothing new of the task's.
	runGit(t, r.worktree, "-c", "user.name=rig", "-c", "user.email=rig@example.com",
		"merge", "--quiet", "--no-edit", "origin/main")
	// HEAD is the merge, with two parents.
	require.Len(t, strings.Fields(gitOutput(t, r.worktree, "rev-list", "--parents", "-n", "1", "HEAD")), 3)
	require.Equal(t, core.UnshippedWork{}, r.unshipped(t, merged))

	// A commit pushed to the merged branch, with no new pull request, is
	// unshipped: its remote copy no longer counts once the pull request
	// merged.
	gitCommit(t, r.worktree, "a3")
	runGit(t, r.worktree, "push", "--quiet")
	require.Equal(t, core.UnshippedWork{Commits: 1}, r.unshipped(t, merged))

	// A new branch from the merged main has nothing unshipped yet.
	runGit(t, r.worktree, "switch", "--quiet", "-c", "part-2", "origin/main")
	require.Equal(t, core.UnshippedWork{}, r.unshipped(t, merged))

	// Commits on the local main that origin lacks are the default branch's,
	// not the task's.
	runGit(t, r.repo, "-c", "user.name=rig", "-c", "user.email=rig@example.com",
		"commit", "--quiet", "--allow-empty", "-m", "local only")
	runGit(t, r.worktree, "switch", "--quiet", "-c", "part-3", "main")
	require.Equal(t, core.UnshippedWork{}, r.unshipped(t, merged))
	gitCommit(t, r.worktree, "p1")
	require.Equal(t, core.UnshippedWork{Commits: 1}, r.unshipped(t, merged))
}

// A pull request whose last commit was made on GitHub, as by its Update branch
// button or an applied suggestion, was squash merged and its branch deleted
// and pruned: neither its head nor its branch is in the repository, but its
// commits ship the worktree's.
func TestRepositoryUnshippedWork_PullRequestCommitsShipWorkAfterTheBranchIsGone(t *testing.T) {
	r := newShippingRepo(t)
	gitCommit(t, r.worktree, "a1")
	a1 := r.head(t)
	gitCommit(t, r.worktree, "a2")
	a2 := r.head(t)
	runGit(t, r.worktree, "push", "--quiet", "-u", "origin", "feat/a")
	r.squashMerge(t, "feat/a", "main")
	runGit(t, r.repo, "push", "--quiet", "origin", "--delete", "feat/a")
	runGit(t, r.worktree, "fetch", "--quiet", "--prune")

	madeOnGitHub := core.ShippedWork{PullRequestCommits: []string{shippedHeadA}}
	require.Equal(t, core.UnshippedWork{Commits: 2}, r.unshipped(t, madeOnGitHub))
	madeOnGitHub.PullRequestCommits = []string{a1, a2, shippedHeadA}
	require.Equal(t, core.UnshippedWork{}, r.unshipped(t, madeOnGitHub))
}

// A pull request into develop, squash merged there, ships the work a branch
// made from develop holds.
func TestRepositoryUnshippedWork_BranchesPullRequestsMergeIntoShipWork(t *testing.T) {
	r := newShippingRepo(t)
	runGit(t, r.repo, "push", "--quiet", "origin", "main:develop")
	runGit(t, r.repo, "branch", "--quiet", "develop", "origin/main")
	gitCommit(t, r.worktree, "a1")
	gitCommit(t, r.worktree, "a2")
	runGit(t, r.worktree, "push", "--quiet", "-u", "origin", "feat/a")
	r.squashMerge(t, "feat/a", "develop")
	runGit(t, r.worktree, "fetch", "--quiet")
	runGit(t, r.worktree, "switch", "--quiet", "-c", "part-2", "origin/develop")

	require.Equal(t, core.UnshippedWork{Commits: 1}, r.unshipped(t, core.ShippedWork{}))
	intoDevelop := core.ShippedWork{BaseBranches: []string{"develop", "main"}}
	require.Equal(t, core.UnshippedWork{}, r.unshipped(t, intoDevelop))
}

// The settings Claude keeps in the worktree, which Rig writes, are not the
// task's work, with no ignore rule for them, while the user's own files there
// are.
func TestRepositoryUnshippedWork_LeavesOutTheFilesRigWrites(t *testing.T) {
	r := newShippingRepo(t)
	settings := filepath.Join(r.worktree, ".claude", "settings.local.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(settings), 0o750))
	require.NoError(t, os.WriteFile(settings, []byte("{}\n"), 0o600))
	rigFile := ".claude/settings.local.json"

	require.Equal(t, core.UnshippedWork{UncommittedChanges: true}, r.unshipped(t, core.ShippedWork{}))
	require.Equal(t, core.UnshippedWork{}, r.unshipped(t, core.ShippedWork{}, rigFile))

	require.NoError(t, os.WriteFile(filepath.Join(r.worktree, ".claude", "notes.md"), []byte("mine\n"), 0o600))
	require.Equal(t, core.UnshippedWork{UncommittedChanges: true}, r.unshipped(t, core.ShippedWork{}, rigFile))
}

// Ignored files are not work, while untracked ones are, also when the
// repository hides them from git status.
func TestRepositoryUnshippedWork_CountsUntrackedButNotIgnoredFiles(t *testing.T) {
	r := newShippingRepo(t)
	exclude := filepath.Join(r.repo, ".git", "info", "exclude")
	require.NoError(t, os.WriteFile(exclude, []byte("*.log\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(r.worktree, "debug.log"), []byte("log\n"), 0o600))
	require.Equal(t, core.UnshippedWork{}, r.unshipped(t, core.ShippedWork{}))

	runGit(t, r.repo, "config", "status.showUntrackedFiles", "no")
	require.NoError(t, os.WriteFile(filepath.Join(r.worktree, "notes.md"), []byte("notes\n"), 0o600))
	require.Empty(t, gitOutput(t, r.worktree, "status", "--porcelain"))
	require.Equal(t, core.UnshippedWork{UncommittedChanges: true}, r.unshipped(t, core.ShippedWork{}))
}

// A file named HEAD is never taken for the revision.
func TestRepositoryUnshippedWork_WorksBesideAFileNamedLikeARevision(t *testing.T) {
	r := newShippingRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(r.worktree, "HEAD"), []byte("HEAD\n"), 0o600))
	runGit(t, r.worktree, "add", "HEAD")
	gitCommit(t, r.worktree, "add a file named HEAD")

	require.Equal(t, core.UnshippedWork{Commits: 1}, r.unshipped(t, core.ShippedWork{}))
}

// A fork's main, on origin, may lag the main of the repository it was forked
// from, on upstream, whose copy ships the work it holds.
func TestRepositoryUnshippedWork_EveryRemotesDefaultBranchShipsWork(t *testing.T) {
	r := newShippingRepo(t)
	upstream := filepath.Join(r.root, "upstream.git")
	runGit(t, r.root, "init", "--quiet", "--bare", upstream)
	runGit(t, r.repo, "remote", "add", "upstream", upstream)
	runGit(t, r.repo, "-c", "user.name=rig", "-c", "user.email=rig@example.com",
		"commit", "--quiet", "--allow-empty", "-m", "merged upstream")
	runGit(t, r.repo, "push", "--quiet", "upstream", "main")
	runGit(t, r.repo, "reset", "--quiet", "--hard", "origin/main")
	runGit(t, r.worktree, "switch", "--quiet", "-c", "part-2", "upstream/main")

	require.Equal(t, core.UnshippedWork{}, r.unshipped(t, core.ShippedWork{}))
	runGit(t, r.repo, "remote", "remove", "upstream")
	require.Equal(t, core.UnshippedWork{Commits: 1}, r.unshipped(t, core.ShippedWork{}))
}

// Changes inside a submodule's own working tree are the submodule's, not the
// task's work, while moving the submodule to another commit is.
func TestRepositoryUnshippedWork_PassesOverChangesInsideSubmodules(t *testing.T) {
	r := newShippingRepo(t)
	library := filepath.Join(r.root, "library")
	initRepo(t, library)
	runGit(t, r.worktree, "-c", "protocol.file.allow=always", "submodule", "--quiet", "add", library, "library")
	gitCommit(t, r.worktree, "add library")
	require.Equal(t, core.UnshippedWork{Commits: 1}, r.unshipped(t, core.ShippedWork{}))

	submodule := filepath.Join(r.worktree, "library")
	require.NoError(t, os.WriteFile(filepath.Join(submodule, "scratch.txt"), []byte("x\n"), 0o600))
	require.Equal(t, core.UnshippedWork{Commits: 1}, r.unshipped(t, core.ShippedWork{}))

	gitCommit(t, submodule, "library change")
	require.Equal(t, core.UnshippedWork{Commits: 1, UncommittedChanges: true}, r.unshipped(t, core.ShippedWork{}))
}

// A partial clone lacks objects its promisor remote has, which git would
// fetch to look a commit up, slowly or, with the remote gone, in vain. Pull
// request commits may well be missing, so they are passed over unfetched.
func TestRepositoryUnshippedWork_NeverFetchesWhatAPartialCloneLacks(t *testing.T) {
	r := newShippingRepo(t)
	source := filepath.Join(r.root, "source")
	initRepo(t, source)
	runGit(t, source, "config", "uploadpack.allowFilter", "true")
	clone := filepath.Join(r.root, "clone")
	runGit(t, r.root, "clone", "--quiet", "--filter=blob:none", "file://"+source, clone)
	require.NoError(t, os.Rename(source, source+"-gone"))
	trace := filepath.Join(r.root, "trace.txt")
	t.Setenv("GIT_TRACE", trace)

	work, err := r.git.UnshippedWork(context.Background(), clone, core.ShippedWork{
		BaseBranches:       []string{gitOutput(t, clone, "branch", "--show-current")},
		PullRequestCommits: []string{shippedHeadA},
	}, nil)

	require.NoError(t, err)
	require.Equal(t, core.UnshippedWork{}, work)
	traced, err := os.ReadFile(trace)
	require.NoError(t, err)
	// Git traces each command it runs, a lazy fetch's included.
	require.NotContains(t, string(traced), " fetch ")
}
