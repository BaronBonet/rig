package core

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestServiceListTasks_MarksMissingTmuxSessionAsBroken(t *testing.T) {
	worktree := t.TempDir()
	svc := newTestService()
	svc.taskRepo.listTasks = []*Task{{
		ID:           "task-1",
		Slug:         "billing-retry-flow",
		RepoRoot:     "/tmp/repo",
		BranchName:   "feat/billing-retry-flow",
		WorktreePath: worktree,
		TmuxSession:  "repo-billing-retry-flow",
		Status:       TaskStatusRunning,
	}}
	svc.gitRepo.branchExists = true
	svc.tmuxRepo.sessionExists = false

	tasks, err := svc.service.ListTasks(t.Context())
	require.NoError(t, err)
	require.Equal(t, TaskStatusBroken, tasks[0].Status)
	require.Contains(t, tasks[0].LastError, "missing tmux session")
}

func TestServiceListTasks_EnrichesRuntimeStateForCodexTask(t *testing.T) {
	worktree := t.TempDir()
	svc := newTestService()
	observedAt := time.Date(2026, 4, 5, 10, 0, 0, 0, time.UTC)
	svc.taskRepo.listTasks = []*Task{{
		ID:               "task-1",
		Slug:             "billing-retry-flow",
		RepoRoot:         "/tmp/repo",
		BranchName:       "feat/billing-retry-flow",
		WorktreePath:     worktree,
		TmuxSession:      "repo-billing-retry-flow",
		AgentWindowName:  "agent",
		EditorWindowName: "editor",
		Provider:         "codex",
		Status:           TaskStatusRunning,
	}}
	svc.gitRepo.branchExists = true
	svc.tmuxRepo.sessionExists = true
	svc.tmuxRepo.windowExists = map[string]map[string]bool{
		"repo-billing-retry-flow": {
			"agent":  true,
			"editor": true,
		},
	}
	svc.runtimeMonitor.snapshot = RuntimeSnapshot{
		PaneID:            "%24",
		ForegroundCommand: "codex",
		Content:           "› review my changes\n  gpt-5.4 high · 82% left",
		ObservedAt:        observedAt,
		LastOutputAt:      time.Date(2026, 4, 5, 9, 59, 55, 0, time.UTC),
	}
	svc.runtimeDetector.state = RuntimeStateNeedsInput

	tasks, err := svc.service.ListTasks(t.Context())
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.Equal(t, RuntimeStateNeedsInput, tasks[0].RuntimeState)
	require.Equal(t, observedAt, tasks[0].RuntimeStateUpdatedAt)
}

func TestServiceListTasks_IgnoresRuntimeSnapshotErrors(t *testing.T) {
	worktree := t.TempDir()
	svc := newTestService()
	svc.taskRepo.listTasks = []*Task{{
		ID:               "task-1",
		Slug:             "billing-retry-flow",
		RepoRoot:         "/tmp/repo",
		BranchName:       "feat/billing-retry-flow",
		WorktreePath:     worktree,
		TmuxSession:      "repo-billing-retry-flow",
		AgentWindowName:  "agent",
		EditorWindowName: "editor",
		Provider:         "codex",
		Status:           TaskStatusRunning,
	}}
	svc.gitRepo.branchExists = true
	svc.tmuxRepo.sessionExists = true
	svc.tmuxRepo.windowExists = map[string]map[string]bool{
		"repo-billing-retry-flow": {
			"agent":  true,
			"editor": true,
		},
	}
	svc.runtimeMonitor.err = errors.New("snapshot failed")

	tasks, err := svc.service.ListTasks(t.Context())
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.Equal(t, RuntimeStateNone, tasks[0].RuntimeState)
	require.True(t, tasks[0].RuntimeStateUpdatedAt.IsZero())
}
