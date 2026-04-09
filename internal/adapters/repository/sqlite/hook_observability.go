package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"agent/internal/core"
)

const hookPreviewMaxLen = 240

type hookRecord struct {
	OccurredAt           time.Time
	TaskID               string
	SessionID            string
	TurnID               string
	EventName            string
	RawPayloadJSON       string
	LastAssistantMessage string
	PromptText           string
	CommandText          string
	CommandResultText    string
	ToolUseID            string
	Model                string
	Cwd                  string
	TranscriptPath       string
	StartSource          string
}

type hookSubscriber struct {
	ch     chan core.HookSessionSummary
	mu     sync.RWMutex
	closed bool
}

type observerSubscriber struct {
	ch     chan core.ObserverTaskUpdate
	mu     sync.RWMutex
	closed bool
}

func newHookSubscriber(buffer int) *hookSubscriber {
	if buffer < 0 {
		buffer = 0
	}

	return &hookSubscriber{
		ch: make(chan core.HookSessionSummary, buffer),
	}
}

func (s *hookSubscriber) publish(summary core.HookSessionSummary) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return false
	}

	select {
	case s.ch <- summary:
		return true
	default:
		return false
	}
}

func (s *hookSubscriber) close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return
	}

	s.closed = true
	close(s.ch)
}

func newObserverSubscriber(buffer int) *observerSubscriber {
	if buffer < 0 {
		buffer = 0
	}

	return &observerSubscriber{
		ch: make(chan core.ObserverTaskUpdate, buffer),
	}
}

func (s *observerSubscriber) publish(update core.ObserverTaskUpdate) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return false
	}

	select {
	case s.ch <- update:
		return true
	default:
		return false
	}
}

func (s *observerSubscriber) close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return
	}

	s.closed = true
	close(s.ch)
}

