package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type stubHookObservabilityRepository struct {
	listHookSessionSummaries func(ctx context.Context, taskIDs []string) (map[string]*HookSessionSummary, error)
	listHookEvents           func(ctx context.Context, taskID string, limit int) ([]HookEvent, error)
	subscribeHookUpdates     func(ctx context.Context) (<-chan HookSessionSummary, func(), error)
}

func (s stubHookObservabilityRepository) ListHookSessionSummaries(ctx context.Context, taskIDs []string) (map[string]*HookSessionSummary, error) {
	if s.listHookSessionSummaries == nil {
		return map[string]*HookSessionSummary{}, nil
	}

	return s.listHookSessionSummaries(ctx, taskIDs)
}

func (s stubHookObservabilityRepository) ListHookEvents(ctx context.Context, taskID string, limit int) ([]HookEvent, error) {
	if s.listHookEvents == nil {
		return nil, nil
	}

	return s.listHookEvents(ctx, taskID, limit)
}

func (s stubHookObservabilityRepository) SubscribeHookSessionUpdates(ctx context.Context) (<-chan HookSessionSummary, func(), error) {
	if s.subscribeHookUpdates == nil {
		ch := make(chan HookSessionSummary)
		return ch, func() { close(ch) }, nil
	}

	return s.subscribeHookUpdates(ctx)
}

func TestServiceListTaskViews_UsesHookSummaryWhenAvailable(t *testing.T) {
	h := newTestService(t)
	task := &Task{
		ID:               "task-1",
		Slug:             "billing-retry-flow",
		RepoRoot:         "/tmp/repo",
		BranchName:       "feat/billing-retry-flow",
		WorktreePath:     t.TempDir(),
		TmuxSession:      "repo-billing-retry-flow",
		AgentWindowName:  "agent",
		EditorWindowName: "editor",
		Provider:         "codex",
		Status:           TaskStatusRunning,
	}
	summary := &HookSessionSummary{
		TaskID:          task.ID,
		SessionID:       "sess-1",
		RuntimePhase:    HookRuntimePhaseRunningCommand,
		LastCommandText: "go test ./...",
	}

	h.taskRepo.listTasks = []*Task{task}
	h.repoClient.repoResources = RepoResources{WorktreeExists: true, BranchExists: true}
	h.sessionClient.sessionResources = SessionResources{
		SessionExists:      true,
		AgentWindowExists:  true,
		EditorWindowExists: true,
	}
	h.service.hooks = stubHookObservabilityRepository{
		listHookSessionSummaries: func(_ context.Context, taskIDs []string) (map[string]*HookSessionSummary, error) {
			require.Equal(t, []string{task.ID}, taskIDs)
			return map[string]*HookSessionSummary{task.ID: summary}, nil
		},
	}

	views, err := h.service.ListTaskViews(t.Context())
	require.NoError(t, err)
	require.Len(t, views, 1)
	require.NotNil(t, views[0].HookSession)
	require.Equal(t, HookRuntimePhaseRunningCommand, views[0].HookSession.RuntimePhase)
	require.Equal(t, "go test ./...", views[0].HookSession.LastCommandText)
}
