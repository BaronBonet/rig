package core

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// pullRequestsTask is a task on feat/billing-export whose worktree is
// /tmp/repo_billing, in /tmp/repo, whose default branch is main.
func pullRequestsTask() *Task {
	return &Task{
		ID:           "task-1",
		RepoRoot:     "/tmp/repo",
		RepoName:     "repo",
		BranchName:   "feat/billing-export",
		WorktreePath: "/tmp/repo_billing",
	}
}

// listTaskPullRequests returns task-1's pull requests, without its unshipped
// work.
func listTaskPullRequests(h *testTaskServiceHarness) ([]PullRequest, error) {
	result, err := h.service.TaskPullRequests(context.Background(), "task-1")
	return result.PullRequests, err
}

func newPullRequestsTestService(t *testing.T) *testTaskServiceHarness {
	t.Helper()
	h := newTestTaskService(t)
	h.taskRepo.listTasks = []*Task{pullRequestsTask()}
	h.pullRequests.defaultBranch = "main"
	return h
}

func TestTaskService_TaskPullRequestsListsEveryTaskBranchsPullRequestsOldestFirst(t *testing.T) {
	h := newPullRequestsTestService(t)
	h.taskRepo.taskBranches = map[string][]string{"task-1": {"feat/billing-old"}}
	h.repoClient.worktreeBranches = map[string][]string{
		"/tmp/repo_billing": {"feat/billing-export", "feat/billing-export-2"},
	}
	h.pullRequests.branchPullRequests = map[string][]PullRequest{
		"feat/billing-old":      {{Number: 98, State: PRStateClosed, HeadBranch: "feat/billing-old"}},
		"feat/billing-export":   {{Number: 123, State: PRStateMerged}, {Number: 120, State: PRStateMerged}},
		"feat/billing-export-2": {{Number: 127, State: PRStateOpen, HeadBranch: "feat/billing-export-2"}},
	}

	pullRequests, err := listTaskPullRequests(h)

	require.NoError(t, err)
	require.Equal(t, []PullRequest{
		{Number: 98, State: PRStateClosed, HeadBranch: "feat/billing-old"},
		{Number: 120, State: PRStateMerged},
		{Number: 123, State: PRStateMerged},
		{Number: 127, State: PRStateOpen, HeadBranch: "feat/billing-export-2"},
	}, pullRequests)
	require.ElementsMatch(t, []string{
		"/tmp/repo:feat/billing-old",
		"/tmp/repo:feat/billing-export",
		"/tmp/repo:feat/billing-export-2",
	}, h.pullRequests.branchListings)
}

func TestTaskService_TaskPullRequestsRecordsTheBranchesNotRecordedBefore(t *testing.T) {
	h := newPullRequestsTestService(t)
	h.taskRepo.taskBranches = map[string][]string{"task-1": {"feat/billing-old"}}
	h.repoClient.worktreeBranches = map[string][]string{
		"/tmp/repo_billing": {"feat/billing-export-2", "feat/billing-old"},
	}

	_, err := listTaskPullRequests(h)
	require.NoError(t, err)
	require.Equal(t, [][]string{{"feat/billing-export", "feat/billing-export-2"}}, h.taskRepo.recordedTaskBranches)

	// Once every branch is recorded, a lookup records nothing.
	_, err = h.service.TaskPullRequests(context.Background(), "task-1")
	require.NoError(t, err)
	require.Len(t, h.taskRepo.recordedTaskBranches, 1)
}

func TestTaskService_TaskPullRequestsLeavesOutTheDefaultBranch(t *testing.T) {
	h := newPullRequestsTestService(t)
	h.pullRequests.defaultBranch = "develop"
	h.repoClient.worktreeBranches = map[string][]string{
		"/tmp/repo_billing": {"develop", "feat/billing-export", "part-2"},
	}
	h.pullRequests.branchPullRequests = map[string][]PullRequest{
		"develop": {{Number: 7, State: PRStateOpen, HeadBranch: "develop"}},
	}

	pullRequests, err := listTaskPullRequests(h)

	require.NoError(t, err)
	require.Empty(t, pullRequests)
	require.ElementsMatch(
		t,
		[]string{"/tmp/repo:feat/billing-export", "/tmp/repo:part-2"},
		h.pullRequests.branchListings,
	)
	require.Equal(t, [][]string{{"feat/billing-export", "part-2"}}, h.taskRepo.recordedTaskBranches)
	require.Equal(t, []string{"/tmp/repo"}, h.pullRequests.defaultBranchRepoRoots)
}

