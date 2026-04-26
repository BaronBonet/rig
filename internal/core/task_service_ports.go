package core

import (
	"context"
	"net/http"
	"time"
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
	State  PRState `json:"state"`
	Number int     `json:"number"`
}

type RepoPullRequest struct {
	Title           string  `json:"title"`
	BranchName      string  `json:"branch_name"`
	State           PRState `json:"state"`
	Number          int     `json:"number"`
	HasExistingTask bool    `json:"has_existing_task"`
}

type CreateTaskSource struct {
	PullRequest *RepoPullRequest `json:"pull_request,omitempty"`
}

type CreateTaskInput struct {
	Source   CreateTaskSource `json:"source"`
	Cwd      string           `json:"cwd"`
	Prompt   string           `json:"prompt"`
	Provider Provider         `json:"provider"`
}

type TaskCreateProgressStep string

const (
	TaskCreateProgressSuggestingName     TaskCreateProgressStep = "suggesting_name"
	TaskCreateProgressCreatingWorktree   TaskCreateProgressStep = "creating_worktree"
	TaskCreateProgressPreparingWorkspace TaskCreateProgressStep = "preparing_workspace"
	TaskCreateProgressStartingSession    TaskCreateProgressStep = "starting_session"
)

type TaskCreateProgressEvent struct {
	Step TaskCreateProgressStep `json:"step"`
}

type TaskCreateProgressReporter interface {
	ReportTaskCreateProgress(step TaskCreateProgressStep)
}

type TaskCreateEvent struct {
	Err      error                    `json:"-"`
	Progress *TaskCreateProgressEvent `json:"progress,omitempty"`
	Task     *Task                    `json:"task,omitempty"`
}

// TaskFrontend is the frontend-facing task application port used by the TUI.
// In the active runtime, `rig` gets a composed implementation from the
// taskdaemon adapter:
//   - most methods are backed by daemon RPC over the local socket
//   - AttachTaskSession is local-only because it attaches this foreground
//     terminal to tmux and cannot be truthfully served by the background daemon
//
// The TUI only knows about this port; it does not know about sockets, daemon
// startup, tmux client mechanics, or in-process service wiring.
type TaskFrontend interface {
	// AttachTaskSession attaches the current terminal to an existing task tmux
	// session for interactive use. This is intentionally client-local behavior,
	// not part of the daemon socket protocol.
	AttachTaskSession(ctx context.Context, task *Task) error
	// GetTaskActivity returns recent persisted activity events for the selected
	// task detail view, ordered oldest-to-newest within the requested window.
	GetTaskActivity(ctx context.Context, taskID string, limit int) ([]TaskActivityEvent, error)
	// GetTaskTokenUsage returns the summed token usage across provider sessions
	// observed for the selected task.
	GetTaskTokenUsage(ctx context.Context, taskID string) (*TaskTokenUsage, error)
	// ListRepoPullRequests lists pull requests for the repository that contains
	// cwd and annotates whether each PR branch already has a local Rig
	// workspace.
	ListRepoPullRequests(ctx context.Context, cwd string) ([]RepoPullRequest, error)
	// PullRequestStatus returns the pull request state for a repository branch,
	// or PRStateNone when no pull request is open or known for that branch.
	PullRequestStatus(ctx context.Context, repoRoot string, branchName string) (*PRStatus, error)
	// ReconnectTaskSession recreates a missing task runtime session from
	// persisted provider resume metadata.
	ReconnectTaskSession(ctx context.Context, taskID string) error
	// CreateTaskStream creates a new task and streams progress events followed by
	// one terminal result event.
	CreateTaskStream(ctx context.Context, input CreateTaskInput) (<-chan TaskCreateEvent, error)
	// DeleteTask deletes the task and its local runtime resources while keeping
	// the Git branch.
	DeleteTask(ctx context.Context, taskID string) error
	// ListTasks returns all known tasks for the frontend to render.
	ListTasks(ctx context.Context) ([]*Task, error)
	// LatestTaskStatus returns the latest published live status for a task, or
	// nil when no status has been published yet.
	LatestTaskStatus(ctx context.Context, taskID string) (*TaskStatusUpdate, error)
	// SubscribeTaskStatus subscribes to live status updates for a task. The
	// subscription lifetime is owned by ctx; cancelling it removes the
	// subscription and closes the update channel.
	SubscribeTaskStatus(ctx context.Context, taskID string) (<-chan TaskStatusUpdate, error)
}

