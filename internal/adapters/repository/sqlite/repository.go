package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"

	"rig/internal/adapters/repository/sqlite/generated"
	"rig/internal/core"

	// Register the "sqlite" database/sql driver used by sql.Open.
	_ "modernc.org/sqlite"
)

var errHealthCheckRepositoryOnly = errors.New("sqlite health-check repository only supports HealthCheck")

type repository struct {
	queries *generated.Queries
	db      *sql.DB
	subs    map[string][]chan core.TaskStatusUpdate
	mu      sync.Mutex
}

type healthCheckRepository struct {
	cfg Config
}

func New(cfg Config) (core.TaskRepository, error) {
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
		subs:    make(map[string][]chan core.TaskStatusUpdate),
	}, nil
}

func NewHealthCheckRepository(cfg Config) core.TaskRepository {
	return &healthCheckRepository{cfg: cfg}
}

func (r *healthCheckRepository) HealthCheck(ctx context.Context) error {
	repo, err := New(r.cfg)
	if err != nil {
		return err
	}
	return repo.HealthCheck(ctx)
}

func (r *healthCheckRepository) CreateTask(context.Context, *core.Task) error {
	return errHealthCheckRepositoryOnly
}

func (r *healthCheckRepository) DeleteTask(context.Context, string) error {
	return errHealthCheckRepositoryOnly
}

func (r *healthCheckRepository) UpdateTask(context.Context, *core.Task) error {
	return errHealthCheckRepositoryOnly
}

func (r *healthCheckRepository) ListTasks(context.Context) ([]*core.Task, error) {
	return nil, errHealthCheckRepositoryOnly
}

func (r *healthCheckRepository) RecordTaskActivity(context.Context, core.TaskActivityEvent) error {
	return errHealthCheckRepositoryOnly
}

func (r *healthCheckRepository) GetTaskActivity(context.Context, string, int) ([]core.TaskActivityEvent, error) {
	return nil, errHealthCheckRepositoryOnly
}

func (r *healthCheckRepository) UpsertTaskStatus(context.Context, core.TaskStatusUpdate) error {
	return errHealthCheckRepositoryOnly
}

func (r *healthCheckRepository) UpsertTaskResumeMetadata(context.Context, core.TaskResumeMetadata) error {
	return errHealthCheckRepositoryOnly
}

func (r *healthCheckRepository) LatestTaskStatus(context.Context, string) (*core.TaskStatusUpdate, error) {
	return nil, errHealthCheckRepositoryOnly
}

func (r *healthCheckRepository) LatestTaskResumeMetadata(context.Context, string) (*core.TaskResumeMetadata, error) {
	return nil, errHealthCheckRepositoryOnly
}

func (r *healthCheckRepository) SubscribeTaskStatus(context.Context, string) (<-chan core.TaskStatusUpdate, error) {
	return nil, errHealthCheckRepositoryOnly
}

func (r *repository) HealthCheck(ctx context.Context) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("sqlite repository not configured")
	}
	if err := r.db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping sqlite database: %w", err)
	}

	var result string
	if err := r.db.QueryRowContext(ctx, "pragma quick_check").Scan(&result); err != nil {
		return fmt.Errorf("run sqlite quick_check: %w", err)
	}
	if strings.TrimSpace(result) != "ok" {
		return fmt.Errorf("sqlite quick_check failed: %s", result)
	}

	return nil
}

func (r *repository) CreateTask(ctx context.Context, task *core.Task) error {
	return r.queries.CreateTask(ctx, createTaskParams(task))
}

func (r *repository) DeleteTask(ctx context.Context, taskID string) error {
	return r.queries.DeleteTask(ctx, strings.TrimSpace(taskID))
}

func (r *repository) UpdateTask(ctx context.Context, task *core.Task) error {
	return r.queries.UpdateTask(ctx, updateTaskParams(task))
}

func (r *repository) ListTasks(ctx context.Context) ([]*core.Task, error) {
	rows, err := r.queries.ListTasks(ctx)
	if err != nil {
		return nil, err
	}

	return tasksFromRows(rows), nil
}

func (r *repository) RecordTaskActivity(ctx context.Context, event core.TaskActivityEvent) error {
	event.TaskID = strings.TrimSpace(event.TaskID)
	if event.TaskID == "" {
		return fmt.Errorf("task activity event task ID is required")
	}

	return r.queries.InsertTaskActivity(ctx, insertTaskActivityParams(event))
}

