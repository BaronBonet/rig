package core

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// maxPaneTraceEntries bounds the cache of hook processes traced to their pane.
// An entry is one conversation as seen from one pane, so the bound only
// matters for a long-running daemon.
const maxPaneTraceEntries = 1024

// attributionRetryDelay paces the tmux and ps calls attribution makes for a
// hook it could not resolve: a trace that failed, or a conversation the Codex
// fallback could not place, such as a Codex running outside any pane. Hooks
// are synchronous, so a persistent failure must not cost those calls on every
// hook event.
const attributionRetryDelay = 10 * time.Second

// paneTraceKey identifies one conversation as seen from one pane hint: the
// $TMUX_PANE and $TMUX its hook reported. A Codex using the shared daemon
// reports the same stale hint for every conversation, so each conversation
// is traced once; a conversation resumed in another pane reports a new hint
// and is traced again.
type paneTraceKey struct {
	provider          Provider
	providerSessionID string
	paneHint          string
	serverHint        TmuxServer
}

// paneTrace is where a hook process was traced to. An empty pane means the
// hook did not run inside any pane's process tree. A failed trace has no pane
// and is not retried before retryAt.
type paneTrace struct {
	retryAt time.Time
	server  TmuxServer
	pane    string
	// nested reports that the hook came from an agent nested in another
	// agent, such as a codex exec or claude -p run by Claude's Bash tool. Its
	// pane belongs to the outer agent.
	nested bool
}

func (t paneTrace) failed() bool {
	return !t.retryAt.IsZero()
}

var lastAgentSessionID atomic.Int64

// newAgentSessionID returns a Rig-generated agent session ID. Like task IDs it
// is the creation time in nanoseconds, bumped past the last ID so that
// sessions opened in the same nanosecond stay unique.
func newAgentSessionID() string {
	for {
		last := lastAgentSessionID.Load()
		next := time.Now().UnixNano()
		if next <= last {
			next = last + 1
		}
		if lastAgentSessionID.CompareAndSwap(last, next) {
			return fmt.Sprintf("%d", next)
		}
	}
}

// recordAgentSession places a hook event in an agent session, applies it,
// and records the event's activity, if any, against that agent session.
// update is the event's mapped status, or nil when it maps to none. Events
// Rig cannot place in a pane stay unattributed. Attribution is best-effort,
// like recovered activity: a failure leaves the event unattributed and never
// fails the hook. The activity is recorded either way, and only a failure to
// record it fails the hook.
func (o *taskObservation) recordAgentSession(
	ctx context.Context,
	task *Task,
	input HookEventInput,
	update *TaskStatusUpdate,
	activity *TaskActivityEvent,
) error {
	source, attributable := o.traceHookEvent(ctx, task, input)

	o.agentSessionMu.Lock()
	defer o.agentSessionMu.Unlock()
	changes := agentSessionChanges{}
	// The publish runs before the unlock and after the activity is recorded,
	// so the published status already carries the event's prompt.
	defer o.publishAgentSessionChanges(ctx, changes)

	agentSessionID := ""
	if attributable {
		agentSessionID, _ = o.placeHookEvent(ctx, task, input, update, source, changes)
	}
	// A subagent's prompt is the instruction its parent gave it, such as a
	// Codex subagent's, not the user's, so it is no agent session's latest
	// prompt.
	if activity != nil && activity.Role == TaskActivityRoleUser && strings.TrimSpace(input.AgentID) != "" {
		agentSessionID = ""
	}
	return o.recordHookActivity(ctx, activity, agentSessionID, changes)
}

// hookEventSource is where a hook event's agent was traced to before the
// event is placed: the pane its hook ran in or, for a Codex whose hooks run
// outside its pane, the tmux snapshot the fallback places it with.
type hookEventSource struct {
	trace    paneTrace
	snapshot *TmuxSnapshot
	// sessionLost reports, for a Codex SessionEnd traced to no pane, that the
	// task's Session is gone, or that tmux could not say: its agent sessions
	// are lost and stay open for Reconnect.
	sessionLost bool
}