// TaskDaemonHookRoute describes one provider hook endpoint the local task
// daemon should expose alongside its frontend socket transport.
type TaskDaemonHookRoute struct {
	Handler http.Handler
	Path    string
}

type TaskDaemonStatus struct {
	SocketPath string
	Error      string
	Running    bool
	Healthy    bool
	Compatible bool
}

// TODO: is all of this information in the struct required?
type HookEventInput struct {
	OccurredAt           time.Time
	TaskID               string
	SessionID            string
	TurnID               string
	EventName            string
	Provider             Provider
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

// TaskDaemon is the application port for the local daemon-backed task
// frontend subsystem.
//
// Frontend returns the daemon-backed TaskFrontend client that the TUI uses to
// talk to the backend over the local socket.
//
// EnsureRunning, Stop, Restart, and Status are lifecycle operations used by
// composition code to manage the long-lived daemon process.
//
// Serve runs the daemon-side transports in the current process. This is used
// only by the re-executed daemon child process, not by the TUI client path.
type TaskDaemon interface {
	Frontend() TaskFrontend
	EnsureRunning(ctx context.Context) error
	Stop(ctx context.Context) error
	Restart(ctx context.Context) error
	Status(ctx context.Context) (*TaskDaemonStatus, error)
	Serve(ctx context.Context, service TaskService, hookRoutes []TaskDaemonHookRoute, stop func()) error
}

type TaskService interface {
	// HealthCheck runs environment diagnostics across task-service dependencies.
	HealthCheck(ctx context.Context) ([]HealthCheck, error)
	// CreateTaskWithProgress creates a new task while reporting coarse-grained
	// creation milestones to the provided reporter when non-nil.
	CreateTaskWithProgress(
		ctx context.Context,
		input CreateTaskInput,
		reporter TaskCreateProgressReporter,
	) (*Task, error)
	// ListRepoPullRequests lists pull requests for the repository that contains
	// cwd and annotates whether each PR branch already has a local Rig
	// workspace.
	ListRepoPullRequests(ctx context.Context, cwd string) ([]RepoPullRequest, error)
	// PullRequestStatus returns the pull request state for a repository branch,
	// or PRStateNone when no pull request is open or known for that branch.
	PullRequestStatus(ctx context.Context, repoRoot string, branchName string) (*PRStatus, error)
	// GetTaskActivity returns recent persisted activity events for the selected
	// task detail view, ordered oldest-to-newest within the requested window.
	GetTaskActivity(ctx context.Context, taskID string, limit int) ([]TaskActivityEvent, error)
	// GetTaskTokenUsage returns the summed token usage across provider sessions
	// observed for the selected task.
	GetTaskTokenUsage(ctx context.Context, taskID string) (*TaskTokenUsage, error)
	// ListTasks returns all known tasks.
	ListTasks(ctx context.Context) ([]*Task, error)
	// LatestTaskStatus returns the latest published live status for a task, or
	// nil when no status has been published yet.
	LatestTaskStatus(ctx context.Context, taskID string) (*TaskStatusUpdate, error)
	// SubscribeTaskStatus subscribes to live status updates for a task. The
	// subscription lifetime is owned by ctx; cancelling it removes the
	// subscription and closes the update channel.
	SubscribeTaskStatus(ctx context.Context, taskID string) (<-chan TaskStatusUpdate, error)
	// HandleHookEvent resolves and publishes any task status update implied by a
	// provider hook event.
	HandleHookEvent(ctx context.Context, input HookEventInput) error
	// DeleteTask deletes the task and its local runtime resources while keeping
	// the Git branch.
	DeleteTask(ctx context.Context, taskID string) error
	// ReconnectTaskSession recreates a missing task runtime session from
	// persisted provider resume metadata.
	ReconnectTaskSession(ctx context.Context, taskID string) error
}

// TaskRepository persists task records and returns their durable state.
type TaskRepository interface {
	// HealthCheck verifies that the repository backend is reachable and its
	// storage-level consistency checks pass.
	HealthCheck(ctx context.Context) error
	// CreateTask stores a newly created task record.
	CreateTask(ctx context.Context, task *Task) error
	// DeleteTask removes a persisted task record.
	DeleteTask(ctx context.Context, taskID string) error
	// UpdateTask persists changes to an existing task record.
	UpdateTask(ctx context.Context, task *Task) error
	// ListTasks returns all known tasks.
	ListTasks(ctx context.Context) ([]*Task, error)
	// RecordTaskActivity persists a compact activity event for the task detail
	// view.
	RecordTaskActivity(ctx context.Context, event TaskActivityEvent) error
	// GetTaskActivity returns recent persisted activity events for the selected
	// task detail view, ordered oldest-to-newest within the requested window.
	GetTaskActivity(ctx context.Context, taskID string, limit int) ([]TaskActivityEvent, error)
	// UpsertTaskStatus stores the latest known live status for a task.
	UpsertTaskStatus(ctx context.Context, update TaskStatusUpdate) error
	// UpsertTaskResumeMetadata stores the latest reconnect metadata for a task.
	UpsertTaskResumeMetadata(ctx context.Context, metadata TaskResumeMetadata) error
	// UpsertTaskProviderSession stores a provider session observed for a task.
	UpsertTaskProviderSession(ctx context.Context, session TaskProviderSession) error
	// LatestTaskStatus returns the latest known live status for a task, or nil
	// when no status has been recorded yet.
	LatestTaskStatus(ctx context.Context, taskID string) (*TaskStatusUpdate, error)
	// LatestTaskResumeMetadata returns the latest known reconnect metadata for a
	// task, or nil when none has been recorded yet.
	LatestTaskResumeMetadata(ctx context.Context, taskID string) (*TaskResumeMetadata, error)
	// ListTaskProviderSessions returns provider sessions observed for a task.
	ListTaskProviderSessions(ctx context.Context, taskID string) ([]TaskProviderSession, error)
	// SubscribeTaskStatus subscribes to live status updates for a task. The
	// subscription lifetime is owned by ctx; cancelling it removes the
	// subscription and closes the update channel.
	SubscribeTaskStatus(ctx context.Context, taskID string) (<-chan TaskStatusUpdate, error)
}

// ProviderClient wraps provider-specific behavior behind one application
// contract.
type ProviderClient interface {
	// HealthCheck verifies that the provider dependency is available.
	HealthCheck(ctx context.Context) error
	// SuggestTaskName derives a task display name and branch type from a prompt.
	SuggestTaskName(ctx context.Context, prompt string) (TaskSuggestion, error)
	// EnsureTaskSessionEnvironment applies any provider-specific runtime
	// configuration required before launching or resuming an interactive session.
	EnsureTaskSessionEnvironment(ctx context.Context) error
	// BuildWorkspaceBootstrapSpec describes the provider-specific files that
	// should be written into the task workspace before launch.
	BuildWorkspaceBootstrapSpec(task *Task) (WorkspaceBootstrapSpec, error)
	// BuildTaskSessionLaunchSpec describes how the provider's CLI should be
	// started inside the task's tmux session.
	BuildTaskSessionLaunchSpec(task *Task) (TaskSessionLaunchSpec, error)
	// BuildReconnectTaskSessionLaunchSpec describes how the provider's CLI
	// should resume an existing logical session inside a recreated tmux session.
	BuildReconnectTaskSessionLaunchSpec(task *Task, sessionID string) (TaskSessionLaunchSpec, error)
	// TaskSessionCommandName returns the foreground process name expected while
	// the provider is running in the task tmux pane.
	TaskSessionCommandName() string
	// HookEventToTaskStatus normalizes a provider hook event into a task status
	// update when the event contributes to the live task status stream.
	HookEventToTaskStatus(input HookEventInput) (*TaskStatusUpdate, error)
	// ReadSessionTokenUsage reads provider-specific token usage from one
	// provider transcript.
	ReadSessionTokenUsage(ctx context.Context, transcriptPath string) (*SessionTokenUsage, error)
}

// GitWorktreeClient manages the Git worktree operations needed by the new task
// service during task creation.
type GitWorktreeClient interface {
	// HealthCheck verifies that Git is available for worktree operations.
	HealthCheck(ctx context.Context) error
	// DetectRepo resolves the canonical repository root, display name, and base
	// branch for the working directory where task creation was requested.
	DetectRepo(ctx context.Context, cwd string) (RepoContext, error)
	// IsBranchUsedByWorktree reports whether the target branch is already checked
	// out by another Git worktree, which prevents creating a duplicate task
	// workspace for the same branch.
	IsBranchUsedByWorktree(ctx context.Context, repoRoot string, branchName string) (bool, error)
	// CreateTaskWorkspace creates a new Git worktree for a task by creating the
	// task branch from the repository base branch and checking it out into the
	// task's worktree path.
	CreateTaskWorkspace(ctx context.Context, task *Task) error
	// CreateTaskWorkspaceFromBranch creates a task worktree by checking out an
	// already existing branch, such as a branch associated with a pull request.
	CreateTaskWorkspaceFromBranch(ctx context.Context, task *Task) error
	// RemoveTaskWorkspace deletes a task worktree while keeping its branch.
	RemoveTaskWorkspace(ctx context.Context, task *Task) error
}

// PullRequestClient lists repository pull requests through an external
// provider such as GitHub.
type PullRequestClient interface {
	// HealthCheck verifies that the pull-request provider is available.
	HealthCheck(ctx context.Context) error
	// ListRepoPullRequests lists open and draft pull requests for the canonical
	// repository root.
	ListRepoPullRequests(ctx context.Context, repoRoot string) ([]RepoPullRequest, error)
	// CheckPullRequestStatus returns pull request state for a branch in the
	// canonical repository root.
	CheckPullRequestStatus(ctx context.Context, repoRoot string, branchName string) (*PRStatus, error)
}

// TmuxSessionClient manages tmux session lifecycle for a task.
type TmuxSessionClient interface {
	// HealthCheck verifies that tmux is available for task sessions.
	HealthCheck(ctx context.Context) error
	// StartTaskSession starts the runtime session for a task using the provider's
	// task session launch spec.
	StartTaskSession(ctx context.Context, task *Task, launch TaskSessionLaunchSpec) error
	// AttachTaskSession attaches to an existing task session.
	AttachTaskSession(ctx context.Context, task *Task) error
	// InspectTaskSession returns the current tmux-side runtime state for the
	// task session. Missing sessions are reported as Exists=false.
	InspectTaskSession(ctx context.Context, task *Task) (TaskSessionRuntimeState, error)
	// DeleteTaskSession tears down the task session during task deletion.
	DeleteTaskSession(ctx context.Context, task *Task) error
}

// TaskWorkspaceManager applies repo-local setup and provider bootstrap files
// after a worktree has been created and before the task session is launched.
type TaskWorkspaceManager interface {
	// SetupTaskWorkspace loads repo configuration and applies any optional
	// repo-local workspace setup needed for the task.
	SetupTaskWorkspace(ctx context.Context, task *Task, repoRoot string) error
	// BootstrapTaskWorkspace writes the provider-specific bootstrap files needed
	// to launch the interactive task session inside the task workspace.
	BootstrapTaskWorkspace(ctx context.Context, task *Task, bootstrapSpec WorkspaceBootstrapSpec) error
}
