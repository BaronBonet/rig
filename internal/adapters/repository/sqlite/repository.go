package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/BaronBonet/rig/internal/adapters/repository/sqlite/generated"
	"github.com/BaronBonet/rig/internal/core"

	// Register the "sqlite" database/sql driver used by sql.Open.
	_ "modernc.org/sqlite"
)

var errHealthCheckRepositoryOnly = errors.New("sqlite health-check repository only supports HealthCheck")

type repository struct {
	queries *generated.Queries
	db      *sql.DB
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
	gooseManaged, err := hasGooseMigrationTable(context.Background(), db)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if stale, err := hasStaleTasksSchema(context.Background(), db, gooseManaged); err != nil {
		_ = db.Close()
		return nil, err
	} else if stale {
		_ = db.Close()
		return nil, fmt.Errorf(
			"stale sqlite schema at %s; automatic reset is disabled to preserve existing data; "+
				"move the database aside or migrate it before starting rig",
			cfg.Path,
		)
	}
	if err := applyGooseMigrations(context.Background(), db, sqlFiles, "migrations"); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := chmodSQLiteFiles(cfg.Path); err != nil {
		_ = db.Close()
		return nil, err
	}

	return &repository{
		db:      db,
		queries: generated.New(db),
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

func (r *healthCheckRepository) ListLatestAgentSessionPrompts(
	context.Context,
	string,
) ([]core.TaskActivityEvent, error) {
	return nil, errHealthCheckRepositoryOnly
}

func (r *healthCheckRepository) UpsertTaskProviderSession(context.Context, core.TaskProviderSession) error {
	return errHealthCheckRepositoryOnly
}

func (r *healthCheckRepository) ListTaskProviderSessions(context.Context, string) ([]core.TaskProviderSession, error) {
	return nil, errHealthCheckRepositoryOnly
}

func (r *healthCheckRepository) CreateAgentSession(context.Context, core.AgentSession) error {
	return errHealthCheckRepositoryOnly
}

func (r *healthCheckRepository) OpenAgentSessionOnPane(
	context.Context,
	core.TmuxServer,
	string,
) (*core.AgentSession, error) {
	return nil, errHealthCheckRepositoryOnly
}

func (r *healthCheckRepository) ListOpenAgentSessions(context.Context, string) ([]core.AgentSession, error) {
	return nil, errHealthCheckRepositoryOnly
}

func (r *healthCheckRepository) LatestAgentSession(context.Context, string) (*core.AgentSession, error) {
	return nil, errHealthCheckRepositoryOnly
}

func (r *healthCheckRepository) UpdateAgentSession(context.Context, core.AgentSession) error {
	return errHealthCheckRepositoryOnly
}

func (r *healthCheckRepository) EndAgentSession(context.Context, string, time.Time) error {
	return errHealthCheckRepositoryOnly
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

func (r *repository) UpsertTaskProviderSession(ctx context.Context, session core.TaskProviderSession) error {
	session.TaskID = strings.TrimSpace(session.TaskID)
	if session.TaskID == "" {
		return fmt.Errorf("task provider session task ID is required")
	}

	if strings.TrimSpace(string(session.Provider)) == "" {
		return fmt.Errorf("task provider session provider is required")
	}

	session.ProviderSessionID = strings.TrimSpace(session.ProviderSessionID)
	if session.ProviderSessionID == "" {
		return fmt.Errorf("task provider session provider session ID is required")
	}

	session.TranscriptPath = strings.TrimSpace(session.TranscriptPath)
	session.StartSource = strings.TrimSpace(session.StartSource)
	session.LastEventName = strings.TrimSpace(session.LastEventName)
	session.Model = strings.TrimSpace(session.Model)
	session.Cwd = strings.TrimSpace(session.Cwd)

	if session.FirstObservedAt.IsZero() {
		session.FirstObservedAt = session.LastObservedAt
	}
	if session.FirstObservedAt.IsZero() {
		session.FirstObservedAt = time.Now().UTC()
	}
	if session.LastObservedAt.IsZero() {
		session.LastObservedAt = session.FirstObservedAt
	}

	return r.queries.UpsertTaskProviderSession(ctx, upsertTaskProviderSessionParams(session))
}

func (r *repository) ListTaskProviderSessions(ctx context.Context, taskID string) ([]core.TaskProviderSession, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, nil
	}

	rows, err := r.queries.ListTaskProviderSessions(ctx, taskID)
	if err != nil {
		return nil, err
	}

	return taskProviderSessionsFromRows(rows), nil
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

func (r *repository) ListLatestAgentSessionPrompts(
	ctx context.Context,
	taskID string,
) ([]core.TaskActivityEvent, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, nil
	}

	rows, err := r.queries.ListLatestAgentSessionPrompts(ctx, taskID)
	if err != nil {
		return nil, err
	}
	return taskActivityEventsFromRows(rows), nil
}

func (r *repository) CreateAgentSession(ctx context.Context, session core.AgentSession) error {
	session.ID = strings.TrimSpace(session.ID)
	if session.ID == "" {
		return fmt.Errorf("agent session ID is required")
	}
	session.TaskID = strings.TrimSpace(session.TaskID)
	if session.TaskID == "" {
		return fmt.Errorf("agent session task ID is required")
	}
	if strings.TrimSpace(string(session.Provider)) == "" {
		return fmt.Errorf("agent session provider is required")
	}
	if session.StartedAt.IsZero() {
		session.StartedAt = time.Now().UTC()
	}

	return r.queries.CreateAgentSession(ctx, createAgentSessionParams(session))
}

func (r *repository) OpenAgentSessionOnPane(
	ctx context.Context,
	server core.TmuxServer,
	pane string,
) (*core.AgentSession, error) {
	pane = strings.TrimSpace(pane)
	if pane == "" {
		return nil, nil
	}

	row, err := r.queries.OpenAgentSessionOnPane(ctx, generated.OpenAgentSessionOnPaneParams{
		TmuxSocketPath: server.SocketPath,
		TmuxServerPid:  int64(server.PID),
		TmuxPane:       pane,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	session := agentSessionFromRow(row)
	return &session, nil
}

func (r *repository) ListOpenAgentSessions(ctx context.Context, taskID string) ([]core.AgentSession, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, nil
	}

	rows, err := r.queries.ListOpenAgentSessions(ctx, taskID)
	if err != nil {
		return nil, err
	}

	// Times are stored as RFC 3339 text without trailing zeros, so their text
	// order is not time order within one second.
	sessions := agentSessionsFromRows(rows)
	slices.SortStableFunc(sessions, func(left, right core.AgentSession) int {
		if order := left.StartedAt.Compare(right.StartedAt); order != 0 {
			return order
		}
		return strings.Compare(left.ID, right.ID)
	})
	return sessions, nil
}

func (r *repository) LatestAgentSession(ctx context.Context, taskID string) (*core.AgentSession, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, nil
	}

	row, err := r.queries.LatestAgentSession(ctx, taskID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	session := agentSessionFromRow(row)
	return &session, nil
}

// UpdateAgentSession leaves an agent session that has already ended
// unchanged: it may have ended between being read and being updated.
func (r *repository) UpdateAgentSession(ctx context.Context, session core.AgentSession) error {
	session.ID = strings.TrimSpace(session.ID)
	if session.ID == "" {
		return fmt.Errorf("agent session ID is required")
	}

	return r.queries.UpdateAgentSession(ctx, updateAgentSessionParams(session))
}

func (r *repository) EndAgentSession(ctx context.Context, agentSessionID string, endedAt time.Time) error {
	agentSessionID = strings.TrimSpace(agentSessionID)
	if agentSessionID == "" {
		return fmt.Errorf("agent session ID is required")
	}
	if endedAt.IsZero() {
		endedAt = time.Now().UTC()
	}

	return r.queries.EndAgentSession(ctx, generated.EndAgentSessionParams{
		EndedAt: formatTime(endedAt),
		ID:      agentSessionID,
	})
}

func openSQLiteDB(path string) (*sql.DB, error) {
	if err := ensureSQLiteDBFileMode(path); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return db, nil
}

func ensureSQLiteDBFileMode(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("open sqlite database file %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close sqlite database file %s: %w", path, err)
	}
	return chmodSQLiteFiles(path)
}

func chmodSQLiteFiles(path string) error {
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		info, err := os.Lstat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("stat sqlite file %s: %w", candidate, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("sqlite file %s must be a regular file", candidate)
		}
		if err := os.Chmod(candidate, 0o600); err != nil {
			return fmt.Errorf("chmod sqlite file %s: %w", candidate, err)
		}
	}
	return nil
}

func hasGooseMigrationTable(ctx context.Context, db *sql.DB) (bool, error) {
	var exists int
	if err := db.QueryRowContext(ctx, `
		select exists(
			select 1
			from sqlite_master
			where type = 'table' and name = 'goose_db_version'
		)
	`).Scan(&exists); err != nil {
		return false, fmt.Errorf("inspect goose migration state: %w", err)
	}
	return exists == 1, nil
}

func hasStaleTasksSchema(ctx context.Context, db *sql.DB, gooseManaged bool) (bool, error) {
	if gooseManaged {
		return false, nil
	}

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