// traceHookEvent traces a hook event to where its agent runs. It may inspect
// tmux, so it runs without agentSessionMu. attributable is false for an
// event no agent session can take: one without a task record, one whose
// trace failed, a nested agent's, or a Claude event from outside tmux.
func (o *taskObservation) traceHookEvent(
	ctx context.Context,
	task *Task,
	input HookEventInput,
) (source hookEventSource, attributable bool) {
	if task == nil {
		return hookEventSource{}, false
	}

	trace, snapshot, err := o.tracePane(ctx, task, input)
	// A nested agent's hook must not touch the outer agent's session, nor
	// reach the Codex fallback, where its SessionStart could move a Codex
	// agent in another pane.
	if err != nil || trace.failed() || trace.nested {
		return hookEventSource{}, false
	}
	if trace.pane != "" {
		return hookEventSource{trace: trace}, true
	}
	// A SessionEnd only ends an agent session it is already bound to, so it
	// never needs the fallback's snapshot.
	if strings.TrimSpace(input.EventName) == HookEventSessionEnd {
		source := hookEventSource{trace: trace}
		if input.Provider == ProviderCodex {
			source.sessionLost = o.taskSessionLost(ctx, task)
		}
		return source, true
	}
	// Claude runs hooks inside the agent's process, so a Claude hook that ran
	// in no pane came from outside tmux. Only a Codex agent using the shared
	// daemon runs hooks outside its pane.
	if input.Provider != ProviderCodex {
		return hookEventSource{}, false
	}

	snapshot, err = o.codexFallbackSnapshot(ctx, task, input, snapshot)
	if err != nil {
		return hookEventSource{}, false
	}
	return hookEventSource{trace: trace, snapshot: snapshot}, true
}

// placeHookEvent places a traced hook event in an agent session and applies
// it. It returns the ID of the agent session that took the event, or empty
// when the event stayed unattributed. Callers hold agentSessionMu.
func (o *taskObservation) placeHookEvent(
	ctx context.Context,
	task *Task,
	input HookEventInput,
	update *TaskStatusUpdate,
	source hookEventSource,
	changes agentSessionChanges,
) (string, error) {
	switch {
	case strings.TrimSpace(input.EventName) == HookEventSessionEnd:
		return "", o.recordSessionEnd(ctx, task, input, source, changes)
	case source.trace.pane != "":
		return o.recordPaneEvent(ctx, task, input, update, source.trace.server, source.trace.pane, true, changes)
	default:
		return o.recordCodexFallbackEvent(ctx, task, input, update, source.snapshot, changes)
	}
}

// agentSessionChanges collects the tasks whose agent sessions one hook event
// changed, so each task's Runtime status is published once, after every
// change is recorded, and never from a half-applied event.
type agentSessionChanges map[string]bool

// publishAgentSessionChanges publishes the Runtime status of every task the
// hook event changed. Callers hold agentSessionMu.
func (o *taskObservation) publishAgentSessionChanges(ctx context.Context, changes agentSessionChanges) {
	if o.statusObserver == nil {
		return
	}
	for taskID := range changes {
		o.statusObserver.agentSessionsChanged(ctx, taskID)
	}
}

// endAgentSession ends an agent session a status cycle found exited or
// closed. A hook may have changed it since the cycle read it, as when the
// user starts the agent again in the same pane after the cycle's snapshot;
// such an agent session stays open, and the next cycle judges it again.
func (o *taskObservation) endAgentSession(ctx context.Context, read AgentSession) error {
	o.agentSessionMu.Lock()
	defer o.agentSessionMu.Unlock()
	open, err := o.tasks.ListOpenAgentSessions(ctx, read.TaskID)
	if err != nil {
		return fmt.Errorf("list open agent sessions: %w", err)
	}
	for _, stored := range open {
		if stored.ID != read.ID || !agentSessionStillAsRead(stored, read) {
			continue
		}
		if err := o.tasks.EndAgentSession(ctx, read.ID, o.now().UTC()); err != nil {
			return fmt.Errorf("end agent session: %w", err)
		}
	}
	return nil
}

// agentSessionStillAsRead reports whether a stored open agent session is
// still as a status cycle read it: in the same pane, launched at the same
// time, on the same conversation, with the same pane trust and status.
func agentSessionStillAsRead(stored AgentSession, read AgentSession) bool {
	return stored.TmuxServer == read.TmuxServer &&
		stored.TmuxPane == read.TmuxPane &&
		stored.LaunchedAt.Equal(read.LaunchedAt) &&
		stored.ProviderSessionID == read.ProviderSessionID &&
		stored.PaneTrusted == read.PaneTrusted &&
		agentSessionStatusesEqual(stored.Status, read.Status)
}

