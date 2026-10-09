package core

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestResolveAgentSessionLiveness_DecisionTable pins the agent session
// liveness decision as pure values: no fakes, no goroutines, no I/O. An open
// agent session is one input alongside the tmux snapshot's view of its pane
// and the task's Session.
func TestResolveAgentSessionLiveness_DecisionTable(t *testing.T) {
	server := TmuxServer{SocketPath: "/private/tmp/tmux-501/default", PID: 4722}
	session := AgentSession{ID: "agent-1", Provider: ProviderCodex, TmuxServer: server, TmuxPane: "%1"}
	now := time.Date(2026, time.October, 9, 10, 0, 0, 0, time.UTC)
	// launched is the agent session of an agent Rig launched the given time
	// before now, from whose pane no hook event has come yet.
	launched := func(ago time.Duration) AgentSession {
		launchedSession := session
		launchedSession.LaunchedAt = now.Add(-ago)
		return launchedSession
	}
	pane := func(command string, children ...string) *TmuxPane {
		found := &TmuxPane{ID: "%1", Command: command}
		for _, child := range children {
			found.Children = append(found.Children, PaneProcess{Command: child})
		}
		return found
	}
	withoutChildEvidence := func(found *TmuxPane) *TmuxPane {
		found.ChildProcessEvidenceUnavailable = true
		return found
	}

	cases := []struct {
		name            string
		session         AgentSession
		sessionExists   bool
		pane            *TmuxPane
		server          TmuxServer
		providerCommand string
		want            agentSessionLiveness
	}{
		{
			name:            "Session gone keeps the agent session for Reconnect",
			session:         session,
			pane:            pane("codex"),
			server:          server,
			providerCommand: "codex",
			want:            agentSessionLost,
		},
		{
			name:            "unknown snapshot server cannot tell",
			session:         session,
			sessionExists:   true,
			providerCommand: "codex",
			want:            agentSessionUnchanged,
		},
		{
			name:            "pane on a restarted tmux server is gone",
			session:         session,
			sessionExists:   true,
			pane:            pane("codex"),
			server:          TmuxServer{SocketPath: server.SocketPath, PID: 9001},
			providerCommand: "codex",
			want:            agentSessionEnded,
		},
		{
			name:            "pane closed while the Session lives",
			session:         session,
			sessionExists:   true,
			server:          server,
			providerCommand: "codex",
			want:            agentSessionEnded,
		},
		{
			name:            "agent session without a pane ends once the Session is seen alive",
			session:         AgentSession{ID: "agent-1", Provider: ProviderCodex},
			sessionExists:   true,
			server:          server,
			providerCommand: "codex",
			want:            agentSessionEnded,
		},
		{
			name:            "foreground provider is live",
			session:         session,
			sessionExists:   true,
			pane:            pane("codex"),
			server:          server,
			providerCommand: "codex",
			want:            agentSessionLive,
		},
		{
			name:            "provider as the pane shell's child is live",
			session:         session,
			sessionExists:   true,
			pane:            pane("zsh", "codex"),
			server:          server,
			providerCommand: "codex",
			want:            agentSessionLive,
		},
		{
			name:            "agent session recorded without a server identity matches by pane",
			session:         AgentSession{ID: "agent-1", Provider: ProviderCodex, TmuxPane: "%1"},
			sessionExists:   true,
			pane:            pane("codex"),
			server:          server,
			providerCommand: "codex",
			want:            agentSessionLive,
		},
		{
			name:            "provider under a suffixed process title still counts",
			session:         session,
			sessionExists:   true,
			pane:            pane("codex-aarch64-a"),
			server:          server,
			providerCommand: "codex",
			want:            agentSessionLive,
		},
		{
			name:            "unrelated command sharing a prefix without dash does not count",
			session:         session,
			sessionExists:   true,
			pane:            pane("codexish"),
			server:          server,
			providerCommand: "codex",
			want:            agentSessionEnded,
		},
		{
			name:            "agent exited back to the shell",
			session:         session,
			sessionExists:   true,
			pane:            pane("zsh"),
			server:          server,
			providerCommand: "codex",
			want:            agentSessionEnded,
		},
		{
			name:            "missing child process evidence cannot prove the agent exited",
			session:         session,
			sessionExists:   true,
			pane:            withoutChildEvidence(pane("zsh")),
			server:          server,
			providerCommand: "codex",
			want:            agentSessionUnchanged,
		},
		{
			name:            "foreground provider proves liveness despite missing child evidence",
			session:         session,
			sessionExists:   true,
			pane:            withoutChildEvidence(pane("codex")),
			server:          server,
			providerCommand: "codex",
			want:            agentSessionLive,
		},
		{
			name:            "empty provider command never matches",
			session:         session,
			sessionExists:   true,
			pane:            pane("codex"),
			server:          server,
			providerCommand: "",
			want:            agentSessionEnded,
		},
		{
			name:            "an agent just launched into the pane's shell is launching",
			session:         launched(3 * time.Second),
			sessionExists:   true,
			pane:            pane("zsh"),
			server:          server,
			providerCommand: "codex",
			want:            agentSessionLaunching,
		},
		{
			name:            "an agent just launched is launching while its pane is missing",
			session:         launched(3 * time.Second),
			sessionExists:   true,
			server:          server,
			providerCommand: "codex",
			want:            agentSessionLaunching,
		},
		{
			name:            "an agent just launched is launching without child process evidence",
			session:         launched(3 * time.Second),
			sessionExists:   true,
			pane:            withoutChildEvidence(pane("zsh")),
			server:          server,
			providerCommand: "codex",
			want:            agentSessionLaunching,
		},
		{
			name:            "an agent just launched that runs is live",
			session:         launched(3 * time.Second),
			sessionExists:   true,
			pane:            pane("zsh", "codex"),
			server:          server,
			providerCommand: "codex",
			want:            agentSessionLive,
		},
		{
			name:            "the launch grace ends: a pane still running only its shell ended",
			session:         launched(agentLaunchGrace),
			sessionExists:   true,
			pane:            pane("zsh"),
			server:          server,
			providerCommand: "codex",
			want:            agentSessionEnded,
		},
		{
			name:            "a just-launched agent whose Session is gone is lost",
			session:         launched(3 * time.Second),
			pane:            pane("zsh"),
			server:          server,
			providerCommand: "codex",
			want:            agentSessionLost,
		},
		{
			name:            "a just-launched agent on a restarted tmux server is gone",
			session:         launched(3 * time.Second),
			sessionExists:   true,
			pane:            pane("zsh"),
			server:          TmuxServer{SocketPath: server.SocketPath, PID: 9001},
			providerCommand: "codex",
			want:            agentSessionEnded,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, resolveAgentSessionLiveness(
				tc.session,
				tc.sessionExists,
				tc.pane,
				tc.server,
				tc.providerCommand,
				now,
			))
		})
	}
}

