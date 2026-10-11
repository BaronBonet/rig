package core

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// TaskPullRequests returns the pull requests of a task's branches, oldest
// first, and the unshipped work in its worktree. Pull request numbers grow
// over time within a repository, so the oldest has the lowest number.
func (s *service) TaskPullRequests(ctx context.Context, taskID string) (TaskPullRequests, error) {
	task, err := taskByID(ctx, s.tasks, taskID)
	if err != nil {
		return TaskPullRequests{}, err
	}
	repoRoot := strings.TrimSpace(task.RepoRoot)
	if repoRoot == "" || s.pullRequests == nil {
		return TaskPullRequests{}, nil
	}

	// Without the default branch, the task's branches are those Rig knew
	// before, and its unshipped work cannot be told.
	defaultBranch, err := s.pullRequests.DefaultBranch(ctx, repoRoot)
	if err != nil {
		defaultBranch = ""
	}
	defaultBranch = strings.TrimSpace(defaultBranch)

	branches, err := s.taskBranches(ctx, task, defaultBranch)
	if err != nil {
		return TaskPullRequests{}, err
	}
	pullRequests, err := s.listPullRequests(ctx, repoRoot, branches)
	if err != nil {
		return TaskPullRequests{}, err
	}

	result := TaskPullRequests{PullRequests: pullRequests}
	if len(pullRequests) > 0 && defaultBranch != "" {
		result.UnshippedWork = s.unshippedWork(ctx, task, defaultBranch, pullRequests)
	}
	return result, nil
}

// listPullRequests returns the pull requests of branches, oldest first, each
// once.
func (s *service) listPullRequests(ctx context.Context, repoRoot string, branches []string) ([]PullRequest, error) {
	var pullRequests []PullRequest
	var errs []error
	for _, branch := range branches {
		branchPullRequests, err := s.pullRequests.ListBranchPullRequests(ctx, repoRoot, branch)
		if err != nil {
			errs = append(errs, fmt.Errorf("list pull requests of branch %s: %w", branch, err))
			continue
		}
		pullRequests = append(pullRequests, branchPullRequests...)
	}
	// Some of a task's pull requests would replace all of them wherever they
	// show, so a lookup that fails for one branch fails for the task.
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	slices.SortStableFunc(pullRequests, func(left, right PullRequest) int {
		return cmp.Compare(left.Number, right.Number)
	})
	return slices.CompactFunc(pullRequests, func(left, right PullRequest) bool {
		return left.Number == right.Number
	}), nil
}

// unshippedWork returns the work in a task's worktree that none of its pull
// requests contains, which is none when the worktree is gone or git cannot
// read it: the pull requests are worth showing without it. Git failing for a
// task warns once, until git next succeeds for it.
func (s *service) unshippedWork(
	ctx context.Context,
	task *Task,
	defaultBranch string,
	pullRequests []PullRequest,
) UnshippedWork {
	work, err := s.gitWorktree.UnshippedWork(
		ctx,
		task.WorktreePath,
		shippedWork(defaultBranch, pullRequests),
		s.rigWorkspaceFiles(task),
	)
	switch {
	case err == nil:
		s.unshippedWorkFailures.succeeded(task.ID)
		return work
	case errors.Is(err, fs.ErrNotExist):
		// A task without its worktree, as when its creation failed, simply
		// has no work in it.
		s.unshippedWorkFailures.succeeded(task.ID)
	case s.unshippedWorkFailures.failed(task.ID):
		s.reportWarning(fmt.Errorf("find task %s unshipped work: %w", task.ID, err))
	}
	return UnshippedWork{}
}

