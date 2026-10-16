package core

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Task is the durable business record for a task.
//
// It intentionally excludes live runtime observations and derived existence
// checks. Those belong in separate runtime/read-side types rather than on the
// core task record itself.
type Task struct {
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	ID        string    `json:"id"`
	// Slug is the stable workspace identifier derived once at task creation from
	// DisplayName and then persisted so branch/worktree/session naming remains
	// stable even if display names collide or later change.
	Slug         string   `json:"slug"`
	Prompt       string   `json:"prompt"`
	DisplayName  string   `json:"display_name"`
	RepoRoot     string   `json:"repo_root"`
	RepoName     string   `json:"repo_name"`
	BranchName   string   `json:"branch_name"`
	WorktreePath string   `json:"worktree_path"`
	TmuxSession  string   `json:"tmux_session"`
	Provider     Provider `json:"provider"`
	// Model and ReasoningLevel are the launch choice made at task creation for
	// the launch provider. Empty means no choice: Rig then passes the provider
	// CLI no flag, so the user's own CLI config applies.
	Model          string `json:"model"`
	ReasoningLevel string `json:"reasoning_level"`
	// CreationStatus tracks whether the durable task has completed its initial
	// workspace/session setup, or whether it can be retried from CreationStep.
	CreationStatus TaskCreationStatus     `json:"creation_status"`
	CreationStep   TaskCreateProgressStep `json:"creation_step"`
	CreationError  string                 `json:"creation_error"`
	// ParentTaskID is the task this task was created from, its parent task,
	// or empty when it has none. It is set only at creation, may be in
	// another repository, and is never changed afterwards, except that
	// deleting the parent clears it. A task's children are the tasks whose
	// ParentTaskID is its ID.
	ParentTaskID string `json:"parent_task_id"`
}

// LaunchChoiceFor returns the task's launch model and reasoning level when
// provider is its launch provider, and nothing for any other provider: the
// choice was made for the launch provider's CLI, so an agent session of
// another provider never gets it.
func (t *Task) LaunchChoiceFor(provider Provider) (model string, reasoningLevel string) {
	if t == nil || t.Provider != provider {
		return "", ""
	}
	return t.Model, t.ReasoningLevel
}

// LaunchChoice is a Launch choice: a Launch model and Launch reasoning level
// made for one provider. Empty fields mean no choice, which leaves each to the
// provider CLI's own default.
type LaunchChoice struct {
	Model          string `json:"model"`
	ReasoningLevel string `json:"reasoning_level"`
}

// IsZero reports whether the choice makes no choice at all.
func (c LaunchChoice) IsZero() bool {
	return c.Model == "" && c.ReasoningLevel == ""
}

type TaskCreationStatus string

const (
	TaskCreationStatusCreating TaskCreationStatus = "creating"
	TaskCreationStatusReady    TaskCreationStatus = "ready"
	TaskCreationStatusFailed   TaskCreationStatus = "failed"
)

type RepoContext struct {
	// Root is the canonical absolute path to the repository root on disk.
	Root       string `json:"root"`
	Name       string `json:"name"`
	BaseBranch string `json:"base_branch"`
}

// KnownRepo is a Known repository: one Rig can create a task in by name,
// because a task is in it or it sits directly inside a repo directory.
type KnownRepo struct {
	// Name is the repository's name: its tasks' repository name, or else
	// the base name of its root.
	Name string `json:"name"`
	// Root is the absolute path to the repository's main checkout.
	Root string `json:"root"`
	// TaskCount is the number of existing tasks in the repository.
	TaskCount int `json:"task_count"`
}

type TaskSuggestion struct {
	Name       string `json:"name"`
	BranchType string `json:"branch_type"`
	// Ticket references the issue or ticket the task works on, such as
	// "GH #18" or "ABC-123". Empty when the task is not about one.
	Ticket string `json:"ticket"`
}

// DisplayName leads the suggested name with the ticket reference, so a task
// working on a ticket stays recognizable by it. With no description of the
// work, the ticket reference alone names the task.
func (s TaskSuggestion) DisplayName() string {
	ticket := strings.TrimSpace(s.Ticket)
	name := strings.TrimSpace(s.Name)
	switch {
	case ticket == "":
		return name
	case name == "":
		return ticket
	case strings.EqualFold(name, ticket) || strings.HasPrefix(strings.ToLower(name), strings.ToLower(ticket)+" "):
		return name
	default:
		return ticket + " " + name
	}
}

