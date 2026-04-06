package sqlite

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"agent/internal/core"

	"github.com/stretchr/testify/require"
)

func newTestRepository(t *testing.T) *Repository {
	t.Helper()

	repo, err := NewRepository(Config{Path: filepath.Join(t.TempDir(), "state.db")})
	require.NoError(t, err)

	return repo
}

func TestRepositoryCreateAndGetTask(t *testing.T) {
	repo := newTestRepository(t)

	task := &core.Task{
		ID:                 "task-1",
		Prompt:             "add billing retry flow",
		DisplayName:        "billing retry flow",
		Slug:               "billing-retry-flow",
		RepoRoot:           "/tmp/repo",
		RepoName:           "repo",
		BaseBranch:         "main",
		BranchName:         "feat/billing-retry-flow",
		WorktreePath:       "/tmp/repo-billing-retry-flow",
		TmuxSession:        "repo-billing-retry-flow",
		AgentWindowName:    "agent",
		EditorWindowName:   "editor",
		Provider:           "codex",
		Status:             core.TaskStatusCreating,
		CreatedAt:          time.Now().UTC(),
		UpdatedAt:          time.Now().UTC(),
		WorktreeExists:     true,
		BranchExists:       true,
		SessionExists:      false,
		AgentWindowExists:  true,
		EditorWindowExists: false,
	}

	require.NoError(t, repo.CreateTask(context.Background(), task))

	got, err := repo.GetTask(context.Background(), "billing-retry-flow")
	require.NoError(t, err)
	require.Equal(t, task.DisplayName, got.DisplayName)
	require.Equal(t, task.BranchName, got.BranchName)
	require.Equal(t, task.Status, got.Status)
	require.Equal(t, task.RepoName, got.RepoName)
	require.Equal(t, task.AgentWindowName, got.AgentWindowName)
	require.Equal(t, task.EditorWindowName, got.EditorWindowName)
	require.Equal(t, task.AgentWindowExists, got.AgentWindowExists)
	require.Equal(t, task.EditorWindowExists, got.EditorWindowExists)
}

func TestRepositoryUpdateTask_RoundTripsNewMetadata(t *testing.T) {
	repo := newTestRepository(t)

	task := &core.Task{
		ID:                 "task-1",
		Prompt:             "add billing retry flow",
		DisplayName:        "billing retry flow",
		Slug:               "billing-retry-flow",
		RepoRoot:           "/tmp/repo",
		RepoName:           "repo",
		BaseBranch:         "main",
		BranchName:         "feat/billing-retry-flow",
		WorktreePath:       "/tmp/repo-billing-retry-flow",
		TmuxSession:        "repo-billing-retry-flow",
		AgentWindowName:    "agent",
		EditorWindowName:   "editor",
		Provider:           "codex",
		Status:             core.TaskStatusCreating,
		CreatedAt:          time.Now().UTC(),
		UpdatedAt:          time.Now().UTC(),
		WorktreeExists:     true,
		BranchExists:       true,
		SessionExists:      false,
		AgentWindowExists:  true,
		EditorWindowExists: false,
	}

	require.NoError(t, repo.CreateTask(context.Background(), task))

	task.RepoName = "repo-updated"
	task.AgentWindowName = "agent-updated"
	task.EditorWindowName = "editor-updated"
	task.AgentWindowExists = false
	task.EditorWindowExists = true
	task.UpdatedAt = time.Now().Add(time.Minute).UTC()

	require.NoError(t, repo.UpdateTask(context.Background(), task))

	got, err := repo.GetTask(context.Background(), task.ID)
	require.NoError(t, err)
	require.Equal(t, task.RepoName, got.RepoName)
	require.Equal(t, task.AgentWindowName, got.AgentWindowName)
	require.Equal(t, task.EditorWindowName, got.EditorWindowName)
	require.Equal(t, task.AgentWindowExists, got.AgentWindowExists)
	require.Equal(t, task.EditorWindowExists, got.EditorWindowExists)
}

func TestRepositoryListTasks_OrdersByCreatedAtAscending(t *testing.T) {
	repo := newTestRepository(t)

	older := &core.Task{
		ID:           "task-1",
		Prompt:       "first prompt",
		DisplayName:  "first task",
		Slug:         "first-task",
		RepoRoot:     "/tmp/repo",
		RepoName:     "repo",
		BaseBranch:   "main",
		BranchName:   "feat/first-task",
		WorktreePath: "/tmp/repo-first-task",
		TmuxSession:  "repo-first-task",
		Provider:     "codex",
		Status:       core.TaskStatusReady,
		CreatedAt:    time.Now().Add(-2 * time.Hour).UTC(),
		UpdatedAt:    time.Now().Add(-2 * time.Hour).UTC(),
	}
	newer := &core.Task{
		ID:           "task-2",
		Prompt:       "second prompt",
		DisplayName:  "second task",
		Slug:         "second-task",
		RepoRoot:     "/tmp/repo",
		RepoName:     "repo",
		BaseBranch:   "main",
		BranchName:   "feat/second-task",
		WorktreePath: "/tmp/repo-second-task",
		TmuxSession:  "repo-second-task",
		Provider:     "codex",
		Status:       core.TaskStatusRunning,
		CreatedAt:    time.Now().Add(-1 * time.Hour).UTC(),
		UpdatedAt:    time.Now().Add(-1 * time.Hour).UTC(),
	}

	require.NoError(t, repo.CreateTask(context.Background(), older))
	require.NoError(t, repo.CreateTask(context.Background(), newer))

	tasks, err := repo.ListTasks(context.Background())
	require.NoError(t, err)
	require.Len(t, tasks, 2)
	require.Equal(t, "first-task", tasks[0].Slug)
	require.Equal(t, "second-task", tasks[1].Slug)
	require.Equal(t, "repo", tasks[0].RepoName)
	require.Equal(t, "repo", tasks[1].RepoName)
}