// The branch the main checkout has checked out says nothing of the default
// branch: it may be one of the task's own, or none while it is detached.
func TestTaskService_TaskPullRequestsIgnoresTheMainCheckoutsBranch(t *testing.T) {
	h := newPullRequestsTestService(t)
	h.repoClient.repoContext.BaseBranch = "feat/billing-export"
	h.repoClient.worktreeBranches = map[string][]string{"/tmp/repo_billing": {"feat/billing-export"}}
	h.pullRequests.branchPullRequests = map[string][]PullRequest{
		"feat/billing-export": {{Number: 120, State: PRStateMerged}},
	}

	pullRequests, err := listTaskPullRequests(h)

	require.NoError(t, err)
	require.Equal(t, []PullRequest{{Number: 120, State: PRStateMerged}}, pullRequests)
	require.Empty(t, h.repoClient.detectedCwds)
}

// A branch deleted after its pull request merged, or one git expired from the
// reflog, stays the task's once recorded, also without the task's worktree.
func TestTaskService_TaskPullRequestsKeepsRecordedBranchesWithoutTheWorktree(t *testing.T) {
	h := newPullRequestsTestService(t)
	h.taskRepo.taskBranches = map[string][]string{"task-1": {"feat/billing-old"}}
	h.pullRequests.branchPullRequests = map[string][]PullRequest{
		"feat/billing-old":    {{Number: 98, State: PRStateMerged}},
		"feat/billing-export": {{Number: 120, State: PRStateOpen}},
	}

	pullRequests, err := listTaskPullRequests(h)

	require.NoError(t, err)
	require.Equal(t, []PullRequest{{Number: 98, State: PRStateMerged}, {Number: 120, State: PRStateOpen}}, pullRequests)
	require.Equal(t, [][]string{{"feat/billing-export"}}, h.taskRepo.recordedTaskBranches)
}

func TestTaskService_TaskPullRequestsKeepsRecordedBranchesWhenGitCannotReadTheWorktree(t *testing.T) {
	h := newPullRequestsTestService(t)
	h.taskRepo.taskBranches = map[string][]string{"task-1": {"feat/billing-export", "feat/billing-old"}}
	h.repoClient.worktreeBranchErr = errors.New("git reflog failed")

	_, err := listTaskPullRequests(h)

	require.NoError(t, err)
	require.ElementsMatch(
		t,
		[]string{"/tmp/repo:feat/billing-export", "/tmp/repo:feat/billing-old"},
		h.pullRequests.branchListings,
	)
	require.Empty(t, h.taskRepo.recordedTaskBranches)
}

// Without the default branch, a checkout of it in the worktree cannot be told
// from the task's own branches, so the worktree's branches are left out and
// only the recorded branches and the task's own are looked up.
func TestTaskService_TaskPullRequestsUsesOnlyKnownBranchesWithoutTheDefaultBranch(t *testing.T) {
	for name, configure := range map[string]func(*pullRequestClientState){
		"lookup fails": func(state *pullRequestClientState) { state.defaultBranchErr = errors.New("gh: not logged in") },
		"none":         func(state *pullRequestClientState) { state.defaultBranch = "" },
	} {
		t.Run(name, func(t *testing.T) {
			h := newPullRequestsTestService(t)
			configure(&h.pullRequests)
			h.taskRepo.taskBranches = map[string][]string{"task-1": {"feat/billing-old"}}
			h.repoClient.worktreeBranches = map[string][]string{"/tmp/repo_billing": {"main", "part-2"}}

			_, err := listTaskPullRequests(h)

			require.NoError(t, err)
			require.ElementsMatch(
				t,
				[]string{"/tmp/repo:feat/billing-export", "/tmp/repo:feat/billing-old"},
				h.pullRequests.branchListings,
			)
			require.Equal(t, [][]string{{"feat/billing-export"}}, h.taskRepo.recordedTaskBranches)
		})
	}
}