// Claude's SessionEnd reasons for a Provider session that ends while its
// agent goes on with a new one.
const (
	claudeSessionEndReasonClear  = "clear"
	claudeSessionEndReasonResume = "resume"
)

// sessionEndOutcome is what a SessionEnd event does to the agent session it
// was placed in.
type sessionEndOutcome int

const (
	// sessionEndIgnored: the event ended another Provider session than the
	// agent session's current one, as Codex's late SessionEnd for a
	// conversation the agent left after /new does.
	sessionEndIgnored sessionEndOutcome = iota
	// sessionEndKept: the agent goes on with a new Provider session, which
	// the SessionStart that follows moves the agent session to.
	sessionEndKept
	// sessionEndEnded: the agent exited.
	sessionEndEnded
)

// resolveSessionEnd is the SessionEnd decision table, kept free of I/O so
// every row is a value-in/value-out test. Claude's /clear and /resume end a
// Provider session but not the agent; every other reason, and every Codex
// SessionEnd, means the agent exited, if the event ended its current Provider
// session.
func resolveSessionEnd(
	provider Provider,
	reason string,
	currentProviderSessionID string,
	eventProviderSessionID string,
) sessionEndOutcome {
	if provider == ProviderClaude {
		switch strings.TrimSpace(reason) {
		case claudeSessionEndReasonClear, claudeSessionEndReasonResume:
			return sessionEndKept
		}
	}
	eventProviderSessionID = strings.TrimSpace(eventProviderSessionID)
	if eventProviderSessionID == "" || eventProviderSessionID != strings.TrimSpace(currentProviderSessionID) {
		return sessionEndIgnored
	}
	return sessionEndEnded
}

// recordSessionEnd ends the agent session whose agent a SessionEnd event says
// exited. It never creates, replaces or moves an agent session: the only
// candidate is the traced pane's open agent session of the event's task and
// provider or, for a Codex whose hooks run outside its pane, the open agent
// session in an untrusted pane bound to the event's Provider session. A
// trusted agent runs its hooks in its pane, so a SessionEnd from outside any
// pane is not its own: the shared daemon may still hold a conversation that a
// Codex without the daemon resumed. Nor does it end an agent session without
// a pane, or one whose task's Session is gone: the daemon ends a conversation
// it holds up to 30 minutes after its agent was lost with the Session, and
// Reconnect could then no longer restore it. Callers hold agentSessionMu.
func (o *taskObservation) recordSessionEnd(
	ctx context.Context,
	task *Task,
	input HookEventInput,
	source hookEventSource,
	changes agentSessionChanges,
) error {
	trace := source.trace
	var session *AgentSession
	switch {
	case trace.pane != "":
		existing, err := o.tasks.OpenAgentSessionOnPane(ctx, trace.server, trace.pane)
		if err != nil {
			return fmt.Errorf("find agent session on pane: %w", err)
		}
		if existing != nil && existing.TaskID == task.ID && existing.Provider == input.Provider {
			session = existing
		}
	case input.Provider == ProviderCodex && !source.sessionLost:
		sessions, err := o.tasks.ListOpenAgentSessions(ctx, task.ID)
		if err != nil {
			return fmt.Errorf("list open agent sessions: %w", err)
		}
		var untrusted []AgentSession
		for _, open := range sessions {
			if !open.PaneTrusted {
				untrusted = append(untrusted, open)
			}
		}
		session = boundAgentSession(untrusted, input.Provider, strings.TrimSpace(input.SessionID))
	}
	if session == nil {
		return nil
	}
	outcome := resolveSessionEnd(input.Provider, input.EndReason, session.ProviderSessionID, input.SessionID)
	if outcome != sessionEndEnded {
		return nil
	}
	if err := o.tasks.EndAgentSession(ctx, session.ID, hookEventTime(input)); err != nil {
		return fmt.Errorf("end agent session: %w", err)
	}
	changes[session.TaskID] = true
	return nil
}

// taskSessionLost reports whether the task's Session is gone, or tmux could
// not say whether it exists. It inspects tmux, so callers do not hold
// agentSessionMu.
func (o *taskObservation) taskSessionLost(ctx context.Context, task *Task) bool {
	runtime, err := o.tmuxSession.InspectTaskSession(ctx, task)
	return err != nil || !runtime.Exists
}

