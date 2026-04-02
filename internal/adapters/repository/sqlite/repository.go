package sqlite

import (
	"context"
	"database/sql"
	"time"

	"agent/internal/core"

	_ "modernc.org/sqlite"
)

type Repository struct {
	db *sql.DB
}

func NewRepository(path string) (*Repository, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}

	repo := &Repository{db: db}
	if err := repo.initSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}

	return repo, nil
}

func (r *Repository) initSchema() error {
	_, err := r.db.Exec(`
create table if not exists tasks (
  id text primary key,
  prompt text not null,
  display_name text not null,
  slug text not null unique,
  repo_root text not null,
  base_branch text not null,
  branch_name text not null,
  worktree_path text not null,
  tmux_session text not null,
  provider text not null,
  status text not null,
  worktree_exists integer not null,
  branch_exists integer not null,
  session_exists integer not null,
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
`)
	return err
}

func (r *Repository) CreateTask(ctx context.Context, task *core.Task) error {
	_, err := r.db.ExecContext(
		ctx,
		`insert into tasks (
			id, prompt, display_name, slug, repo_root, base_branch, branch_name,
			worktree_path, tmux_session, provider, status, worktree_exists,
			branch_exists, session_exists, last_error, created_at, updated_at, last_reconciled_at
		) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		task.ID,
		task.Prompt,
		task.DisplayName,
		task.Slug,
		task.RepoRoot,
		task.BaseBranch,
		task.BranchName,
		task.WorktreePath,
		task.TmuxSession,
		task.Provider,
		string(task.Status),
		boolToInt(task.WorktreeExists),
		boolToInt(task.BranchExists),
		boolToInt(task.SessionExists),
		task.LastError,
		formatTime(task.CreatedAt),
		formatTime(task.UpdatedAt),
		formatTime(task.LastReconciledAt),
	)
	return err
}

func (r *Repository) UpdateTask(ctx context.Context, task *core.Task) error {
	_, err := r.db.ExecContext(
		ctx,
		`update tasks set
			prompt = ?,
			display_name = ?,
			slug = ?,
			repo_root = ?,
			base_branch = ?,
			branch_name = ?,
			worktree_path = ?,
			tmux_session = ?,
			provider = ?,
			status = ?,
			worktree_exists = ?,
			branch_exists = ?,
			session_exists = ?,
			last_error = ?,
			created_at = ?,
			updated_at = ?,
			last_reconciled_at = ?
		where id = ?`,
		task.Prompt,
		task.DisplayName,
		task.Slug,
		task.RepoRoot,
		task.BaseBranch,
		task.BranchName,
		task.WorktreePath,
		task.TmuxSession,
		task.Provider,
		string(task.Status),
		boolToInt(task.WorktreeExists),
		boolToInt(task.BranchExists),
		boolToInt(task.SessionExists),
		task.LastError,
		formatTime(task.CreatedAt),
		formatTime(task.UpdatedAt),
		formatTime(task.LastReconciledAt),
		task.ID,
	)
	return err
}

func (r *Repository) GetTask(ctx context.Context, idOrSlug string) (*core.Task, error) {
	row := r.db.QueryRowContext(
		ctx,
		`select
			id, prompt, display_name, slug, repo_root, base_branch, branch_name,
			worktree_path, tmux_session, provider, status, worktree_exists,
			branch_exists, session_exists, last_error, created_at, updated_at, last_reconciled_at
		from tasks where id = ? or slug = ? limit 1`,
		idOrSlug,
		idOrSlug,
	)

	task, err := scanTask(row)
	if err == sql.ErrNoRows {
		return nil, core.ErrTaskNotFound
	}

	return task, err
}

func (r *Repository) ListTasks(ctx context.Context) ([]*core.Task, error) {
	rows, err := r.db.QueryContext(
		ctx,
		`select
			id, prompt, display_name, slug, repo_root, base_branch, branch_name,
			worktree_path, tmux_session, provider, status, worktree_exists,
			branch_exists, session_exists, last_error, created_at, updated_at, last_reconciled_at
		from tasks
		order by updated_at desc`,
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

type rowScanner interface {
	Scan(dest ...any) error
}

func scanTask(scanner rowScanner) (*core.Task, error) {
	var (
		task             core.Task
		status           string
		worktreeExists   int
		branchExists     int
		sessionExists    int
		createdAt        string
		updatedAt        string
		lastReconciledAt string
	)

	err := scanner.Scan(
		&task.ID,
		&task.Prompt,
		&task.DisplayName,
		&task.Slug,
		&task.RepoRoot,
		&task.BaseBranch,
		&task.BranchName,
		&task.WorktreePath,
		&task.TmuxSession,
		&task.Provider,
		&status,
		&worktreeExists,
		&branchExists,
		&sessionExists,
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
