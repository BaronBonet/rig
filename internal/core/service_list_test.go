package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type recordingBootstrapper struct {
	calls []*Task
}

func (b *recordingBootstrapper) BootstrapTaskWorkspace(_ context.Context, task *Task) error {
	b.calls = append(b.calls, cloneTask(task))
	return nil
}

func TestServiceListTasks_MarksMissingTmuxSessionAsBroken(t *testing.T) {
	worktree := t.TempDir()
	svc := newTestService(t)
	svc.taskRepo.listTasks = []*Task{{
		ID:           "task-1",
		Slug:         "billing-retry-flow",
		RepoRoot:     "/tmp/repo",
		BranchName:   "feat/billing-retry-flow",
		WorktreePath: worktree,
		TmuxSession:  "repo-billing-retry-flow",
		Status:       TaskStatusRunning,
	}}
	svc.repoClient.repoResources = RepoResources{WorktreeExists: true, BranchExists: true}
	svc.sessionClient.sessionResources = SessionResources{}

	tasks, err := svc.service.ListTasks(t.Context())
	require.NoError(t, err)
	require.Equal(t, TaskStatusBroken, tasks[0].Status)
	require.Contains(t, tasks[0].LastError, "missing tmux session")
}

func TestServiceListTasks_EnrichesRuntimeStateForCodexTask(t *testing.T) {
	worktree := t.TempDir()
	svc := newTestService(t)
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
	svc.repoClient.repoResources = RepoResources{WorktreeExists: true, BranchExists: true}
	svc.sessionClient.sessionResources = SessionResources{
		SessionExists:      true,
		AgentWindowExists:  true,
		EditorWindowExists: true,
	}
	svc.sessionClient.snapshot = RuntimeSnapshot{
		PaneID:            "%24",
		ForegroundCommand: "codex",
		Content:           "› review my changes\n  gpt-5.4 high · 82% left",
		ObservedAt:        observedAt,
		LastOutputAt:      time.Date(2026, 4, 5, 9, 59, 55, 0, time.UTC),
	}
	svc.providerRepo.runtimeState = RuntimeStateNeedsInput

	tasks, err := svc.service.ListTasks(t.Context())
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.Equal(t, RuntimeStateNeedsInput, tasks[0].RuntimeState)
	require.Equal(t, observedAt, tasks[0].RuntimeStateUpdatedAt)
}

func TestServiceListTasks_SnapshotErrorIsBestEffort(t *testing.T) {
	worktree := t.TempDir()
	svc := newTestService(t)
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
	svc.repoClient.repoResources = RepoResources{WorktreeExists: true, BranchExists: true}
	svc.sessionClient.sessionResources = SessionResources{
		SessionExists:      true,
		AgentWindowExists:  true,
		EditorWindowExists: true,
	}
	svc.sessionClient.snapshotErr = errors.New("snapshot failed")

	tasks, err := svc.service.ListTasks(t.Context())
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.Equal(t, RuntimeStateNone, tasks[0].RuntimeState)
}

func TestServiceListTasks_RuntimeSnapshotTimeoutIsBestEffort(t *testing.T) {
	worktree := t.TempDir()
	svc := newTestService(t)
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
	svc.repoClient.repoResources = RepoResources{WorktreeExists: true, BranchExists: true}
	svc.sessionClient.sessionResources = SessionResources{
		SessionExists:      true,
		AgentWindowExists:  true,
		EditorWindowExists: true,
	}
	svc.sessionClient.snapshotHook = func(ctx context.Context, _ *Task) (RuntimeSnapshot, error) {
		select {
		case <-ctx.Done():
			return RuntimeSnapshot{}, ctx.Err()
		case <-time.After(500 * time.Millisecond):
			return RuntimeSnapshot{ForegroundCommand: "codex"}, nil
		}
	}

	start := time.Now()
	tasks, err := svc.service.ListTasks(t.Context())

	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.Equal(t, RuntimeStateNone, tasks[0].RuntimeState)
	require.Less(t, time.Since(start), 400*time.Millisecond)
}

func TestServiceListTasks_RebootstrapsExistingCodexWorkspace(t *testing.T) {
	worktree := t.TempDir()
	svc := newTestService(t)
	bootstrap := &recordingBootstrapper{}
	svc.service.bootstrap = bootstrap
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
	svc.repoClient.repoResources = RepoResources{WorktreeExists: true, BranchExists: true}
	svc.sessionClient.sessionResources = SessionResources{
		SessionExists:      true,
		AgentWindowExists:  true,
		EditorWindowExists: true,
	}

	_, err := svc.service.ListTasks(t.Context())

	require.NoError(t, err)
	require.Len(t, bootstrap.calls, 1)
	require.Equal(t, worktree, bootstrap.calls[0].WorktreePath)
	require.Equal(t, "codex", bootstrap.calls[0].Provider)
}