func (r *repository) GetTaskActivity(ctx context.Context, taskID string, limit int) ([]core.TaskActivityEvent, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, nil
	}

	if limit <= 0 {
		rows, err := r.queries.ListTaskActivityByTaskID(ctx, taskID)
		if err != nil {
			return nil, err
		}
		return taskActivityEventsFromRows(rows), nil
	}

	rows, err := r.queries.ListTaskActivityByTaskIDLimitedDesc(ctx, generated.ListTaskActivityByTaskIDLimitedDescParams{
		TaskID: taskID,
		Limit:  int64(limit),
	})
	if err != nil {
		return nil, err
	}

	events := taskActivityEventsFromRows(rows)
	slices.Reverse(events)
	return events, nil
}

func (r *repository) UpsertTaskStatus(ctx context.Context, update core.TaskStatusUpdate) error {
	update.TaskID = strings.TrimSpace(update.TaskID)
	if update.TaskID == "" {
		return fmt.Errorf("task status update task ID is required")
	}

	if err := r.queries.UpsertTaskStatus(ctx, upsertTaskStatusParams(update)); err != nil {
		return err
	}

	r.mu.Lock()
	subscribers := append([]chan core.TaskStatusUpdate(nil), r.subs[update.TaskID]...)
	r.mu.Unlock()

	for _, subscriber := range subscribers {
		select {
		case subscriber <- update:
		default:
		}
	}

	return nil
}

func (r *repository) UpsertTaskResumeMetadata(ctx context.Context, metadata core.TaskResumeMetadata) error {
	metadata.TaskID = strings.TrimSpace(metadata.TaskID)
	if metadata.TaskID == "" {
		return fmt.Errorf("task resume metadata task ID is required")
	}

	return r.queries.UpsertTaskResumeMetadata(ctx, upsertTaskResumeMetadataParams(metadata))
}

func (r *repository) LatestTaskStatus(ctx context.Context, taskID string) (*core.TaskStatusUpdate, error) {
	row, err := r.queries.LatestTaskStatus(ctx, strings.TrimSpace(taskID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return taskStatusUpdateFromRow(row), nil
}

func (r *repository) LatestTaskResumeMetadata(ctx context.Context, taskID string) (*core.TaskResumeMetadata, error) {
	row, err := r.queries.LatestTaskResumeMetadata(ctx, strings.TrimSpace(taskID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return taskResumeMetadataFromRow(row), nil
}

func (r *repository) SubscribeTaskStatus(ctx context.Context, taskID string) (<-chan core.TaskStatusUpdate, error) {
	taskID = strings.TrimSpace(taskID)
	updates := make(chan core.TaskStatusUpdate, 8)

	r.mu.Lock()
	r.subs[taskID] = append(r.subs[taskID], updates)
	r.mu.Unlock()

	go func() {
		<-ctx.Done()
		r.mu.Lock()
		defer r.mu.Unlock()

		subscribers := r.subs[taskID]
		filtered := subscribers[:0]
		for _, subscriber := range subscribers {
			if subscriber != updates {
				filtered = append(filtered, subscriber)
			}
		}
		if len(filtered) == 0 {
			delete(r.subs, taskID)
		} else {
			r.subs[taskID] = filtered
		}
		close(updates)
	}()

	return updates, nil
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

	if !slices.Equal(columns, []string{
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
		"created_at",
		"updated_at",
	}) {
		return true, nil
	}

	statusRows, err := db.QueryContext(ctx, "pragma table_info(task_status)")
	if err != nil {
		return false, fmt.Errorf("inspect task_status schema: %w", err)
	}
	defer statusRows.Close()

	var statusColumns []string
	for statusRows.Next() {
		var (
			cid        int
			name       string
			columnType string
			notNull    int
			defaultVal sql.NullString
			primaryKey int
		)
		if err := statusRows.Scan(&cid, &name, &columnType, &notNull, &defaultVal, &primaryKey); err != nil {
			return false, fmt.Errorf("scan task_status schema: %w", err)
		}
		statusColumns = append(statusColumns, name)
	}
	if err := statusRows.Err(); err != nil {
		return false, fmt.Errorf("read task_status schema: %w", err)
	}
	if len(statusColumns) == 0 {
		return true, nil
	}

	return !slices.Equal(statusColumns, []string{
		"task_id",
		"provider",
		"phase",
		"raw_event_name",
		"observed_at",
	}), nil
}

func removeSQLiteFiles(path string) error {
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Remove(candidate); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove disposable sqlite file %s: %w", candidate, err)
		}
	}
	return nil
}
