package tasksqlite

import (
	"context"

	sqliterepo "rig/internal/adapters/repository/sqlite"
	"rig/internal/core"
)

type repository struct {
	repo *sqliterepo.Repository
}

func FromRepository(repo *sqliterepo.Repository) core.TaskStore {
	return &repository{repo: repo}
}

func New(cfg sqliterepo.Config) (core.TaskStore, error) {
	repo, err := sqliterepo.NewRepository(cfg)
	if err != nil {
		return nil, err
	}

	return FromRepository(repo), nil
}

func (r *repository) CreateTask(ctx context.Context, task *core.Task) error {
	return r.repo.CreateTask(ctx, task)
}

func (r *repository) UpdateTask(ctx context.Context, task *core.Task) error {
	return r.repo.UpdateTask(ctx, task)
}

func (r *repository) GetTask(context.Context, string) (*core.Task, error) {
	panic("tasksqlite.Repository.GetTask not implemented")
}

func (r *repository) ListTasks(ctx context.Context) ([]*core.Task, error) {
	return r.repo.ListTasks(ctx)
}
