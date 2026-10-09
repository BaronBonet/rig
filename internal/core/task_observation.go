package core

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

const defaultTaskStatusRecoveryPollInterval = 2 * time.Second
const defaultTaskStatusRecoveryWorkerLimit = 2

// taskObservation is the Task observation module: everything Rig derives
// from provider hook events and provider session history lives here — the
// agent sessions hook events are attributed to, each agent session's runtime
// status (including read-time liveness against the live tmux session and
// provider-side recovery of stale status), the task's runtime status rolled
// up from them, persisted activity, and token usage.
type taskObservation struct {
	tasks          TaskRepository
	tmuxSession    TmuxSessionClient
	providers      map[Provider]ProviderClient
	providerConfig ProviderConfigStore
	// recoveryPollInterval paces the subscription recovery loop that
	// re-derives status while at least one task has live interest.
	recoveryPollInterval time.Duration
	recoveryWorkerLimit  int
	statusCacheMaxAge    time.Duration
	statusObserver       *taskStatusObserver
	// paneTraces caches where hook processes were traced to, and
	// codexFallbackRetries when the Codex fallback may next inspect tmux for
	// a conversation it could not place or failed to inspect, keyed by
	// Provider session ID.
	paneTraces           *boundedCache[paneTraceKey, paneTrace]
	codexFallbackRetries *boundedCache[string, time.Time]
	newAgentSessionID    func() string
	now                  func() time.Time
	// agentSessionMu serializes reading and writing agent sessions, because
	// concurrent hook events can come from the same pane.
	agentSessionMu sync.Mutex
}

type taskObservationOptions struct {
	recoveryPollInterval time.Duration
	recoveryWorkerLimit  int
	statusCacheMaxAge    time.Duration
}

func newTaskObservation(
	tasks TaskRepository,
	tmuxSession TmuxSessionClient,
	providers map[Provider]ProviderClient,
	providerConfig ProviderConfigStore,
) *taskObservation {
	return newTaskObservationWithOptions(
		tasks,
		tmuxSession,
		providers,
		providerConfig,
		taskObservationOptions{},
	)
}

func newTaskObservationWithOptions(
	tasks TaskRepository,
	tmuxSession TmuxSessionClient,
	providers map[Provider]ProviderClient,
	providerConfig ProviderConfigStore,
	options taskObservationOptions,
) *taskObservation {
	if options.recoveryPollInterval <= 0 {
		options.recoveryPollInterval = defaultTaskStatusRecoveryPollInterval
	}
	if options.recoveryWorkerLimit <= 0 {
		options.recoveryWorkerLimit = defaultTaskStatusRecoveryWorkerLimit
	}
	if options.statusCacheMaxAge <= 0 {
		options.statusCacheMaxAge = options.recoveryPollInterval
	}
	observation := &taskObservation{
		tasks:                tasks,
		tmuxSession:          tmuxSession,
		providers:            providers,
		providerConfig:       providerConfig,
		recoveryPollInterval: options.recoveryPollInterval,
		recoveryWorkerLimit:  options.recoveryWorkerLimit,
		statusCacheMaxAge:    options.statusCacheMaxAge,
		paneTraces:           newBoundedCache[paneTraceKey, paneTrace](maxPaneTraceEntries),
		codexFallbackRetries: newBoundedCache[string, time.Time](maxPaneTraceEntries),
		newAgentSessionID:    newAgentSessionID,
		now:                  time.Now,
	}
	observation.statusObserver = newTaskStatusObserver(observation)
	return observation
}

// supportedProviderClient returns the adapter client for a supported provider
// without requiring the provider to be configured. Use it for read-side
// behavior that must keep working for agents whose provider is no longer
// configured.
func supportedProviderClient(
	providers map[Provider]ProviderClient,
	provider Provider,
) (ProviderClient, error) {
	providerClient, ok := providers[provider]
	if !ok {
		return nil, fmt.Errorf("provider %q unavailable", provider)
	}

	return providerClient, nil
}

func (o *taskObservation) GetTaskActivity(
	ctx context.Context,
	taskID string,
	limit int,
) ([]TaskActivityEvent, error) {
	taskID = strings.TrimSpace(taskID)
	events, err := o.tasks.GetTaskActivity(ctx, taskID, 0)
	if err != nil {
		return nil, err
	}
	events = mergeTaskActivity(events, o.recoveredTaskActivity(ctx, taskID, events))

	if limit <= 0 {
		return events, nil
	}

	return activityWindowWithLastUserPrompt(events, limit), nil
}

