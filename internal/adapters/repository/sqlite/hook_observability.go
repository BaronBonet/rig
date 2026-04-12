package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"rig/internal/adapters/repository/sqlite/generated"
	"rig/internal/core"
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

type hookTaskMatchKind int

const (
	hookTaskMatchUnknown hookTaskMatchKind = iota
	hookTaskMatchTaskID
	hookTaskMatchWorktree
	hookTaskMatchSessionID
)

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

	taskID, matchKind, err := r.resolveHookTaskID(ctx, raw)
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

	qtx := r.queries.WithTx(tx)

	previous, err := loadHookSessionSummary(ctx, qtx, taskID)
	if err != nil {
		return nil, err
	}
	if shouldIgnoreForeignSessionEvent(previous, record, matchKind) {
		return previous, nil
	}
	previousObserver, err := loadObserverSummary(ctx, qtx, taskID)
	if err != nil {
		return nil, err
	}
	if err := qtx.InsertHookEvent(ctx, hookEventParamsFromRecord(record)); err != nil {
		return nil, err
	}

	next := deriveHookSessionSummary(previous, record)
	next.TaskID = taskID
	nextObserver := deriveObserverSummary(previousObserver, next)
	if err := qtx.UpsertHookSessionSummary(ctx, hookSessionSummaryParams(next)); err != nil {
		return nil, err
	}
	if nextObserver != nil {
		if err := qtx.UpsertObserverSummary(ctx, observerSummaryParams(nextObserver, time.Now().UTC())); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	r.publishHookSessionUpdate(*next)
	r.publishObserverTaskUpdate(observerTaskUpdateFromSummary(nextObserver))
	return next, nil
}

func (r *Repository) ListHookSessionSummaries(
	ctx context.Context,
	taskIDs []string,
) (map[string]*core.HookSessionSummary, error) {
	if err := r.unavailableErr(); err != nil {
		return nil, err
	}

	summaries := make(map[string]*core.HookSessionSummary)

	if len(taskIDs) == 0 {
		rows, err := r.queries.ListAllHookSessionSummaries(ctx)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			summary := hookSessionSummaryFromListAllRow(row)
			summaries[summary.TaskID] = summary
		}
		return summaries, nil
	}

	rows, err := r.queries.ListHookSessionSummariesByTaskIDs(ctx, taskIDs)
	if err != nil {
		return nil, err
	}

	for _, row := range rows {
		summary := hookSessionSummaryFromListByTaskIDsRow(row)
		summaries[summary.TaskID] = summary
	}

	return summaries, nil
}

func (r *Repository) ListHookEvents(ctx context.Context, taskID string, limit int) ([]core.HookEvent, error) {
	if err := r.unavailableErr(); err != nil {
		return nil, err
	}

	var (
		rows []generated.TaskHookEvent
		err  error
	)
	if limit > 0 {
		rows, err = r.queries.ListHookEventsByTaskIDLimited(ctx, generated.ListHookEventsByTaskIDLimitedParams{
			TaskID: taskID,
			Limit:  int64(limit),
		})
	} else {
		rows, err = r.queries.ListHookEventsByTaskID(ctx, taskID)
	}
	if err != nil {
		return nil, err
	}

	events := make([]core.HookEvent, 0, len(rows))
	for _, row := range rows {
		events = append(events, hookEventFromRow(row))
	}

	return events, nil
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

func (r *Repository) resolveHookTaskID(
	ctx context.Context,
	raw core.HookEventInput,
) (string, hookTaskMatchKind, error) {
	lookup := []struct {
		value string
		query func(context.Context, string) (string, error)
		kind  hookTaskMatchKind
	}{
		{
			value: raw.TaskID,
			query: r.queries.GetTaskIDByID,
			kind:  hookTaskMatchTaskID,
		},
		{
			value: raw.Cwd,
			query: r.queries.GetTaskIDByWorktreePath,
			kind:  hookTaskMatchWorktree,
		},
		{
			value: raw.SessionID,
			query: r.queries.GetTaskIDBySessionID,
			kind:  hookTaskMatchSessionID,
		},
	}

	for _, candidate := range lookup {
		if strings.TrimSpace(candidate.value) == "" {
			continue
		}
		taskID, err := candidate.query(ctx, candidate.value)
		if err == nil {
			return taskID, candidate.kind, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", hookTaskMatchUnknown, err
		}
	}

	return "", hookTaskMatchUnknown, fmt.Errorf("%w: map hook event to managed task", core.ErrUnmanagedHookEvent)
}

func shouldIgnoreForeignSessionEvent(
	previous *core.HookSessionSummary,
	record hookRecord,
	matchKind hookTaskMatchKind,
) bool {
	if previous == nil || matchKind != hookTaskMatchWorktree {
		return false
	}
	if previous.SessionID == "" || record.SessionID == "" || previous.SessionID == record.SessionID {
		return false
	}
	if shouldAllowSessionTakeover(previous, record) {
		return false
	}
	return true
}

func shouldAllowSessionTakeover(previous *core.HookSessionSummary, record hookRecord) bool {
	if previous == nil || record.EventName != "SessionStart" {
		return false
	}

	source := strings.TrimSpace(record.StartSource)
	if source == "resume" || previous.LastEventName == "Stop" {
		return true
	}

	// Allow a fresh "startup" session to take over when the previous session
	// is not actively running a command. Subagents start while the parent is
	// in RunningCommand phase (it just dispatched the Agent tool); legitimate
	// new sessions start when the previous session has gone idle or finished.
	if source == "startup" && previous.RuntimePhase != core.HookRuntimePhaseRunningCommand {
		return true
	}

	return false
}

type hookSessionSummaryGetter interface {
	GetHookSessionSummaryByTaskID(
		ctx context.Context,
		taskID string,
	) (generated.GetHookSessionSummaryByTaskIDRow, error)
}

type observerSummaryGetter interface {
	GetObserverSummaryByTaskID(ctx context.Context, taskID string) (generated.GetObserverSummaryByTaskIDRow, error)
}

func loadHookSessionSummary(
	ctx context.Context,
	q hookSessionSummaryGetter,
	taskID string,
) (*core.HookSessionSummary, error) {
	row, err := q.GetHookSessionSummaryByTaskID(ctx, taskID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return hookSessionSummaryFromGetRow(row), nil
}

func loadObserverSummary(ctx context.Context, q observerSummaryGetter, taskID string) (*core.ObserverSummary, error) {
	row, err := q.GetObserverSummaryByTaskID(ctx, taskID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return observerSummaryFromGetRow(row), nil
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
		next.LastPromptSubmittedAt = event.OccurredAt
		next.LastAssistantMessage = ""
		next.LastCommandText = ""
		next.LastCommandResultText = ""
		next.RuntimePhase = core.HookRuntimePhasePrompted
	case "PreToolUse":
		next.CurrentTurnID = firstNonEmpty(event.TurnID, next.CurrentTurnID)
		next.LastCommandText = trimPreview(event.CommandText)
		next.RuntimePhase = core.HookRuntimePhaseRunningCommand
		next.CommandCount++
	case "PermissionRequest":
		next.CurrentTurnID = firstNonEmpty(event.TurnID, next.CurrentTurnID)
		if event.CommandText != "" {
			next.LastCommandText = trimPreview(event.CommandText)
		}
		next.RuntimePhase = core.HookRuntimePhaseWaitingPermission
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