// TestRollUpTaskStatus_DecisionTable pins how a task's Runtime status follows
// its most urgent live agent session.
func TestRollUpTaskStatus_DecisionTable(t *testing.T) {
	at := func(minute int) time.Time {
		return time.Date(2026, time.October, 9, 10, minute, 0, 0, time.UTC)
	}
	agent := func(id string, provider Provider, phase TaskStatusPhase, minute int) AgentSession {
		return AgentSession{
			ID:        id,
			Provider:  provider,
			StartedAt: at(0),
			Status: AgentSessionStatus{
				ObservedAt:   at(minute),
				RawEventName: string(phase) + "-event",
				Phase:        phase,
			},
		}
	}
	// lead is the agent session the task's status comes from.
	update := func(lead string, provider Provider, phase TaskStatusPhase, rawEventName string, minute int) *TaskStatusUpdate {
		return &TaskStatusUpdate{
			TaskID:             "task-1",
			Provider:           provider,
			Phase:              phase,
			RawEventName:       rawEventName,
			ObservedAt:         at(minute),
			LeadAgentSessionID: lead,
		}
	}
	background := agent("agent-bg", ProviderCodex, TaskStatusPhaseWorkingInBackground, 9)
	background.Status.BackgroundWork = TaskBackgroundWork{Subagents: 2}
	starting := agent("agent-new", ProviderClaude, "", 0)
	starting.StartedAt = at(8)
	starting.Status = AgentSessionStatus{}
	ended := agent("agent-old", ProviderCodex, TaskStatusPhaseWorking, 3)

	cases := []struct {
		name   string
		live   []AgentSession
		latest *AgentSession
		want   *TaskStatusUpdate
	}{
		{
			name: "never had an agent session",
			want: nil,
		},
		{
			name:   "no live agent session is stopped",
			latest: &ended,
			want:   update("", ProviderCodex, TaskStatusPhaseStopped, "TaskSessionStopped", 3),
		},
		{
			name: "needs input outranks working",
			live: []AgentSession{
				agent("agent-a", ProviderClaude, TaskStatusPhaseWorking, 5),
				agent("agent-b", ProviderCodex, TaskStatusPhaseWaitingForInput, 1),
			},
			want: update("agent-b", ProviderCodex, TaskStatusPhaseWaitingForInput, "waiting_for_input-event", 1),
		},
		{
			name: "working outranks working in background",
			live: []AgentSession{
				background,
				agent("agent-a", ProviderClaude, TaskStatusPhaseWorking, 2),
			},
			want: update("agent-a", ProviderClaude, TaskStatusPhaseWorking, "working-event", 2),
		},
		{
			name: "working in background outranks starting",
			live: []AgentSession{starting, background},
			want: &TaskStatusUpdate{
				TaskID:             "task-1",
				Provider:           ProviderCodex,
				Phase:              TaskStatusPhaseWorkingInBackground,
				RawEventName:       "working_in_background-event",
				ObservedAt:         at(9),
				BackgroundWork:     TaskBackgroundWork{Subagents: 2},
				LeadAgentSessionID: "agent-bg",
			},
		},
		{
			name: "an agent session without a status counts as starting",
			live: []AgentSession{starting},
			want: update("agent-new", ProviderClaude, TaskStatusPhaseStarting, "", 8),
		},
		{
			name: "ties go to the most recent status change",
			live: []AgentSession{
				agent("agent-a", ProviderClaude, TaskStatusPhaseWaitingForInput, 4),
				agent("agent-b", ProviderCodex, TaskStatusPhaseWaitingForInput, 6),
				agent("agent-c", ProviderClaude, TaskStatusPhaseWaitingForInput, 5),
			},
			want: update("agent-b", ProviderCodex, TaskStatusPhaseWaitingForInput, "waiting_for_input-event", 6),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, rollUpTaskStatus("task-1", tc.live, tc.latest))
		})
	}
}

