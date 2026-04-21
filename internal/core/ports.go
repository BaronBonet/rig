package core

import "context"

type RepoConfig struct {
	Seed           SeedConfig
	Exists         bool
	ConfigFileName string
}

type SeedConfig struct {
	Copy        []string
	SetupScript string
}

type RepoResources struct {
	WorktreeExists bool
	BranchExists   bool
}

type SessionResources struct {
	SessionExists      bool
	TaskWindowExists   bool
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
	TaskWindowName   string
	EditorWindowName string
}

type SeedWorkspaceInput struct {
	RepoRoot      string
	WorktreePath  string
	RelativePaths []string
}

type RepoConfigLoader interface {
	LoadRepoConfig(ctx context.Context, repoRoot string) (RepoConfig, error)
}

type WorkspaceSeeder interface {
	SeedWorkspace(ctx context.Context, in SeedWorkspaceInput, progress func(string)) error
	ValidateSeedPaths(ctx context.Context, repoRoot string, relativePaths []string) error
}

type RunSetupScriptInput struct {
	RepoRoot     string
	WorktreePath string
	ScriptPath   string
}

type SetupScriptRunner interface {
	RunSetupScript(ctx context.Context, in RunSetupScriptInput, output func(string)) error
	ValidateSetupScript(ctx context.Context, repoRoot string, scriptPath string) error
}

type TaskWorkspaceBootstrapper interface {
	BootstrapTaskWorkspace(ctx context.Context, task *Task) error
}

type RepoClient interface {
	IsAvailable(ctx context.Context) error
	DetectRepo(ctx context.Context, cwd string) (RepoContext, error)
	IsBranchUsedByWorktree(ctx context.Context, repoRoot string, branchName string) (bool, error)
	CreateTaskWorkspace(ctx context.Context, task *Task) error
	CreateTaskWorkspaceFromBranch(ctx context.Context, task *Task) error
	RemoveTaskWorkspace(ctx context.Context, task *Task) error
	InspectTaskWorkspace(ctx context.Context, task *Task) (RepoResources, error)
}

type RuntimeMonitor interface {
	Snapshot(ctx context.Context, task *Task) (RuntimeSnapshot, error)
	Close() error
}

type RuntimeProviderClient interface {
	IsAvailable(ctx context.Context) error
	SuggestTaskName(ctx context.Context, prompt string) (TaskSuggestion, error)
	BuildTaskSessionLaunchSpec(task *Task) (TaskSessionLaunchSpec, error)
	DetectRuntimeState(snapshot RuntimeSnapshot) RuntimeState
}

type RestoreLaunchProvider interface {
	RestoreTaskSessionLaunchSpec(task *Task, hookSession *HookSessionSummary) (TaskSessionLaunchSpec, error)
}

type SessionUsageReader interface {
	ReadSessionTokenUsage(ctx context.Context, provider string, transcriptPath string) (*SessionTokenUsage, error)
}

type PRStatusChecker interface {
	IsAvailable(ctx context.Context) error
	CheckPRStatus(ctx context.Context, repoRoot string, branchName string) (*PRStatus, error)
	ListRepoPullRequests(ctx context.Context, repoRoot string) ([]RepoPullRequest, error)
}
