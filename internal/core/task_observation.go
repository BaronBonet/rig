package core

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const defaultTaskStatusRecoveryPollInterval = 2 * time.Second

// taskObservation is the Task observation module: everything Rig derives
// from provider hook events and provider session history lives here — a
// task's runtime status (including read-time derivation against the live
// tmux session and provider-side recovery of stale status), adoption of
// manually launched provider sessions, persisted activity, and token usage.
type taskObservation struct {
	tasks          TaskRepository
	tmuxSession    TmuxSessionClient
	providers      map[Provider]ProviderClient
	providerConfig ProviderConfigStore
	// recoveryPollInterval paces the subscription recovery loop that
	// re-derives status while a subscriber is attached.
	recoveryPollInterval time.Duration
}

func newTaskObservation(
	tasks TaskRepository,
	tmuxSession TmuxSessionClient,
	providers map[Provider]ProviderClient,
	providerConfig ProviderConfigStore,
) *taskObservation {
	return &taskObservation{
		tasks:                tasks,
		tmuxSession:          tmuxSession,
		providers:            providers,
		providerConfig:       providerConfig,
		recoveryPollInterval: defaultTaskStatusRecoveryPollInterval,
	}
}

// supportedProviderClient returns the adapter client for a supported provider
// without requiring the provider to be configured. Use it for read-side
// behavior that must keep working for tasks whose active provider is no
// longer configured.
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

// recordActiveProvider persists a new active provider on the task record. It
// runs only after the new provider is known to own the task session, so a
// failed switch or adoption never changes the recorded active provider.
func recordActiveProvider(
	ctx context.Context,
	tasks TaskRepository,
	task *Task,
	provider Provider,
) (*Task, error) {
	task.Provider = provider
	task.UpdatedAt = time.Now().UTC()
	if err := tasks.UpdateTask(ctx, task); err != nil {
		return nil, fmt.Errorf("record active provider: %w", err)
	}

	// A task's runtime status is driven only by its active provider, so a
	// persisted status row left behind by the previous provider is re-stamped.
	// A durable status/record provider mismatch would otherwise put every TUI
	// session into a permanent reload loop until the new provider's first hook
	// event happened to overwrite the row.
	update, err := tasks.LatestTaskStatus(ctx, task.ID)
	if err != nil {
		return nil, fmt.Errorf("record active provider: read latest status: %w", err)
	}
	if update != nil && update.Provider != provider {
		update.Provider = provider
		if err := tasks.UpsertTaskStatus(ctx, *update); err != nil {
			return nil, fmt.Errorf("record active provider: re-stamp status: %w", err)
		}
	}
	return task, nil
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
	update, err := o.tasks.LatestTaskStatus(ctx, strings.TrimSpace(taskID))
	if err != nil || update == nil {
		return update, err
	}

	return o.currentStatus(ctx, update), nil
}

func (o *taskObservation) SubscribeTaskStatus(
	ctx context.Context,
	taskID string,
) (<-chan TaskStatusUpdate, error) {
	taskID = strings.TrimSpace(taskID)
	updates, err := o.tasks.SubscribeTaskStatus(ctx, taskID)
	if err != nil {
		return nil, err
	}

	return o.subscribeWithRecovery(ctx, taskID, updates), nil
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

	drivesRuntimeStatus := true
	if task, taskErr := taskByID(ctx, o.tasks, input.TaskID); taskErr == nil && task.Provider != input.Provider {
		if isProviderAdoptionEvent(input) {
			// Manual provider adoption: a configured provider started a session in
			// this task's workspace, so it becomes the active provider immediately.
			// Rig's tmux session reference is intentionally left untouched.
			if _, adoptErr := recordActiveProvider(ctx, o.tasks, task, input.Provider); adoptErr != nil {
				return adoptErr
			}
		} else {
			// Late hooks from a provider that is no longer active may still record
			// session history and activity, but never drive current runtime status.
			drivesRuntimeStatus = false
		}
	}

	if err := o.recordHookSession(ctx, input); err != nil {
		return err
	}
	if err := o.recordHookActivity(ctx, input); err != nil {
		return err
	}
	if !drivesRuntimeStatus {
		return nil
	}

	update, err := providerClient.HookEventToTaskStatus(input)
	if err != nil {
		return err
	}
	if update == nil {
		return nil
	}
	normalizeProviderHookStatusUpdate(update, input)
	if update.TaskID == "" {
		return fmt.Errorf("task status update task ID is required")
	}

	return o.tasks.UpsertTaskStatus(ctx, *update)
}

