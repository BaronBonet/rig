package core

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var taskStatusRecoveryPollInterval = 2 * time.Second

func getTaskActivity(ctx context.Context, service *taskService, taskID string, limit int) ([]TaskActivityEvent, error) {
	taskID = strings.TrimSpace(taskID)
	events, err := service.tasks.GetTaskActivity(ctx, taskID, 0)
	if err != nil {
		return nil, err
	}
	events = mergeTaskActivity(events, recoveredTaskActivity(ctx, service, taskID, events))

	if limit <= 0 {
		return events, nil
	}

	return activityWindowWithLastUserPrompt(events, limit), nil
}

func getTaskTokenUsage(ctx context.Context, service *taskService, taskID string) (*TaskTokenUsage, error) {
	sessions, err := service.tasks.ListTaskProviderSessions(ctx, strings.TrimSpace(taskID))
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
		providerClient, err := service.providerClientFor(session.Provider)
		if err != nil {
			return nil, err
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

func latestTaskStatus(ctx context.Context, service *taskService, taskID string) (*TaskStatusUpdate, error) {
	update, err := service.tasks.LatestTaskStatus(ctx, strings.TrimSpace(taskID))
	if err != nil || update == nil {
		return update, err
	}

	return currentTaskStatus(ctx, service, update), nil
}

func subscribeTaskStatus(
	ctx context.Context,
	service *taskService,
	taskID string,
) (<-chan TaskStatusUpdate, error) {
	taskID = strings.TrimSpace(taskID)
	updates, err := service.tasks.SubscribeTaskStatus(ctx, taskID)
	if err != nil {
		return nil, err
	}

	return subscribeTaskStatusWithRecovery(ctx, service, taskID, updates, taskStatusRecoveryPollInterval), nil
}

func handleProviderHookEvent(ctx context.Context, service *taskService, input HookEventInput) error {
	if input.Provider == "" {
		return ErrUnmanagedHookEvent
	}

	providerClient, err := service.providerClientFor(input.Provider)
	if err != nil {
		return err
	}

	input.TaskID = strings.TrimSpace(input.TaskID)
	if input.TaskID == "" {
		resolvedTaskID, err := service.resolveTaskIDFromCwd(ctx, input.Cwd)
		if err != nil {
			return err
		}
		input.TaskID = resolvedTaskID
	}

	if err := recordProviderHookSession(ctx, service, input); err != nil {
		return err
	}
	if err := recordProviderHookActivity(ctx, service, input); err != nil {
		return err
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

	return service.tasks.UpsertTaskStatus(ctx, *update)
}

func subscribeTaskStatusWithRecovery(
	ctx context.Context,
	service *taskService,
	taskID string,
	updates <-chan TaskStatusUpdate,
	pollInterval time.Duration,
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

		if !send(latestTaskStatusNoSubscribe(ctx, service, taskID)) {
			return
		}

		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case update, ok := <-updates:
				if !ok {
					return
				}
				current := currentTaskStatus(ctx, service, &update)
				if !send(current) {
					return
				}
			case <-ticker.C:
				if !send(latestTaskStatusNoSubscribe(ctx, service, taskID)) {
					return
				}
			}
		}
	}()

	return recovered
}

func latestTaskStatusNoSubscribe(ctx context.Context, service *taskService, taskID string) *TaskStatusUpdate {
	update, err := service.tasks.LatestTaskStatus(ctx, taskID)
	if err != nil || update == nil {
		return nil
	}
	return currentTaskStatus(ctx, service, update)
}

func currentTaskStatus(ctx context.Context, service *taskService, update *TaskStatusUpdate) *TaskStatusUpdate {
	if update == nil || update.Phase == TaskStatusPhaseStopped {
		return update
	}

	task, err := service.taskByID(ctx, update.TaskID)
	if err != nil {
		return update
	}

	runtime, err := service.tmuxSession.InspectTaskSession(ctx, task)
	if err != nil {
		return update
	}

	providerClient, err := service.providerClientFor(task.Provider)
	if err != nil {
		return update
	}

	if taskSessionRunningProvider(runtime, providerClient.TaskSessionCommandName()) {
		recovered := recoveredTaskStatus(ctx, service, providerClient, update)
		if recovered != nil {
			return recovered
		}
		return update
	}

	stopped := *update
	stopped.Phase = TaskStatusPhaseStopped
	stopped.RawEventName = "TaskSessionStopped"
	return &stopped
}

func recoveredTaskStatus(
	ctx context.Context,
	service *taskService,
	providerClient ProviderClient,
	update *TaskStatusUpdate,
) *TaskStatusUpdate {
	if update.Phase == TaskStatusPhaseStopped {
		return nil
	}

	sessions, err := service.tasks.ListTaskProviderSessions(ctx, update.TaskID)
	if err != nil {
		return nil
	}
	recovered, err := providerClient.RecoverLatestTaskStatus(ctx, *update, sessions)
	if err != nil {
		return nil
	}
	return recovered
}

func recoveredTaskActivity(
	ctx context.Context,
	service *taskService,
	taskID string,
	events []TaskActivityEvent,
) []TaskActivityEvent {
	sessions, err := service.tasks.ListTaskProviderSessions(ctx, taskID)
	if err != nil {
		return nil
	}

	after := latestTaskActivityObservedAt(events)
	var recovered []TaskActivityEvent
	for _, session := range latestProviderSessionsByID(sessions) {
		providerClient, err := service.providerClientFor(session.Provider)
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

func recordProviderHookSession(ctx context.Context, service *taskService, input HookEventInput) error {
	input.SessionID = strings.TrimSpace(input.SessionID)
	if input.SessionID == "" {
		return nil
	}

	observedAt := input.OccurredAt
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	if err := service.tasks.UpsertTaskProviderSession(ctx, TaskProviderSession{
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
	if err := service.tasks.UpsertTaskResumeMetadata(ctx, TaskResumeMetadata{
		TaskID:     input.TaskID,
		Provider:   input.Provider,
		SessionID:  input.SessionID,
		ObservedAt: input.OccurredAt,
	}); err != nil {
		return fmt.Errorf("upsert task resume metadata: %w", err)
	}
	return nil
}

func recordProviderHookActivity(ctx context.Context, service *taskService, input HookEventInput) error {
	activity := taskActivityEventFromHookInput(input)
	if activity == nil {
		return nil
	}
	if err := service.tasks.RecordTaskActivity(ctx, *activity); err != nil {
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
	case "UserPromptSubmit":
		event.Role = TaskActivityRoleUser
		event.Text = compactActivityText(input.PromptText)
	case "PostToolUse":
		event.Role = TaskActivityRoleAssistant
		event.Text = compactActivityText(input.CommandText)
	case "Stop":
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
