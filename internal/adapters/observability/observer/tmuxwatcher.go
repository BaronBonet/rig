package observer

import (
	"context"
	"fmt"
	"strings"
	"time"

	"rig/internal/core"
)

type observerTaskLister interface {
	ListTasks(ctx context.Context) ([]*core.Task, error)
}

type observerTaskUpdater interface {
	UpdateTask(ctx context.Context, task *core.Task) error
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

func (w *TMuxWatcher) RefreshTaskByID(ctx context.Context, taskID string) error {
	if w == nil || strings.TrimSpace(taskID) == "" {
		return nil
	}
	if w.tasks == nil {
		return fmt.Errorf("tmux watcher task lister not configured")
	}

	tasks, err := w.tasks.ListTasks(ctx)
	if err != nil {
		return err
	}

	target := strings.TrimSpace(taskID)
	for _, task := range tasks {
		if task == nil || strings.TrimSpace(task.ID) != target {
			continue
		}
		return w.refreshTask(ctx, task)
	}

	return nil
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

	hookSummary := w.lookupHookSessionSummary(ctx, task.ID)
	snapshot, err := w.monitor.Snapshot(ctx, task)
	if err != nil {
		if summary := w.summaryFromHookOnSnapshotFailure(task, hookSummary); summary != nil {
			return w.persistSummary(ctx, summary, string(task.Provider))
		}
		return w.persistSummary(ctx, &core.ObserverSummary{
			TaskID:                task.ID,
			DisplayStatus:         core.DisplayStatusDisconnected,
			DisplayActivity:       core.DisplayActivityNone,
			ProcessAlive:          false,
			LastRuntimeObservedAt: w.now().UTC(),
		}, string(task.Provider))
	}

	if observedProvider := w.resolveObservedProvider(snapshot, hookSummary); observedProvider != "" &&
		observedProvider != strings.TrimSpace(string(task.Provider)) {
		if err := w.persistTaskProvider(ctx, task, observedProvider); err != nil {
			return err
		}
	}

	provider := w.providers[string(task.Provider)]
	if provider == nil {
		return nil
	}

	runtimeState := provider.DetectRuntimeState(snapshot)

	// Hook data is more authoritative than tmux snapshot parsing. When
	// hooks indicate the provider is actively working, override the
	// tmux-based runtime state which may incorrectly detect a visible
	// prompt as "needs input".
	runtimeState = w.overrideWithHookPhase(snapshot, hookSummary, runtimeState, string(task.Provider))

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
	return w.persistSummary(ctx, summary, string(task.Provider))
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

func (w *TMuxWatcher) lookupHookSessionSummary(ctx context.Context, taskID string) *core.HookSessionSummary {
	if strings.TrimSpace(taskID) == "" || w.hooks == nil {
		return nil
	}

	summaries, err := w.hooks.ListHookSessionSummaries(ctx, []string{taskID})
	if err != nil {
		return nil
	}

	return summaries[taskID]
}

// hookStaleThreshold is a safety net: if no hook events arrive for this
// long, stop trusting the hook phase and fall back to tmux detection.
// This guards against hooks silently breaking.
const hookStaleThreshold = 5 * time.Minute

func (w *TMuxWatcher) overrideWithHookPhase(
	snapshot core.RuntimeSnapshot,
	hs *core.HookSessionSummary,
	rs core.RuntimeState,
	provider string,
) core.RuntimeState {
	if hs == nil {
		return rs
	}

	// A provider-emitted Stop means the session is ready for the next prompt.
	// For Codex, keep honoring that even if the pane has returned to a shell or
	// the hook is older than the general freshness window.
	if provider == "codex" && hs.RuntimePhase == core.HookRuntimePhaseIdle && hs.LastEventName == "Stop" {
		return core.RuntimeStateNeedsInput
	}

	// If tmux can't see a provider process at all, trust tmux for
	// non-Codex providers. Codex can transiently fail to surface a
	// detectable process between turns even while fresh hooks still
	// prove the session is live.
	if rs == core.RuntimeStateNone && provider != "codex" {
		return rs
	}

	// For non-Codex providers, a finished snapshot remains authoritative.
	// Codex can briefly return the pane to a shell while fresh hooks still
	// prove the turn is active, so allow fresh Codex hooks to override below.
	if rs == core.RuntimeStateFinished && provider != "codex" {
		return rs
	}

	if hs.LastActivityAt.IsZero() {
		return rs
	}

	// Safety net: if hooks haven't arrived in a long time, they may
	// have broken. Fall back to tmux detection.
	age := snapshot.ObservedAt.Sub(hs.LastActivityAt)
	if age > hookStaleThreshold {
		return rs
	}

	if provider == "codex" {
		switch hs.RuntimePhase {
		case core.HookRuntimePhaseWaitingPermission:
			return core.RuntimeStateNeedsInput
		case core.HookRuntimePhasePrompted, core.HookRuntimePhaseRunningCommand:
			return core.RuntimeStateRunning
		case core.HookRuntimePhaseIdle:
			if rs == core.RuntimeStateNeedsInput {
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
			return core.RuntimeStateNeedsInput
		}
		// Between tools (e.g. PostToolUse) Claude is still working —
		// don't trust tmux which may see a stale ❯ prompt and falsely
		// report NeedsInput.
		return core.RuntimeStateRunning
	}

	return rs
}

func (w *TMuxWatcher) summaryFromHookOnSnapshotFailure(
	task *core.Task,
	hs *core.HookSessionSummary,
) *core.ObserverSummary {
	if task == nil || hs == nil {
		return nil
	}

	display, alive := w.displayStateFromHookWithoutSnapshot(string(task.Provider), hs)
	if display.Primary == "" {
		return nil
	}

	return &core.ObserverSummary{
		TaskID:                task.ID,
		DisplayStatus:         display.Primary,
		DisplayActivity:       display.Activity,
		ProcessAlive:          alive,
		LastRuntimeObservedAt: w.now().UTC(),
	}
}

func (w *TMuxWatcher) displayStateFromHookWithoutSnapshot(
	provider string,
	hs *core.HookSessionSummary,
) (core.DisplayState, bool) {
	if hs == nil {
		return core.DisplayState{}, false
	}

	if provider == "codex" && hs.RuntimePhase == core.HookRuntimePhaseIdle && hs.LastEventName == "Stop" {
		return core.DisplayState{Primary: core.DisplayStatusNeedsInput}, true
	}

	if hs.LastActivityAt.IsZero() {
		return core.DisplayState{}, false
	}

	if w.now().UTC().Sub(hs.LastActivityAt) > hookStaleThreshold {
		return core.DisplayState{}, false
	}

	switch hs.RuntimePhase {
	case core.HookRuntimePhaseWaitingPermission:
		return core.DisplayState{Primary: core.DisplayStatusNeedsInput}, true
	case core.HookRuntimePhasePrompted:
		return core.DisplayState{Primary: core.DisplayStatusWorking}, true
	case core.HookRuntimePhaseRunningCommand:
		return core.DisplayState{Primary: core.DisplayStatusWorking, Activity: core.DisplayActivityCommand}, true
	case core.HookRuntimePhaseIdle:
		if hs.LastEventName == "Stop" {
			return core.DisplayState{Primary: core.DisplayStatusNeedsInput}, true
		}
		if provider == "codex" || provider == "claude" {
			return core.DisplayState{Primary: core.DisplayStatusWorking}, true
		}
	}

	return core.DisplayState{}, false
}

func (w *TMuxWatcher) resolveObservedProvider(
	snapshot core.RuntimeSnapshot,
	hookSummary *core.HookSessionSummary,
) string {
	if w.isFreshHookSummary(snapshot, hookSummary) {
		if provider := core.InferProviderFromHookSession(hookSummary); provider != "" {
			return provider
		}
	}

	return core.InferProviderFromRuntimeSnapshot(snapshot)
}

func (w *TMuxWatcher) isFreshHookSummary(snapshot core.RuntimeSnapshot, hs *core.HookSessionSummary) bool {
	if hs == nil || hs.LastActivityAt.IsZero() {
		return false
	}
	observedAt := snapshot.ObservedAt
	if observedAt.IsZero() {
		observedAt = w.now().UTC()
	}
	return observedAt.Sub(hs.LastActivityAt) <= hookStaleThreshold
}

func (w *TMuxWatcher) persistTaskProvider(ctx context.Context, task *core.Task, provider string) error {
	updater, ok := w.tasks.(observerTaskUpdater)
	if !ok || task == nil {
		task.Provider = core.AgentProvider(provider)
		return nil
	}

	task.Provider = core.AgentProvider(provider)
	task.UpdatedAt = w.now().UTC()
	return updater.UpdateTask(ctx, task)
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

func (w *TMuxWatcher) persistSummary(ctx context.Context, summary *core.ObserverSummary, provider string) error {
	if err := w.repo.UpsertObserverSummary(ctx, summary); err != nil {
		return err
	}
	if w.hub != nil {
		w.hub.Publish(observerTaskUpdateFromSummary(summary, nil, provider))
	}
	return nil
}