// isProviderAdoptionEvent reports whether a hook event may adopt a manually
// launched provider as the task's active provider. Only session-start events
// qualify so that late or stray hooks cannot change the active provider.
func isProviderAdoptionEvent(input HookEventInput) bool {
	return strings.TrimSpace(input.EventName) == HookEventSessionStart
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

func (o *taskObservation) subscribeWithRecovery(
	ctx context.Context,
	taskID string,
	updates <-chan TaskStatusUpdate,
) <-chan TaskStatusUpdate {
	recovered := make(chan TaskStatusUpdate, 8)

	go func() {
		defer close(recovered)

		var lastSent *TaskStatusUpdate
		send := func(update *TaskStatusUpdate) bool {
			if update == nil || taskStatusUpdatesEqual(lastSent, update) {
				return true
			}
			copy := *update
			select {
			case recovered <- copy:
				lastSent = &copy
				return true
			case <-ctx.Done():
				return false
			}
		}

		if !send(o.latestStatusNoSubscribe(ctx, taskID)) {
			return
		}

		ticker := time.NewTicker(o.recoveryPollInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case update, ok := <-updates:
				if !ok {
					return
				}
				current := o.currentStatus(ctx, &update)
				if !send(current) {
					return
				}
			case <-ticker.C:
				if !send(o.latestStatusNoSubscribe(ctx, taskID)) {
					return
				}
			}
		}
	}()

	return recovered
}

func (o *taskObservation) latestStatusNoSubscribe(ctx context.Context, taskID string) *TaskStatusUpdate {
	update, err := o.tasks.LatestTaskStatus(ctx, taskID)
	if err != nil || update == nil {
		return nil
	}
	return o.currentStatus(ctx, update)
}

// statusResolution is the pure outcome of the runtime-status decision: what a
// persisted status should become given the live session state.
type statusResolution int

const (
	// statusKeep: the persisted phase stands.
	statusKeep statusResolution = iota
	// statusStopped: the session or provider process is gone; the task reads
	// as stopped regardless of the persisted phase.
	statusStopped
	// statusTryRecover: the provider is running, so provider-side state may
	// hold a newer observation than the persisted one.
	statusTryRecover
)

// resolveStatus is the runtime-status decision table, kept free of I/O so
// every row is a value-in/value-out test. The persisted status row is not
// authoritative: it is one input alongside the live tmux runtime state and
// the active provider's expected session command.
func resolveStatus(
	update *TaskStatusUpdate,
	runtime TaskSessionRuntimeState,
	providerCommand string,
) statusResolution {
	if update == nil || update.Phase == TaskStatusPhaseStopped {
		return statusKeep
	}
	if taskSessionRunningProvider(runtime, providerCommand) {
		return statusTryRecover
	}
	return statusStopped
}

// currentStatus gathers the live inputs for one persisted status, asks
// resolveStatus what to do, and executes only that action. Gathering failures
// keep the persisted status: observation must degrade, never invent state.
func (o *taskObservation) currentStatus(ctx context.Context, update *TaskStatusUpdate) *TaskStatusUpdate {
	if update == nil || update.Phase == TaskStatusPhaseStopped {
		return update
	}

	task, err := taskByID(ctx, o.tasks, update.TaskID)
	if err != nil {
		return update
	}

	runtime, err := o.tmuxSession.InspectTaskSession(ctx, task)
	if err != nil {
		return update
	}

	providerClient, err := supportedProviderClient(o.providers, task.Provider)
	if err != nil {
		return update
	}

	switch resolveStatus(update, runtime, providerClient.TaskSessionCommandName()) {
	case statusTryRecover:
		if recovered := o.recoveredStatus(ctx, providerClient, update); recovered != nil {
			return recovered
		}
		return update
	case statusStopped:
		stopped := *update
		stopped.Phase = TaskStatusPhaseStopped
		stopped.RawEventName = "TaskSessionStopped"
		return &stopped
	default:
		return update
	}
}

func (o *taskObservation) recoveredStatus(
	ctx context.Context,
	providerClient ProviderClient,
	update *TaskStatusUpdate,
) *TaskStatusUpdate {
	if update.Phase == TaskStatusPhaseStopped {
		return nil
	}

	sessions, err := o.tasks.ListTaskProviderSessions(ctx, update.TaskID)
	if err != nil {
		return nil
	}
	recovered, err := providerClient.RecoverLatestTaskStatus(ctx, *update, sessions)
	if err != nil {
		return nil
	}
	return recovered
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
	if err := o.tasks.UpsertTaskResumeMetadata(ctx, TaskResumeMetadata{
		TaskID:     input.TaskID,
		Provider:   input.Provider,
		SessionID:  input.SessionID,
		ObservedAt: input.OccurredAt,
	}); err != nil {
		return fmt.Errorf("upsert task resume metadata: %w", err)
	}
	return nil
}

func (o *taskObservation) recordHookActivity(ctx context.Context, input HookEventInput) error {
	activity := taskActivityEventFromHookInput(input)
	if activity == nil {
		return nil
	}
	if err := o.tasks.RecordTaskActivity(ctx, *activity); err != nil {
		return fmt.Errorf("record task activity: %w", err)
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

func taskStatusUpdatesEqual(left *TaskStatusUpdate, right *TaskStatusUpdate) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func taskSessionRunningProvider(runtime TaskSessionRuntimeState, commandName string) bool {
	if !runtime.Exists {
		return false
	}

	expectedCommand := filepath.Base(strings.TrimSpace(commandName))
	if expectedCommand == "" {
		return false
	}

	for _, command := range runtime.ActiveCommands {
		activeCommand := filepath.Base(strings.TrimSpace(command))
		if taskSessionCommandsMatch(activeCommand, expectedCommand) {
			return true
		}
	}

	return false
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
