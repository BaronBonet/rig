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
	Hooks     core.HookObservabilityRepository
	Hub       *Hub
	Providers map[string]core.ProviderClient
	Now       func() time.Time
}

type TMuxWatcher struct {
	tasks     observerTaskLister
	monitor   core.RuntimeMonitor
	repo      core.ObserverRuntimeRepository
	hooks     core.HookObservabilityRepository
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
		hooks:     cfg.Hooks,
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

	// Hook data is more authoritative than tmux snapshot parsing. When
	// hooks indicate the provider is actively working, override the
	// tmux-based runtime state which may incorrectly detect a visible
	// prompt as "needs input".
	runtimeState = w.overrideWithHookPhase(ctx, task, snapshot, runtimeState)

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

// hookStaleThreshold is a safety net: if no hook events arrive for this
// long, stop trusting the hook phase and fall back to tmux detection.
// This guards against hooks silently breaking.
const hookStaleThreshold = 5 * time.Minute

func (w *TMuxWatcher) overrideWithHookPhase(
	ctx context.Context,
	task *core.Task,
	snapshot core.RuntimeSnapshot,
	rs core.RuntimeState,
) core.RuntimeState {
	if task == nil || w.hooks == nil {
		return rs
	}

	// If tmux says the process isn't running at all, trust tmux —
	// the agent has exited regardless of what hooks last reported.
	if rs == core.RuntimeStateNone || rs == core.RuntimeStateFinished {
		return rs
	}

	summaries, err := w.hooks.ListHookSessionSummaries(ctx, []string{task.ID})
	if err != nil {
		return rs
	}

	hs := summaries[task.ID]
	if hs == nil || hs.LastActivityAt.IsZero() {
		return rs
	}

	// Safety net: if hooks haven't arrived in a long time, they may
	// have broken. Fall back to tmux detection.
	age := snapshot.ObservedAt.Sub(hs.LastActivityAt)
	if age > hookStaleThreshold {
		return rs
	}

	if task.Provider == "codex" {
		switch hs.RuntimePhase {
		case core.HookRuntimePhaseWaitingPermission:
			return core.RuntimeStateNeedsInput
		case core.HookRuntimePhasePrompted, core.HookRuntimePhaseRunningCommand:
			return core.RuntimeStateRunning
		case core.HookRuntimePhaseIdle:
			if hs.LastEventName == "PostToolUse" && rs == core.RuntimeStateNeedsInput {
				return rs
			}
			if hs.LastEventName == "Stop" {
				return rs
			}
			return core.RuntimeStateRunning
		}
	}

	switch hs.RuntimePhase {
	case core.HookRuntimePhaseWaitingPermission:
		return core.RuntimeStateNeedsInput
	case core.HookRuntimePhasePrompted, core.HookRuntimePhaseRunningCommand:
		return core.RuntimeStateRunning
	case core.HookRuntimePhaseIdle:
		if hs.LastEventName == "Stop" {
			return rs
		}
		return core.RuntimeStateRunning
	}

	return rs
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
