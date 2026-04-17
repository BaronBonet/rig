package core

import "context"

type TaskStore interface {
	CreateTask(ctx context.Context, task *Task) error
	UpdateTask(ctx context.Context, task *Task) error
	GetTask(ctx context.Context, idOrSlug string) (*Task, error)
	ListTasks(ctx context.Context) ([]*Task, error)
}

type AgentClient interface {
	SuggestTaskName(ctx context.Context, prompt string) (TaskSuggestion, error)
	LaunchRequest(task *Task) (LaunchRequest, error)
}

type WorkspaceClient interface {
	DetectRepo(ctx context.Context, cwd string) (RepoContext, error)
	IsBranchUsedByWorktree(ctx context.Context, repoRoot string, branchName string) (bool, error)
	CreateTaskWorkspace(ctx context.Context, task *Task) error
	CreateTaskWorkspaceFromBranch(ctx context.Context, task *Task) error
}

type RuntimeClient interface {
	StartTaskSession(ctx context.Context, task *Task, launch LaunchRequest) error
	OpenTaskSession(ctx context.Context, task *Task) error
}

type ProjectConfigRepository interface {
	LoadRepoConfig(ctx context.Context, repoRoot string) (RepoConfig, error)
}
