package claudeagent

import (
	"context"

	claudeclient "rig/internal/adapters/client/claude"
	"rig/internal/core"
	"rig/internal/pkg/execx"
)

type Repository struct {
	repo *claudeclient.Repository
}

func NewRepository(runner execx.Runner, cfg claudeclient.Config) *Repository {
	return &Repository{repo: claudeclient.NewRepository(runner, cfg)}
}

func (r *Repository) SuggestTaskName(ctx context.Context, prompt string) (core.TaskSuggestion, error) {
	return r.repo.SuggestTaskName(ctx, prompt)
}

func (r *Repository) BuildWorkspaceBootstrapSpec(task *core.Task) (core.WorkspaceBootstrapSpec, error) {
	return r.repo.BuildWorkspaceBootstrapSpec(task)
}

func (r *Repository) BuildTaskSessionLaunchSpec(task *core.Task) (core.TaskSessionLaunchSpec, error) {
	return r.repo.BuildTaskSessionLaunchSpec(task)
}
