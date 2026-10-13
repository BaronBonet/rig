package git

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/BaronBonet/rig/internal/core"
	"github.com/BaronBonet/rig/internal/pkg/subprocess"
)

type repository struct {
	runner subprocess.Runner
}

func New(runner subprocess.Runner) core.GitWorktreeClient {
	return &repository{runner: runner}
}

func (r *repository) HealthCheck(ctx context.Context) error {
	_, err := r.runner.Run(ctx, "", "git", "--version")
	return err
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
	for _, entry := range parseWorktreeEntries(result.Stdout) {
		if entry.prunable {
			continue
		}
		if entry.branch == target {
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

func (r *repository) CreateTaskWorkspaceFromPullRequest(
	ctx context.Context,
	task *core.Task,
	pullRequestNumber int,
) error {
	refspec := fmt.Sprintf("+refs/pull/%d/head:refs/heads/%s", pullRequestNumber, task.BranchName)
	if _, err := r.runner.Run(ctx, task.RepoRoot, "git", "fetch", "origin", refspec); err != nil {
		return err
	}

	return r.CreateTaskWorkspaceFromBranch(ctx, task)
}

func (r *repository) RemoveTaskWorkspace(ctx context.Context, task *core.Task) error {
	if task == nil || strings.TrimSpace(task.WorktreePath) == "" {
		return nil
	}
	if _, err := os.Stat(task.WorktreePath); err != nil {
		if os.IsNotExist(err) {
			_, pruneErr := r.runner.Run(ctx, task.RepoRoot, "git", "worktree", "prune")
			return pruneErr
		}
		return err
	}

	_, err := r.runner.Run(
		ctx,
		task.RepoRoot,
		"git",
		"worktree",
		"remove",
		"--force",
		task.WorktreePath,
	)
	return err
}

// ListWorktreeBranches reads the worktree's own HEAD reflog, where git logs
// each checkout in the worktree, as "checkout: moving from X to Y", and each
// rename of its branch, as "Branch: renamed refs/heads/X to refs/heads/Y".
// Whatever the checkout was given, git logs the side it moves from as the
// branch HEAD was on, or as a whole commit ID when HEAD was detached. The side
// it moves to is the name the checkout was given, which may be a revision, a
// tag or a remote-tracking branch, so it is left out: a branch HEAD moved to
// is the side a later checkout moves from, or the branch checked out now.
func (r *repository) ListWorktreeBranches(ctx context.Context, worktreePath string) ([]string, error) {
	if _, err := os.Stat(worktreePath); err != nil {
		return nil, err
	}

	reflog, err := r.runner.Run(ctx, worktreePath, "git", "reflog", "show", "HEAD", "--format=%gs")
	if err != nil {
		return nil, err
	}
	current, err := r.runner.Run(ctx, worktreePath, "git", "branch", "--show-current")
	if err != nil {
		return nil, err
	}

	branches := reflogBranchNames(reflog.Stdout)
	if branch := strings.TrimSpace(current.Stdout); branch != "" {
		branches = append(branches, branch)
	}
	slices.Sort(branches)
	return slices.Compact(branches), nil
}

// noLazyFetch stops git fetching the objects a partial clone lacks from its
// promisor remote, which could take longer than a refresh, unreachable, as
// for a pull request commit that was never fetched. Git before 2.44 ignores
// it.
const noLazyFetch = "GIT_NO_LAZY_FETCH=1"

// UnshippedWork asks git status for uncommitted changes, and counts the
// non-merge commits on HEAD that none of shipped's refs and commits reaches
// with rev-list, which reads them from stdin, as there may be hundreds.
//
//   - Status takes no optional locks, so it never holds the index lock while
//     an agent runs git in the worktree. It lists untracked files whatever
//     the repository's configuration says, leaves rigFiles out with exclude
//     pathspecs, and passes over changes inside submodules' own working trees,
//     which this repository's pull requests cannot contain.
//   - Rev-list passes over refs and commits git does not have, such as a pull
//     request head made on GitHub that was never fetched. A merge brings in
//     nothing of the task's own: its other parents are shipped or counted by
//     themselves.
//   - Both end their revisions with --, so a file named like one, such as
//     HEAD, is never taken for it.
func (r *repository) UnshippedWork(
	ctx context.Context,
	worktreePath string,
	shipped core.ShippedWork,
	rigFiles []string,
) (core.UnshippedWork, error) {
	if _, err := os.Stat(worktreePath); err != nil {
		return core.UnshippedWork{}, err
	}

	statusArgs := []string{
		"--no-optional-locks",
		"status",
		"--porcelain",
		"--untracked-files=normal",
		"--ignore-submodules=dirty",
		"--",
		".",
	}
	for _, file := range rigFiles {
		statusArgs = append(statusArgs, ":(exclude,literal)"+file)
	}
	status, err := r.git(ctx, worktreePath, "", statusArgs...)
	if err != nil {
		return core.UnshippedWork{}, err
	}
	remotes, err := r.git(ctx, worktreePath, "", "remote")
	if err != nil {
		return core.UnshippedWork{}, err
	}

	var revisions strings.Builder
	revisions.WriteString("--not\n")
	for _, revision := range shippedRevisions(shipped, strings.Fields(remotes.Stdout)) {
		revisions.WriteString(revision + "\n")
	}
	count, err := r.git(
		ctx,
		worktreePath,
		revisions.String(),
		"rev-list",
		"--count",
		"--no-merges",
		"--ignore-missing",
		"--stdin",
		"HEAD",
		"--",
	)
	if err != nil {
		return core.UnshippedWork{}, err
	}
	commits, err := strconv.Atoi(strings.TrimSpace(count.Stdout))
	if err != nil {
		return core.UnshippedWork{}, fmt.Errorf("read unshipped commit count %q: %w", count.Stdout, err)
	}

	return core.UnshippedWork{
		Commits:            commits,
		UncommittedChanges: strings.TrimSpace(status.Stdout) != "",
	}, nil
}

// git runs git in worktreePath with stdin, never fetching objects a partial
// clone lacks.
func (r *repository) git(
	ctx context.Context,
	worktreePath string,
	stdin string,
	args ...string,
) (subprocess.Result, error) {
	return r.runner.RunWithStdin(ctx, subprocess.RunWithStdinOptions{
		Cwd:   worktreePath,
		Stdin: stdin,
		Name:  "git",
		Args:  args,
		Env:   []string{noLazyFetch},
	})
}

// shippedRevisions names, for rev-list, what shipped contains: each base
// branch and each remote's copy of it, the pull request commits, and each
// remote's copy of each open pull request's head branch. A commit that is
// not a whole commit ID is left out, so it can never pass as an option.
func shippedRevisions(shipped core.ShippedWork, remotes []string) []string {
	var revisions []string
	remoteBranches := func(branch string) {
		for _, remote := range remotes {
			revisions = append(revisions, "refs/remotes/"+remote+"/"+branch)
		}
	}
	for _, branch := range shipped.BaseBranches {
		if branch != "" {
			revisions = append(revisions, "refs/heads/"+branch)
			remoteBranches(branch)
		}
	}
	for _, commit := range shipped.PullRequestCommits {
		if isFullObjectID(commit) {
			revisions = append(revisions, commit)
		}
	}
	for _, branch := range shipped.OpenHeadBranches {
		if branch != "" {
			remoteBranches(branch)
		}
	}
	return revisions
}

// reflogBranchNames returns the branches a HEAD reflog shows HEAD was on,
// newest first: each branch a checkout moved away from, and both names of each
// branch rename. Git ref names hold no spaces, so " to " always separates the
// two sides of an entry.
func reflogBranchNames(reflog string) []string {
	var names []string
	for _, line := range strings.Split(reflog, "\n") {
		line = strings.TrimSpace(line)
		if moved, ok := strings.CutPrefix(line, "checkout: moving from "); ok {
			if from, _, ok := strings.Cut(moved, " to "); ok && from != "" && !isFullObjectID(from) {
				names = append(names, from)
			}
			continue
		}
		renamed, ok := strings.CutPrefix(line, "Branch: renamed ")
		if !ok {
			continue
		}
		from, to, ok := strings.Cut(renamed, " to ")
		if !ok {
			continue
		}
		for _, ref := range []string{from, to} {
			if name, ok := strings.CutPrefix(ref, "refs/heads/"); ok && name != "" {
				names = append(names, name)
			}
		}
	}
	return names
}

// isFullObjectID reports whether name is a whole SHA-1 or SHA-256 object ID,
// in lower case, as git logs a checkout moving from a detached HEAD.
func isFullObjectID(name string) bool {
	if len(name) != 40 && len(name) != 64 {
		return false
	}
	for _, r := range name {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// ListRepoCheckouts lists the primary checkouts directly inside dir from the
// filesystem alone. A primary checkout's .git is a directory, while a linked
// worktree's is a file pointing back at its repository, so worktrees sitting
// beside their repositories are left out. A symlink to a checkout counts, at
// the place it links to. The roots are physical paths, as DetectRepo reports
// a repository's root, so that the same repository is recognised whichever
// way it was found.
func (r *repository) ListRepoCheckouts(_ context.Context, dir string) ([]string, error) {
	physicalDir, err := physicalPath(dir)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(physicalDir)
	if err != nil {
		return nil, err
	}

	var roots []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		root := filepath.Join(physicalDir, entry.Name())
		// Stat follows a symlink, so a symlinked checkout passes too.
		gitDir, err := os.Stat(filepath.Join(root, ".git"))
		if err != nil || !gitDir.IsDir() {
			continue
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			if root, err = physicalPath(root); err != nil {
				continue
			}
		}
		roots = append(roots, root)
	}
	slices.Sort(roots)
	return slices.Compact(roots), nil
}

func (r *repository) CheckoutDefaultBranch(ctx context.Context, repoRoot string) error {
	branch := "main"
	result, err := r.runner.Run(ctx, repoRoot, "git", "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err == nil {
		branch = strings.TrimPrefix(strings.TrimSpace(result.Stdout), "origin/")
	}
	_, err = r.runner.Run(ctx, repoRoot, "git", "checkout", branch)
	return err
}

// physicalPath returns path as git reports a repository's root: with its
// symlinks resolved and each component spelled the way the filesystem lists
// it, since a case-insensitive filesystem, as macOS's usually is, finds a
// path typed in any case.
func physicalPath(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	return onDiskCase(resolved), nil
}

// onDiskCase returns the clean absolute path with each component spelled the
// way the directory holding it lists it. A relative path, or a component in a
// directory that cannot be read, is left as it is.
func onDiskCase(path string) string {
	if !filepath.IsAbs(path) {
		return path
	}
	volume := filepath.VolumeName(path)
	cased := volume + string(filepath.Separator)
	for _, name := range strings.Split(path[len(volume):], string(filepath.Separator)) {
		if name != "" {
			cased = filepath.Join(cased, onDiskName(cased, name))
		}
	}
	return cased
}

// onDiskName returns the entry of dir that name finds: the entry named name
// exactly, else the first whose name matches it ignoring case, else name.
func onDiskName(dir string, name string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return name
	}
	cased := name
	for _, entry := range entries {
		switch {
		case entry.Name() == name:
			return name
		case cased == name && strings.EqualFold(entry.Name(), name):
			cased = entry.Name()
		}
	}
	return cased
}

type worktreeEntry struct {
	branch   string
	prunable bool
}

func parseWorktreeEntries(worktreeList string) []worktreeEntry {
	entries := []worktreeEntry{{}}
	for _, line := range strings.Split(worktreeList, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			if entries[len(entries)-1] != (worktreeEntry{}) {
				entries = append(entries, worktreeEntry{})
			}
			continue
		}

		current := &entries[len(entries)-1]
		switch {
		case strings.HasPrefix(line, "branch "):
			current.branch = strings.TrimSpace(strings.TrimPrefix(line, "branch "))
		case strings.HasPrefix(line, "prunable "):
			current.prunable = true
		}
	}

	if len(entries) > 0 && entries[len(entries)-1] == (worktreeEntry{}) {
		entries = entries[:len(entries)-1]
	}
	return entries
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
