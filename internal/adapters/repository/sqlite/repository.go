package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"rig/internal/adapters/repository/sqlite/generated"
	"rig/internal/core"

	_ "modernc.org/sqlite"
)

type Repository struct {
	db                       *sql.DB
	queries                  *generated.Queries
	path                     string
	initErr                  error
	mu                       sync.Mutex
	nextHookSubscriberID     int
	hookSubscribers          map[int]*hookSubscriber
	nextObserverSubscriberID int
	observerSubscribers      map[int]*observerSubscriber
}

type Config struct {
	Path string
}

const (
	defaultAgentWindowName  = "agent"
	defaultEditorWindowName = "editor"
)

func NewRepository(cfg Config) (*Repository, error) {
	repo := &Repository{
		path:                cfg.Path,
		hookSubscribers:     make(map[int]*hookSubscriber),
		observerSubscribers: make(map[int]*observerSubscriber),
	}

	if err := ValidateConfig(cfg); err != nil {
		repo.initErr = err
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
		repo.db = nil
		return repo, nil
	}

	repo.db = db
	repo.queries = generated.New(db)
	return repo, nil
}

func ValidateConfig(cfg Config) error {
	if filepath.Dir(cfg.Path) == "." {
		return fmt.Errorf("sqlite path %q must include a parent directory", cfg.Path)
	}

	if err := os.MkdirAll(filepath.Dir(cfg.Path), 0o755); err != nil {
		return err
	}

	return nil
}

func (r *Repository) IsAvailable(ctx context.Context) error {
	if err := r.unavailableErr(); err != nil {
		return err
	}

	return r.db.PingContext(ctx)
}

func (r *Repository) CreateTask(ctx context.Context, task *core.Task) error {
	if err := r.unavailableErr(); err != nil {
		return err
	}

	return r.queries.CreateTask(ctx, createTaskParams(task))
}

func (r *Repository) UpdateTask(ctx context.Context, task *core.Task) error {
	if err := r.unavailableErr(); err != nil {
		return err
	}

	return r.queries.UpdateTask(ctx, updateTaskParams(task))
}

func (r *Repository) GetTask(ctx context.Context, idOrSlug string) (*core.Task, error) {
	if err := r.unavailableErr(); err != nil {
		return nil, err
	}

	row, err := r.queries.GetTaskByIDOrSlug(ctx, idOrSlug)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, core.ErrTaskNotFound
	}

	if err != nil {
		return nil, err
	}

	return taskFromRow(row), nil
}

func (r *Repository) ListTasks(ctx context.Context) ([]*core.Task, error) {
	if err := r.unavailableErr(); err != nil {
		return nil, err
	}

	rows, err := r.queries.ListTasks(ctx)
	if err != nil {
		return nil, err
	}

	return tasksFromRows(rows), nil
}

func (r *Repository) ListObserverSummaries(
	ctx context.Context,
	taskIDs []string,
) (map[string]*core.ObserverSummary, error) {
	if err := r.unavailableErr(); err != nil {
		return nil, err
	}

	summaries := make(map[string]*core.ObserverSummary)
	if len(taskIDs) == 0 {
		rows, err := r.queries.ListAllObserverSummaries(ctx)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			summary := observerSummaryFromListAllRow(row)
			summaries[summary.TaskID] = summary
		}
		return summaries, nil
	}

	rows, err := r.queries.ListObserverSummariesByTaskIDs(ctx, taskIDs)
	if err != nil {
		return nil, err
	}

	for _, row := range rows {
		summary := observerSummaryFromListByTaskIDsRow(row)
		summaries[summary.TaskID] = summary
	}

	return summaries, nil
}

func (r *Repository) UpsertObserverSummary(ctx context.Context, summary *core.ObserverSummary) error {
	if err := r.unavailableErr(); err != nil {
		return err
	}
	if summary == nil {
		return nil
	}

	err := r.queries.UpsertObserverSummary(ctx, observerSummaryParams(summary, time.Now().UTC()))
	if err != nil {
		return err
	}

	r.publishObserverTaskUpdate(observerTaskUpdateFromSummary(summary))
	return nil
}

func (r *Repository) unavailableErr() error {
	if r.initErr != nil {
		return r.initErr
	}
	if r.db == nil {
		return fmt.Errorf("sqlite repository unavailable")
	}

	return nil
}

func boolToInt(v bool) int {
	if v {
		return 1
	}

	return 0
}

func formatTime(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}

	return ts.UTC().Format(time.RFC3339Nano)
}

func parseTime(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}

	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}
	}

	return parsed
}
