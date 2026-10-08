package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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

func (r *repository) CreateTaskWorkspace(ctx context.Context, task *core.Task, baseRef string) error {
	if strings.TrimSpace(baseRef) == "" {
		repoCtx, err := r.DetectRepo(ctx, task.RepoRoot)
		if err != nil {
			return err
		}
		_, err = r.runner.Run(ctx, task.RepoRoot, "git", "worktree", "add", task.WorktreePath, "-b",
			task.BranchName, repoCtx.BaseBranch)
		return err
	}

	// --no-track: a task branch started from origin/<base> must not take the
	// base branch as its upstream, or a bare push could land on the base.
	_, err := r.runner.Run(ctx, task.RepoRoot, "git", "worktree", "add", "--no-track", task.WorktreePath, "-b",
		task.BranchName, baseRef)
	return err
}

func (r *repository) ResolveBaseRef(ctx context.Context, repoRoot string, baseBranch string) (string, error) {
	baseBranch = strings.TrimSpace(baseBranch)
	if baseBranch == "" {
		return "", nil
	}
	if _, err := r.runner.Run(ctx, repoRoot, "git", "fetch", "--quiet", "origin", baseBranch); err == nil {
		return "origin/" + baseBranch, nil
	}
	if _, err := r.runner.Run(ctx, repoRoot, "git", "rev-parse", "--verify", "--quiet",
		"refs/heads/"+baseBranch); err == nil {
		return baseBranch, nil
	}
	return "", fmt.Errorf("base branch %q is neither on origin nor a local branch", baseBranch)
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

// WorktreeRootOf walks up from dir to the nearest directory holding a .git
// entry: a directory for a main checkout, a file for a linked worktree. It
// never walks up from a directory that no longer exists, so edits made in a
// since-deleted worktree are not attributed to a repository that contained it.
func (r *repository) WorktreeRootOf(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" || !filepath.IsAbs(dir) {
		return ""
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return ""
	}

	current := filepath.Clean(dir)
	for {
		if _, err := os.Lstat(filepath.Join(current, ".git")); err == nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
}

func (r *repository) InspectWorktree(ctx context.Context, root string) (*core.WorktreeRef, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, nil
	}
	if !hasLiveGitDir(root) {
		return nil, nil
	}

	commonDirResult, err := r.runner.Run(ctx, root, "git", "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return nil, err
	}
	branchResult, err := r.runner.Run(ctx, root, "git", "branch", "--show-current")
	if err != nil {
		return nil, err
	}

	return &core.WorktreeRef{
		Root:     root,
		RepoName: repoNameFromCommonDir(strings.TrimSpace(commonDirResult.Stdout)),
		Branch:   strings.TrimSpace(branchResult.Stdout),
	}, nil
}

// hasLiveGitDir reports whether root still has a Git directory: either a .git
// directory, or a linked worktree's .git file whose gitdir still exists. A
// pruned linked worktree leaves the .git file pointing at nothing.
func hasLiveGitDir(root string) bool {
	dotGit := filepath.Join(root, ".git")
	info, err := os.Lstat(dotGit)
	if err != nil {
		return false
	}
	if info.IsDir() {
		return true
	}

	contents, err := os.ReadFile(dotGit)
	if err != nil {
		return false
	}
	gitDir, ok := strings.CutPrefix(strings.TrimSpace(string(contents)), "gitdir:")
	if !ok {
		return false
	}
	gitDir = strings.TrimSpace(gitDir)
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(root, gitDir)
	}
	_, err = os.Stat(gitDir)
	return err == nil
}

// repoNameFromCommonDir names a repository after its shared Git directory, so
// every worktree of one repository reports the same name: "/src/api/.git" and
// "/src/api.git" are both "api".
func repoNameFromCommonDir(commonDir string) string {
	commonDir = filepath.Clean(commonDir)
	name := filepath.Base(commonDir)
	if strings.HasPrefix(name, ".") {
		return filepath.Base(filepath.Dir(commonDir))
	}
	return strings.TrimSuffix(name, ".git")
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
