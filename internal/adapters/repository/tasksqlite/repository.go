package tasksqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"rig/internal/adapters/repository/tasksqlite/generated"
	"rig/internal/core"
	"slices"

	// Register the "sqlite" database/sql driver used by sql.Open.
	_ "modernc.org/sqlite"
)

type repository struct {
	db      *sql.DB
	queries *generated.Queries
}

func New(cfg Config) (core.TaskStore, error) {
	if err := ValidateConfig(cfg); err != nil {
		return nil, err
	}

	db, err := openSQLiteDB(cfg.Path)
	if err != nil {
		return nil, err
	}

	// Apply SQLite PRAGMAs on the new connection before running migrations so
	// the database enforces the connection-level behavior this adapter expects.
	if err := applyBootstrapSQL(context.Background(), db, sqlFiles, "bootstrap/connection.sql"); err != nil {
		_ = db.Close()
		return nil, err
	}
	if stale, err := hasStaleTasksSchema(context.Background(), db); err != nil {
		_ = db.Close()
		return nil, err
	} else if stale {
		_ = db.Close()
		if err := removeSQLiteFiles(cfg.Path); err != nil {
			return nil, err
		}
		db, err = openSQLiteDB(cfg.Path)
		if err != nil {
			return nil, err
		}
		if err := applyBootstrapSQL(context.Background(), db, sqlFiles, "bootstrap/connection.sql"); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	if err := applyGooseMigrations(context.Background(), db, sqlFiles, "migrations"); err != nil {
		_ = db.Close()
		return nil, err
	}

	return &repository{
		db:      db,
		queries: generated.New(db),
	}, nil
}

func (r *repository) CreateTask(ctx context.Context, task *core.Task) error {
	return r.queries.CreateTask(ctx, createTaskParams(task))
}

func (r *repository) UpdateTask(ctx context.Context, task *core.Task) error {
	return r.queries.UpdateTask(ctx, updateTaskParams(task))
}

func (r *repository) GetTask(ctx context.Context, id string) (*core.Task, error) {
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
	rows, err := r.queries.ListTasks(ctx)
	if err != nil {
		return nil, err
	}

	return tasksFromRows(rows), nil
}

func openSQLiteDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return db, nil
}

func hasStaleTasksSchema(ctx context.Context, db *sql.DB) (bool, error) {
	rows, err := db.QueryContext(ctx, "pragma table_info(tasks)")
	if err != nil {
		return false, fmt.Errorf("inspect tasks schema: %w", err)
	}
	defer rows.Close()

	var columns []string
	for rows.Next() {
		var (
			cid        int
			name       string
			columnType string
			notNull    int
			defaultVal sql.NullString
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultVal, &primaryKey); err != nil {
			return false, fmt.Errorf("scan tasks schema: %w", err)
		}
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("read tasks schema: %w", err)
	}
	if len(columns) == 0 {
		return false, nil
	}

	return !slices.Equal(columns, []string{
		"id",
		"slug",
		"prompt",
		"display_name",
		"repo_root",
		"repo_name",
		"branch_name",
		"worktree_path",
		"tmux_session",
		"provider",
		"status",
		"created_at",
		"updated_at",
	}), nil
}

func removeSQLiteFiles(path string) error {
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Remove(candidate); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove disposable tasksqlite file %s: %w", candidate, err)
		}
	}
	return nil
}
