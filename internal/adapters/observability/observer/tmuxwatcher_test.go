package observer

import (
	"context"
	"errors"
	"testing"
	"time"

	"agent/internal/core"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestTMuxWatcher_RefreshesAffectedTaskOnPaneActivity(t *testing.T) {
	now := time.Date(2026, 4, 9, 12, 0, 0, 0, time.UTC)
	task := &core.Task{
		ID:              "task-1",
		TmuxSession:     "repo_task-1",
		AgentWindowName: "agent",
		Provider:        "codex",
		Status:          core.TaskStatusRunning,
	}

	monitor := core.NewMockRuntimeMonitor(t)
	monitor.EXPECT().Snapshot(mock.Anything, task).Return(core.RuntimeSnapshot{
		SessionName:       task.TmuxSession,
		PaneID:            "%24",
		ForegroundCommand: "zsh",
		ObservedAt:        now,
	}, nil).Once()

	provider := stubTMuxWatcherProvider{runtimeState: core.RuntimeStateNeedsInput}
	repo := &stubWatcherObserverRepository{}
	watcher := NewTMuxWatcher(TMuxWatcherConfig{
		Tasks:     stubObserverTaskLister{tasks: []*core.Task{task}},
		Monitor:   monitor,
		Repo:      repo,
		Providers: map[string]core.ProviderClient{"codex": provider},
	})

	err := watcher.HandleSessionActivity(context.Background(), "repo_task-1")
	require.NoError(t, err)
	require.NotNil(t, repo.lastUpsert)
	require.Equal(t, task.ID, repo.lastUpsert.TaskID)
	require.Equal(t, core.DisplayStatusNeedsInput, repo.lastUpsert.DisplayStatus)
	require.Equal(t, core.DisplayActivityNone, repo.lastUpsert.DisplayActivity)
	require.True(t, repo.lastUpsert.ProcessAlive)
	require.Equal(t, now, repo.lastUpsert.LastRuntimeObservedAt)
}

func TestTMuxWatcher_RefreshAllMarksTaskDisconnectedWhenSnapshotFails(t *testing.T) {
	now := time.Date(2026, 4, 9, 12, 5, 0, 0, time.UTC)
	task := &core.Task{
		ID:              "task-1",
		TmuxSession:     "repo_task-1",
		AgentWindowName: "agent",
		Provider:        "codex",
		Status:          core.TaskStatusRunning,
	}

	monitor := core.NewMockRuntimeMonitor(t)
	monitor.EXPECT().Snapshot(mock.Anything, task).Return(core.RuntimeSnapshot{}, errors.New("session missing")).Once()

	repo := &stubWatcherObserverRepository{}
	watcher := NewTMuxWatcher(TMuxWatcherConfig{
		Tasks:     stubObserverTaskLister{tasks: []*core.Task{task}},
		Monitor:   monitor,
		Repo:      repo,
		Providers: map[string]core.ProviderClient{"codex": stubTMuxWatcherProvider{}},
		Now:       func() time.Time { return now },
	})

	err := watcher.RefreshAll(context.Background())
	require.NoError(t, err)
	require.NotNil(t, repo.lastUpsert)
	require.Equal(t, task.ID, repo.lastUpsert.TaskID)
	require.Equal(t, core.DisplayStatusDisconnected, repo.lastUpsert.DisplayStatus)
	require.Equal(t, core.DisplayActivityNone, repo.lastUpsert.DisplayActivity)
	require.False(t, repo.lastUpsert.ProcessAlive)
	require.Equal(t, now, repo.lastUpsert.LastRuntimeObservedAt)
}

func TestTMuxWatcher_RefreshAllMarksTaskDisconnectedWhenProviderFinished(t *testing.T) {
	now := time.Date(2026, 4, 9, 12, 10, 0, 0, time.UTC)
	task := &core.Task{
		ID:              "task-1",
		TmuxSession:     "repo_task-1",
		AgentWindowName: "agent",
		Provider:        "codex",
		Status:          core.TaskStatusRunning,
	}

	monitor := core.NewMockRuntimeMonitor(t)
	monitor.EXPECT().Snapshot(mock.Anything, task).Return(core.RuntimeSnapshot{
		SessionName:       task.TmuxSession,
		PaneID:            "%24",
		ForegroundCommand: "zsh",
		HadAgentBinding:   true,
		ObservedAt:        now,
	}, nil).Once()

	repo := &stubWatcherObserverRepository{}
	watcher := NewTMuxWatcher(TMuxWatcherConfig{
		Tasks:     stubObserverTaskLister{tasks: []*core.Task{task}},
		Monitor:   monitor,
		Repo:      repo,
		Providers: map[string]core.ProviderClient{"codex": stubTMuxWatcherProvider{runtimeState: core.RuntimeStateFinished}},
	})

	err := watcher.RefreshAll(context.Background())
	require.NoError(t, err)
	require.NotNil(t, repo.lastUpsert)
	require.Equal(t, core.DisplayStatusDisconnected, repo.lastUpsert.DisplayStatus)
	require.Equal(t, core.DisplayActivityNone, repo.lastUpsert.DisplayActivity)
	require.False(t, repo.lastUpsert.ProcessAlive)
	require.Equal(t, now, repo.lastUpsert.LastRuntimeObservedAt)
}

type stubObserverTaskLister struct {
	tasks []*core.Task
	err   error
}

func (s stubObserverTaskLister) ListTasks(context.Context) ([]*core.Task, error) {
	return s.tasks, s.err
}

type stubWatcherObserverRepository struct {
	lastUpsert *core.ObserverSummary
}

func (s *stubWatcherObserverRepository) ListObserverSummaries(context.Context, []string) (map[string]*core.ObserverSummary, error) {
	return map[string]*core.ObserverSummary{}, nil
}

func (s *stubWatcherObserverRepository) UpsertObserverSummary(_ context.Context, summary *core.ObserverSummary) error {
	if summary == nil {
		return nil
	}
	clone := *summary
	s.lastUpsert = &clone
	return nil
}

func (s *stubWatcherObserverRepository) SubscribeObserverTaskUpdates(context.Context) (<-chan core.ObserverTaskUpdate, func(), error) {
	ch := make(chan core.ObserverTaskUpdate)
	close(ch)
	return ch, func() {}, nil
}

type stubTMuxWatcherProvider struct {
	runtimeState core.RuntimeState
}

func (s stubTMuxWatcherProvider) IsAvailable(context.Context) error {
	return nil
}

func (s stubTMuxWatcherProvider) SuggestTaskName(context.Context, string) (string, error) {
	return "", nil
}

func (s stubTMuxWatcherProvider) LaunchRequest(*core.Task) (core.LaunchRequest, error) {
	return core.LaunchRequest{}, nil
}

func (s stubTMuxWatcherProvider) DetectRuntimeState(core.RuntimeSnapshot) core.RuntimeState {
	return s.runtimeState
}
