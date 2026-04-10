package observer

import (
	"context"
	"errors"
	"testing"
	"time"

	"agent/internal/adapters/client/codex"
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
		Tasks:   stubObserverTaskLister{tasks: []*core.Task{task}},
		Monitor: monitor,
		Repo:    repo,
		Providers: map[string]core.ProviderClient{
			"codex": stubTMuxWatcherProvider{runtimeState: core.RuntimeStateFinished},
		},
	})

	err := watcher.RefreshAll(context.Background())
	require.NoError(t, err)
	require.NotNil(t, repo.lastUpsert)
	require.Equal(t, core.DisplayStatusDisconnected, repo.lastUpsert.DisplayStatus)
	require.Equal(t, core.DisplayActivityNone, repo.lastUpsert.DisplayActivity)
	require.False(t, repo.lastUpsert.ProcessAlive)
	require.Equal(t, now, repo.lastUpsert.LastRuntimeObservedAt)
}

func TestTMuxWatcher_RefreshAllPublishesObserverUpdate(t *testing.T) {
	now := time.Date(2026, 4, 9, 12, 15, 0, 0, time.UTC)
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
		ForegroundCommand: "go",
		HadAgentBinding:   true,
		ObservedAt:        now,
	}, nil).Once()

	repo := &stubWatcherObserverRepository{}
	hub := NewHub()
	updates, release := hub.Subscribe(t.Context())
	defer release()

	watcher := NewTMuxWatcher(TMuxWatcherConfig{
		Tasks:   stubObserverTaskLister{tasks: []*core.Task{task}},
		Monitor: monitor,
		Repo:    repo,
		Providers: map[string]core.ProviderClient{
			"codex": stubTMuxWatcherProvider{runtimeState: core.RuntimeStateRunning},
		},
		Hub: hub,
	})

	err := watcher.RefreshAll(context.Background())
	require.NoError(t, err)

	select {
	case update := <-updates:
		require.Equal(t, task.ID, update.TaskID)
		require.Equal(t, core.DisplayStatusWorking, update.DisplayStatus)
		require.Equal(t, core.DisplayActivityCommand, update.DisplayActivity)
		require.Equal(t, now, update.LastActivityAt)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for observer task update")
	}
}

func TestTMuxWatcher_OverrideWithHookPhase_PermissionRequestYieldsNeedsInput(t *testing.T) {
	now := time.Date(2026, 4, 9, 12, 20, 0, 0, time.UTC)
	task := &core.Task{
		ID:              "task-1",
		TmuxSession:     "repo_task-1",
		AgentWindowName: "agent",
		Provider:        "claude",
		Status:          core.TaskStatusRunning,
	}

	monitor := core.NewMockRuntimeMonitor(t)
	monitor.EXPECT().Snapshot(mock.Anything, task).Return(core.RuntimeSnapshot{
		SessionName:       task.TmuxSession,
		PaneID:            "%24",
		ForegroundCommand: "claude",
		ObservedAt:        now,
	}, nil).Once()

	// The tmux detector returns Running (from recent output), but hooks
	// report WaitingPermission — the hook should win.
	provider := stubTMuxWatcherProvider{runtimeState: core.RuntimeStateRunning}
	repo := &stubWatcherObserverRepository{}
	hooks := &stubHookObservabilityRepository{
		summaries: map[string]*core.HookSessionSummary{
			task.ID: {
				TaskID:         task.ID,
				RuntimePhase:   core.HookRuntimePhaseWaitingPermission,
				LastActivityAt: now.Add(-1 * time.Second),
				LastEventName:  "PermissionRequest",
			},
		},
	}

	watcher := NewTMuxWatcher(TMuxWatcherConfig{
		Tasks:     stubObserverTaskLister{tasks: []*core.Task{task}},
		Monitor:   monitor,
		Repo:      repo,
		Hooks:     hooks,
		Providers: map[string]core.ProviderClient{"claude": provider},
	})

	err := watcher.RefreshAll(context.Background())
	require.NoError(t, err)
	require.NotNil(t, repo.lastUpsert)
	require.Equal(t, core.DisplayStatusNeedsInput, repo.lastUpsert.DisplayStatus)
	require.True(t, repo.lastUpsert.ProcessAlive)
}