func TestTaskService_TaskPullRequestsListsAPullRequestOnce(t *testing.T) {
	h := newPullRequestsTestService(t)
	h.repoClient.worktreeBranches = map[string][]string{"/tmp/repo_billing": {"feat/c", "feat/c2"}}
	h.pullRequests.branchPullRequests = map[string][]PullRequest{
		"feat/c":  {{Number: 130, State: PRStateOpen, HeadBranch: "feat/c2"}},
		"feat/c2": {{Number: 130, State: PRStateOpen, HeadBranch: "feat/c2"}, {Number: 128, State: PRStateMerged}},
	}

	pullRequests, err := listTaskPullRequests(h)

	require.NoError(t, err)
	require.Equal(t, []PullRequest{
		{Number: 128, State: PRStateMerged},
		{Number: 130, State: PRStateOpen, HeadBranch: "feat/c2"},
	}, pullRequests)
}

// Some of the task's pull requests would replace all of them where they show,
// so one branch that cannot be looked up fails the lookup.
func TestTaskService_TaskPullRequestsFailsWhenABranchCannotBeLookedUp(t *testing.T) {
	h := newPullRequestsTestService(t)
	h.repoClient.worktreeBranches = map[string][]string{"/tmp/repo_billing": {"part-2"}}
	h.pullRequests.branchPullRequests = map[string][]PullRequest{
		"feat/billing-export": {{Number: 120, State: PRStateMerged}},
	}
	h.pullRequests.branchPullRequestErrs = map[string]error{"part-2": errors.New("gh: not logged in")}

	pullRequests, err := listTaskPullRequests(h)

	require.ErrorContains(t, err, "gh: not logged in")
	require.Nil(t, pullRequests)
}

func TestTaskService_TaskPullRequestsStillListsWhenRecordingFails(t *testing.T) {
	h := newPullRequestsTestService(t)
	h.taskRepo.recordTaskBranchErr = errors.New("database is locked")
	h.pullRequests.branchPullRequests = map[string][]PullRequest{
		"feat/billing-export": {{Number: 120, State: PRStateOpen}},
	}

	pullRequests, err := listTaskPullRequests(h)

	require.NoError(t, err)
	require.Equal(t, []PullRequest{{Number: 120, State: PRStateOpen}}, pullRequests)
	require.Len(t, h.warnings, 1)
	require.ErrorContains(t, h.warnings[0], "database is locked")
}

func TestTaskService_StoredTaskPullRequestsAreTheLastOnesListed(t *testing.T) {
	h := newPullRequestsTestService(t)
	ctx := context.Background()

	stored, err := h.service.StoredTaskPullRequests(ctx, "task-1")
	require.NoError(t, err)
	require.Empty(t, stored.PullRequests)

	h.pullRequests.branchPullRequests = map[string][]PullRequest{
		"feat/billing-export": {{Number: 120, State: PRStateOpen}},
	}
	listed, err := h.service.TaskPullRequests(ctx, "task-1")
	require.NoError(t, err)

	// A failed lookup keeps the last ones listed, without asking gh.
	h.pullRequests.branchPullRequestErrs = map[string]error{"feat/billing-export": errors.New("gh: offline")}
	_, err = h.service.TaskPullRequests(ctx, "task-1")
	require.Error(t, err)
	listings := len(h.pullRequests.branchListings)

	stored, err = h.service.StoredTaskPullRequests(ctx, "task-1")
	require.NoError(t, err)
	require.Equal(t, listed, stored)
	require.Len(t, h.pullRequests.branchListings, listings)
}

func TestTaskService_TaskPullRequestsStillListsWhenSavingFails(t *testing.T) {
	h := newPullRequestsTestService(t)
	h.taskRepo.saveTaskPullRequestsErr = errors.New("database is locked")
	h.pullRequests.branchPullRequests = map[string][]PullRequest{
		"feat/billing-export": {{Number: 120, State: PRStateOpen}},
	}

	pullRequests, err := listTaskPullRequests(h)

	require.NoError(t, err)
	require.Equal(t, []PullRequest{{Number: 120, State: PRStateOpen}}, pullRequests)
	require.Len(t, h.warnings, 1)
	require.ErrorContains(t, h.warnings[0], "database is locked")
}

func TestTaskService_TaskPullRequestsFailsWhenTheRecordedBranchesCannotBeRead(t *testing.T) {
	h := newPullRequestsTestService(t)
	h.taskRepo.listTaskBranchesErr = errors.New("database is locked")

	_, err := listTaskPullRequests(h)

	require.ErrorContains(t, err, "database is locked")
	require.Empty(t, h.pullRequests.branchListings)
}

func TestTaskService_TaskPullRequestsFailsForAnUnknownTask(t *testing.T) {
	h := newPullRequestsTestService(t)

	_, err := h.service.TaskPullRequests(context.Background(), "no-such-task")

	require.ErrorIs(t, err, ErrTaskNotFound)
}

