package git

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
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
