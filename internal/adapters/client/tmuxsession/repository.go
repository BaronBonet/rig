package tmuxsession

import (
	"context"

	tmuxclient "rig/internal/adapters/client/tmux"
	"rig/internal/core"
	"rig/internal/pkg/execx"
)

type Repository struct {
	repo *tmuxclient.Repository
}

func NewRepository(runner execx.Runner) *Repository {
	return &Repository{repo: tmuxclient.NewRepository(runner)}
}

func (r *Repository) StartTaskSession(ctx context.Context, task *core.Task, launch core.TaskSessionLaunchSpec) error {
	return r.repo.StartTaskSession(ctx, task, launch)
}

func (r *Repository) OpenTaskSession(context.Context, *core.Task) error {
	panic("tmuxsession.Repository.OpenTaskSession not implemented")
}

func (r *Repository) DeleteTaskSession(context.Context, *core.Task) error {
	panic("tmuxsession.Repository.DeleteTaskSession not implemented")
}

func (r *Repository) InspectTaskSession(context.Context, *core.Task) (core.SessionResources, error) {
	panic("tmuxsession.Repository.InspectTaskSession not implemented")
}

func (r *Repository) SnapshotTaskSession(context.Context, *core.Task) (core.RuntimeSnapshot, error) {
	panic("tmuxsession.Repository.SnapshotTaskSession not implemented")
}
