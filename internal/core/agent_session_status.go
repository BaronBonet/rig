package core

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// taskStatusRawEventStopped names the published status of a task none of
// whose agent sessions is live.
const taskStatusRawEventStopped = "TaskSessionStopped"

// agentLaunchGrace is how long a status cycle gives an agent Rig launched to
// show up in its pane: the launch command is typed into the pane's shell, so
// the agent's process appears a moment after its window does.
const agentLaunchGrace = 10 * time.Second

// agentSessionLiveness is the pure outcome of checking one open agent session
// against a tmux snapshot.
type agentSessionLiveness int

const (
	// agentSessionUnchanged: the snapshot cannot tell, so the previous verdict
	// stands.
	agentSessionUnchanged agentSessionLiveness = iota
	// agentSessionLive: its pane runs its provider, so provider-side state may
	// hold a newer observation than its hook-driven status.
	agentSessionLive
	// agentSessionLaunching: Rig launched the agent moments ago and no hook
	// event has come from its pane yet, so a pane that does not show the agent
	// yet proves nothing. It counts as live.
	agentSessionLaunching
	// agentSessionLost: the task's Session is gone. The agent session stays
	// open so Reconnect can restore it, but it is not live.
	agentSessionLost
	// agentSessionEnded: the agent exited, or its pane or window closed.
	agentSessionEnded
)

// resolveAgentSessionLiveness is the agent session liveness decision table,
// kept free of I/O so every row is a value-in/value-out test. sessionExists
// reports whether the task's Session has any pane; pane is the snapshot's
// pane with the agent session's pane ID, or nil when there is none; server is
// the snapshot's tmux server; now is when the snapshot was judged.
func resolveAgentSessionLiveness(
	session AgentSession,
	sessionExists bool,
	pane *TmuxPane,
	server TmuxServer,
	providerCommand string,
	now time.Time,
) agentSessionLiveness {
	if !sessionExists {
		return agentSessionLost
	}
	if server.IsZero() {
		return agentSessionUnchanged
	}
	// Pane IDs restart on a new tmux server, so a pane recorded on another
	// server is gone even when the snapshot has a pane with the same ID.
	if !session.TmuxServer.IsZero() && session.TmuxServer != server {
		return agentSessionEnded
	}
	if pane != nil && paneRunningProvider(*pane, providerCommand) {
		return agentSessionLive
	}
	if session.launching(now) {
		return agentSessionLaunching
	}
	if pane == nil {
		return agentSessionEnded
	}
	// Missing child process evidence cannot prove the agent exited.
	if pane.ChildProcessEvidenceUnavailable {
		return agentSessionUnchanged
	}
	return agentSessionEnded
}

// rollUpTaskStatus derives a task's Runtime status from its live agent
// sessions: the most urgent one wins, and ties go to the most recent status
// change. With no live agent session the task is stopped, unless it never had
// an agent session at all, which leaves it without a status. latest is the
// task's most recently started agent session, open or ended.
func rollUpTaskStatus(taskID string, live []AgentSession, latest *AgentSession) *TaskStatusUpdate {
	var winner *AgentSession
	for i := range live {
		if winner == nil || moreUrgentAgentSession(live[i], *winner) {
			winner = &live[i]
		}
	}
	if winner != nil {
		status := rolledUpAgentSessionStatus(*winner)
		return &TaskStatusUpdate{
			TaskID:             taskID,
			Provider:           winner.Provider,
			Phase:              status.Phase,
			RawEventName:       status.RawEventName,
			ObservedAt:         status.ObservedAt,
			BackgroundWork:     status.BackgroundWork,
			LeadAgentSessionID: winner.ID,
		}
	}
	if latest == nil {
		return nil
	}
	return &TaskStatusUpdate{
		TaskID:       taskID,
		Provider:     latest.Provider,
		Phase:        TaskStatusPhaseStopped,
		RawEventName: taskStatusRawEventStopped,
		ObservedAt:   agentSessionStatusTime(*latest),
	}
}

// taskStatusUrgency ranks agent session phases for the task's Runtime status:
// an agent waiting on the user outranks one working, which outranks one
// working in the background, which outranks one starting. A live agent
// session without a status yet counts as starting.
func taskStatusUrgency(phase TaskStatusPhase) int {
	switch phase {
	case TaskStatusPhaseWaitingForInput:
		return 3
	case TaskStatusPhaseWorking:
		return 2
	case TaskStatusPhaseWorkingInBackground:
		return 1
	default:
		return 0
	}
}

