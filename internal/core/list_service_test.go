package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTmuxSessionClientContract_OnlyRequiresTaskLifecycleMethods(t *testing.T) {
	var _ TmuxSessionClient = (*MockTmuxSessionClient)(nil)
}

func TestTaskService_ListTasksReturnsRepositoryTasks(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.listTasks = []*Task{
		{ID: "task-1", Slug: "repo-a-task"},
		{ID: "task-2", Slug: "repo-b-task"},
	}

	tasks, err := svc.service.ListTasks(t.Context())
	require.NoError(t, err)
	require.Len(t, tasks, 2)
	require.Equal(t, []string{"task-1", "task-2"}, []string{tasks[0].ID, tasks[1].ID})
}

func TestTaskService_ListRepoPullRequests_MarksExistingWorkspaceBranches(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.listTasks = []*Task{
		{
			ID:         "task-1",
			RepoRoot:   "/tmp/repo",
			BranchName: "feat/auth",
		},
	}
	svc.repoClient.branchInUse["feat/billing"] = true
	svc.pullRequests.listRepoPullRequests = []RepoPullRequest{
		{Number: 41, Title: "Billing", BranchName: "feat/billing", State: PRStateOpen},
		{Number: 42, Title: "Auth", BranchName: "feat/auth", State: PRStateDraft},
		{Number: 43, Title: "Search", BranchName: "feat/search", State: PRStateOpen},
	}

	prs, err := svc.service.ListRepoPullRequests(t.Context(), "/tmp/repo/worktree")

	require.NoError(t, err)
	require.Equal(t, "/tmp/repo", svc.pullRequests.lastListRepoRoot)
	require.Equal(t, []RepoPullRequest{
		{Number: 41, Title: "Billing", BranchName: "feat/billing", State: PRStateOpen, HasExistingTask: true},
		{Number: 42, Title: "Auth", BranchName: "feat/auth", State: PRStateDraft, HasExistingTask: true},
		{Number: 43, Title: "Search", BranchName: "feat/search", State: PRStateOpen, HasExistingTask: false},
	}, prs)
}

func TestTaskService_DeleteTaskRemovesSessionWorkspaceAndRecord(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.listTasks = []*Task{{
		ID:           "task-1",
		Slug:         "repo-a-task",
		DisplayName:  "repo a task",
		RepoRoot:     "/tmp/repo",
		BranchName:   "feat/repo-a-task",
		WorktreePath: "/tmp/repo-a-task",
		TmuxSession:  "repo_a_task",
	}}

	err := svc.service.DeleteTask(t.Context(), "task-1")

	require.NoError(t, err)
	require.NotNil(t, svc.sessionClient.deletedTask)
	require.Equal(t, "task-1", svc.sessionClient.deletedTask.ID)
	require.NotNil(t, svc.repoClient.removedTask)
	require.Equal(t, "/tmp/repo-a-task", svc.repoClient.removedTask.WorktreePath)
	require.Equal(t, "task-1", svc.taskRepo.deletedTaskID)
}

func TestTaskService_DeleteTaskReturnsNotFoundWhenTaskDoesNotExist(t *testing.T) {
	svc := newTestTaskService(t)

	err := svc.service.DeleteTask(t.Context(), "missing")

	require.ErrorIs(t, err, ErrTaskNotFound)
	require.Nil(t, svc.sessionClient.deletedTask)
	require.Nil(t, svc.repoClient.removedTask)
	require.Empty(t, svc.taskRepo.deletedTaskID)
}
