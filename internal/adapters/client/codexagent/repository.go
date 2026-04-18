package codexagent

import (
	"context"

	codexclient "rig/internal/adapters/client/codex"
	"rig/internal/core"
	"rig/internal/pkg/execx"
)

type Repository struct {
	repo *codexclient.Repository
}

func NewRepository(runner execx.Runner, cfg codexclient.Config) *Repository {
	return &Repository{repo: codexclient.NewRepository(runner, cfg)}
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