func (r *Repository) IngestHookEvent(ctx context.Context, raw core.HookEventInput) (*core.HookSessionSummary, error) {
	if err := r.unavailableErr(); err != nil {
		return nil, err
	}

	taskID, err := r.resolveHookTaskID(ctx, raw)
	if err != nil {
		return nil, err
	}

	record := hookRecord{
		OccurredAt:           raw.OccurredAt.UTC(),
		TaskID:               taskID,
		SessionID:            strings.TrimSpace(raw.SessionID),
		TurnID:               strings.TrimSpace(raw.TurnID),
		EventName:            strings.TrimSpace(raw.EventName),
		RawPayloadJSON:       strings.TrimSpace(raw.RawPayloadJSON),
		LastAssistantMessage: strings.TrimSpace(raw.LastAssistantMessage),
		PromptText:           strings.TrimSpace(raw.PromptText),
		CommandText:          strings.TrimSpace(raw.CommandText),
		CommandResultText:    strings.TrimSpace(raw.CommandResultText),
		ToolUseID:            strings.TrimSpace(raw.ToolUseID),
		Model:                strings.TrimSpace(raw.Model),
		Cwd:                  strings.TrimSpace(raw.Cwd),
		TranscriptPath:       strings.TrimSpace(raw.TranscriptPath),
		StartSource:          strings.TrimSpace(raw.StartSource),
	}
	if record.OccurredAt.IsZero() {
		record.OccurredAt = time.Now().UTC()
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	previous, err := loadHookSessionSummary(ctx, tx, taskID)
	if err != nil {
		return nil, err
	}
	previousObserver, err := loadObserverSummary(ctx, tx, taskID)
	if err != nil {
		return nil, err
	}
	if err := insertHookEvent(ctx, tx, record); err != nil {
		return nil, err
	}

	next := deriveHookSessionSummary(previous, record)
	next.TaskID = taskID
	nextObserver := deriveObserverSummary(previousObserver, next)
	if err := upsertHookSessionSummary(ctx, tx, next); err != nil {
		return nil, err
	}
	if err := upsertObserverSummary(ctx, tx, nextObserver); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	r.publishHookSessionUpdate(*next)
	r.publishObserverTaskUpdate(observerTaskUpdateFromSummary(nextObserver))
	return next, nil
}

func (r *Repository) ListHookSessionSummaries(ctx context.Context, taskIDs []string) (map[string]*core.HookSessionSummary, error) {
	if err := r.unavailableErr(); err != nil {
		return nil, err
	}

	query := `select
		task_id, session_id, model, cwd, transcript_path, start_source,
		current_turn_id, last_event_name, runtime_phase, started_at,
		last_activity_at, last_stop_at, last_prompt_preview,
		last_command_preview, last_command_result_preview,
		last_assistant_message, command_count
	from task_hook_sessions`
	args := make([]any, 0, len(taskIDs))
	if len(taskIDs) > 0 {
		query += ` where task_id in (` + placeholders(len(taskIDs)) + `)`
		for _, taskID := range taskIDs {
			args = append(args, taskID)
		}
	}
	query += ` order by task_id asc`

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	summaries := make(map[string]*core.HookSessionSummary)
	for rows.Next() {
		summary, scanErr := scanHookSessionSummary(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		summaries[summary.TaskID] = summary
	}

	return summaries, rows.Err()
}

func (r *Repository) ListHookEvents(ctx context.Context, taskID string, limit int) ([]core.HookEvent, error) {
	if err := r.unavailableErr(); err != nil {
		return nil, err
	}

	args := []any{taskID}
	query := `select
		id, task_id, session_id, turn_id, event_name, occurred_at,
		raw_payload_json, last_assistant_message, prompt_preview,
		command_preview, command_result_preview, tool_use_id
	from task_hook_events
	where task_id = ?
	order by occurred_at desc, id desc`
	if limit > 0 {
		query += ` limit ?`
		args = append(args, limit)
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]core.HookEvent, 0)
	for rows.Next() {
		event, scanErr := scanHookEvent(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		events = append(events, event)
	}

	return events, rows.Err()
}

func (r *Repository) SubscribeHookSessionUpdates(ctx context.Context) (<-chan core.HookSessionSummary, func(), error) {
	if err := r.unavailableErr(); err != nil {
		return nil, nil, err
	}

	subscriber := newHookSubscriber(16)

	r.mu.Lock()
	if r.hookSubscribers == nil {
		r.hookSubscribers = make(map[int]*hookSubscriber)
	}
	id := r.nextHookSubscriberID
	r.nextHookSubscriberID++
	r.hookSubscribers[id] = subscriber
	r.mu.Unlock()

	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			r.mu.Lock()
			current, ok := r.hookSubscribers[id]
			if ok {
				delete(r.hookSubscribers, id)
			}
			r.mu.Unlock()
			if ok {
				current.close()
			}
		})
	}

	if done := ctx.Done(); done != nil {
		go func() {
			<-done
			cleanup()
		}()
	}

	return subscriber.ch, cleanup, nil
}

func (r *Repository) SubscribeObserverTaskUpdates(ctx context.Context) (<-chan core.ObserverTaskUpdate, func(), error) {
	if err := r.unavailableErr(); err != nil {
		return nil, nil, err
	}

	subscriber := newObserverSubscriber(16)

	r.mu.Lock()
	id := r.nextObserverSubscriberID
	r.nextObserverSubscriberID++
	r.observerSubscribers[id] = subscriber
	r.mu.Unlock()

	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			r.mu.Lock()
			current, ok := r.observerSubscribers[id]
			if ok {
				delete(r.observerSubscribers, id)
			}
			r.mu.Unlock()
			if ok {
				current.close()
			}
		})
	}

	if ctx != nil && ctx.Done() != nil {
		go func() {
			<-ctx.Done()
			cleanup()
		}()
	}

	return subscriber.ch, cleanup, nil
}

