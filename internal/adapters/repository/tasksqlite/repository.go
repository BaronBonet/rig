package tasksqlite

import (
	"context"

	sqliterepo "rig/internal/adapters/repository/sqlite"
	"rig/internal/core"
)

type Repository struct {
	repo *sqliterepo.Repository
}

func FromRepository(repo *sqliterepo.Repository) *Repository {
	return &Repository{repo: repo}
}

func NewRepository(cfg sqliterepo.Config) (*Repository, error) {
	repo, err := sqliterepo.NewRepository(cfg)
	if err != nil {
		return nil, err
	}

	return FromRepository(repo), nil
}

func (r *Repository) CreateTask(ctx context.Context, task *core.Task) error {
	return r.repo.CreateTask(ctx, task)
}

func (r *Repository) UpdateTask(ctx context.Context, task *core.Task) error {
	return r.repo.UpdateTask(ctx, task)
}

func (r *Repository) GetTask(context.Context, string) (*core.Task, error) {
	panic("tasksqlite.Repository.GetTask not implemented")
}

func (r *Repository) ListTasks(ctx context.Context) ([]*core.Task, error) {
	return r.repo.ListTasks(ctx)
}
