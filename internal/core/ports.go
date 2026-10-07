package core

import (
	"context"
	"net/http"
	"strings"
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
	// Workspace requests a folder Task in Cwd. Empty creates a worktree Task
	// when Cwd is inside a Git worktree and a folder Task otherwise.
	Workspace WorkspaceKind `json:"workspace,omitempty"`
	// ProviderEnv is the requesting rig window's provider configuration; the
	// Task keeps it for every provider it launches.
	ProviderEnv ProviderEnv `json:"provider_env,omitempty"`
	// Model and Effort are the launch options the new Task's provider starts
	// with; empty means the provider's default.
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
}

// Launch returns the launch options the input asks for.
func (i CreateTaskInput) Launch() LaunchOptions {
	return LaunchOptions{Model: strings.TrimSpace(i.Model), Effort: strings.TrimSpace(i.Effort)}
}

// NewTaskSessionInput starts a fresh provider session in an existing Task: a
// new conversation with a small context, in the same workspace and tmux
// session, instead of compacting a long one. Prompt is typed into the new
// session; with Handoff, a note written from the previous session is
// referenced from it.
type NewTaskSessionInput struct {
	TaskID string `json:"task_id"`
	Prompt string `json:"prompt"`
	// Provider is the provider to start; empty keeps the Task's active one.
	Provider Provider `json:"provider,omitempty"`
	Model    string   `json:"model,omitempty"`
	Effort   string   `json:"effort,omitempty"`
	// Handoff asks the previous session of the Task's active provider for a
	// handoff note before the new session starts. It is skipped quietly when
	// the Task has no previous session.
	Handoff bool `json:"handoff,omitempty"`
	// NewWindow starts the session alongside the Task's running one, as a
	// child Task in a new window of the Task's tmux session, instead of
	// replacing it in the Task's own window.
	NewWindow bool `json:"new_window,omitempty"`
}

type TaskCreateProgressStep string

const (
	TaskCreateProgressSuggestingName     TaskCreateProgressStep = "suggesting_name"
	TaskCreateProgressCreatingWorktree   TaskCreateProgressStep = "creating_worktree"
	TaskCreateProgressPreparingWorkspace TaskCreateProgressStep = "preparing_workspace"
	TaskCreateProgressStartingSession    TaskCreateProgressStep = "starting_session"
	// TaskCreateProgressWritingHandoff is a new-session milestone: the
	// previous session is writing its handoff note.
	TaskCreateProgressWritingHandoff TaskCreateProgressStep = "writing_handoff"
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
	// session for interactive use. This is intentionally client-local behavior,
	// not part of the daemon socket protocol.
	AttachTaskSession(ctx context.Context, task *Task) error
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
	// ListTaskWorktrees returns the existing worktrees the task's provider
	// sessions have edited, most recently edited first, each with the branch it
	// has checked out now and the branch it had at the task's last edit there.
	ListTaskWorktrees(ctx context.Context, taskID string) ([]TaskWorktree, error)
	// ListImportableSessions returns the configured providers' sessions that
	// were started in folder or below it and do not belong to a task yet, most
	// recently active first, read from the session stores env points at.
	ListImportableSessions(ctx context.Context, folder string, env ProviderEnv) ([]ProviderSessionSummary, error)
	// ImportSession makes a provider session started outside Rig a folder task
	// that runs with env, the configuration the session was listed from, and
	// resumes it in the task's own session. When the task is created but its
	// session fails to start, both the task and the error are returned.
	ImportSession(ctx context.Context, session ProviderSessionSummary, env ProviderEnv) (*Task, error)
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
	// ReconnectTaskSession recreates a missing task runtime session from
	// persisted provider resume metadata.
	ReconnectTaskSession(ctx context.Context, taskID string) error
	// ShelveTask takes the task and its child tasks off the current list. Their
	// provider sessions end and everything else is kept, so opening the task
	// later resumes its latest session. It refuses while any of them is
	// working.
	ShelveTask(ctx context.Context, taskID string) error
	// UnshelveTask puts the task and its child tasks back on the current list.
	// Their sessions start again when they are opened.
	UnshelveTask(ctx context.Context, taskID string) error
	// RenameTask gives the task a new name and drops its original ask, so new
	// sessions are not briefed on what it was first for.
	RenameTask(ctx context.Context, taskID string, name string) error
	// GetProviderSetup returns the user's provider setup, or nil when provider
	// setup has never completed.
	GetProviderSetup(ctx context.Context) (*ProviderSetup, error)
	// SaveProviderSetup validates and persists the user's provider setup after
	// running provider setup checks for every configured provider.
	SaveProviderSetup(ctx context.Context, setup ProviderSetup) error
	// DetectProviders reports which supported providers pass provider setup
	// checks on this machine.
	DetectProviders(ctx context.Context) ([]ProviderDetection, error)
	// SwitchTaskProvider makes another configured provider the task's active
	// provider by launching it in the existing task workspace. It refuses while
	// the current provider process is still running and only records the new
	// active provider after the new provider launches successfully.
	SwitchTaskProvider(ctx context.Context, taskID string, provider Provider) (*Task, error)
	// NewTaskSessionStream starts a fresh provider session in an existing
	// task, optionally after writing a handoff note from the previous one,
	// and streams progress events followed by exactly one terminal result
	// event. It refuses while a provider is still running in the task session.
	NewTaskSessionStream(ctx context.Context, input NewTaskSessionInput) (<-chan TaskCreateEvent, error)
	// GetLaunchSettings returns every configured provider's launch options and
	// the options each was last launched with.
	GetLaunchSettings(ctx context.Context) (*LaunchSettings, error)
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
	// GetLaunchDefaults returns the launch options each provider was last
	// launched with; nil when none were recorded.
	GetLaunchDefaults(ctx context.Context) (map[Provider]LaunchOptions, error)
	// SaveLaunchDefaults records the launch options a provider was last
	// launched with, keeping the rest of the config.
	SaveLaunchDefaults(ctx context.Context, provider Provider, options LaunchOptions) error
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
	// UpsertTaskWorktreeRecord stores the latest observation of a worktree the
	// task has edited, replacing any earlier one for the same worktree.
	UpsertTaskWorktreeRecord(ctx context.Context, record TaskWorktreeRecord) error
	// ListTaskWorktreeRecords returns every stored worktree observation for a
	// task.
	ListTaskWorktreeRecords(ctx context.Context, taskID string) ([]TaskWorktreeRecord, error)
	// SubscribeTaskStatus subscribes to live status updates for a task. The
	// subscription lifetime is owned by ctx; cancelling it removes the
	// subscription and closes the update channel.
	SubscribeTaskStatus(ctx context.Context, taskID string) (<-chan TaskStatusUpdate, error)
}

