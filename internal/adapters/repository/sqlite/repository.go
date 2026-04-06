package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"agent/internal/core"

	_ "modernc.org/sqlite"
)

type Repository struct {
	db      *sql.DB
	path    string
	initErr error
}

type Config struct {
	Path string
}

const (
	defaultAgentWindowName  = "agent"
	defaultEditorWindowName = "editor"
)

func NewRepository(cfg Config) (*Repository, error) {
	repo := &Repository{path: cfg.Path}

	if err := ValidateConfig(cfg); err != nil {
		repo.initErr = err
		return repo, nil
	}

	db, err := sql.Open("sqlite", cfg.Path)
	if err != nil {
		repo.initErr = err
		return repo, nil
	}

	repo.db = db
	if err := repo.initSchema(); err != nil {
		repo.initErr = err
		_ = db.Close()
		repo.db = nil
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

func (r *Repository) initSchema() error {
	ctx := context.Background()
	if _, err := r.db.ExecContext(ctx, `
create table if not exists tasks (
  id text primary key,
  prompt text not null,
  display_name text not null,
  slug text not null unique,
  repo_root text not null,
  repo_name text not null default '',
  base_branch text not null,
  branch_name text not null,
  worktree_path text not null,
  tmux_session text not null,
  agent_window_name text not null default 'agent',
  editor_window_name text not null default 'editor',
  provider text not null,
  status text not null,
  worktree_exists integer not null,
  branch_exists integer not null,
  session_exists integer not null,
  agent_window_exists integer not null default 0,
  editor_window_exists integer not null default 0,
  last_error text not null default '',
  created_at text not null,
  updated_at text not null,
  last_reconciled_at text not null default ''
);

create table if not exists events (
  id integer primary key autoincrement,
  task_id text not null,
  event_type text not null,
  payload text not null,
  created_at text not null
);
`); err != nil {
		return err
	}

	for _, stmt := range []string{
		`alter table tasks add column repo_name text not null default ''`,
		`alter table tasks add column agent_window_name text not null default 'agent'`,
		`alter table tasks add column editor_window_name text not null default 'editor'`,
		`alter table tasks add column agent_window_exists integer not null default 0`,
		`alter table tasks add column editor_window_exists integer not null default 0`,
	} {
		column := columnNameFromAlter(stmt)
		if err := addColumnIfMissing(r.db, "tasks", column, stmt); err != nil {
			return err
		}
	}

	if err := r.backfillLegacyTaskRows(); err != nil {
		return err
	}

	return nil
}

func (r *Repository) CreateTask(ctx context.Context, task *core.Task) error {
	if err := r.unavailableErr(); err != nil {
		return err
	}

	_, err := r.db.ExecContext(
		ctx,
		`insert into tasks (
			id, prompt, display_name, slug, repo_root, repo_name, base_branch, branch_name,
			worktree_path, tmux_session, agent_window_name, editor_window_name,
			provider, status, worktree_exists, branch_exists, session_exists,
			agent_window_exists, editor_window_exists, last_error,
			created_at, updated_at, last_reconciled_at
		) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		task.ID,
		task.Prompt,
		task.DisplayName,
		task.Slug,
		task.RepoRoot,
		task.RepoName,
		task.BaseBranch,
		task.BranchName,
		task.WorktreePath,
		task.TmuxSession,
		task.AgentWindowName,
		task.EditorWindowName,
		task.Provider,
		string(task.Status),
		boolToInt(task.WorktreeExists),
		boolToInt(task.BranchExists),
		boolToInt(task.SessionExists),
		boolToInt(task.AgentWindowExists),
		boolToInt(task.EditorWindowExists),
		task.LastError,
		formatTime(task.CreatedAt),
		formatTime(task.UpdatedAt),
		formatTime(task.LastReconciledAt),
	)
	return err
}

func (r *Repository) UpdateTask(ctx context.Context, task *core.Task) error {
	if err := r.unavailableErr(); err != nil {
		return err
	}

	_, err := r.db.ExecContext(
		ctx,
		`update tasks set
			prompt = ?,
			display_name = ?,
			slug = ?,
			repo_root = ?,
			repo_name = ?,
			base_branch = ?,
			branch_name = ?,
			worktree_path = ?,
			tmux_session = ?,
			agent_window_name = ?,
			editor_window_name = ?,
			provider = ?,
			status = ?,
			worktree_exists = ?,
			branch_exists = ?,
			session_exists = ?,
			agent_window_exists = ?,
			editor_window_exists = ?,
			last_error = ?,
			created_at = ?,
			updated_at = ?,
			last_reconciled_at = ?
		where id = ?`,
		task.Prompt,
		task.DisplayName,
		task.Slug,
		task.RepoRoot,
		task.RepoName,
		task.BaseBranch,
		task.BranchName,
		task.WorktreePath,
		task.TmuxSession,
		task.AgentWindowName,
		task.EditorWindowName,
		task.Provider,
		string(task.Status),
		boolToInt(task.WorktreeExists),
		boolToInt(task.BranchExists),
		boolToInt(task.SessionExists),
		boolToInt(task.AgentWindowExists),
		boolToInt(task.EditorWindowExists),
		task.LastError,
		formatTime(task.CreatedAt),
		formatTime(task.UpdatedAt),
		formatTime(task.LastReconciledAt),
		task.ID,
	)
	return err
}

func (r *Repository) GetTask(ctx context.Context, idOrSlug string) (*core.Task, error) {
	if err := r.unavailableErr(); err != nil {
		return nil, err
	}

	row := r.db.QueryRowContext(
		ctx,
		`select
			id, prompt, display_name, slug, repo_root, repo_name,
			base_branch, branch_name, worktree_path, tmux_session,
			agent_window_name, editor_window_name, provider, status,
			worktree_exists, branch_exists, session_exists,
			agent_window_exists, editor_window_exists, last_error,
			created_at, updated_at, last_reconciled_at
		from tasks where id = ? or slug = ? limit 1`,
		idOrSlug,
		idOrSlug,
	)

	task, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, core.ErrTaskNotFound
	}

	return task, err
}

func (r *Repository) ListTasks(ctx context.Context) ([]*core.Task, error) {
	if err := r.unavailableErr(); err != nil {
		return nil, err
	}

	rows, err := r.db.QueryContext(
		ctx,
		`select
			id, prompt, display_name, slug, repo_root, repo_name,
			base_branch, branch_name, worktree_path, tmux_session,
			agent_window_name, editor_window_name, provider, status,
			worktree_exists, branch_exists, session_exists,
			agent_window_exists, editor_window_exists, last_error,
			created_at, updated_at, last_reconciled_at
		from tasks
		order by created_at asc`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []*core.Task
	for rows.Next() {
		task, scanErr := scanTask(rows)
		if scanErr != nil {
			return nil, scanErr
		}

		tasks = append(tasks, task)
	}

	return tasks, rows.Err()
}

func (r *Repository) AppendEvent(ctx context.Context, taskID, eventType, payload string) error {
	if err := r.unavailableErr(); err != nil {
		return err
	}

	_, err := r.db.ExecContext(
		ctx,
		`insert into events (task_id, event_type, payload, created_at) values (?, ?, ?, ?)`,
		taskID,
		eventType,
		payload,
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	return err
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

type rowScanner interface {
	Scan(dest ...any) error
}

func scanTask(scanner rowScanner) (*core.Task, error) {
	var (
		task               core.Task
		status             string
		worktreeExists     int
		branchExists       int
		sessionExists      int
		agentWindowExists  int
		editorWindowExists int
		createdAt          string
		updatedAt          string
		lastReconciledAt   string
	)

	err := scanner.Scan(
		&task.ID,
		&task.Prompt,
		&task.DisplayName,
		&task.Slug,
		&task.RepoRoot,
		&task.RepoName,
		&task.BaseBranch,
		&task.BranchName,
		&task.WorktreePath,
		&task.TmuxSession,
		&task.AgentWindowName,
		&task.EditorWindowName,
		&task.Provider,
		&status,
		&worktreeExists,
		&branchExists,
		&sessionExists,
		&agentWindowExists,
		&editorWindowExists,
		&task.LastError,
		&createdAt,
		&updatedAt,
		&lastReconciledAt,
	)
	if err != nil {
		return nil, err
	}

	task.Status = core.TaskStatus(status)
	task.WorktreeExists = worktreeExists == 1
	task.BranchExists = branchExists == 1
	task.SessionExists = sessionExists == 1
	task.AgentWindowExists = agentWindowExists == 1
	task.EditorWindowExists = editorWindowExists == 1
	task.CreatedAt = parseTime(createdAt)
	task.UpdatedAt = parseTime(updatedAt)
	task.LastReconciledAt = parseTime(lastReconciledAt)

	return &task, nil
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

func addColumnIfMissing(db *sql.DB, table, column, statement string) error {
	exists, err := hasColumn(db, table, column)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	_, err = db.ExecContext(context.Background(), statement)
	return err
}

func columnNameFromAlter(statement string) string {
	const prefix = `alter table tasks add column `
	if len(statement) <= len(prefix) || statement[:len(prefix)] != prefix {
		return ""
	}

	rest := statement[len(prefix):]
	for i, r := range rest {
		if r == ' ' {
			return rest[:i]
		}
	}

	return rest
}

func hasColumn(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.QueryContext(
		context.Background(),
		`pragma table_info(`+table+`)`,
	)
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			cid          int
			name         string
			colType      string
			notNull      int
			defaultValue sql.NullString
			pk           int
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultValue, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}

	return false, rows.Err()
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