// tracePane returns the pane a hook process ran in, traced through the
// process tree once per conversation and pane hint. snapshot is set when
// this call inspected tmux. A failed trace is remembered, so the same hook
// source is not traced again before attributionRetryDelay has passed.
func (o *taskObservation) tracePane(
	ctx context.Context,
	task *Task,
	input HookEventInput,
) (paneTrace, *TmuxSnapshot, error) {
	if input.HookPID <= 0 {
		return paneTrace{}, nil, nil
	}

	key := paneTraceKey{
		provider:          input.Provider,
		providerSessionID: strings.TrimSpace(input.SessionID),
		paneHint:          strings.TrimSpace(input.TmuxPane),
		serverHint:        input.TmuxServer,
	}
	if trace, ok := o.paneTraces.get(key); ok && (!trace.failed() || o.now().Before(trace.retryAt)) {
		return trace, nil, nil
	}

	snapshot, location, err := o.tmuxSession.LocateProcessPane(ctx, task, input.HookPID)
	if err != nil {
		o.paneTraces.put(key, paneTrace{retryAt: o.now().Add(attributionRetryDelay)})
		return paneTrace{}, nil, fmt.Errorf("trace hook process to its pane: %w", err)
	}
	trace := paneTrace{pane: location.Pane}
	if location.Pane != "" {
		trace.server = snapshot.Server
		trace.nested = o.agentProcessCount(location.Chain) > 1
	}
	o.paneTraces.put(key, trace)
	return trace, &snapshot, nil
}

// agentProcessCount counts the processes on a pane's process chain that run a
// supported provider's CLI. More than one means an agent nested in another.
func (o *taskObservation) agentProcessCount(chain []string) int {
	agentCommands := make([]string, 0, len(o.providers))
	for _, providerClient := range o.providers {
		if command := strings.TrimSpace(providerClient.TaskSessionCommandName()); command != "" {
			agentCommands = append(agentCommands, filepath.Base(command))
		}
	}

	count := 0
	for _, command := range chain {
		activeCommand := filepath.Base(strings.TrimSpace(command))
		for _, agentCommand := range agentCommands {
			if taskSessionCommandsMatch(activeCommand, agentCommand) {
				count++
				break
			}
		}
	}
	return count
}

// codexFallbackSnapshot returns the tmux snapshot the Codex fallback needs to
// find an untracked Codex pane, or nil when the fallback does not need one:
// the conversation is already bound to an agent session, or it was not
// placed recently. A snapshot already taken for this event is reused.
func (o *taskObservation) codexFallbackSnapshot(
	ctx context.Context,
	task *Task,
	input HookEventInput,
	snapshot *TmuxSnapshot,
) (*TmuxSnapshot, error) {
	providerSessionID := strings.TrimSpace(input.SessionID)
	sessions, err := o.tasks.ListOpenAgentSessions(ctx, task.ID)
	if err != nil {
		return nil, fmt.Errorf("list open agent sessions: %w", err)
	}
	if boundAgentSession(sessions, input.Provider, providerSessionID) != nil {
		return nil, nil
	}
	if snapshot != nil {
		return snapshot, nil
	}
	if retryAt, ok := o.codexFallbackRetries.get(providerSessionID); ok && o.now().Before(retryAt) {
		return nil, nil
	}

	located, _, err := o.tmuxSession.LocateProcessPane(ctx, task, 0)
	if err != nil {
		o.codexFallbackRetries.put(providerSessionID, o.now().Add(attributionRetryDelay))
		return nil, fmt.Errorf("inspect tmux for the codex fallback: %w", err)
	}
	return &located, nil
}