func TestTaskService_TaskPullRequestsListsNoneWithoutARepositoryOrClient(t *testing.T) {
	h := newPullRequestsTestService(t)
	h.taskRepo.listTasks[0].RepoRoot = ""

	pullRequests, err := listTaskPullRequests(h)
	require.NoError(t, err)
	require.Empty(t, pullRequests)

	h = newPullRequestsTestService(t)
	h.service = NewTaskService(TaskServiceDependencies{
		Tasks:       h.taskRepoMock,
		GitWorktree: h.repoClientMock,
		TmuxSession: h.sessionClientMock,
		Providers:   map[Provider]ProviderClient{ProviderCodex: h.providerClientMock},
		Workspace:   h.workspaceMock,
		UserConfig:  h.userConfigMock,
	})

	pullRequests, err = listTaskPullRequests(h)
	require.NoError(t, err)
	require.Empty(t, pullRequests)
}

// shippingTestService is a task with merged #120, closed #121 into develop,
// draft #125 stacked on open #127's branch, and open #127 and #128, whose
// worktree has had their branches checked out. Claude writes its workspace
// settings into the worktree.
func shippingTestService(t *testing.T) *testTaskServiceHarness {
	t.Helper()
	h := newPullRequestsTestService(t)
	h.repoClient.worktreeBranches = map[string][]string{
		"/tmp/repo_billing": {"feat/billing-export", "feat/billing-export-2", "feat/billing-csv", "feat/billing-old"},
	}
	h.pullRequests.branchPullRequests = map[string][]PullRequest{
		"feat/billing-export": {{
			Number: 120, State: PRStateMerged, HeadBranch: "feat/billing-export", BaseBranch: "main",
			HeadCommit: "c120", Commits: []string{"c118", "c120"},
		}},
		"feat/billing-old": {{
			Number: 121, State: PRStateClosed, HeadBranch: "feat/billing-old", BaseBranch: "develop",
			HeadCommit: "c121", Commits: []string{"c121"},
		}},
		"feat/billing-csv": {{
			Number: 125, State: PRStateDraft, HeadBranch: "feat/billing-csv", BaseBranch: "feat/billing-export-2",
			HeadCommit: "c125", Commits: []string{"c124", "c125"},
		}},
		"feat/billing-export-2": {
			{
				Number: 127, State: PRStateOpen, HeadBranch: "feat/billing-export-2", BaseBranch: "main",
				HeadCommit: "c127", Commits: []string{"c120", "c127"},
			},
			{Number: 128, State: PRStateOpen, HeadBranch: "feat/billing-export-2", BaseBranch: "release/2.0"},
		},
	}
	h.claudeRepo.bootstrapSpec = WorkspaceBootstrapSpec{Files: []WorkspaceBootstrapFile{
		{Path: ".claude/settings.local.json"},
	}}
	return h
}

// Every commit of every pull request ships the task's work, and so do the
// default branch and the branches the pull requests merge into, and an open
// or draft pull request's head branch, as pushed. A merged or closed one's
// branch may have had more pushed to it since. Files Rig writes are not the
// task's work.
func TestTaskService_TaskPullRequestsFindsTheWorkNoPullRequestContains(t *testing.T) {
	h := shippingTestService(t)
	h.repoClient.unshippedWork = map[string]UnshippedWork{
		"/tmp/repo_billing": {Commits: 2, UncommittedChanges: true},
	}
	h.providerRepo.bootstrapSpec = WorkspaceBootstrapSpec{Files: []WorkspaceBootstrapFile{
		{Path: "./.codex/../.codex/hooks.json"},
		{Path: "/etc/elsewhere"},
		{Path: "../outside"},
	}}

	result, err := h.service.TaskPullRequests(context.Background(), "task-1")

	require.NoError(t, err)
	require.Len(t, result.PullRequests, 5)
	require.Equal(t, UnshippedWork{Commits: 2, UncommittedChanges: true}, result.UnshippedWork)
	require.Equal(t, []unshippedWorkLookup{{
		worktreePath: "/tmp/repo_billing",
		shipped: ShippedWork{
			// #125 merges into #127's branch, which is the task's own.
			BaseBranches:       []string{"develop", "main", "release/2.0"},
			PullRequestCommits: []string{"c118", "c120", "c121", "c124", "c125", "c127"},
			OpenHeadBranches:   []string{"feat/billing-csv", "feat/billing-export-2"},
		},
		rigFiles: []string{".claude/settings.local.json", ".codex/hooks.json"},
	}}, h.repoClient.unshippedWorkLookups)
	require.Empty(t, h.warnings)
}