var validBranchTypes = map[string]bool{
	"feat":     true,
	"fix":      true,
	"chore":    true,
	"refactor": true,
	"docs":     true,
	"test":     true,
	"style":    true,
	"perf":     true,
	"ci":       true,
	"build":    true,
}

func (s TaskSuggestion) BranchTypeOrDefault() string {
	if s.BranchType != "" && validBranchTypes[s.BranchType] {
		return s.BranchType
	}
	return "feat"
}

type WorkspaceBootstrapSpec struct {
	Files []WorkspaceBootstrapFile
}

type WorkspaceBootstrapFile struct {
	Path     string
	Content  []byte
	FileMode os.FileMode
	// Merge, when non-nil, integrates this file into an existing destination
	// instead of overwriting it: the workspace manager calls it with the
	// current file contents and writes the result, preserving content the
	// provider does not own. When the destination does not exist, Content is
	// written as-is.
	Merge func(existing []byte) ([]byte, error)
}

// TaskStatusPhase is the small application-facing runtime status model used by
// the first hook-driven status stream slice.
type TaskStatusPhase string

const (
	TaskStatusPhaseStarting TaskStatusPhase = "starting"
	TaskStatusPhaseWorking  TaskStatusPhase = "working"
	// TaskStatusPhaseWaitingForInput means the root agent ended its turn, or
	// asks for the user's approval or answer. A turn end is the user's move
	// even while the turn's background work runs, so that work only rides
	// along as the status's TaskBackgroundWork.
	TaskStatusPhaseWaitingForInput TaskStatusPhase = "waiting_for_input"
	TaskStatusPhaseStopped         TaskStatusPhase = "stopped"
)

// TaskBackgroundWork counts the provider background work still in flight when
// the root agent ended its turn, while it waits for input. Providers report
// the full in-flight set at every turn end, so the counts are replaced, never
// accumulated.
type TaskBackgroundWork struct {
	Subagents int `json:"subagents,omitempty"`
	Shells    int `json:"shells,omitempty"`
	Monitors  int `json:"monitors,omitempty"`
	Workflows int `json:"workflows,omitempty"`
	Other     int `json:"other,omitempty"`
}

func (w TaskBackgroundWork) Total() int {
	return w.Subagents + w.Shells + w.Monitors + w.Workflows + w.Other
}

func (w TaskBackgroundWork) IsZero() bool {
	return w.Total() == 0
}

// TaskStatusUpdate is the live status message published by the observer
// process. It is intentionally separate from the durable Task record.
type TaskStatusUpdate struct {
	ObservedAt     time.Time          `json:"observed_at"`
	TaskID         string             `json:"task_id"`
	RawEventName   string             `json:"raw_event_name"`
	Provider       Provider           `json:"provider"`
	Phase          TaskStatusPhase    `json:"phase"`
	BackgroundWork TaskBackgroundWork `json:"background_work"`
	// LeadAgentSessionID is the live agent session the task's status comes
	// from: its most urgent one. It is empty while none is live.
	LeadAgentSessionID string `json:"lead_agent_session_id,omitempty"`
	// AgentSessions are the task's live agent sessions, oldest first.
	AgentSessions []LiveAgentSession `json:"agent_sessions,omitempty"`
}

// LeadAgentSession returns the live agent session the task's status comes
// from, or nil when none is live.
func (u *TaskStatusUpdate) LeadAgentSession() *LiveAgentSession {
	if u == nil || u.LeadAgentSessionID == "" {
		return nil
	}
	for i := range u.AgentSessions {
		if u.AgentSessions[i].ID == u.LeadAgentSessionID {
			return &u.AgentSessions[i]
		}
	}
	return nil
}