// recordCodexFallbackEvent places a Codex event whose hook ran outside its
// pane, as hooks of a Codex using the shared daemon do:
//  1. the open agent session in a pane whose current Provider session is the
//     event's;
//  2. otherwise the task Session's one pane running Codex without an open
//     Codex agent session, which gets an untrusted agent session;
//  3. otherwise, for a SessionStart or UserPromptSubmit, the task's one open
//     Codex agent session in an untrusted pane, which moves to the event's
//     Provider session, as after /new or /resume;
//  4. otherwise the event stays unattributed.
//
// An agent session without a pane is never a candidate: only Reconnect
// restores it, to the conversation it holds.
func (o *taskObservation) recordCodexFallbackEvent(
	ctx context.Context,
	task *Task,
	input HookEventInput,
	update *TaskStatusUpdate,
	snapshot *TmuxSnapshot,
	changes agentSessionChanges,
) (string, error) {
	providerSessionID := strings.TrimSpace(input.SessionID)
	sessions, err := o.tasks.ListOpenAgentSessions(ctx, task.ID)
	if err != nil {
		return "", fmt.Errorf("list open agent sessions: %w", err)
	}
	if bound := boundAgentSession(sessions, input.Provider, providerSessionID); bound != nil {
		return o.applyAgentSessionEvent(ctx, *bound, input, update, false, changes)
	}

	if snapshot != nil {
		pane, found, err := o.untrackedProviderPane(ctx, task, input.Provider, *snapshot)
		if err != nil {
			return "", err
		}
		if found {
			return o.recordPaneEvent(ctx, task, input, update, snapshot.Server, pane, false, changes)
		}
		o.codexFallbackRetries.put(providerSessionID, o.now().Add(attributionRetryDelay))
	}

	if providerSessionID == "" || !movesAgentSession(input) {
		return "", nil
	}
	var untrusted []AgentSession
	for _, session := range sessions {
		if session.TmuxPane != "" && session.Provider == input.Provider && !session.PaneTrusted {
			untrusted = append(untrusted, session)
		}
	}
	if len(untrusted) != 1 {
		return "", nil
	}
	return o.applyAgentSessionEvent(ctx, untrusted[0], input, update, false, changes)
}

// boundAgentSession returns the open agent session in a pane whose current
// Provider session is providerSessionID, or nil. An agent session without a
// pane is never bound: no agent of it runs, and only Reconnect restores it.
func boundAgentSession(sessions []AgentSession, provider Provider, providerSessionID string) *AgentSession {
	if providerSessionID == "" {
		return nil
	}
	for i := range sessions {
		if sessions[i].TmuxPane != "" && sessions[i].Provider == provider &&
			sessions[i].ProviderSessionID == providerSessionID {
			return &sessions[i]
		}
	}
	return nil
}

// untrackedProviderPane returns the task Session's one pane running the
// provider's CLI that has no open agent session of that provider. found is
// false when there is no such pane or more than one.
func (o *taskObservation) untrackedProviderPane(
	ctx context.Context,
	task *Task,
	provider Provider,
	snapshot TmuxSnapshot,
) (string, bool, error) {
	providerClient, ok := o.providers[provider]
	if !ok {
		return "", false, nil
	}
	commandName := providerClient.TaskSessionCommandName()

	panesByID := make(map[string]TmuxPane, len(snapshot.Panes))
	for _, pane := range snapshot.Panes {
		panesByID[pane.ID] = pane
	}

	var untracked []string
	for _, paneID := range snapshot.TaskSessionPanes[task.ID] {
		pane, ok := panesByID[paneID]
		if !ok || !paneRunningProvider(pane, commandName) {
			continue
		}
		existing, err := o.tasks.OpenAgentSessionOnPane(ctx, snapshot.Server, paneID)
		if err != nil {
			return "", false, fmt.Errorf("find agent session on pane: %w", err)
		}
		if existing != nil && existing.Provider == provider {
			continue
		}
		untracked = append(untracked, paneID)
	}
	if len(untracked) != 1 {
		return "", false, nil
	}
	return untracked[0], true, nil
}

