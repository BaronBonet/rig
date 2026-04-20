package core

import "context"

type CreateTaskSource struct {
	PullRequest *RepoPullRequest
}

type CreateTaskInput struct {
	Cwd      string
	Prompt   string
	Provider string
	Source   CreateTaskSource
}

// TaskFrontend is the frontend-facing task application port used by the TUI.
// The transport layer that serves the frontend, such as the local status
// daemon, implements this port and forwards requests into the underlying task
// application service.
type TaskFrontend interface {
	// CreateTask creates a new task and returns the durable task record that the
	// frontend should render immediately.
	CreateTask(ctx context.Context, input CreateTaskInput) (*Task, error)
	// LatestTaskStatus returns the latest published live status for a task, or
	// nil when no status has been published yet.
	LatestTaskStatus(ctx context.Context, taskID string) (*TaskStatusUpdate, error)
	// SubscribeTaskStatus subscribes to live status updates for a task. The
	// subscription lifetime is owned by ctx; cancelling it removes the
	// subscription and closes the update channel.
	SubscribeTaskStatus(ctx context.Context, taskID string) (<-chan TaskStatusUpdate, error)
}

// TaskFrontendServer is the runtime application port for a long-lived
// frontend-facing transport, such as the local task daemon. It exposes the
// frontend task API and the lifecycle hook needed to start serving requests.
type TaskFrontendServer interface {
	TaskFrontend
	Serve(ctx context.Context) error
}

type TaskService interface {
	// CreateTask creates a new task from either a prompt or a pull request source.
	CreateTask(ctx context.Context, input CreateTaskInput) (*Task, error)
	// LatestTaskStatus returns the latest published live status for a task, or
	// nil when no status has been published yet.
	LatestTaskStatus(ctx context.Context, taskID string) (*TaskStatusUpdate, error)
	// SubscribeTaskStatus subscribes to live status updates for a task. The
	// subscription lifetime is owned by ctx; cancelling it removes the
	// subscription and closes the update channel.
	SubscribeTaskStatus(ctx context.Context, taskID string) (<-chan TaskStatusUpdate, error)
	// PublishTaskStatus publishes a normalized live status update for a task.
	PublishTaskStatus(ctx context.Context, update TaskStatusUpdate) error
	// HandleHookEvent resolves and publishes any task status update implied by a
	// provider hook event.
	HandleHookEvent(ctx context.Context, input HookEventInput) error
}

// TaskRepository persists task records and returns their durable state.
type TaskRepository interface {
	// CreateTask stores a newly created task record.
	CreateTask(ctx context.Context, task *Task) error
	// UpdateTask persists changes to an existing task record.
	UpdateTask(ctx context.Context, task *Task) error
	// ListTasks returns all known tasks.
	ListTasks(ctx context.Context) ([]*Task, error)
	// UpsertTaskStatus stores the latest known live status for a task.
	UpsertTaskStatus(ctx context.Context, update TaskStatusUpdate) error
	// LatestTaskStatus returns the latest known live status for a task, or nil
	// when no status has been recorded yet.
	LatestTaskStatus(ctx context.Context, taskID string) (*TaskStatusUpdate, error)
	// SubscribeTaskStatus subscribes to live status updates for a task. The
	// subscription lifetime is owned by ctx; cancelling it removes the
	// subscription and closes the update channel.
	SubscribeTaskStatus(ctx context.Context, taskID string) (<-chan TaskStatusUpdate, error)
}

// AgentClient wraps provider-specific agent behavior behind one application
// contract.
type AgentClient interface {
	// SuggestTaskName derives a task display name and branch type from a prompt.
	SuggestTaskName(ctx context.Context, prompt string) (TaskSuggestion, error)
	// BuildWorkspaceBootstrapSpec describes the provider-specific files that
	// should be written into the task workspace before launch.
	BuildWorkspaceBootstrapSpec(task *Task) (WorkspaceBootstrapSpec, error)
	// BuildTaskSessionLaunchSpec describes how the provider's CLI should be
	// started inside the task's tmux session.
	BuildTaskSessionLaunchSpec(task *Task) (TaskSessionLaunchSpec, error)
	// HookEventToTaskStatus normalizes a provider hook event into a task status
	// update when the event contributes to the live task status stream.
	HookEventToTaskStatus(input HookEventInput) (*TaskStatusUpdate, error)
}

// GitWorktreeClient manages the Git worktree operations needed by the new task
// service during task creation.
type GitWorktreeClient interface {
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
}

// TmuxSessionClient manages tmux session lifecycle for a task.
//
// This is the session-facing port used by the task service for task-scoped tmux
// operations. Environment-level diagnostics such as "is tmux installed" do not
// belong on this port.
type TmuxSessionClient interface {
	// StartTaskSession starts the runtime session for a task using the provider's
	// task session launch spec.
	StartTaskSession(ctx context.Context, task *Task, launch TaskSessionLaunchSpec) error
	// OpenTaskSession attaches to an existing task session.
	OpenTaskSession(ctx context.Context, task *Task) error
	// DeleteTaskSession tears down the task session.
	DeleteTaskSession(ctx context.Context, task *Task) error
	// InspectTaskSession reports the current session-side resources for a task.
	InspectTaskSession(ctx context.Context, task *Task) (SessionResources, error)
	// SnapshotTaskSession captures a runtime snapshot of the current task session.
	SnapshotTaskSession(ctx context.Context, task *Task) (RuntimeSnapshot, error)
}

// WorkspacePreparer applies local workspace setup after a worktree has been
// created and before the task session is launched.
type WorkspacePreparer interface {
	// PrepareTaskWorkspace loads repo configuration and applies any local
	// workspace setup needed for the task.
	PrepareTaskWorkspace(ctx context.Context, task *Task, repoRoot string, bootstrapSpec WorkspaceBootstrapSpec) error
}
