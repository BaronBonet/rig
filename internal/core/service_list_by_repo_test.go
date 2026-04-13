package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestListTaskViewsByRepo_FiltersToRepo(t *testing.T) {
	worktree := t.TempDir()
	svc := newTestService(t)
	svc.taskRepo.listTasksByRepo = []*Task{{
		ID:               "task-1",
		Slug:             "add-auth",
		RepoRoot:         "/tmp/repo-a",
		RepoName:         "repo-a",
		BranchName:       "feat/add-auth",
		WorktreePath:     worktree,
		TmuxSession:      "repo-a_add-auth",
		AgentWindowName:  "agent",
		EditorWindowName: "editor",
		Provider:         "codex",
		Status:           TaskStatusRunning,
	}}
	svc.repoClient.repoResources = RepoResources{WorktreeExists: true, BranchExists: true}
	svc.sessionClient.sessionResources = SessionResources{
		SessionExists:      true,
		AgentWindowExists:  true,
		EditorWindowExists: true,
	}

	views, err := svc.service.ListTaskViewsByRepo(t.Context(), "/tmp/repo-a")
	require.NoError(t, err)
	require.Len(t, views, 1)
	require.Equal(t, "task-1", views[0].Task.ID)
	require.Equal(t, "/tmp/repo-a", views[0].Task.RepoRoot)
}

func TestListTaskViewsByRepo_EmptyWhenNoMatch(t *testing.T) {
	svc := newTestService(t)
	svc.taskRepo.listTasksByRepo = []*Task{}
	svc.repoClient.repoResources = RepoResources{}
	svc.sessionClient.sessionResources = SessionResources{}

	views, err := svc.service.ListTaskViewsByRepo(t.Context(), "/tmp/nonexistent")
	require.NoError(t, err)
	require.Empty(t, views)
}