func (o *taskObservation) GetTaskTokenUsage(ctx context.Context, taskID string) (*TaskTokenUsage, error) {
	sessions, err := o.tasks.ListTaskProviderSessions(ctx, strings.TrimSpace(taskID))
	if err != nil {
		return nil, err
	}

	latestBySession := latestProviderSessionsByID(sessions)
	if len(latestBySession) == 0 {
		return nil, nil
	}

	var total TaskTokenUsage
	for _, session := range latestBySession {
		transcriptPath := strings.TrimSpace(session.TranscriptPath)
		if transcriptPath == "" {
			continue
		}
		providerClient, err := supportedProviderClient(o.providers, session.Provider)
		if err != nil {
			continue
		}
		usage, err := providerClient.ReadSessionTokenUsage(ctx, transcriptPath)
		if err != nil {
			return nil, fmt.Errorf("read session token usage %q: %w", transcriptPath, err)
		}
		if usage == nil || usage.IsZero() {
			continue
		}

		total.SessionCount++
		total.InputTokens += usage.InputTokens
		total.OutputTokens += usage.OutputTokens
		total.CachedInputTokens += usage.CachedInputTokens
		total.CacheCreationInputTokens += usage.CacheCreationInputTokens
		total.ReasoningOutputTokens += usage.ReasoningOutputTokens
		total.TotalTokens += usage.TotalTokens
	}

	if total.IsZero() {
		return nil, nil
	}

	return &total, nil
}

func (o *taskObservation) LatestTaskStatus(ctx context.Context, taskID string) (*TaskStatusUpdate, error) {
	return o.statusObserver.LatestTaskStatus(ctx, strings.TrimSpace(taskID))
}

func (o *taskObservation) SubscribeTaskStatus(
	ctx context.Context,
	taskID string,
) (<-chan TaskStatusUpdate, error) {
	return o.statusObserver.SubscribeTaskStatus(ctx, strings.TrimSpace(taskID))
}

func (o *taskObservation) HandleHookEvent(ctx context.Context, input HookEventInput) error {
	if input.Provider == "" {
		return ErrUnmanagedHookEvent
	}

	providerClient, err := supportedProviderClient(o.providers, input.Provider)
	if err != nil {
		return err
	}

	// Hook routes exist for every supported provider, so observation decides
	// here whether an incoming hook is actionable: hooks from providers the
	// user has not configured are ignored without task changes.
	if o.providerConfig == nil {
		return fmt.Errorf("provider config store not configured")
	}
	setup, err := o.providerConfig.GetProviderSetup(ctx)
	if err != nil {
		return err
	}
	if setup == nil || !setup.IsConfigured(input.Provider) {
		return ErrUnmanagedHookEvent
	}

	input.TaskID = strings.TrimSpace(input.TaskID)
	if input.TaskID == "" {
		resolvedTaskID, err := o.resolveTaskIDFromCwd(ctx, input.Cwd)
		if err != nil {
			return err
		}
		input.TaskID = resolvedTaskID
	}

	if err := o.recordHookSession(ctx, input); err != nil {
		return err
	}

	update, err := providerClient.HookEventToTaskStatus(input)
	if err != nil {
		return err
	}
	if update != nil {
		normalizeProviderHookStatusUpdate(update, input)
	}

	// The event drives the status of the agent session it came from, whatever
	// provider the task was created with, and its activity is recorded
	// against that agent session. Without a task record, task is nil and
	// there is nothing to place the event in.
	task, _ := taskByID(ctx, o.tasks, input.TaskID)
	return o.recordAgentSession(ctx, task, input, update, taskActivityEventFromHookInput(input))
}

func (o *taskObservation) resolveTaskIDFromCwd(ctx context.Context, cwd string) (string, error) {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return "", ErrUnmanagedHookEvent
	}

	tasks, err := o.tasks.ListTasks(ctx)
	if err != nil {
		return "", fmt.Errorf("list tasks for hook resolution: %w", err)
	}

	for _, task := range tasks {
		if task != nil && strings.TrimSpace(task.WorktreePath) == cwd {
			return strings.TrimSpace(task.ID), nil
		}
	}

	return "", ErrUnmanagedHookEvent
}