// recordPaneEvent applies an event placed in a pane and returns the ID of the
// agent session that took it. The pane's open agent session takes the event
// unless it belongs to another task or provider, in which case it ended when
// the pane's agent changed and a new agent session opens.
func (o *taskObservation) recordPaneEvent(
	ctx context.Context,
	task *Task,
	input HookEventInput,
	update *TaskStatusUpdate,
	server TmuxServer,
	pane string,
	trusted bool,
	changes agentSessionChanges,
) (string, error) {
	existing, err := o.tasks.OpenAgentSessionOnPane(ctx, server, pane)
	if err != nil {
		return "", fmt.Errorf("find agent session on pane: %w", err)
	}
	if existing != nil && (existing.TaskID != task.ID || existing.Provider != input.Provider) {
		if err := o.tasks.EndAgentSession(ctx, existing.ID, hookEventTime(input)); err != nil {
			return "", fmt.Errorf("end replaced agent session: %w", err)
		}
		changes[existing.TaskID] = true
		existing = nil
	}
	if existing != nil {
		return o.applyAgentSessionEvent(ctx, *existing, input, update, trusted, changes)
	}

	session := AgentSession{
		ID:                o.newAgentSessionID(),
		TaskID:            task.ID,
		Provider:          input.Provider,
		TmuxServer:        server,
		TmuxPane:          pane,
		PaneTrusted:       trusted,
		ProviderSessionID: strings.TrimSpace(input.SessionID),
		StartedAt:         hookEventTime(input),
	}
	if update != nil {
		session.Status = agentSessionStatusFromUpdate(*update)
	}
	if err := o.tasks.CreateAgentSession(ctx, session); err != nil {
		return "", fmt.Errorf("create agent session: %w", err)
	}
	changes[task.ID] = true
	return session.ID, nil
}

// recordLaunchedAgentSession records the agent session of an agent Rig has
// just launched in pane and publishes its task's status at once, so the agent
// shows as starting before its first hook event. It trusts its pane: Rig
// started the agent there, and launches Codex without the shared daemon. When
// a hook from the agent won the race and opened its agent session on the pane
// already, that agent session is returned instead. Any other open agent
// session on the pane is stale, so it ends.
func (o *taskObservation) recordLaunchedAgentSession(
	ctx context.Context,
	task *Task,
	provider Provider,
	pane TmuxPaneRef,
) (AgentSession, error) {
	o.agentSessionMu.Lock()
	defer o.agentSessionMu.Unlock()
	changes := agentSessionChanges{}
	defer o.publishAgentSessionChanges(ctx, changes)

	opened, err := o.claimLaunchPane(ctx, task, provider, pane, changes)
	if err != nil {
		return AgentSession{}, err
	}
	if opened != nil {
		return *opened, nil
	}
	return o.createLaunchedAgentSession(ctx, task, provider, pane, changes)
}

// detachAgentSessionsForRestore readies a task's agent sessions for
// Reconnect, which has read them: it ends those in end, which have nothing to
// resume, and detaches those in restore from the panes of the lost Session. A
// detached agent session has no pane and is starting, and it gets the launch
// grace, so a status cycle that sees the new Session before Reconnect moves it
// to its agent's pane keeps it. It returns the detached agent sessions by ID.
// An agent session in end that a hook changed since Reconnect read it stays
// as it is. One in restore whose status alone changed is detached all the
// same, as the detach resets the status; one that otherwise changed or ended
// is left as it is and returned in changed.
func (o *taskObservation) detachAgentSessionsForRestore(
	ctx context.Context,
	taskID string,
	restore []AgentSession,
	end []AgentSession,
) (detached map[string]AgentSession, changed []AgentSession, err error) {
	o.agentSessionMu.Lock()
	defer o.agentSessionMu.Unlock()
	changes := agentSessionChanges{}
	defer o.publishAgentSessionChanges(ctx, changes)

	open, err := o.tasks.ListOpenAgentSessions(ctx, taskID)
	if err != nil {
		return nil, nil, fmt.Errorf("list open agent sessions: %w", err)
	}
	stored := make(map[string]AgentSession, len(open))
	for _, session := range open {
		stored[session.ID] = session
	}

	now := o.now().UTC()
	for _, read := range end {
		if current, ok := stored[read.ID]; !ok || !agentSessionStillAsRead(current, read) {
			continue
		}
		if err := o.tasks.EndAgentSession(ctx, read.ID, now); err != nil {
			return nil, nil, fmt.Errorf("end agent session: %w", err)
		}
		changes[taskID] = true
	}

	detached = make(map[string]AgentSession, len(restore))
	for _, read := range restore {
		current, ok := stored[read.ID]
		if !ok || !agentSessionPlacedAsRead(current, read) {
			changed = append(changed, read)
			continue
		}
		current.TmuxServer = TmuxServer{}
		current.TmuxPane = ""
		current.PaneTrusted = false
		current.LaunchedAt = now
		current.Status = AgentSessionStatus{}
		if err := o.tasks.UpdateAgentSession(ctx, current); err != nil {
			return nil, nil, fmt.Errorf("detach agent session: %w", err)
		}
		changes[taskID] = true
		detached[current.ID] = current
	}
	return detached, changed, nil
}

