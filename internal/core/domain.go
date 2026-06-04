package core

import (
	"os"
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
	// CreationStatus tracks whether the durable task has completed its initial
	// workspace/session setup, or whether it can be retried from CreationStep.
	CreationStatus TaskCreationStatus     `json:"creation_status"`
	CreationStep   TaskCreateProgressStep `json:"creation_step"`
	CreationError  string                 `json:"creation_error"`
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

type TaskSuggestion struct {
	Name       string `json:"name"`
	BranchType string `json:"branch_type"`
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
}

// TaskStatusPhase is the small application-facing runtime status model used by
// the first hook-driven status stream slice.
type TaskStatusPhase string

const (
	TaskStatusPhaseStarting        TaskStatusPhase = "starting"
	TaskStatusPhaseWorking         TaskStatusPhase = "working"
	TaskStatusPhaseWaitingForInput TaskStatusPhase = "waiting_for_input"
	TaskStatusPhaseStopped         TaskStatusPhase = "stopped"
)

// TaskStatusUpdate is the live status message published by the observer
// process. It is intentionally separate from the durable Task record.
type TaskStatusUpdate struct {
	ObservedAt   time.Time       `json:"observed_at"`
	TaskID       string          `json:"task_id"`
	RawEventName string          `json:"raw_event_name"`
	Provider     Provider        `json:"provider"`
	Phase        TaskStatusPhase `json:"phase"`
}

// TaskSessionRuntimeState is the current tmux-side state of a task session.
type TaskSessionRuntimeState struct {
	ActiveCommands []string
	Exists         bool
}

type TaskActivityRole string

const (
	TaskActivityRoleUser      TaskActivityRole = "user"
	TaskActivityRoleAssistant TaskActivityRole = "assistant"
)

// TaskActivityEvent is the compact persisted read model used by the detail
// panel to show the last human prompt and recent LLM actions for a task.
type TaskActivityEvent struct {
	ObservedAt time.Time        `json:"observed_at"`
	TaskID     string           `json:"task_id"`
	TurnID     string           `json:"turn_id"`
	EventName  string           `json:"event_name"`
	Role       TaskActivityRole `json:"role"`
	Text       string           `json:"text"`
}

// TaskResumeMetadata is the minimal provider runtime state needed to reconnect
// a task session after its tmux session has been lost.
type TaskResumeMetadata struct {
	ObservedAt time.Time `json:"observed_at"`
	TaskID     string    `json:"task_id"`
	SessionID  string    `json:"session_id"`
	Provider   Provider  `json:"provider"`
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
	Model             string    `json:"model"`
	Cwd               string    `json:"cwd"`
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

// Provider identifies the supported interactive runtime backing a task.
type Provider string

const (
	ProviderCodex  Provider = "codex"
	ProviderClaude Provider = "claude"
)

// TaskSessionLaunchSpec is the handoff from a provider client to the tmux
// session client for starting an interactive task session.
//
// This is not a domain object. It is an application-facing integration DTO that
// describes how the tmux adapter should start the provider's CLI.
type TaskSessionLaunchSpec struct {
	// Command is the argv launched in the task's task tmux window, for example
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
}