// LiveAgentSession is one live agent session as its task's status update
// carries it.
type LiveAgentSession struct {
	ID       string   `json:"id"`
	Provider Provider `json:"provider"`
	// Pane is the tmux pane the agent runs in.
	Pane TmuxPaneRef `json:"pane"`
	// Location is where the pane sat at the last tmux inspection, or nil
	// before one has seen it.
	Location *TmuxPaneLocation `json:"location,omitempty"`
	// Status is the agent session's status as the task's status was derived
	// from it: recovered from provider-side state when that applies, and
	// starting while the agent session has none yet.
	Status AgentSessionStatus `json:"status"`
	// LatestPrompt is the newest prompt submitted to the agent, or empty when
	// there is none.
	LatestPrompt string `json:"latest_prompt,omitempty"`
	// Name is the short name of the agent's current conversation, or empty
	// while that has none.
	Name string `json:"name,omitempty"`
}

// TmuxPaneRef identifies one tmux pane: its server and its pane ID, such as
// "%44". Pane IDs are unique only while their server runs, so a pane is
// identified by both.
type TmuxPaneRef struct {
	Server TmuxServer `json:"server"`
	ID     string     `json:"id"`
}

// TmuxPaneLocation is where a tmux pane sits: its window's index and name,
// and its index in that window. Windows can be renamed and moved, so a
// location is only as current as the inspection that found it.
type TmuxPaneLocation struct {
	WindowName  string `json:"window_name"`
	WindowIndex int    `json:"window_index"`
	PaneIndex   int    `json:"pane_index"`
}

// TaskSessionRuntimeState is the current tmux-side state of a task session.
type TaskSessionRuntimeState struct {
	Exists bool
}

// TmuxServer identifies one running tmux server. Pane IDs are unique only
// while their server runs and restart at %0 on a new server, so a pane is
// identified by its server and pane ID together.
type TmuxServer struct {
	SocketPath string `json:"socket_path"`
	PID        int    `json:"pid"`
}

// IsZero reports whether the server is unknown, as for an agent running
// outside tmux.
func (s TmuxServer) IsZero() bool {
	return s.SocketPath == "" && s.PID == 0
}

// TmuxPane is one pane of a tmux server inventory.
type TmuxPane struct {
	// ID is the server-unique pane ID, such as "%44".
	ID string
	// Session is the normalized name of a tmux session the pane is in, as Rig
	// normalizes task Session names. A pane linked into several sessions,
	// such as through grouped sessions or linked windows, is listed once,
	// under a task Session when one of them is.
	Session    string
	WindowName string
	// Command is the pane's foreground process title, which some provider
	// CLIs rewrite.
	Command string
	// Children are the direct children of the pane's root process, typically
	// the CLI the pane shell is running.
	Children    []PaneProcess
	WindowIndex int
	PaneIndex   int
	// PID is the pane's root process, or zero when tmux did not report it.
	PID                             int
	ChildProcessEvidenceUnavailable bool
}

// PaneProcess is one direct child of a tmux pane's root process. StartedAt
// is zero when ps reported no parseable elapsed time.
type PaneProcess struct {
	StartedAt time.Time
	Command   string
}

// TmuxSnapshot is one shared inspection of the tmux server that hosts task
// Sessions.
type TmuxSnapshot struct {
	// TaskSessionPanes lists the IDs of every pane in each inspected task's
	// Session, across all its windows, keyed by task ID. Tasks whose Session
	// is missing are absent.
	TaskSessionPanes map[string][]string
	// Server is zero when the server's identity is unknown: no server is
	// running, it has no panes, or tmux did not expand socket_path and pid.
	// Zero never means "another server".
	Server TmuxServer
	// Panes lists every pane on the server once, in tmux's order.
	Panes []TmuxPane
}

// ProcessPane is the tmux pane a process runs in: the pane whose root process
// is the process or one of its ancestors.
type ProcessPane struct {
	// Pane is the pane's ID, or empty when no pane's process tree contains the
	// process.
	Pane string
	// Chain lists the command of every process from the process up to and
	// including the pane's root process, innermost first. An agent nested in
	// another agent, such as a codex exec run by Claude, puts two agent CLIs on
	// the chain.
	Chain []string
}