func moreUrgentAgentSession(candidate AgentSession, current AgentSession) bool {
	candidateUrgency := taskStatusUrgency(candidate.Status.Phase)
	currentUrgency := taskStatusUrgency(current.Status.Phase)
	if candidateUrgency != currentUrgency {
		return candidateUrgency > currentUrgency
	}
	candidateChangedAt := agentSessionStatusTime(candidate)
	currentChangedAt := agentSessionStatusTime(current)
	if !candidateChangedAt.Equal(currentChangedAt) {
		return candidateChangedAt.After(currentChangedAt)
	}
	return candidate.ID > current.ID
}

// agentSessionStatusTime is when an agent session's status last changed or,
// while it has no status yet, when Rig launched its agent, as when Reconnect
// restored it, or else when the agent session started.
func agentSessionStatusTime(session AgentSession) time.Time {
	if !session.Status.ObservedAt.IsZero() {
		return session.Status.ObservedAt
	}
	if !session.LaunchedAt.IsZero() {
		return session.LaunchedAt
	}
	return session.StartedAt
}

// rolledUpAgentSessionStatus is a live agent session's status as its task's
// status counts it: starting, since it started, while it has none yet.
func rolledUpAgentSessionStatus(session AgentSession) AgentSessionStatus {
	status := session.Status
	if status.Phase == "" {
		status.Phase = TaskStatusPhaseStarting
	}
	status.ObservedAt = agentSessionStatusTime(session)
	return status
}

// agentSessionEvidence is a task's persisted agent session state: its open
// agent sessions with their hook-driven statuses and latest prompts, and its
// most recently started agent session, open or ended, if it ever had one.
type agentSessionEvidence struct {
	open   []AgentSession
	latest *AgentSession
	// prompts maps an open agent session's ID to its newest prompt.
	prompts map[string]string
}

func (o *taskObservation) readAgentSessionEvidence(
	ctx context.Context,
	taskID string,
) (agentSessionEvidence, error) {
	// The latest agent session is read first: one opened between the two
	// reads then shows up among the open ones rather than going missing.
	latest, err := o.tasks.LatestAgentSession(ctx, taskID)
	if err != nil {
		return agentSessionEvidence{}, fmt.Errorf("read latest agent session: %w", err)
	}
	open, err := o.tasks.ListOpenAgentSessions(ctx, taskID)
	if err != nil {
		return agentSessionEvidence{}, fmt.Errorf("list open agent sessions: %w", err)
	}
	evidence := agentSessionEvidence{open: open, latest: latest}
	if len(open) == 0 {
		return evidence, nil
	}
	prompts, err := o.tasks.ListLatestAgentSessionPrompts(ctx, taskID)
	if err != nil {
		return agentSessionEvidence{}, fmt.Errorf("list latest agent session prompts: %w", err)
	}
	evidence.prompts = make(map[string]string, len(prompts))
	for _, prompt := range prompts {
		evidence.prompts[prompt.AgentSessionID] = prompt.Text
	}
	return evidence, nil
}

func (e agentSessionEvidence) empty() bool {
	return e.latest == nil && len(e.open) == 0
}

// agentSessionLiveView carries what the last status cycle learned about a
// task's agent sessions beyond their hook evidence: which ones are lost, the
// statuses recovered for live ones, and where their panes sit. Views built
// from hook evidence between cycles keep it. Each finding holds only while
// the agent session is in the pane the cycle judged: Reconnect moves agent
// sessions to new panes.
type agentSessionLiveView struct {
	// lost maps a lost agent session's ID to the pane it was judged in.
	lost      map[string]TmuxPaneRef
	recovered map[string]recoveredAgentSessionStatus
	locations map[string]paneLocation
}

func newAgentSessionLiveView() agentSessionLiveView {
	return agentSessionLiveView{
		lost:      make(map[string]TmuxPaneRef),
		recovered: make(map[string]recoveredAgentSessionStatus),
		locations: make(map[string]paneLocation),
	}
}

// isLost reports whether the cycle found the agent session lost in the pane
// it is in now.
func (v agentSessionLiveView) isLost(session AgentSession) bool {
	pane, ok := v.lost[session.ID]
	return ok && pane == session.paneRef()
}