func TestNewRepository_CreatesParentDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state.db")

	repo, err := NewRepository(Config{Path: path})
	require.NoError(t, err)
	require.NotNil(t, repo)
}

func TestValidateConfig_CreatesParentDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state.db")

	require.NoError(t, ValidateConfig(Config{Path: path}))
	_, err := os.Stat(filepath.Dir(path))
	require.NoError(t, err)
}

func TestValidateConfig_RejectsFileParent(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "blocker")
	require.NoError(t, os.WriteFile(parent, []byte("x"), 0o644))

	err := ValidateConfig(Config{Path: filepath.Join(parent, "state.db")})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a directory")
}

func TestRepositoryIsAvailable_ValidatesConfiguredPath(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "blocker")
	require.NoError(t, os.WriteFile(parent, []byte("x"), 0o644))

	repo, err := NewRepository(Config{Path: filepath.Join(parent, "state.db")})
	require.NoError(t, err)

	err = repo.IsAvailable(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a directory")
}

func TestNewRepository_ReturnsUnavailableRepositoryForInvalidConfig(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "blocker")
	require.NoError(t, os.WriteFile(parent, []byte("x"), 0o644))

	repo, err := NewRepository(Config{Path: filepath.Join(parent, "state.db")})
	require.NoError(t, err)
	require.NotNil(t, repo)

	err = repo.IsAvailable(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a directory")

	_, err = repo.ListTasks(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a directory")
}

func TestNewRepository_MigratesLegacyTaskRow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")

	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)

	_, err = db.ExecContext(context.Background(), `
create table tasks (
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
`)
	require.NoError(t, err)
	_, err = db.ExecContext(context.Background(), `
insert into tasks (
  id, prompt, display_name, slug, repo_root, base_branch, branch_name,
  worktree_path, tmux_session, provider, status, worktree_exists,
  branch_exists, session_exists, last_error, created_at, updated_at, last_reconciled_at
) values (
  'legacy-task',
  'legacy prompt',
  'legacy task',
  'legacy-task',
  '/tmp/repo',
  'main',
  'feat/legacy-task',
  '/tmp/repo-legacy-task',
  'repo-legacy-task',
  'codex',
  'ready',
  1,
  0,
  0,
  '',
  '2026-04-04T10:00:00Z',
  '2026-04-04T10:00:00Z',
  ''
);
`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	repo, err := NewRepository(Config{Path: path})
	require.NoError(t, err)
	require.NotNil(t, repo)

	got, err := repo.GetTask(context.Background(), "legacy-task")
	require.NoError(t, err)
	require.Equal(t, "repo", got.RepoName)
	require.Equal(t, defaultAgentWindowName, got.AgentWindowName)
	require.Equal(t, defaultEditorWindowName, got.EditorWindowName)
	require.False(t, got.AgentWindowExists)
	require.False(t, got.EditorWindowExists)

	got.RepoName = "repo-updated"
	got.AgentWindowName = "agent-updated"
	got.EditorWindowName = "editor-updated"
	got.AgentWindowExists = true
	got.EditorWindowExists = true
	got.UpdatedAt = time.Now().UTC()

	require.NoError(t, repo.UpdateTask(context.Background(), got))

	updated, err := repo.GetTask(context.Background(), "legacy-task")
	require.NoError(t, err)
	require.Equal(t, "repo-updated", updated.RepoName)
	require.Equal(t, "agent-updated", updated.AgentWindowName)
	require.Equal(t, "editor-updated", updated.EditorWindowName)
	require.True(t, updated.AgentWindowExists)
	require.True(t, updated.EditorWindowExists)

	db, err = sql.Open("sqlite", path)
	require.NoError(t, err)
	defer db.Close()

	columns := taskTableColumns(t, db)
	for _, column := range []string{
		"repo_name",
		"agent_window_name",
		"editor_window_name",
		"agent_window_exists",
		"editor_window_exists",
	} {
		require.Contains(t, columns, column)
	}
}

func taskTableColumns(t *testing.T, db *sql.DB) map[string]struct{} {
	t.Helper()

	rows, err := db.QueryContext(context.Background(), `pragma table_info(tasks)`)
	require.NoError(t, err)
	defer rows.Close()

	columns := make(map[string]struct{})
	for rows.Next() {
		var (
			cid          int
			colName      string
			colType      string
			notNull      int
			defaultValue sql.NullString
			pk           int
		)
		require.NoError(t, rows.Scan(&cid, &colName, &colType, &notNull, &defaultValue, &pk))
		columns[colName] = struct{}{}
	}
	require.NoError(t, rows.Err())

	return columns
}
