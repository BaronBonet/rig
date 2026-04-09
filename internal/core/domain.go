package core

import "time"

type Config struct {
	Provider string
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
	HookRuntimePhaseReady          HookRuntimePhase = "ready"
	HookRuntimePhasePrompted       HookRuntimePhase = "prompted"
	HookRuntimePhaseRunningCommand HookRuntimePhase = "running_command"
	HookRuntimePhaseIdle           HookRuntimePhase = "idle"
	HookRuntimePhaseFinished       HookRuntimePhase = "finished"
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

type DisplayState struct {
	Primary  DisplayStatus   `json:"primary"`
	Activity DisplayActivity `json:"activity"`
}

type Task struct {
	CreatedAt             time.Time    `json:"created_at"`
	UpdatedAt             time.Time    `json:"updated_at"`
	LastReconciledAt      time.Time    `json:"last_reconciled_at"`
	RuntimeStateUpdatedAt time.Time    `json:"runtime_state_updated_at"`
	ID                    string       `json:"id"`
	Prompt                string       `json:"prompt"`
	DisplayName           string       `json:"display_name"`
	Slug                  string       `json:"slug"`
	RepoRoot              string       `json:"repo_root"`
	RepoName              string       `json:"repo_name"`
	BaseBranch            string       `json:"base_branch"`
	BranchName            string       `json:"branch_name"`
	WorktreePath          string       `json:"worktree_path"`
	TmuxSession           string       `json:"tmux_session"`
	AgentWindowName       string       `json:"agent_window_name"`
	EditorWindowName      string       `json:"editor_window_name"`
	Provider              string       `json:"provider"`
	Status                TaskStatus   `json:"status"`
	RuntimeState          RuntimeState `json:"runtime_state"`
	LastError             string       `json:"last_error"`
	WorktreeExists        bool         `json:"worktree_exists"`
	BranchExists          bool         `json:"branch_exists"`
	SessionExists         bool         `json:"session_exists"`
	AgentWindowExists     bool         `json:"agent_window_exists"`
	EditorWindowExists    bool         `json:"editor_window_exists"`
}

type HookSessionSummary struct {
	StartedAt             time.Time
	LastActivityAt        time.Time
	LastStopAt            time.Time
	TaskID                string
	SessionID             string
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

type TaskView struct {
	Task        *Task
	HookSession *HookSessionSummary
}

type TaskProgressStep string

const (
	TaskProgressNaming           TaskProgressStep = "naming"
	TaskProgressNameSelected     TaskProgressStep = "name_selected"
	TaskProgressWorktreeCreating TaskProgressStep = "worktree_creating"
	TaskProgressWorkspaceSeeding TaskProgressStep = "workspace_seeding"
	TaskProgressWorkspaceSeeded  TaskProgressStep = "workspace_seeded"
	TaskProgressTmuxStarting     TaskProgressStep = "tmux_starting"
	TaskProgressAgentLaunching   TaskProgressStep = "agent_launching"
	TaskProgressTaskCreated      TaskProgressStep = "task_created"
	TaskProgressSessionOpening   TaskProgressStep = "session_opening"
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
