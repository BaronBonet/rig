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
}

type CodexRepository interface {
	ProposeTaskName(ctx context.Context, prompt string) (string, error)
	BuildLaunchCommand(task *Task) ([]string, error)
	IsAvailable(ctx context.Context) error
}
