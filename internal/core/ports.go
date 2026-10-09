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
// It is TaskService plus the one operation that must run in the foreground
// process. In the active runtime, `rig` gets a composed implementation from
// the taskdaemon adapter:
//   - the TaskService methods are backed by daemon RPC over the local socket
//   - AttachTaskSession is local-only because it attaches this foreground
//     terminal to tmux and cannot be truthfully served by the background daemon
//
// The TUI only knows about this port; it does not know about sockets, daemon
// startup, tmux client mechanics, or in-process service wiring.
type TaskFrontend interface {
	TaskService
	// AttachTaskSession attaches the current terminal to an existing task tmux
	// session for interactive use, landing on pane when it is set. This is
	// intentionally client-local behavior, not part of the daemon socket
	// protocol.
	AttachTaskSession(ctx context.Context, task *Task, pane *TmuxPaneRef) error
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
	AgentID              string
	AgentType            string
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
	NotificationType     string
	// EndReason is why a SessionEnd event's Provider session ended, as the
	// provider reports it, such as Claude's "clear" or "prompt_input_exit".
	EndReason string
	// TmuxPane is the ID of the tmux pane the hook's agent runs in, such as
	// "%44". It and TmuxServer are both empty when the agent runs outside
	// tmux.
	TmuxPane   string
	TmuxServer TmuxServer
	// HookPID is the process ID of the hook command that forwarded the event,
	// or zero when unknown. Its ancestors include the agent process that ran
	// the hook.
	HookPID int
	// BackgroundWork is the provider work still in flight when a turn ended.
	// Only turn-end events carry it.
	BackgroundWork TaskBackgroundWork
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

// TaskService is the task application port. It is the exact operation set the
// daemon socket can serve, so both real adapters implement every method
// honestly: the in-process core service and the daemon socket client used by
// the TUI (via TaskFrontend).
//
// Operations that only one process can perform live elsewhere:
// AttachTaskSession on TaskFrontend (foreground terminal only),
// HandleHookEvent on HookEventHandler and HealthCheck on HealthChecker
// (in-process only, never served over the socket).
type TaskService interface {
	// CreateTaskStream creates a new task and streams progress events followed by
	// exactly one terminal result event (Task or Err). The stream lifetime is
	// owned by ctx; cancelling it stops the stream.
	CreateTaskStream(ctx context.Context, input CreateTaskInput) (<-chan TaskCreateEvent, error)
	// RetryTaskCreationStream resumes a failed task creation from its recorded
	// failed step and streams the same progress milestones as initial creation,
	// followed by exactly one terminal result event.
	RetryTaskCreationStream(ctx context.Context, taskID string) (<-chan TaskCreateEvent, error)
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
	// DeleteTask deletes the task and its local runtime resources while keeping
	// the Git branch.
	DeleteTask(ctx context.Context, taskID string) error
	// ReconnectTaskSession recreates a task's missing Session and restores its
	// open agent sessions there, each resuming its current Provider session
	// with its own provider, and launches the task's launch provider fresh
	// when there is nothing to restore. It does nothing while the Session
	// exists. Once the Session exists, an agent session that cannot be
	// restored doesn't stop the others, and the error names each one.
	ReconnectTaskSession(ctx context.Context, taskID string) error
	// GetProviderSetup returns the user's provider setup, or nil when provider
	// setup has never completed.
	GetProviderSetup(ctx context.Context) (*ProviderSetup, error)
	// SaveProviderSetup validates and persists the user's provider setup after
	// running provider setup checks for every configured provider.
	SaveProviderSetup(ctx context.Context, setup ProviderSetup) error
	// DetectProviders reports which supported providers pass provider setup
	// checks on this machine.
	DetectProviders(ctx context.Context) ([]ProviderDetection, error)
}

// HookEventHandler consumes provider hook events inside the daemon process.
// It is deliberately not part of TaskService: hook events arrive over the
// daemon's loopback hook server, never over the frontend socket.
type HookEventHandler interface {
	// HandleHookEvent resolves and publishes any task status update implied by a
	// provider hook event.
	HandleHookEvent(ctx context.Context, input HookEventInput) error
}

// HealthChecker runs environment diagnostics across task-service
// dependencies. It is deliberately not part of TaskService: doctor runs
// in-process against locally constructed dependencies, never over the
// frontend socket.
type HealthChecker interface {
	HealthCheck(ctx context.Context) ([]HealthCheck, error)
}

// ProviderConfigStore reads and writes the user-level provider setup that
// records which supported providers are configured and which one is the
// default. Provider setup intentionally lives outside the task database.
type ProviderConfigStore interface {
	// GetProviderSetup returns the persisted provider setup, or nil when
	// provider setup has never completed. Implementations apply any validated
	// runtime default-provider override before returning.
	GetProviderSetup(ctx context.Context) (*ProviderSetup, error)
	// SaveProviderSetup validates and atomically persists the provider setup.
	SaveProviderSetup(ctx context.Context, setup ProviderSetup) error
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
	// ListLatestAgentSessionPrompts returns the newest prompt activity of each
	// of a task's open agent sessions. Agent sessions without a prompt have
	// none.
	ListLatestAgentSessionPrompts(ctx context.Context, taskID string) ([]TaskActivityEvent, error)
	// UpsertTaskProviderSession stores a provider session observed for a task.
	UpsertTaskProviderSession(ctx context.Context, session TaskProviderSession) error
	// ListTaskProviderSessions returns provider sessions observed for a task.
	ListTaskProviderSessions(ctx context.Context, taskID string) ([]TaskProviderSession, error)
	// CreateAgentSession stores a newly opened agent session. At most one open
	// agent session may hold a given tmux server and pane.
	CreateAgentSession(ctx context.Context, session AgentSession) error
	// OpenAgentSessionOnPane returns the open agent session on a tmux server's
	// pane, or nil when there is none.
	OpenAgentSessionOnPane(ctx context.Context, server TmuxServer, pane string) (*AgentSession, error)
	// ListOpenAgentSessions returns a task's open agent sessions, oldest first.
	ListOpenAgentSessions(ctx context.Context, taskID string) ([]AgentSession, error)
	// LatestAgentSession returns a task's most recently started agent session,
	// open or ended, or nil when the task has never had one.
	LatestAgentSession(ctx context.Context, taskID string) (*AgentSession, error)
	// UpdateAgentSession stores an open agent session's pane, pane trust,
	// launch time, current Provider session, and status.
	UpdateAgentSession(ctx context.Context, session AgentSession) error
	// EndAgentSession marks an open agent session as ended.
	EndAgentSession(ctx context.Context, agentSessionID string, endedAt time.Time) error
}

// ProviderClient wraps provider-specific behavior behind one application
// contract.
type ProviderClient interface {
	// Doctor verifies that the provider dependency and provider-specific Rig
	// integration are available.
	Doctor(ctx context.Context) error
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
	// RecoverAgentSessionStatus returns a computed replacement for one agent
	// session's stale status when provider-side state holds a newer
	// observation. conversation is the Provider session history of the agent
	// session's current Provider session only: its root transcript and, for
	// Codex, its subagents' transcripts, which share the session ID.
	// providerStartedAt is when the agent's process started, or zero when
	// process evidence does not say.
	RecoverAgentSessionStatus(
		ctx context.Context,
		current AgentSessionStatus,
		conversation []TaskProviderSession,
		providerStartedAt time.Time,
	) (*AgentSessionStatus, error)
	// ReadSessionActivity reads provider-specific user and assistant activity
	// from one provider transcript after the supplied timestamp.
	ReadSessionActivity(
		ctx context.Context,
		session TaskProviderSession,
		after time.Time,
	) ([]TaskActivityEvent, error)
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
	// CreateTaskWorkspaceFromPullRequest fetches a pull request head ref into
	// the task branch before checking it out into the task's worktree path.
	CreateTaskWorkspaceFromPullRequest(ctx context.Context, task *Task, pullRequestNumber int) error
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
	// StartTaskSession creates the task's tmux session and launches the
	// provider's task session launch spec in its task window. It returns the
	// pane it launched in, or a zero pane when tmux did not report one. It
	// returns ErrTaskSessionExists, and types nothing, when the session already
	// exists.
	StartTaskSession(ctx context.Context, task *Task, launch TaskSessionLaunchSpec) (TmuxPaneRef, error)
	// SplitAgentPane splits a new pane into the window of the task's tmux
	// session that holds pane beside, or into its task window when beside is
	// empty, beside the window's other panes, and launches the launch spec's
	// command, without prefilled input, in it. It returns that pane. Agents
	// split in one after another sit side by side in that order. A pane whose
	// launch fails is closed again.
	SplitAgentPane(ctx context.Context, task *Task, beside string, launch TaskSessionLaunchSpec) (TmuxPaneRef, error)
	// AttachTaskSession attaches to an existing task session. When pane is set,
	// the client lands on that pane, in whichever tmux session holds it. A pane
	// that no longer exists, or a pane ID the client's tmux server gives to
	// another pane, falls back to the task session.
	AttachTaskSession(ctx context.Context, task *Task, pane *TmuxPaneRef) error
	// InspectTaskSession returns the current tmux-side runtime state for the
	// task session. Missing sessions are reported as Exists=false.
	InspectTaskSession(ctx context.Context, task *Task) (TaskSessionRuntimeState, error)
	// InspectTaskSessions returns one shared snapshot of the tmux server for
	// the supplied tasks: the panes of each task's Session keyed by task ID,
	// with missing sessions absent, plus the server's identity and every pane
	// on it.
	InspectTaskSessions(ctx context.Context, tasks []*Task) (TmuxSnapshot, error)
	// LocateProcessPane inspects the tmux server once, as InspectTaskSessions
	// does for the supplied task, and returns the pane whose root process is
	// pid or one of its ancestors, with the commands of the processes in
	// between. The pane is empty when pid is not positive or no pane's
	// process tree contains it. Without process evidence the ancestry is
	// unknown and an error is returned.
	LocateProcessPane(ctx context.Context, task *Task, pid int) (TmuxSnapshot, ProcessPane, error)
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
