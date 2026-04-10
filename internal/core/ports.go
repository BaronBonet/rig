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
	SetupFiles   map[string][]byte // relative path -> content, written to worktree before launch
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

type HookEventIngestor interface {
	IngestHookEvent(ctx context.Context, raw HookEventInput) (*HookSessionSummary, error)
}

type HookObservabilityRepository interface {
	ListHookSessionSummaries(ctx context.Context, taskIDs []string) (map[string]*HookSessionSummary, error)
	ListHookEvents(ctx context.Context, taskID string, limit int) ([]HookEvent, error)
	SubscribeHookSessionUpdates(ctx context.Context) (<-chan HookSessionSummary, func(), error)
}

type ObserverRuntimeRepository interface {
	ListObserverSummaries(ctx context.Context, taskIDs []string) (map[string]*ObserverSummary, error)
	UpsertObserverSummary(ctx context.Context, summary *ObserverSummary) error
	SubscribeObserverTaskUpdates(ctx context.Context) (<-chan ObserverTaskUpdate, func(), error)
}

type RepoConfigLoader interface {
	LoadRepoConfig(ctx context.Context, repoRoot string) (RepoConfig, error)
}

type WorkspaceSeeder interface {
	SeedWorkspace(ctx context.Context, in SeedWorkspaceInput, progress func(string)) error
	ValidateSeedPaths(ctx context.Context, repoRoot string, relativePaths []string) error
}

type TaskWorkspaceBootstrapper interface {
	BootstrapTaskWorkspace(ctx context.Context, task *Task) error
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

type RuntimeMonitor interface {
	Snapshot(ctx context.Context, task *Task) (RuntimeSnapshot, error)
	Close() error
}

type ProviderClient interface {
	IsAvailable(ctx context.Context) error
	SuggestTaskName(ctx context.Context, prompt string) (string, error)
	LaunchRequest(task *Task) (LaunchRequest, error)
	DetectRuntimeState(snapshot RuntimeSnapshot) RuntimeState
}

type PRStatusChecker interface {
	CheckPRStatus(ctx context.Context, repoRoot string, branchName string) (*PRStatus, error)
}