// agentSessionPlacedAsRead reports whether a stored open agent session still
// runs where, and on what, Reconnect read it: in the same pane, launched at
// the same time, on the same conversation, with the same pane trust. Its
// status may have changed.
func agentSessionPlacedAsRead(stored AgentSession, read AgentSession) bool {
	stored.Status = read.Status
	return agentSessionStillAsRead(stored, read)
}

// recordRestoredAgentSession moves an agent session Reconnect detached to the
// pane its agent resumed in, and publishes its task's status at once. Like an
// agent Rig launches, it trusts the pane and gets the launch grace; it stays
// starting, as the detach left it, until its first hook event, and keeps its
// Provider session and start time. When the agent's first hook event won the
// race and opened an agent session on the pane already, that one goes on and
// the detached one ends. When the detached one ended meanwhile, as after a
// restore slower than the launch grace, the agent gets a new agent session,
// as a launch does.
func (o *taskObservation) recordRestoredAgentSession(
	ctx context.Context,
	task *Task,
	detached AgentSession,
	pane TmuxPaneRef,
) (AgentSession, error) {
	o.agentSessionMu.Lock()
	defer o.agentSessionMu.Unlock()
	changes := agentSessionChanges{}
	defer o.publishAgentSessionChanges(ctx, changes)

	opened, err := o.claimLaunchPane(ctx, task, detached.Provider, pane, changes)
	if err != nil {
		return AgentSession{}, err
	}
	if opened != nil {
		if err := o.tasks.EndAgentSession(ctx, detached.ID, o.now().UTC()); err != nil {
			return AgentSession{}, fmt.Errorf("end superseded agent session: %w", err)
		}
		changes[task.ID] = true
		return *opened, nil
	}

	open, err := o.tasks.ListOpenAgentSessions(ctx, task.ID)
	if err != nil {
		return AgentSession{}, fmt.Errorf("list open agent sessions: %w", err)
	}
	index := slices.IndexFunc(open, func(session AgentSession) bool { return session.ID == detached.ID })
	if index < 0 {
		return o.createLaunchedAgentSession(ctx, task, detached.Provider, pane, changes)
	}
	session := open[index]
	session.TmuxServer = pane.Server
	session.TmuxPane = pane.ID
	session.PaneTrusted = true
	session.LaunchedAt = o.now().UTC()
	if err := o.tasks.UpdateAgentSession(ctx, session); err != nil {
		return AgentSession{}, fmt.Errorf("move agent session: %w", err)
	}
	changes[task.ID] = true
	return session, nil
}

// claimLaunchPane readies the pane Rig launched provider's agent in for the
// agent's agent session. It returns the agent session the agent's first hook
// event opened there already, or nil, after ending any other open agent
// session on the pane, which is stale. Callers hold agentSessionMu.
func (o *taskObservation) claimLaunchPane(
	ctx context.Context,
	task *Task,
	provider Provider,
	pane TmuxPaneRef,
	changes agentSessionChanges,
) (*AgentSession, error) {
	existing, err := o.tasks.OpenAgentSessionOnPane(ctx, pane.Server, pane.ID)
	if err != nil {
		return nil, fmt.Errorf("find agent session on pane: %w", err)
	}
	if existing == nil {
		return nil, nil
	}
	if existing.TaskID == task.ID && existing.Provider == provider {
		return existing, nil
	}
	if err := o.tasks.EndAgentSession(ctx, existing.ID, o.now().UTC()); err != nil {
		return nil, fmt.Errorf("end replaced agent session: %w", err)
	}
	changes[existing.TaskID] = true
	return nil, nil
}

// createLaunchedAgentSession opens the agent session of an agent Rig launched
// in pane: trusted, starting, and in its launch grace. Callers hold
// agentSessionMu.
func (o *taskObservation) createLaunchedAgentSession(
	ctx context.Context,
	task *Task,
	provider Provider,
	pane TmuxPaneRef,
	changes agentSessionChanges,
) (AgentSession, error) {
	launchedAt := o.now().UTC()
	session := AgentSession{
		ID:          o.newAgentSessionID(),
		TaskID:      task.ID,
		Provider:    provider,
		TmuxServer:  pane.Server,
		TmuxPane:    pane.ID,
		PaneTrusted: true,
		StartedAt:   launchedAt,
		LaunchedAt:  launchedAt,
	}
	if err := o.tasks.CreateAgentSession(ctx, session); err != nil {
		return AgentSession{}, fmt.Errorf("create agent session: %w", err)
	}
	changes[task.ID] = true
	return session, nil
}