// AgentSession is one agent process, such as a Claude or Codex CLI, running in
// one tmux pane for a task. Rig records it when it launches the agent, or else
// from the first hook event it can place in the pane, and keeps the agent's
// current Provider session and its hook-driven status.
type AgentSession struct {
	// StartedAt is when the agent session started, which orders a task's agent
	// sessions.
	StartedAt time.Time
	// EndedAt is zero while the agent session is open.
	EndedAt time.Time
	// LaunchedAt is when Rig launched the agent in its current pane. The first
	// hook event traced to that pane clears it, as the agent then runs, and it
	// is zero for an agent session a hook event opened. While it is set, a
	// status cycle gives the agent agentLaunchGrace to start.
	LaunchedAt time.Time
	ID         string
	TaskID     string
	Provider   Provider
	// TmuxServer and TmuxPane place the agent. Pane IDs are unique only within
	// their server, so they identify a pane together. Both are empty for an
	// agent session without a pane: one Reconnect is restoring, or one an
	// upgrade made from a task's last conversation.
	TmuxServer TmuxServer
	TmuxPane   string
	// ProviderSessionID is the agent's current Provider session: the
	// conversation it is running now, which Reconnect resumes. /clear and
	// /resume replace it.
	ProviderSessionID string
	Status            AgentSessionStatus
	// Name is the agent's short name, such as "GH #124 Task CLI", derived
	// like a task's name from the first prompt of a conversation. It is empty
	// until the agent is named.
	Name string
	// NamedProviderSessionID is the Provider session Name names: the one
	// whose first prompt gave it, or one that went on from that, as a resume
	// or a fork does. A conversation the agent starts afresh, as after
	// /clear, is not it, so its first prompt renames the agent.
	NamedProviderSessionID string
	// PaneTrusted reports whether a hook process was traced to the pane
	// through the process tree. An untrusted pane was bound by the Codex
	// fallback, because a Codex agent using the shared daemon runs its hooks
	// outside the pane.
	PaneTrusted bool
}

// IsOpen reports whether the agent session has not ended.
func (s AgentSession) IsOpen() bool {
	return s.EndedAt.IsZero()
}

// paneRef is the pane the agent session runs in, with an empty ID while it
// has none.
func (s AgentSession) paneRef() TmuxPaneRef {
	return TmuxPaneRef{Server: s.TmuxServer, ID: s.TmuxPane}
}

// launching reports whether Rig launched the agent less than agentLaunchGrace
// before now and no hook event from its pane has arrived since.
func (s AgentSession) launching(now time.Time) bool {
	return !s.LaunchedAt.IsZero() && now.Sub(s.LaunchedAt) < agentLaunchGrace
}

// AgentSessionStatus is an agent session's latest hook-driven runtime status.
// Phase is empty until an event of its current Provider session maps to one.
type AgentSessionStatus struct {
	ObservedAt     time.Time          `json:"observed_at"`
	RawEventName   string             `json:"raw_event_name"`
	Phase          TaskStatusPhase    `json:"phase"`
	BackgroundWork TaskBackgroundWork `json:"background_work"`
}

type TaskActivityRole string

const (
	TaskActivityRoleUser      TaskActivityRole = "user"
	TaskActivityRoleAssistant TaskActivityRole = "assistant"
)

// TaskActivityEvent is the compact persisted read model used by the detail
// panel to show the last human prompt and recent LLM actions for a task.
type TaskActivityEvent struct {
	ObservedAt time.Time `json:"observed_at"`
	TaskID     string    `json:"task_id"`
	// AgentSessionID is the agent session whose hook event this activity came
	// from. It is empty for activity Rig could not place in an agent session
	// and for activity recovered from transcripts.
	AgentSessionID string           `json:"agent_session_id,omitempty"`
	TurnID         string           `json:"turn_id"`
	EventName      string           `json:"event_name"`
	Role           TaskActivityRole `json:"role"`
	Text           string           `json:"text"`
}

type TaskProviderSession struct {
	FirstObservedAt   time.Time `json:"first_observed_at"`
	LastObservedAt    time.Time `json:"last_observed_at"`
	TaskID            string    `json:"task_id"`
	Provider          Provider  `json:"provider"`
	ProviderSessionID string    `json:"provider_session_id"`
	TranscriptPath    string    `json:"transcript_path"`
	StartSource       string    `json:"start_source"`
	LastEventName     string    `json:"last_event_name"`
	// Model is the latest model the conversation switched to, or else the one
	// it started with.
	Model string `json:"model"`
	Cwd   string `json:"cwd"`
	// ModelSwitch marks an observed event that switched the conversation to
	// Model, which then replaces the stored model unless a newer event was
	// stored. Any other event's model only fills a missing one. It is never
	// stored, so a stored Provider session never has it set.
	ModelSwitch bool `json:"-"`
}

type SessionTokenUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CachedInputTokens        int `json:"cached_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	ReasoningOutputTokens    int `json:"reasoning_output_tokens"`
	TotalTokens              int `json:"total_tokens"`
}

func (u SessionTokenUsage) IsZero() bool {
	return u.InputTokens == 0 &&
		u.OutputTokens == 0 &&
		u.CachedInputTokens == 0 &&
		u.CacheCreationInputTokens == 0 &&
		u.ReasoningOutputTokens == 0 &&
		u.TotalTokens == 0
}

type TaskTokenUsage struct {
	SessionCount             int `json:"session_count"`
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CachedInputTokens        int `json:"cached_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	ReasoningOutputTokens    int `json:"reasoning_output_tokens"`
	TotalTokens              int `json:"total_tokens"`
}

func (u TaskTokenUsage) IsZero() bool {
	return u.SessionCount == 0 &&
		u.InputTokens == 0 &&
		u.OutputTokens == 0 &&
		u.CachedInputTokens == 0 &&
		u.CacheCreationInputTokens == 0 &&
		u.ReasoningOutputTokens == 0 &&
		u.TotalTokens == 0
}

// TaskUsage is what a task's agents have used: the token usage summed across
// the task's Provider sessions, how many times each live Agent session's
// context was compacted, how full each one's context window is, and the model
// each one runs on.
type TaskUsage struct {
	// Tokens is nil when no transcript reports token usage.
	Tokens *TaskTokenUsage `json:"tokens,omitempty"`
	// AgentCompactions holds, by Agent session ID, the compactions of each
	// open Agent session whose current Provider session was compacted.
	AgentCompactions map[string]Compactions `json:"agent_compactions,omitempty"`
	// AgentContext holds, by Agent session ID, the context usage of each open
	// Agent session whose current Provider session records it.
	AgentContext map[string]ContextUsage `json:"agent_context,omitempty"`
	// AgentModels holds, by Agent session ID, the model and reasoning level
	// each open Agent session runs on, as its current Provider session
	// records them.
	AgentModels map[string]AgentModel `json:"agent_models,omitempty"`
}

// AgentModel is the model and reasoning level an agent runs on: those of its
// conversation's latest turn, which follow a /model switch, or before its
// first turn the model its hooks reported.
type AgentModel struct {
	// Model is the provider's own ID for the model, such as claude-opus-5-5.
	Model string `json:"model"`
	// ReasoningLevel is empty when the conversation does not record it.
	ReasoningLevel string `json:"reasoning_level,omitempty"`
}

// ContextUsage is how full a conversation's context window is.
type ContextUsage struct {
	// Tokens is what the agent's latest request put in its context. After a
	// compaction it is Codex's own count of what the compaction left, while a
	// Claude agent's context is unknown until its next request.
	Tokens int `json:"tokens"`
	// Window is the model's context window size in tokens, or 0 when it is
	// unknown.
	Window int `json:"window,omitempty"`
}

// Compactions counts how many times a conversation's context was compacted:
// summarized to free room in the context window, losing detail. Auto and
// Manual split the total by what triggered each compaction where the provider
// records it, which Codex does not.
type Compactions struct {
	Total  int `json:"total"`
	Auto   int `json:"auto,omitempty"`
	Manual int `json:"manual,omitempty"`
}

// ConversationUsage is what one conversation's transcript records about its
// root agent: how many times its context was compacted, how full it is, and
// the model it runs on.
type ConversationUsage struct {
	Compactions Compactions
	// Context is nil when the transcript does not record it yet.
	Context *ContextUsage
	// Model and ReasoningLevel are the latest turn's, so they follow a /model
	// switch. Before the first turn Model is the one the conversation's hooks
	// reported, and either is empty when nothing records it.
	Model          string
	ReasoningLevel string
}

// Provider identifies the supported interactive runtime backing a task.
type Provider string

const (
	ProviderCodex  Provider = "codex"
	ProviderClaude Provider = "claude"
)