// ProviderClient wraps provider-specific behavior behind one application
// contract.
type ProviderClient interface {
	// Doctor verifies that the provider dependency and provider-specific Rig
	// integration are available.
	Doctor(ctx context.Context) error
	// SuggestTaskName derives a task display name and branch type from a
	// prompt, running the provider with env.
	SuggestTaskName(ctx context.Context, prompt string, env ProviderEnv) (TaskSuggestion, error)
	// EnsureTaskSessionEnvironment applies any provider-specific runtime
	// configuration required before launching or resuming an interactive
	// session that runs with env.
	EnsureTaskSessionEnvironment(ctx context.Context, env ProviderEnv) error
	// BuildWorkspaceBootstrapSpec describes the provider-specific files that
	// should be written into the task workspace before launch.
	BuildWorkspaceBootstrapSpec(task *Task) (WorkspaceBootstrapSpec, error)
	// LaunchOptions lists the models and efforts the provider's CLI accepts,
	// and whether it can write a handoff note from a previous session.
	LaunchOptions() ProviderLaunchOptions
	// WriteSessionHandoff asks a previous provider session of the task, in
	// print mode and without touching the task's interactive session, for a
	// handoff note a fresh session can start from, and returns the path it
	// wrote the note to. focus, when not empty, is what the next session will
	// be asked to do, so the note covers what it needs for that.
	// ErrHandoffUnsupported when the provider cannot.
	WriteSessionHandoff(ctx context.Context, task *Task, session TaskProviderSession, focus string) (string, error)
	// BuildTaskSessionLaunchSpec describes how the provider's CLI should be
	// started inside the task's tmux session, with the task's ProviderEnv and
	// launch options.
	BuildTaskSessionLaunchSpec(task *Task) (TaskSessionLaunchSpec, error)
	// BuildReconnectTaskSessionLaunchSpec describes how the provider's CLI
	// should resume an existing logical session inside a recreated tmux session.
	BuildReconnectTaskSessionLaunchSpec(task *Task, sessionID string) (TaskSessionLaunchSpec, error)
	// TaskSessionCommandName returns the foreground process name expected while
	// the provider is running in the task tmux pane.
	TaskSessionCommandName() string
	// ExitCommand is the command that, typed at the provider's idle prompt,
	// ends its interactive session.
	ExitCommand() string
	// HookEventToTaskStatus normalizes a provider hook event into a task status
	// update when the event contributes to the live task status stream.
	HookEventToTaskStatus(input HookEventInput) (*TaskStatusUpdate, error)
	// RecoverLatestTaskStatus returns a computed replacement for a stale latest
	// task status when provider-side state contains a newer observation.
	// providerStartedAt is when the provider process running in the task
	// session started, or zero when process evidence does not say.
	RecoverLatestTaskStatus(
		ctx context.Context,
		current TaskStatusUpdate,
		sessions []TaskProviderSession,
		providerStartedAt time.Time,
	) (*TaskStatusUpdate, error)
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
	// ReadSessionTitle reads the title the user gave one provider session,
	// empty when they gave none. A title the provider generated is not one.
	ReadSessionTitle(ctx context.Context, transcriptPath string) (string, error)
	// ReadSessionFileChanges reads the file edits one provider session made,
	// including edits made by its subagents, as absolute paths.
	ReadSessionFileChanges(ctx context.Context, session TaskProviderSession) ([]SessionFileChange, error)
	// ListFolderSessions lists up to limit (all when limit <= 0) provider
	// sessions started in folder or any folder below it, most recently active
	// first, from the provider's
	// own session store, the one env points at. Each session's Cwd is the
	// folder it was started in, where it must be resumed.
	ListFolderSessions(ctx context.Context, folder string, limit int, env ProviderEnv) ([]ProviderSessionSummary, error)
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
	// task branch from baseRef, or from the branch the main checkout has checked
	// out when baseRef is empty, and checking it out into the task's worktree
	// path.
	CreateTaskWorkspace(ctx context.Context, task *Task, baseRef string) error
	// ResolveBaseRef fetches baseBranch from origin and returns the ref new task
	// branches start from: origin/<baseBranch>, or the local branch when the
	// fetch fails, so task creation still works offline.
	ResolveBaseRef(ctx context.Context, repoRoot string, baseBranch string) (string, error)
	// CreateTaskWorkspaceFromBranch creates a task worktree by checking out an
	// already existing branch, such as a branch associated with a pull request.
	CreateTaskWorkspaceFromBranch(ctx context.Context, task *Task) error
	// CreateTaskWorkspaceFromPullRequest fetches a pull request head ref into
	// the task branch before checking it out into the task's worktree path.
	CreateTaskWorkspaceFromPullRequest(ctx context.Context, task *Task, pullRequestNumber int) error
	// RemoveTaskWorkspace deletes a task worktree while keeping its branch.
	RemoveTaskWorkspace(ctx context.Context, task *Task) error
	// WorktreeRootOf returns the root of the Git worktree containing dir, or ""
	// when dir does not exist or is not inside a Git worktree. It reads only the
	// filesystem, so it is cheap enough to call once per edited directory.
	WorktreeRootOf(dir string) string
	// InspectWorktree reports the repository and the branch checked out now for
	// the worktree rooted at root, or nil when root is no longer a worktree.
	InspectWorktree(ctx context.Context, root string) (*WorktreeRef, error)
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
	// task session launch spec. It launches the command only; PrefillInput is
	// typed by PrefillTaskSession once the provider is ready.
	StartTaskSession(ctx context.Context, task *Task, launch TaskSessionLaunchSpec) error
	// PrefillTaskSession waits for the provider's ready marker in the task
	// session and types the launch spec's PrefillInput without submitting it.
	PrefillTaskSession(ctx context.Context, task *Task, launch TaskSessionLaunchSpec) error
	// SubmitTaskInput types text into the task's window and submits it with
	// Enter, for a command the provider running there should run.
	SubmitTaskInput(ctx context.Context, task *Task, text string) error
	// AttachTaskSession attaches to an existing task session.
	AttachTaskSession(ctx context.Context, task *Task) error
	// InspectTaskSession returns the current tmux-side runtime state for the
	// task session. Missing sessions are reported as Exists=false.
	InspectTaskSession(ctx context.Context, task *Task) (TaskSessionRuntimeState, error)
	// InspectTaskSessions returns one shared runtime snapshot for the supplied
	// tasks. The result is keyed by task ID; missing sessions are included with
	// Exists=false.
	InspectTaskSessions(ctx context.Context, tasks []*Task) (map[string]TaskSessionRuntimeState, error)
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
	// LoadRepoSettings reads the repository's own settings for the worktree
	// tasks Rig creates in it.
	LoadRepoSettings(repoRoot string) (RepoSettings, error)
}