// applyAgentSessionEvent applies an event to the agent session it was placed
// in and returns that agent session's ID. SessionStart and UserPromptSubmit
// move the agent session to the event's Provider session, and so does the
// first event of an agent session that has none yet, as one Rig launched has.
// Any other event from a Provider session that is not the current one is
// late, so it changes nothing. trusted records that the event's hook was
// traced to the agent session's pane.
func (o *taskObservation) applyAgentSessionEvent(
	ctx context.Context,
	session AgentSession,
	input HookEventInput,
	update *TaskStatusUpdate,
	trusted bool,
	changes agentSessionChanges,
) (string, error) {
	changed := false
	if trusted && !session.PaneTrusted {
		session.PaneTrusted = true
		changed = true
	}
	// A hook from the agent's pane shows the agent Rig launched there runs.
	if trusted && !session.LaunchedAt.IsZero() {
		session.LaunchedAt = time.Time{}
		changed = true
	}

	providerSessionID := strings.TrimSpace(input.SessionID)
	current := providerSessionID == "" || providerSessionID == session.ProviderSessionID
	if !current && (session.ProviderSessionID == "" || movesAgentSession(input)) {
		session.ProviderSessionID = providerSessionID
		session.Status = AgentSessionStatus{}
		current = true
		changed = true
	}
	if current && update != nil {
		session.Status = agentSessionStatusFromUpdate(*update)
		changed = true
	}

	if !changed {
		return session.ID, nil
	}
	if err := o.tasks.UpdateAgentSession(ctx, session); err != nil {
		return "", fmt.Errorf("update agent session: %w", err)
	}
	changes[session.TaskID] = true
	return session.ID, nil
}

// movesAgentSession reports whether an event starts or continues the agent's
// current conversation, so it may move the agent session to its Provider
// session.
func movesAgentSession(input HookEventInput) bool {
	switch strings.TrimSpace(input.EventName) {
	case HookEventSessionStart, HookEventUserPromptSubmit:
		return true
	default:
		return false
	}
}

func agentSessionStatusFromUpdate(update TaskStatusUpdate) AgentSessionStatus {
	return AgentSessionStatus{
		ObservedAt:     update.ObservedAt,
		RawEventName:   update.RawEventName,
		Phase:          update.Phase,
		BackgroundWork: update.BackgroundWork,
	}
}

// paneRunningProvider reports whether a pane's foreground command, or one of
// its root process's children, is the provider's CLI.
func paneRunningProvider(pane TmuxPane, commandName string) bool {
	expectedCommand := filepath.Base(strings.TrimSpace(commandName))
	if expectedCommand == "" {
		return false
	}

	commands := make([]string, 0, len(pane.Children)+1)
	commands = append(commands, pane.Command)
	for _, child := range pane.Children {
		commands = append(commands, child.Command)
	}
	for _, command := range commands {
		if taskSessionCommandsMatch(filepath.Base(strings.TrimSpace(command)), expectedCommand) {
			return true
		}
	}
	return false
}

func hookEventTime(input HookEventInput) time.Time {
	if input.OccurredAt.IsZero() {
		return time.Now().UTC()
	}
	return input.OccurredAt
}

// boundedCache is a small map that evicts its oldest entry once full. It is
// safe for concurrent use.
type boundedCache[K comparable, V any] struct {
	entries map[K]V
	order   []K
	limit   int
	mu      sync.Mutex
}

func newBoundedCache[K comparable, V any](limit int) *boundedCache[K, V] {
	return &boundedCache[K, V]{entries: make(map[K]V), limit: limit}
}

func (c *boundedCache[K, V]) get(key K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	value, ok := c.entries[key]
	return value, ok
}

func (c *boundedCache[K, V]) put(key K, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.entries[key]; !ok {
		if len(c.entries) >= c.limit && len(c.order) > 0 {
			delete(c.entries, c.order[0])
			c.order = c.order[1:]
		}
		c.order = append(c.order, key)
	}
	c.entries[key] = value
}

func (c *boundedCache[K, V]) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}
