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

type WorkspacePreparer interface {
	PrepareTaskWorkspace(ctx context.Context, task *Task, repoRoot string) error
}