// Without a pull request, none of a task's work is in one, so there is
// nothing to tell apart.
func TestTaskService_TaskPullRequestsFindsNoUnshippedWorkWithoutPullRequests(t *testing.T) {
	h := newPullRequestsTestService(t)
	h.repoClient.unshippedWork = map[string]UnshippedWork{"/tmp/repo_billing": {Commits: 3}}

	result, err := h.service.TaskPullRequests(context.Background(), "task-1")

	require.NoError(t, err)
	require.Equal(t, TaskPullRequests{}, result)
	require.Empty(t, h.repoClient.unshippedWorkLookups)
}

func TestTaskService_TaskPullRequestsFindsNoUnshippedWorkWithoutTheDefaultBranch(t *testing.T) {
	h := shippingTestService(t)
	h.pullRequests.defaultBranchErr = errors.New("gh: not logged in")
	h.taskRepo.taskBranches = map[string][]string{"task-1": {"feat/billing-export-2"}}
	h.repoClient.unshippedWork = map[string]UnshippedWork{"/tmp/repo_billing": {Commits: 3}}

	result, err := h.service.TaskPullRequests(context.Background(), "task-1")

	require.NoError(t, err)
	require.Len(t, result.PullRequests, 3)
	require.Equal(t, UnshippedWork{}, result.UnshippedWork)
	require.Empty(t, h.repoClient.unshippedWorkLookups)
}

func TestTaskService_TaskPullRequestsKeepsThePullRequestsWhenGitFails(t *testing.T) {
	h := shippingTestService(t)
	h.repoClient.unshippedWorkErr = errors.New("git rev-list failed")

	result, err := h.service.TaskPullRequests(context.Background(), "task-1")

	require.NoError(t, err)
	require.Len(t, result.PullRequests, 5)
	require.Equal(t, UnshippedWork{}, result.UnshippedWork)
	require.Len(t, h.warnings, 1)
	require.ErrorContains(t, h.warnings[0], "git rev-list failed")
}

// Git failing for a task on every refresh warns once, until git succeeds for
// it again. Other tasks warn for themselves.
func TestTaskService_TaskPullRequestsWarnsOfALastingGitFailureOnce(t *testing.T) {
	h := shippingTestService(t)
	h.repoClient.unshippedWorkErr = errors.New("git rev-list failed")
	for range 2 {
		_, err := h.service.TaskPullRequests(context.Background(), "task-1")
		require.NoError(t, err)
	}
	require.Len(t, h.warnings, 1)

	h = shippingTestService(t)
	h.repoClient.unshippedWork = map[string]UnshippedWork{"/tmp/repo_billing": {Commits: 1}}
	for _, failing := range []bool{false, true, false, true} {
		h.repoClient.unshippedWorkErr = nil
		if failing {
			h.repoClient.unshippedWorkErr = errors.New("git rev-list failed")
		}
		_, err := h.service.TaskPullRequests(context.Background(), "task-1")
		require.NoError(t, err)
	}
	require.Len(t, h.warnings, 2)

	other := pullRequestsTask()
	other.ID = "task-2"
	h.taskRepo.listTasks = append(h.taskRepo.listTasks, other)
	_, err := h.service.TaskPullRequests(context.Background(), "task-2")
	require.NoError(t, err)
	require.Len(t, h.warnings, 3)
}

// A task whose worktree is gone, as when its creation failed, has no work in
// it, which is no cause for a warning.
func TestTaskService_TaskPullRequestsFindsNoUnshippedWorkWithoutTheWorktree(t *testing.T) {
	h := shippingTestService(t)
	h.taskRepo.taskBranches = map[string][]string{"task-1": {"feat/billing-export-2"}}
	h.repoClient.worktreeBranches = nil

	result, err := h.service.TaskPullRequests(context.Background(), "task-1")

	require.NoError(t, err)
	require.NotEmpty(t, result.PullRequests)
	require.Equal(t, UnshippedWork{}, result.UnshippedWork)
	require.Len(t, h.repoClient.unshippedWorkLookups, 1)
	require.Empty(t, h.warnings)
}