func TestTMuxWatcher_OverrideWithHookPhase_CodexPostToolUseContinuePromptYieldsNeedsInput(t *testing.T) {
	now := time.Date(2026, 4, 10, 10, 30, 0, 0, time.UTC)
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
		ForegroundCommand: "codex",
		Content:           "Cont\x1b[32minue?\x1b[0m\n",
		ObservedAt:        now,
	}, nil).Once()

	hooks := &stubHookObservabilityRepository{
		summaries: map[string]*core.HookSessionSummary{
			task.ID: {
				TaskID:         task.ID,
				RuntimePhase:   core.HookRuntimePhaseIdle,
				LastEventName:  "PostToolUse",
				LastActivityAt: now.Add(-1 * time.Second),
			},
		},
	}

	repo := &stubWatcherObserverRepository{}
	watcher := NewTMuxWatcher(TMuxWatcherConfig{
		Tasks:     stubObserverTaskLister{tasks: []*core.Task{task}},
		Monitor:   monitor,
		Repo:      repo,
		Hooks:     hooks,
		Providers: map[string]core.ProviderClient{"codex": detectingTMuxWatcherProvider{
			detect: codex.NewRuntimeDetector(2 * time.Second).Detect,
		}},
	})

	require.NoError(t, watcher.RefreshAll(context.Background()))
	require.Equal(t, core.DisplayStatusNeedsInput, repo.lastUpsert.DisplayStatus)
}

func TestTMuxWatcher_OverrideWithHookPhase_CodexStopUsesPromptFallbackForNeedsInput(t *testing.T) {
	now := time.Date(2026, 4, 10, 10, 35, 0, 0, time.UTC)
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
		ForegroundCommand: "codex",
		Content:           "› \n  gpt-5.4 high · 82% left\n",
		ObservedAt:        now,
	}, nil).Once()

	hooks := &stubHookObservabilityRepository{
		summaries: map[string]*core.HookSessionSummary{
			task.ID: {
				TaskID:         task.ID,
				RuntimePhase:   core.HookRuntimePhaseIdle,
				LastEventName:  "Stop",
				LastActivityAt: now.Add(-1 * time.Second),
			},
		},
	}

	repo := &stubWatcherObserverRepository{}
	watcher := NewTMuxWatcher(TMuxWatcherConfig{
		Tasks:     stubObserverTaskLister{tasks: []*core.Task{task}},
		Monitor:   monitor,
		Repo:      repo,
		Hooks:     hooks,
		Providers: map[string]core.ProviderClient{"codex": detectingTMuxWatcherProvider{
			detect: codex.NewRuntimeDetector(2 * time.Second).Detect,
		}},
	})

	require.NoError(t, watcher.RefreshAll(context.Background()))
	require.Equal(t, core.DisplayStatusNeedsInput, repo.lastUpsert.DisplayStatus)
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

func (s *stubWatcherObserverRepository) ListObserverSummaries(
	context.Context,
	[]string,
) (map[string]*core.ObserverSummary, error) {
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

func (s *stubWatcherObserverRepository) SubscribeObserverTaskUpdates(
	context.Context,
) (<-chan core.ObserverTaskUpdate, func(), error) {
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

func (s stubTMuxWatcherProvider) SuggestTaskName(context.Context, string) (core.TaskSuggestion, error) {
	return core.TaskSuggestion{}, nil
}

func (s stubTMuxWatcherProvider) LaunchRequest(*core.Task) (core.LaunchRequest, error) {
	return core.LaunchRequest{}, nil
}

func (s stubTMuxWatcherProvider) DetectRuntimeState(core.RuntimeSnapshot) core.RuntimeState {
	return s.runtimeState
}

type detectingTMuxWatcherProvider struct {
	detect func(core.RuntimeSnapshot) core.RuntimeState
}

func (s detectingTMuxWatcherProvider) IsAvailable(context.Context) error {
	return nil
}

func (s detectingTMuxWatcherProvider) SuggestTaskName(context.Context, string) (core.TaskSuggestion, error) {
	return core.TaskSuggestion{}, nil
}

func (s detectingTMuxWatcherProvider) LaunchRequest(*core.Task) (core.LaunchRequest, error) {
	return core.LaunchRequest{}, nil
}

func (s detectingTMuxWatcherProvider) DetectRuntimeState(snapshot core.RuntimeSnapshot) core.RuntimeState {
	if s.detect == nil {
		return core.RuntimeStateNone
	}
	return s.detect(snapshot)
}

type stubHookObservabilityRepository struct {
	summaries map[string]*core.HookSessionSummary
}

func (s *stubHookObservabilityRepository) ListHookSessionSummaries(
	_ context.Context,
	taskIDs []string,
) (map[string]*core.HookSessionSummary, error) {
	result := make(map[string]*core.HookSessionSummary)
	for _, id := range taskIDs {
		if hs, ok := s.summaries[id]; ok {
			result[id] = hs
		}
	}
	return result, nil
}

func (s *stubHookObservabilityRepository) ListHookEvents(
	context.Context,
	string,
	int,
) ([]core.HookEvent, error) {
	return nil, nil
}

func (s *stubHookObservabilityRepository) SubscribeHookSessionUpdates(
	context.Context,
) (<-chan core.HookSessionSummary, func(), error) {
	ch := make(chan core.HookSessionSummary)
	close(ch)
	return ch, func() {}, nil
}
