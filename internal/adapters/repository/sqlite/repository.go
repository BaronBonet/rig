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

	"agent/internal/adapters/repository/sqlite/generated"
	"agent/internal/core"

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

	if err := seedLegacyMigrationState(context.Background(), db); err != nil {
		repo.initErr = err
		_ = db.Close()
		return repo, nil
	}

	if err := applyMigrations(context.Background(), db, sqlFiles, "migrations"); err != nil {
		repo.initErr = err
		_ = db.Close()
		repo.db = nil
		return repo, nil
	}

	repo.db = db
	repo.queries = generated.New(db)
	if err := repo.backfillLegacyTaskRows(); err != nil {
		repo.initErr = err
		_ = db.Close()
		repo.db = nil
		repo.queries = nil
		return repo, nil
	}

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

	rows, err := r.queries.ListObserverSummaries(ctx)
	if err != nil {
		return nil, err
	}

	summaries := make(map[string]*core.ObserverSummary)
	if len(taskIDs) == 0 {
		for _, row := range rows {
			summary := observerSummaryFromListRow(row)
			summaries[summary.TaskID] = summary
		}
		return summaries, nil
	}

	requested := make(map[string]struct{}, len(taskIDs))
	for _, taskID := range taskIDs {
		requested[taskID] = struct{}{}
	}

	for _, row := range rows {
		if _, ok := requested[row.TaskID]; !ok {
			continue
		}
		summary := observerSummaryFromListRow(row)
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

func (r *Repository) AppendEvent(ctx context.Context, taskID, eventType, payload string) error {
	if err := r.unavailableErr(); err != nil {
		return err
	}

	return r.queries.AppendEvent(ctx, appendEventParams(taskID, eventType, payload))
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

func (r *Repository) backfillLegacyTaskRows() error {
	ctx := context.Background()
	rows, err := r.db.QueryContext(
		ctx,
		`select id, repo_root, repo_name, agent_window_name, editor_window_name from tasks`,
	)
	if err != nil {
		return err
	}
	defer rows.Close()

	type legacyTaskRow struct {
		id               string
		repoRoot         string
		repoName         string
		agentWindowName  string
		editorWindowName string
	}

	var legacyRows []legacyTaskRow
	for rows.Next() {
		var row legacyTaskRow
		if err := rows.Scan(
			&row.id, &row.repoRoot, &row.repoName,
			&row.agentWindowName, &row.editorWindowName,
		); err != nil {
			return err
		}
		legacyRows = append(legacyRows, row)
	}

	if err := rows.Err(); err != nil {
		return err
	}

	for _, row := range legacyRows {
		desiredRepoName := row.repoName
		if desiredRepoName == "" {
			desiredRepoName = defaultRepoName(row.repoRoot)
		}
		desiredAgentWindowName := row.agentWindowName
		if desiredAgentWindowName == "" {
			desiredAgentWindowName = defaultAgentWindowName
		}
		desiredEditorWindowName := row.editorWindowName
		if desiredEditorWindowName == "" {
			desiredEditorWindowName = defaultEditorWindowName
		}

		if desiredRepoName == row.repoName && desiredAgentWindowName == row.agentWindowName &&
			desiredEditorWindowName == row.editorWindowName {
			continue
		}

		if _, err := r.db.ExecContext(
			ctx,
			`update tasks set repo_name = ?, agent_window_name = ?, editor_window_name = ? where id = ?`,
			desiredRepoName,
			desiredAgentWindowName,
			desiredEditorWindowName,
			row.id,
		); err != nil {
			return err
		}
	}

	return nil
}

func defaultRepoName(repoRoot string) string {
	name := filepath.Base(repoRoot)
	if name == "." || name == string(filepath.Separator) || name == "" {
		return ""
	}

	return name
}
