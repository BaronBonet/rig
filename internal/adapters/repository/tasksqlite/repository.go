package tasksqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"rig/internal/adapters/repository/tasksqlite/generated"
	"rig/internal/core"

	_ "modernc.org/sqlite"
)

type repository struct {
	db      *sql.DB
	queries *generated.Queries
	initErr error
}

func New(cfg Config) (core.TaskStore, error) {
	repo := &repository{}

	if err := ValidateConfig(cfg); err != nil {
		repo.initErr = fmt.Errorf("validate tasksqlite config: %w", err)
		return repo, nil
	}

	db, err := sql.Open("sqlite", cfg.Path)
	if err != nil {
		repo.initErr = err
		return repo, nil
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := applyBootstrapSQL(context.Background(), db, sqlFiles, "bootstrap/connection.sql"); err != nil {
		repo.initErr = err
		_ = db.Close()
		return repo, nil
	}
	if err := applyGooseMigrations(context.Background(), db, sqlFiles, "migrations"); err != nil {
		repo.initErr = err
		_ = db.Close()
		return repo, nil
	}

	repo.db = db
	repo.queries = generated.New(db)
	return repo, nil
}

func (r *repository) CreateTask(ctx context.Context, task *core.Task) error {
	if err := r.unavailableErr(); err != nil {
		return err
	}
	return r.queries.CreateTask(ctx, createTaskParams(task))
}

func (r *repository) UpdateTask(ctx context.Context, task *core.Task) error {
	if err := r.unavailableErr(); err != nil {
		return err
	}
	return r.queries.UpdateTask(ctx, updateTaskParams(task))
}

func (r *repository) GetTask(ctx context.Context, id string) (*core.Task, error) {
	if err := r.unavailableErr(); err != nil {
		return nil, err
	}

	row, err := r.queries.GetTaskByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, core.ErrTaskNotFound
	}
	if err != nil {
		return nil, err
	}

	return taskFromRow(row), nil
}

func (r *repository) ListTasks(ctx context.Context) ([]*core.Task, error) {
	if err := r.unavailableErr(); err != nil {
		return nil, err
	}

	rows, err := r.queries.ListTasks(ctx)
	if err != nil {
		return nil, err
	}

	return tasksFromRows(rows), nil
}

func (r *repository) unavailableErr() error {
	if r.initErr != nil {
		return r.initErr
	}
	if r.db == nil {
		return fmt.Errorf("tasksqlite repository unavailable")
	}
	return nil
}