func (o *taskObservation) recoveredTaskActivity(
	ctx context.Context,
	taskID string,
	events []TaskActivityEvent,
) []TaskActivityEvent {
	sessions, err := o.tasks.ListTaskProviderSessions(ctx, taskID)
	if err != nil {
		return nil
	}

	after := latestTaskActivityObservedAt(events)
	var recovered []TaskActivityEvent
	for _, session := range latestProviderSessionsByID(sessions) {
		providerClient, err := supportedProviderClient(o.providers, session.Provider)
		if err != nil {
			continue
		}
		activity, err := providerClient.ReadSessionActivity(ctx, session, after)
		if err != nil {
			continue
		}
		recovered = append(recovered, activity...)
	}
	return recovered
}

func (o *taskObservation) recordHookSession(ctx context.Context, input HookEventInput) error {
	input.SessionID = strings.TrimSpace(input.SessionID)
	if input.SessionID == "" {
		return nil
	}
	// A SessionEnd adds nothing to its Provider session's history, and Codex
	// sends one up to 30 minutes after the agent left that Provider session:
	// recording it could change which transcript token usage and activity
	// recovery read.
	if strings.TrimSpace(input.EventName) == HookEventSessionEnd {
		return nil
	}

	observedAt := input.OccurredAt
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	if err := o.tasks.UpsertTaskProviderSession(ctx, TaskProviderSession{
		TaskID:            input.TaskID,
		Provider:          input.Provider,
		ProviderSessionID: input.SessionID,
		TranscriptPath:    strings.TrimSpace(input.TranscriptPath),
		StartSource:       strings.TrimSpace(input.StartSource),
		Model:             strings.TrimSpace(input.Model),
		Cwd:               strings.TrimSpace(input.Cwd),
		FirstObservedAt:   observedAt,
		LastObservedAt:    observedAt,
		LastEventName:     strings.TrimSpace(input.EventName),
	}); err != nil {
		return fmt.Errorf("upsert task provider session: %w", err)
	}
	return nil
}

// recordHookActivity records a hook event's activity, if any, against the
// agent session the event was placed in, or none. An agent session's new
// prompt changes what its task's status carries, so it marks the task
// changed. Callers hold agentSessionMu.
func (o *taskObservation) recordHookActivity(
	ctx context.Context,
	activity *TaskActivityEvent,
	agentSessionID string,
	changes agentSessionChanges,
) error {
	if activity == nil {
		return nil
	}
	event := *activity
	event.AgentSessionID = agentSessionID
	if err := o.tasks.RecordTaskActivity(ctx, event); err != nil {
		return fmt.Errorf("record task activity: %w", err)
	}
	if agentSessionID != "" && event.Role == TaskActivityRoleUser {
		changes[event.TaskID] = true
	}
	return nil
}

func normalizeProviderHookStatusUpdate(update *TaskStatusUpdate, input HookEventInput) {
	if strings.TrimSpace(update.TaskID) == "" {
		update.TaskID = input.TaskID
	}
	if update.Provider == "" {
		update.Provider = input.Provider
	}
	if update.ObservedAt.IsZero() {
		update.ObservedAt = input.OccurredAt
	}
	update.TaskID = strings.TrimSpace(update.TaskID)
}

// taskStatusUpdatesEqual reports whether two updates say the same thing,
// including about every live agent session, so a side agent's change is
// published even when the task's own status stays the same.
func taskStatusUpdatesEqual(left *TaskStatusUpdate, right *TaskStatusUpdate) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.ObservedAt.Equal(right.ObservedAt) &&
		left.TaskID == right.TaskID &&
		left.RawEventName == right.RawEventName &&
		left.Provider == right.Provider &&
		left.Phase == right.Phase &&
		left.BackgroundWork == right.BackgroundWork &&
		left.LeadAgentSessionID == right.LeadAgentSessionID &&
		slices.EqualFunc(left.AgentSessions, right.AgentSessions, liveAgentSessionsEqual)
}

func liveAgentSessionsEqual(left LiveAgentSession, right LiveAgentSession) bool {
	sameLocation := left.Location == nil && right.Location == nil ||
		left.Location != nil && right.Location != nil && *left.Location == *right.Location
	return left.ID == right.ID &&
		left.Provider == right.Provider &&
		left.Pane == right.Pane &&
		sameLocation &&
		agentSessionStatusesEqual(left.Status, right.Status) &&
		left.LatestPrompt == right.LatestPrompt
}