func (r *Repository) resolveHookTaskID(ctx context.Context, raw core.HookEventInput) (string, error) {
	lookup := []struct {
		value string
		query string
		args  []any
	}{
		{
			value: raw.TaskID,
			query: `select id from tasks where id = ? limit 1`,
			args:  []any{raw.TaskID},
		},
		{
			value: raw.Cwd,
			query: `select id from tasks where worktree_path = ? order by created_at desc limit 1`,
			args:  []any{raw.Cwd},
		},
		{
			value: raw.SessionID,
			query: `select task_id from task_hook_sessions where session_id = ? limit 1`,
			args:  []any{raw.SessionID},
		},
	}

	for _, candidate := range lookup {
		if strings.TrimSpace(candidate.value) == "" {
			continue
		}
		taskID, err := queryHookTaskID(ctx, r.db, candidate.query, candidate.args...)
		if err == nil {
			return taskID, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
	}

	return "", fmt.Errorf("%w: map hook event to managed task", core.ErrUnmanagedHookEvent)
}

func queryHookTaskID(ctx context.Context, db queryRower, query string, args ...any) (string, error) {
	var taskID string
	if err := db.QueryRowContext(ctx, query, args...).Scan(&taskID); err != nil {
		return "", err
	}
	return taskID, nil
}

type queryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func loadHookSessionSummary(ctx context.Context, tx *sql.Tx, taskID string) (*core.HookSessionSummary, error) {
	row := tx.QueryRowContext(ctx, `select
		task_id, session_id, model, cwd, transcript_path, start_source,
		current_turn_id, last_event_name, runtime_phase, started_at,
		last_activity_at, last_stop_at, last_prompt_preview,
		last_command_preview, last_command_result_preview,
		last_assistant_message, command_count
	from task_hook_sessions
	where task_id = ?`, taskID)

	summary, err := scanHookSessionSummary(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return summary, err
}

func loadObserverSummary(ctx context.Context, tx *sql.Tx, taskID string) (*core.ObserverSummary, error) {
	row := tx.QueryRowContext(ctx, `select task_id, display_status, display_activity, process_alive, last_runtime_observed_at
from task_observer_summaries
where task_id = ?`, taskID)

	summary, err := scanObserverSummary(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return summary, err
}

func insertHookEvent(ctx context.Context, tx *sql.Tx, record hookRecord) error {
	_, err := tx.ExecContext(ctx, `insert into task_hook_events (
		task_id, session_id, turn_id, event_name, occurred_at,
		raw_payload_json, last_assistant_message, prompt_preview,
		command_preview, command_result_preview, tool_use_id
	) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.TaskID,
		record.SessionID,
		record.TurnID,
		record.EventName,
		formatTime(record.OccurredAt),
		record.RawPayloadJSON,
		trimPreview(record.LastAssistantMessage),
		trimPreview(record.PromptText),
		trimPreview(record.CommandText),
		trimPreview(record.CommandResultText),
		record.ToolUseID,
	)
	return err
}

func upsertHookSessionSummary(ctx context.Context, tx *sql.Tx, summary *core.HookSessionSummary) error {
	_, err := tx.ExecContext(ctx, `insert into task_hook_sessions (
		task_id, session_id, model, cwd, transcript_path, start_source,
		current_turn_id, last_event_name, runtime_phase, started_at,
		last_activity_at, last_stop_at, last_prompt_preview,
		last_command_preview, last_command_result_preview,
		last_assistant_message, command_count, updated_at
	) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	on conflict(task_id) do update set
		session_id = excluded.session_id,
		model = excluded.model,
		cwd = excluded.cwd,
		transcript_path = excluded.transcript_path,
		start_source = excluded.start_source,
		current_turn_id = excluded.current_turn_id,
		last_event_name = excluded.last_event_name,
		runtime_phase = excluded.runtime_phase,
		started_at = excluded.started_at,
		last_activity_at = excluded.last_activity_at,
		last_stop_at = excluded.last_stop_at,
		last_prompt_preview = excluded.last_prompt_preview,
		last_command_preview = excluded.last_command_preview,
		last_command_result_preview = excluded.last_command_result_preview,
		last_assistant_message = excluded.last_assistant_message,
		command_count = excluded.command_count,
		updated_at = excluded.updated_at`,
		summary.TaskID,
		summary.SessionID,
		summary.Model,
		summary.Cwd,
		summary.TranscriptPath,
		summary.StartSource,
		summary.CurrentTurnID,
		summary.LastEventName,
		string(summary.RuntimePhase),
		formatTime(summary.StartedAt),
		formatTime(summary.LastActivityAt),
		formatTime(summary.LastStopAt),
		summary.LastPromptText,
		summary.LastCommandText,
		summary.LastCommandResultText,
		summary.LastAssistantMessage,
		summary.CommandCount,
		formatTime(summary.LastActivityAt),
	)
	return err
}

func upsertObserverSummary(ctx context.Context, tx *sql.Tx, summary *core.ObserverSummary) error {
	if summary == nil {
		return nil
	}

	_, err := tx.ExecContext(ctx, `insert into task_observer_summaries (
		task_id, display_status, display_activity, process_alive,
		last_runtime_observed_at, updated_at
	) values (?, ?, ?, ?, ?, ?)
	on conflict(task_id) do update set
		display_status = excluded.display_status,
		display_activity = excluded.display_activity,
		process_alive = excluded.process_alive,
		last_runtime_observed_at = excluded.last_runtime_observed_at,
		updated_at = excluded.updated_at`,
		summary.TaskID,
		string(summary.DisplayStatus),
		string(summary.DisplayActivity),
		boolToInt(summary.ProcessAlive),
		formatTime(summary.LastRuntimeObservedAt),
		formatTime(time.Now().UTC()),
	)
	return err
}

func scanHookSessionSummary(scanner rowScanner) (*core.HookSessionSummary, error) {
	var (
		summary        core.HookSessionSummary
		runtimePhase   string
		startedAt      string
		lastActivityAt string
		lastStopAt     string
	)

	err := scanner.Scan(
		&summary.TaskID,
		&summary.SessionID,
		&summary.Model,
		&summary.Cwd,
		&summary.TranscriptPath,
		&summary.StartSource,
		&summary.CurrentTurnID,
		&summary.LastEventName,
		&runtimePhase,
		&startedAt,
		&lastActivityAt,
		&lastStopAt,
		&summary.LastPromptText,
		&summary.LastCommandText,
		&summary.LastCommandResultText,
		&summary.LastAssistantMessage,
		&summary.CommandCount,
	)
	if err != nil {
		return nil, err
	}

	summary.RuntimePhase = core.HookRuntimePhase(runtimePhase)
	summary.StartedAt = parseTime(startedAt)
	summary.LastActivityAt = parseTime(lastActivityAt)
	summary.LastStopAt = parseTime(lastStopAt)
	return &summary, nil
}

func scanHookEvent(scanner rowScanner) (core.HookEvent, error) {
	var (
		event      core.HookEvent
		occurredAt string
	)

	err := scanner.Scan(
		&event.ID,
		&event.TaskID,
		&event.SessionID,
		&event.TurnID,
		&event.EventName,
		&occurredAt,
		&event.RawPayloadJSON,
		&event.LastAssistantMessage,
		&event.PromptText,
		&event.CommandText,
		&event.CommandResultText,
		&event.ToolUseID,
	)
	if err != nil {
		return core.HookEvent{}, err
	}

	event.OccurredAt = parseTime(occurredAt)
	return event, nil
}

func deriveHookSessionSummary(previous *core.HookSessionSummary, event hookRecord) *core.HookSessionSummary {
	next := cloneHookSessionSummary(previous)
	if event.EventName == "SessionStart" && previous != nil && previous.SessionID != "" &&
		event.SessionID != "" && previous.SessionID != event.SessionID {
		next = &core.HookSessionSummary{TaskID: previous.TaskID}
	}

	next.TaskID = firstNonEmpty(next.TaskID, event.TaskID)
	if event.SessionID != "" {
		next.SessionID = event.SessionID
	}
	if event.Model != "" {
		next.Model = event.Model
	}
	if event.Cwd != "" {
		next.Cwd = event.Cwd
	}
	if event.TranscriptPath != "" {
		next.TranscriptPath = event.TranscriptPath
	}
	next.LastEventName = event.EventName
	next.LastActivityAt = event.OccurredAt
	if event.LastAssistantMessage != "" {
		next.LastAssistantMessage = trimPreview(event.LastAssistantMessage)
	}

	switch event.EventName {
	case "SessionStart":
		if event.StartSource != "" {
			next.StartSource = event.StartSource
		}
		next.StartedAt = firstNonZeroTime(next.StartedAt, event.OccurredAt)
		next.CurrentTurnID = ""
		next.LastPromptText = ""
		next.LastCommandText = ""
		next.LastCommandResultText = ""
		next.LastAssistantMessage = ""
		next.CommandCount = 0
		next.RuntimePhase = core.HookRuntimePhaseReady
	case "UserPromptSubmit":
		next.CurrentTurnID = firstNonEmpty(event.TurnID, next.CurrentTurnID)
		next.LastPromptText = trimPreview(event.PromptText)
		next.RuntimePhase = core.HookRuntimePhasePrompted
	case "PreToolUse":
		next.CurrentTurnID = firstNonEmpty(event.TurnID, next.CurrentTurnID)
		next.LastCommandText = trimPreview(event.CommandText)
		next.RuntimePhase = core.HookRuntimePhaseRunningCommand
		next.CommandCount++
	case "PostToolUse":
		next.CurrentTurnID = firstNonEmpty(event.TurnID, next.CurrentTurnID)
		if event.CommandText != "" {
			next.LastCommandText = trimPreview(event.CommandText)
		}
		next.LastCommandResultText = trimPreview(event.CommandResultText)
		next.RuntimePhase = core.HookRuntimePhaseIdle
	case "Stop":
		next.CurrentTurnID = firstNonEmpty(event.TurnID, next.CurrentTurnID)
		next.LastStopAt = event.OccurredAt
		next.RuntimePhase = core.HookRuntimePhaseIdle
	default:
		if next.RuntimePhase == "" {
			next.RuntimePhase = core.HookRuntimePhaseReady
		}
	}

	return next
}

func deriveObserverSummary(previous *core.ObserverSummary, hookSession *core.HookSessionSummary) *core.ObserverSummary {
	next := cloneObserverSummary(previous)
	if hookSession != nil {
		next.TaskID = firstNonEmpty(next.TaskID, hookSession.TaskID)
	}

	if hookSession != nil && hookSession.RuntimePhase == core.HookRuntimePhaseRunningCommand {
		next.DisplayActivity = core.DisplayActivityCommand
		return next
	}

	next.DisplayActivity = core.DisplayActivityNone
	return next
}

func cloneObserverSummary(summary *core.ObserverSummary) *core.ObserverSummary {
	if summary == nil {
		return &core.ObserverSummary{}
	}

	clone := *summary
	return &clone
}

func cloneHookSessionSummary(summary *core.HookSessionSummary) *core.HookSessionSummary {
	if summary == nil {
		return &core.HookSessionSummary{}
	}

	clone := *summary
	return &clone
}

func trimPreview(value string) string {
	compacted := strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if compacted == "" {
		return ""
	}

	runes := []rune(compacted)
	if len(runes) <= hookPreviewMaxLen {
		return compacted
	}
	if hookPreviewMaxLen <= 3 {
		return string(runes[:hookPreviewMaxLen])
	}

	return string(runes[:hookPreviewMaxLen-3]) + "..."
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstNonZeroTime(current, candidate time.Time) time.Time {
	if !current.IsZero() {
		return current
	}
	return candidate
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}

	var builder strings.Builder
	for i := 0; i < n; i++ {
		if i > 0 {
			builder.WriteString(", ")
		}
		builder.WriteString("?")
	}
	return builder.String()
}

func (r *Repository) publishHookSessionUpdate(summary core.HookSessionSummary) {
	r.mu.Lock()
	subscribers := make([]*hookSubscriber, 0, len(r.hookSubscribers))
	for _, subscriber := range r.hookSubscribers {
		subscribers = append(subscribers, subscriber)
	}
	r.mu.Unlock()

	for _, subscriber := range subscribers {
		subscriber.publish(summary)
	}
}

func (r *Repository) publishObserverTaskUpdate(update core.ObserverTaskUpdate) {
	if r == nil || strings.TrimSpace(update.TaskID) == "" {
		return
	}

	r.mu.Lock()
	subscribers := make([]*observerSubscriber, 0, len(r.observerSubscribers))
	for _, subscriber := range r.observerSubscribers {
		subscribers = append(subscribers, subscriber)
	}
	r.mu.Unlock()

	for _, subscriber := range subscribers {
		subscriber.publish(update)
	}
}

func observerTaskUpdateFromSummary(summary *core.ObserverSummary) core.ObserverTaskUpdate {
	if summary == nil {
		return core.ObserverTaskUpdate{}
	}

	return core.ObserverTaskUpdate{
		TaskID:          summary.TaskID,
		DisplayStatus:   summary.DisplayStatus,
		DisplayActivity: summary.DisplayActivity,
		LastActivityAt:  summary.LastRuntimeObservedAt,
	}
}
