package core

import "time"

type Config struct {
	Provider AgentProvider
}

type TaskStatus string

const (
	TaskStatusCreating TaskStatus = "creating"
	TaskStatusReady    TaskStatus = "ready"
	TaskStatusRunning  TaskStatus = "running"
	TaskStatusDegraded TaskStatus = "degraded"
	TaskStatusBroken   TaskStatus = "broken"
	TaskStatusCleaned  TaskStatus = "cleaned"
)

func (s TaskStatus) IsTerminal() bool {
	return s == TaskStatusBroken || s == TaskStatusCleaned
}

type RuntimeState string

const (
	RuntimeStateNone       RuntimeState = ""
	RuntimeStateRunning    RuntimeState = "running"
	RuntimeStateNeedsInput RuntimeState = "needs_input"
	RuntimeStateFinished   RuntimeState = "finished"
)

type HookRuntimePhase string

const (
	HookRuntimePhaseReady             HookRuntimePhase = "ready"
	HookRuntimePhasePrompted          HookRuntimePhase = "prompted"
	HookRuntimePhaseRunningCommand    HookRuntimePhase = "running_command"
	HookRuntimePhaseWaitingPermission HookRuntimePhase = "waiting_permission"
	HookRuntimePhaseIdle              HookRuntimePhase = "idle"
	HookRuntimePhaseFinished          HookRuntimePhase = "finished"
)

type DisplayStatus string

const (
	DisplayStatusFinished     DisplayStatus = "finished"
	DisplayStatusNeedsInput   DisplayStatus = "needs_input"
	DisplayStatusWorking      DisplayStatus = "working"
	DisplayStatusDisconnected DisplayStatus = "disconnected"
)

type DisplayActivity string

const (
	DisplayActivityNone    DisplayActivity = ""
	DisplayActivityCommand DisplayActivity = "command"
)

type PRState string

const (
	PRStateNone   PRState = ""
	PRStateOpen   PRState = "open"
	PRStateDraft  PRState = "draft"
	PRStateMerged PRState = "merged"
	PRStateClosed PRState = "closed"
)

type PRStatus struct {
	State  PRState
	Number int
}

type RepoPullRequest struct {
	Number          int
	Title           string
	BranchName      string
	State           PRState
	HasExistingTask bool
}

type DisplayState struct {
	Primary  DisplayStatus   `json:"primary"`
	Activity DisplayActivity `json:"activity"`
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

type HookSessionSummary struct {
	StartedAt             time.Time
	LastActivityAt        time.Time
	LastStopAt            time.Time
	LastPromptSubmittedAt time.Time
	TaskID                string
	SessionID             string
	Provider              string
	Model                 string
	Cwd                   string
	TranscriptPath        string
	StartSource           string
	CurrentTurnID         string
	LastEventName         string
	RuntimePhase          HookRuntimePhase
	LastPromptText        string
	LastCommandText       string
	LastCommandResultText string
	LastAssistantMessage  string
	CommandCount          int
}

type ObserverSummary struct {
	TaskID                string
	DisplayStatus         DisplayStatus
	DisplayActivity       DisplayActivity
	ProcessAlive          bool
	LastRuntimeObservedAt time.Time
}

type ObserverTaskUpdate struct {
	TaskID          string              `json:"task_id"`
	Provider        string              `json:"provider,omitempty"`
	DisplayStatus   DisplayStatus       `json:"display_status"`
	DisplayActivity DisplayActivity     `json:"display_activity"`
	LastActivityAt  time.Time           `json:"last_activity_at"`
	HookSession     *HookSessionSummary `json:"hook_session,omitempty"`
}

type HookEvent struct {
	OccurredAt           time.Time
	ID                   int64
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
}

type HookEventInput struct {
	OccurredAt           time.Time
	TaskID               string
	SessionID            string
	TurnID               string
	EventName            string
	Provider             string
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

type SessionTokenUsage struct {
	InputTokens              int
	OutputTokens             int
	CachedInputTokens        int
	CacheCreationInputTokens int
	ReasoningOutputTokens    int
	TotalTokens              int
}

func (u SessionTokenUsage) IsZero() bool {
	return u.InputTokens == 0 &&
		u.OutputTokens == 0 &&
		u.CachedInputTokens == 0 &&
		u.CacheCreationInputTokens == 0 &&
		u.ReasoningOutputTokens == 0 &&
		u.TotalTokens == 0
}

type TaskView struct {
	Task        *Task
	HookSession *HookSessionSummary
	Observer    *ObserverSummary
	PR          *PRStatus
	TokenUsage  *SessionTokenUsage
}

type TaskProgressStep string

const (
	TaskProgressNaming             TaskProgressStep = "naming"
	TaskProgressNameSelected       TaskProgressStep = "name_selected"
	TaskProgressWorktreeCreating   TaskProgressStep = "worktree_creating"
	TaskProgressWorkspaceSeeding   TaskProgressStep = "workspace_seeding"
	TaskProgressWorkspaceSeeded    TaskProgressStep = "workspace_seeded"
	TaskProgressTmuxStarting       TaskProgressStep = "tmux_starting"
	TaskProgressAgentLaunching     TaskProgressStep = "agent_launching"
	TaskProgressTaskCreated        TaskProgressStep = "task_created"
	TaskProgressSetupScriptRunning TaskProgressStep = "setup_script_running"
	TaskProgressSessionOpening     TaskProgressStep = "session_opening"
)

type TaskProgress struct {
	Task    *Task
	Step    TaskProgressStep
	Message string
}

type RuntimeSnapshot struct {
	SessionName       string
	WindowName        string
	PaneID            string
	HadAgentBinding   bool
	ForegroundCommand string
	Content           string
	ObservedAt        time.Time
	LastOutputAt      time.Time
}