func taskSessionCommandsMatch(activeCommand, expectedCommand string) bool {
	if activeCommand == expectedCommand {
		return true
	}
	return strings.HasPrefix(activeCommand, expectedCommand+"-")
}

func taskActivityEventFromHookInput(input HookEventInput) *TaskActivityEvent {
	event := TaskActivityEvent{
		TaskID:     strings.TrimSpace(input.TaskID),
		TurnID:     strings.TrimSpace(input.TurnID),
		EventName:  strings.TrimSpace(input.EventName),
		ObservedAt: input.OccurredAt,
	}

	switch event.EventName {
	case HookEventUserPromptSubmit:
		event.Role = TaskActivityRoleUser
		event.Text = compactActivityText(input.PromptText)
	case HookEventPostToolUse:
		event.Role = TaskActivityRoleAssistant
		event.Text = compactActivityText(input.CommandText)
	case HookEventStop:
		event.Role = TaskActivityRoleAssistant
		event.Text = compactActivityText(input.LastAssistantMessage)
	default:
		return nil
	}

	if event.TaskID == "" || event.Text == "" {
		return nil
	}

	return &event
}

func activityWindowWithLastUserPrompt(events []TaskActivityEvent, limit int) []TaskActivityEvent {
	if limit <= 0 || len(events) <= limit {
		return events
	}

	window := append([]TaskActivityEvent(nil), events[len(events)-limit:]...)

	lastUserIndex := -1
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Role == TaskActivityRoleUser && strings.TrimSpace(events[i].Text) != "" {
			lastUserIndex = i
			break
		}
	}
	if lastUserIndex < 0 {
		return window
	}

	lastUser := events[lastUserIndex]
	for _, event := range window {
		if event == lastUser {
			return window
		}
	}

	return append([]TaskActivityEvent{lastUser}, window...)
}

func latestTaskActivityObservedAt(events []TaskActivityEvent) time.Time {
	var latest time.Time
	for _, event := range events {
		if event.ObservedAt.After(latest) {
			latest = event.ObservedAt
		}
	}
	return latest
}

func mergeTaskActivity(stored []TaskActivityEvent, recovered []TaskActivityEvent) []TaskActivityEvent {
	merged := make([]TaskActivityEvent, 0, len(stored)+len(recovered))
	merged = append(merged, stored...)
	merged = append(merged, recovered...)
	sort.SliceStable(merged, func(i, j int) bool {
		left := merged[i]
		right := merged[j]
		if !left.ObservedAt.Equal(right.ObservedAt) {
			return left.ObservedAt.Before(right.ObservedAt)
		}
		if left.Role != right.Role {
			return left.Role < right.Role
		}
		if left.EventName != right.EventName {
			return left.EventName < right.EventName
		}
		return left.Text < right.Text
	})
	return merged
}

func latestProviderSessionsByID(sessions []TaskProviderSession) []TaskProviderSession {
	latestByKey := make(map[string]TaskProviderSession)
	for _, session := range sessions {
		provider := strings.TrimSpace(string(session.Provider))
		sessionID := strings.TrimSpace(session.ProviderSessionID)
		transcriptPath := strings.TrimSpace(session.TranscriptPath)
		if provider == "" || sessionID == "" || transcriptPath == "" {
			continue
		}

		session.Provider = Provider(provider)
		session.ProviderSessionID = sessionID
		session.TranscriptPath = transcriptPath
		key := provider + "\x00" + sessionID
		current, ok := latestByKey[key]
		if !ok || session.LastObservedAt.After(current.LastObservedAt) {
			latestByKey[key] = session
		}
	}

	latest := make([]TaskProviderSession, 0, len(latestByKey))
	for _, session := range latestByKey {
		latest = append(latest, session)
	}
	sort.SliceStable(latest, func(i, j int) bool {
		left := latest[i]
		right := latest[j]
		if !left.LastObservedAt.Equal(right.LastObservedAt) {
			return left.LastObservedAt.Before(right.LastObservedAt)
		}
		if left.Provider != right.Provider {
			return left.Provider < right.Provider
		}
		return left.ProviderSessionID < right.ProviderSessionID
	})
	return latest
}

func compactActivityText(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}