// Canonical provider hook event names. HookEventInput.EventName carries these
// values across the provider seam: provider adapters declare which of them
// they observe (and forward them by name), and task observation consumes them
// for agent sessions, their status, and activity.
const (
	HookEventSessionStart      = "SessionStart"
	HookEventSessionEnd        = "SessionEnd"
	HookEventUserPromptSubmit  = "UserPromptSubmit"
	HookEventPreToolUse        = "PreToolUse"
	HookEventPostToolUse       = "PostToolUse"
	HookEventStop              = "Stop"
	HookEventStopFailure       = "StopFailure"
	HookEventNotification      = "Notification"
	HookEventPermissionRequest = "PermissionRequest"
	HookEventSubagentStart     = "SubagentStart"
	// HookEventPostModelSwitch reports that the agent's conversation moved to
	// another model, which HookEventInput.Model carries.
	HookEventPostModelSwitch = "PostModelSwitch"
)

// SupportedProviders returns every provider Rig knows how to integrate with,
// in stable display order. Supported is not the same as configured: a provider
// becomes usable only after the user enables it during provider setup.
func SupportedProviders() []Provider {
	return []Provider{ProviderCodex, ProviderClaude}
}

func IsSupportedProvider(provider Provider) bool {
	for _, supported := range SupportedProviders() {
		if provider == supported {
			return true
		}
	}
	return false
}

// ProviderSetup is the user-level provider configuration produced by provider
// setup. It lives in user config, never in the task database.
type ProviderSetup struct {
	Configured []Provider `json:"configured"`
	Default    Provider   `json:"default"`
}

func (s ProviderSetup) IsConfigured(provider Provider) bool {
	for _, configured := range s.Configured {
		if configured == provider {
			return true
		}
	}
	return false
}

func (s ProviderSetup) Validate() error {
	if len(s.Configured) == 0 {
		return fmt.Errorf("provider setup requires at least one configured provider")
	}
	seen := make(map[Provider]bool, len(s.Configured))
	for _, provider := range s.Configured {
		if !IsSupportedProvider(provider) {
			return fmt.Errorf("provider %q is not a supported provider", provider)
		}
		if seen[provider] {
			return fmt.Errorf("provider %q is configured more than once", provider)
		}
		seen[provider] = true
	}
	if s.Default == "" {
		return fmt.Errorf("provider setup requires a default provider")
	}
	if !s.IsConfigured(s.Default) {
		return fmt.Errorf("default provider %q is not a configured provider", s.Default)
	}
	return nil
}

// ProviderDetection reports whether one supported provider passed Rig's
// provider setup checks on this machine.
type ProviderDetection struct {
	Provider Provider `json:"provider"`
	Ready    bool     `json:"ready"`
	Detail   string   `json:"detail,omitempty"`
}

// ProviderModel is one model a provider offers, as its CLI reports it at
// runtime. Rig keeps no list of models or reasoning levels of its own.
type ProviderModel struct {
	// ID is the value the provider CLI's model flag takes.
	ID string `json:"id"`
	// ResolvedID is the model ID that ID stands for when ID is an alias, as
	// the provider's transcripts and hooks name the model, or empty when the
	// provider does not say.
	ResolvedID  string `json:"resolved_id,omitempty"`
	DisplayName string `json:"display_name"`
	// ReasoningLevels are the reasoning levels the model accepts, in provider
	// order. Empty means the model takes no reasoning level.
	ReasoningLevels []string `json:"reasoning_levels,omitempty"`
}

// TaskSessionLaunchSpec is the handoff from a provider client to the tmux
// session client for starting an interactive task session.
//
// This is not a domain object. It is an application-facing integration DTO that
// describes how the tmux adapter should start the provider's CLI.
type TaskSessionLaunchSpec struct {
	// Command is the argv launched in the agent's tmux pane, for example
	// []string{"codex"}.
	Command []string
	// ReadyMarker is the terminal prompt marker emitted by the provider when it is
	// ready to receive interactive input. The tmux session client waits for this
	// marker before typing PrefillInput into the window.
	ReadyMarker string
	// PrefillInput is the text typed into the interactive provider after the
	// command has started and the ReadyMarker has appeared. For create-task,
	// this is the drafted task prompt that is placed into the fresh task
	// session without being submitted.
	PrefillInput []string
	// SubmitPrefill submits PrefillInput once it is typed, as pressing Enter
	// would, so the agent starts on it at once. Core sets it only for an
	// initial task creation that asked to submit its prompt; retrying a
	// failed creation and Reconnect never set it.
	SubmitPrefill bool
}