// shippedWork is what a task's work ships in: the repository's default
// branch and the branches its pull requests merge into, every commit of its
// pull requests, and the head branches of its open and draft ones. A pull
// request stacked on another of the task's merges into that one's head
// branch, which ships nothing until that one does, so the task's own head
// branches are never base branches.
func shippedWork(defaultBranch string, pullRequests []PullRequest) ShippedWork {
	shipped := ShippedWork{BaseBranches: []string{defaultBranch}}
	var headBranches []string
	for _, pullRequest := range pullRequests {
		headBranches = append(headBranches, pullRequest.HeadBranch)
		shipped.BaseBranches = append(shipped.BaseBranches, pullRequest.BaseBranch)
		shipped.PullRequestCommits = append(shipped.PullRequestCommits, pullRequest.HeadCommit)
		shipped.PullRequestCommits = append(shipped.PullRequestCommits, pullRequest.Commits...)
		if pullRequest.State == PRStateOpen || pullRequest.State == PRStateDraft {
			shipped.OpenHeadBranches = append(shipped.OpenHeadBranches, pullRequest.HeadBranch)
		}
	}
	shipped.BaseBranches = slices.DeleteFunc(shipped.BaseBranches, func(branch string) bool {
		return branch != defaultBranch && slices.Contains(headBranches, branch)
	})
	shipped.BaseBranches = sortedNonEmpty(shipped.BaseBranches)
	shipped.PullRequestCommits = sortedNonEmpty(shipped.PullRequestCommits)
	shipped.OpenHeadBranches = sortedNonEmpty(shipped.OpenHeadBranches)
	return shipped
}

// rigWorkspaceFiles returns the files, relative to a task's worktree, that
// any provider's workspace bootstrap writes into it, such as Claude's
// workspace settings. They are Rig's, never the task's work. Every provider
// counts, configured or not, as one configured before may have left its files
// behind.
func (s *service) rigWorkspaceFiles(task *Task) []string {
	var files []string
	for _, providerClient := range s.providers {
		if providerClient == nil {
			continue
		}
		spec, err := providerClient.BuildWorkspaceBootstrapSpec(task)
		if err != nil {
			continue
		}
		for _, file := range spec.Files {
			path := filepath.Clean(strings.TrimSpace(file.Path))
			if path == "." || filepath.IsAbs(path) || path == ".." ||
				strings.HasPrefix(path, ".."+string(filepath.Separator)) {
				continue
			}
			files = append(files, filepath.ToSlash(path))
		}
	}
	return sortedNonEmpty(files)
}

func sortedNonEmpty(values []string) []string {
	values = slices.DeleteFunc(values, func(value string) bool {
		return strings.TrimSpace(value) == ""
	})
	slices.Sort(values)
	return slices.Compact(values)
}

// taskFailures remembers the tasks for which an operation last failed, so a
// failure that persists is reported once rather than on every refresh.
type taskFailures struct {
	failing map[string]bool
	mu      sync.Mutex
}

// failed records that the operation failed for a task, and reports whether
// that is news: whether it had not been failing for the task.
func (f *taskFailures) failed(taskID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failing[taskID] {
		return false
	}
	if f.failing == nil {
		f.failing = map[string]bool{}
	}
	f.failing[taskID] = true
	return true
}

// succeeded records that the operation succeeded for a task, so its next
// failure is news again.
func (f *taskFailures) succeeded(taskID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.failing, taskID)
}

// taskBranches returns a task's branches, its Task branches, sorted: the
// branches recorded for it, its own branch, and the branches its worktree has
// had checked out, less the repository's default branch. It records the ones
// not recorded before, so a branch stays the task's once git has expired the
// reflog entries that found it. Without the default branch, an empty string,
// the worktree's branches cannot be told from it, so they are left out, and
// so they are when the worktree is gone or git cannot read it.
func (s *service) taskBranches(ctx context.Context, task *Task, defaultBranch string) ([]string, error) {
	recorded, err := s.tasks.ListTaskBranches(ctx, task.ID)
	if err != nil {
		return nil, fmt.Errorf("list task branches: %w", err)
	}

	branches := append(slices.Clone(recorded), strings.TrimSpace(task.BranchName))
	if defaultBranch != "" {
		if checkedOut, err := s.gitWorktree.ListWorktreeBranches(ctx, task.WorktreePath); err == nil {
			branches = append(branches, checkedOut...)
		}
	}

	branches = slices.DeleteFunc(branches, func(branch string) bool {
		return branch == "" || branch == defaultBranch
	})
	slices.Sort(branches)
	branches = slices.Compact(branches)

	var unrecorded []string
	for _, branch := range branches {
		if !slices.Contains(recorded, branch) {
			unrecorded = append(unrecorded, branch)
		}
	}
	if len(unrecorded) > 0 {
		// The branches are known for now; failing to record them only risks
		// losing one git expires from the reflog later.
		if err := s.tasks.RecordTaskBranches(ctx, task.ID, unrecorded); err != nil {
			s.reportWarning(fmt.Errorf("record task %s branches: %w", task.ID, err))
		}
	}
	return branches, nil
}
