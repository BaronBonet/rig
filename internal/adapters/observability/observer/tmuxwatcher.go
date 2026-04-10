package observer

import (
	"context"
	"fmt"
	"strings"
	"time"

	"agent/internal/core"
)

type observerTaskLister interface {
	ListTasks(ctx context.Context) ([]*core.Task, error)
}

type TMuxWatcherConfig struct {
	Tasks     observerTaskLister
	Monitor   core.RuntimeMonitor
	Repo      core.ObserverRuntimeRepository
	Hub       *Hub
	Providers map[string]core.ProviderClient
	Now       func() time.Time
}

type TMuxWatcher struct {
	tasks     observerTaskLister
	monitor   core.RuntimeMonitor
	repo      core.ObserverRuntimeRepository
	hub       *Hub
	providers map[string]core.ProviderClient
	now       func() time.Time
}

func NewTMuxWatcher(cfg TMuxWatcherConfig) *TMuxWatcher {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	return &TMuxWatcher{
		tasks:     cfg.Tasks,
		monitor:   cfg.Monitor,
		repo:      cfg.Repo,
		hub:       cfg.Hub,
		providers: cfg.Providers,
		now:       cfg.Now,
	}
}

func (w *TMuxWatcher) HandleSessionActivity(ctx context.Context, sessionName string) error {
	if w == nil {
		return nil
	}
	if w.tasks == nil {
		return fmt.Errorf("tmux watcher task lister not configured")
	}
	if w.monitor == nil {
		return fmt.Errorf("tmux watcher runtime monitor not configured")
	}
	if w.repo == nil {
		return fmt.Errorf("tmux watcher observer repository not configured")
	}

	task, err := w.findTaskBySession(ctx, sessionName)
	if err != nil || task == nil {
		return err
	}

	return w.refreshTask(ctx, task)
}

func (w *TMuxWatcher) RefreshAll(ctx context.Context) error {
	if w == nil {
		return nil
	}
	if w.tasks == nil {
		return fmt.Errorf("tmux watcher task lister not configured")
	}

	tasks, err := w.tasks.ListTasks(ctx)
	if err != nil {
		return err
	}

	for _, task := range tasks {
		if task == nil || strings.TrimSpace(task.TmuxSession) == "" {
			continue
		}
		if err := w.refreshTask(ctx, task); err != nil {
			continue
		}
	}

	return nil
}

func (w *TMuxWatcher) Close() error {
	if w == nil || w.monitor == nil {
		return nil
	}

	return w.monitor.Close()
}

func (w *TMuxWatcher) refreshTask(ctx context.Context, task *core.Task) error {
	if task == nil {
		return nil
	}

	provider := w.providers[task.Provider]
	if provider == nil {
		return nil
	}

	snapshot, err := w.monitor.Snapshot(ctx, task)
	if err != nil {
		return w.persistSummary(ctx, &core.ObserverSummary{
			TaskID:                task.ID,
			DisplayStatus:         core.DisplayStatusDisconnected,
			DisplayActivity:       core.DisplayActivityNone,
			ProcessAlive:          false,
			LastRuntimeObservedAt: w.now().UTC(),
		})
	}

	runtimeState := provider.DetectRuntimeState(snapshot)
	processAlive := runtimeState == core.RuntimeStateRunning || runtimeState == core.RuntimeStateNeedsInput
	display := DeriveDisplayStatus(StatusInput{
		TaskStatus:    task.Status,
		RuntimeState:  runtimeState,
		ProcessAlive:  processAlive,
		ActiveCommand: isForegroundCommandActivity(snapshot.ForegroundCommand),
	})

	observedAt := snapshot.ObservedAt.UTC()
	if observedAt.IsZero() {
		observedAt = w.now().UTC()
	}

	summary := &core.ObserverSummary{
		TaskID:                task.ID,
		DisplayStatus:         display.Primary,
		DisplayActivity:       display.Activity,
		ProcessAlive:          processAlive,
		LastRuntimeObservedAt: observedAt,
	}
	return w.persistSummary(ctx, summary)
}

func (w *TMuxWatcher) findTaskBySession(ctx context.Context, sessionName string) (*core.Task, error) {
	tasks, err := w.tasks.ListTasks(ctx)
	if err != nil {
		return nil, err
	}

	target := strings.TrimSpace(sessionName)
	for _, task := range tasks {
		if task == nil {
			continue
		}
		if strings.TrimSpace(task.TmuxSession) == target {
			return task, nil
		}
	}

	return nil, nil
}

func isForegroundCommandActivity(command string) bool {
	command = strings.TrimSpace(strings.ToLower(command))
	if command == "" {
		return false
	}

	switch command {
	case "sh", "bash", "zsh", "fish", "dash", "ksh", "codex", "claude", "node":
		return false
	default:
		return true
	}
}

func (w *TMuxWatcher) persistSummary(ctx context.Context, summary *core.ObserverSummary) error {
	if err := w.repo.UpsertObserverSummary(ctx, summary); err != nil {
		return err
	}
	if w.hub != nil {
		w.hub.Publish(observerTaskUpdateFromSummary(summary, nil))
	}
	return nil
}
