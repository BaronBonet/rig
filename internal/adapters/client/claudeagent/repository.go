package claudeagent

import (
	"context"

	claudeclient "rig/internal/adapters/client/claude"
	"rig/internal/core"
	"rig/internal/pkg/subprocess"
)

type repository struct {
	repo *claudeclient.Repository
}

func New(runner subprocess.Runner, cfg claudeclient.Config) core.AgentClient {
	return &repository{repo: claudeclient.NewRepository(runner, cfg)}
}

func (r *repository) SuggestTaskName(ctx context.Context, prompt string) (core.TaskSuggestion, error) {
	return r.repo.SuggestTaskName(ctx, prompt)
}

func (r *repository) BuildWorkspaceBootstrapSpec(task *core.Task) (core.WorkspaceBootstrapSpec, error) {
	return r.repo.BuildWorkspaceBootstrapSpec(task)
}

func (r *repository) BuildTaskSessionLaunchSpec(task *core.Task) (core.TaskSessionLaunchSpec, error) {
	return r.repo.BuildTaskSessionLaunchSpec(task)
}

func (r *repository) HookEventToTaskStatus(core.HookEventInput) (*core.TaskStatusUpdate, error) {
	return nil, core.ErrUnmanagedHookEvent
}