// TestResolveSessionEnd_DecisionTable pins what a SessionEnd event does to the
// agent session it was placed in.
func TestResolveSessionEnd_DecisionTable(t *testing.T) {
	cases := []struct {
		name     string
		provider Provider
		reason   string
		current  string
		event    string
		want     sessionEndOutcome
	}{
		{
			name:     "Claude /clear keeps the agent session for its next conversation",
			provider: ProviderClaude,
			reason:   "clear",
			current:  "sess-a",
			event:    "sess-a",
			want:     sessionEndKept,
		},
		{
			name:     "Claude /resume keeps the agent session for the resumed conversation",
			provider: ProviderClaude,
			reason:   "resume",
			current:  "sess-a",
			event:    "sess-a",
			want:     sessionEndKept,
		},
		{
			name:     "Claude exiting at the prompt ends the agent session",
			provider: ProviderClaude,
			reason:   "prompt_input_exit",
			current:  "sess-a",
			event:    "sess-a",
			want:     sessionEndEnded,
		},
		{
			name:     "Claude logging out ends the agent session",
			provider: ProviderClaude,
			reason:   "logout",
			current:  "sess-a",
			event:    "sess-a",
			want:     sessionEndEnded,
		},
		{
			name:     "any other Claude reason ends the agent session",
			provider: ProviderClaude,
			reason:   "other",
			current:  "sess-a",
			event:    "sess-a",
			want:     sessionEndEnded,
		},
		{
			name:     "a Claude reason Rig does not know ends the agent session",
			provider: ProviderClaude,
			reason:   "bypass_permissions_disabled",
			current:  "sess-a",
			event:    "sess-a",
			want:     sessionEndEnded,
		},
		{
			name:     "Claude exiting another conversation than the current one is ignored",
			provider: ProviderClaude,
			reason:   "prompt_input_exit",
			current:  "sess-b",
			event:    "sess-a",
			want:     sessionEndIgnored,
		},
		{
			name:     "Codex ending the current conversation ends the agent session",
			provider: ProviderCodex,
			reason:   "other",
			current:  "codex-a",
			event:    "codex-a",
			want:     sessionEndEnded,
		},
		{
			name:     "Codex ending a conversation the agent left after /new is ignored",
			provider: ProviderCodex,
			reason:   "other",
			current:  "codex-b",
			event:    "codex-a",
			want:     sessionEndIgnored,
		},
		{
			name:     "only Claude keeps an agent session for /clear",
			provider: ProviderCodex,
			reason:   "clear",
			current:  "codex-a",
			event:    "codex-a",
			want:     sessionEndEnded,
		},
		{
			name:     "an event without a conversation is ignored",
			provider: ProviderCodex,
			reason:   "other",
			current:  "",
			event:    "",
			want:     sessionEndIgnored,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, resolveSessionEnd(tc.provider, tc.reason, tc.current, tc.event))
		})
	}
}