// location returns where the agent session's pane sat at the cycle, if the
// cycle saw the pane it is in now.
func (v agentSessionLiveView) location(session AgentSession) (TmuxPaneLocation, bool) {
	located, ok := v.locations[session.ID]
	if !ok || located.pane != session.paneRef() {
		return TmuxPaneLocation{}, false
	}
	return located.location, true
}

// paneLocation is where a status cycle saw a pane sit.
type paneLocation struct {
	pane     TmuxPaneRef
	location TmuxPaneLocation
}

// recoveredAgentSessionStatus is a status recovered from provider-side state.
// It replaces the agent session's hook-driven status only while that status is
// still the one it was recovered from, in the same pane: newer hook evidence
// always wins.
type recoveredAgentSessionStatus struct {
	pane              TmuxPaneRef
	providerSessionID string
	from              AgentSessionStatus
	status            AgentSessionStatus
}

func (r recoveredAgentSessionStatus) appliesTo(session AgentSession) bool {
	return r.pane == session.paneRef() &&
		r.providerSessionID == session.ProviderSessionID &&
		agentSessionStatusesEqual(r.from, session.Status)
}

func agentSessionStatusesEqual(left AgentSessionStatus, right AgentSessionStatus) bool {
	return left.ObservedAt.Equal(right.ObservedAt) &&
		left.RawEventName == right.RawEventName &&
		left.Phase == right.Phase &&
		left.BackgroundWork == right.BackgroundWork
}

// taskStatusView builds a task's Runtime status, and the live agent sessions
// it carries, from its agent session evidence and the last cycle's live view.
func taskStatusView(taskID string, evidence agentSessionEvidence, liveView agentSessionLiveView) *TaskStatusUpdate {
	live := make([]AgentSession, 0, len(evidence.open))
	for _, session := range evidence.open {
		if liveView.isLost(session) {
			continue
		}
		if recovered, ok := liveView.recovered[session.ID]; ok && recovered.appliesTo(session) {
			session.Status = recovered.status
		}
		live = append(live, session)
	}
	update := rollUpTaskStatus(taskID, live, evidence.latest)
	if update == nil || len(live) == 0 {
		return update
	}
	update.AgentSessions = make([]LiveAgentSession, 0, len(live))
	for _, session := range live {
		entry := liveAgentSession(session, evidence.prompts[session.ID])
		if location, ok := liveView.location(session); ok {
			entry.Location = &location
		}
		update.AgentSessions = append(update.AgentSessions, entry)
	}
	return update
}

// liveAgentSession is a live agent session as its task's status carries it,
// with latestPrompt as its newest prompt and no location.
func liveAgentSession(session AgentSession, latestPrompt string) LiveAgentSession {
	return LiveAgentSession{
		ID:           session.ID,
		Provider:     session.Provider,
		Pane:         session.paneRef(),
		Status:       rolledUpAgentSessionStatus(session),
		LatestPrompt: latestPrompt,
	}
}

// conversationHistory returns the Provider session history of an agent
// session's current Provider session: its root transcript and, for Codex, its
// subagents' transcripts, which share the session ID.
func conversationHistory(history []TaskProviderSession, session AgentSession) []TaskProviderSession {
	providerSessionID := strings.TrimSpace(session.ProviderSessionID)
	if providerSessionID == "" {
		return nil
	}
	var conversation []TaskProviderSession
	for _, entry := range history {
		if entry.Provider == session.Provider && strings.TrimSpace(entry.ProviderSessionID) == providerSessionID {
			conversation = append(conversation, entry)
		}
	}
	return conversation
}

// paneProviderStartedAt returns when the newest process running the provider's
// CLI in a pane started, or zero when process evidence has no start time, as
// for a CLI that is the pane's root process.
func paneProviderStartedAt(pane *TmuxPane, commandName string) time.Time {
	var startedAt time.Time
	expectedCommand := filepath.Base(strings.TrimSpace(commandName))
	if pane == nil || expectedCommand == "" {
		return startedAt
	}
	for _, child := range pane.Children {
		activeCommand := filepath.Base(strings.TrimSpace(child.Command))
		if taskSessionCommandsMatch(activeCommand, expectedCommand) && child.StartedAt.After(startedAt) {
			startedAt = child.StartedAt
		}
	}
	return startedAt
}
