package core

import "context"

type RepoContext struct {
	Root       string
	Name       string
	BaseBranch string
}

type RepoConfig struct {
	Seed   SeedConfig
	Exists bool
}

type SeedConfig struct {
	Copy []string
}

type LaunchRequest struct {
	Command      []string
	Prompt       string
	InitialInput []string
}

type RepoResources struct {
	WorktreeExists bool
	BranchExists   bool
}

type SessionResources struct {
	SessionExists      bool
	AgentWindowExists  bool
	EditorWindowExists bool
}

type CreateWorktreeInput struct {
	RepoRoot     string
	BaseBranch   string
	BranchName   string
	WorktreePath string
}

type CreateSessionInput struct {
	SessionName      string
	WorkingDir       string
	AgentWindowName  string
	EditorWindowName string
}

type SeedWorkspaceInput struct {
	RepoRoot      string
	WorktreePath  string
	RelativePaths []string
}

type TaskRepository interface {
	IsAvailable(ctx context.Context) error
	CreateTask(ctx context.Context, task *Task) error
	UpdateTask(ctx context.Context, task *Task) error
	GetTask(ctx context.Context, idOrSlug string) (*Task, error)
	ListTasks(ctx context.Context) ([]*Task, error)
	AppendEvent(ctx context.Context, taskID, eventType, payload string) error
}

type RepoConfigRepository interface {
	LoadRepoConfig(ctx context.Context, repoRoot string) (RepoConfig, error)
}

type WorkspaceSeeder interface {
	SeedWorkspace(ctx context.Context, in SeedWorkspaceInput, progress func(string)) error
	ValidateSeedPaths(ctx context.Context, repoRoot string, relativePaths []string) error
}

type RepoClient interface {
	IsAvailable(ctx context.Context) error
	DetectRepo(ctx context.Context, cwd string) (RepoContext, error)
	CreateTaskWorkspace(ctx context.Context, task *Task) error
	RemoveTaskWorkspace(ctx context.Context, task *Task) error
	InspectTaskWorkspace(ctx context.Context, task *Task) (RepoResources, error)
}

type SessionClient interface {
	IsAvailable(ctx context.Context) error
	StartTaskSession(ctx context.Context, task *Task, launch LaunchRequest) error
	OpenTaskSession(ctx context.Context, task *Task) error
	DeleteTaskSession(ctx context.Context, task *Task) error
	InspectTaskSession(ctx context.Context, task *Task) (SessionResources, error)
	SnapshotTaskSession(ctx context.Context, task *Task) (RuntimeSnapshot, error)
}

type ProviderClient interface {
	IsAvailable(ctx context.Context) error
	SuggestTaskName(ctx context.Context, prompt string) (string, error)
	LaunchRequest(task *Task) (LaunchRequest, error)
	DetectRuntimeState(snapshot RuntimeSnapshot) RuntimeState
}

// Legacy compatibility during the boundary refactor. These are kept only so the
// existing adapters and composition root can be bridged into the higher-level
// ports above until later tasks move the mechanics out of core.
type GitRepository interface {
	IsAvailable(ctx context.Context) error
	DetectRepo(ctx context.Context, cwd string) (RepoContext, error)
	BranchExists(ctx context.Context, repoRoot, branch string) (bool, error)
	CreateWorktree(ctx context.Context, in CreateWorktreeInput) error
	RemoveWorktree(ctx context.Context, repoRoot, path string) error
}

type TmuxRepository interface {
	IsAvailable(ctx context.Context) error
	SessionExists(ctx context.Context, session string) (bool, error)
	WindowExists(ctx context.Context, session, window string) (bool, error)
	CreateSession(ctx context.Context, in CreateSessionInput) error
	KillSession(ctx context.Context, session string) error
	AttachOrSwitch(ctx context.Context, session string) error
	SendKeysToWindow(ctx context.Context, session, window string, command []string) error
	TypeInWindow(ctx context.Context, session, window string, command []string) error
	CapturePaneContent(ctx context.Context, session, window string) (string, error)
}

type ProviderRepository interface {
	ProposeTaskName(ctx context.Context, prompt string) (string, error)
	BuildLaunchCommand(task *Task) ([]string, error)
	PromptMarker() string
	IsAvailable(ctx context.Context) error
}
