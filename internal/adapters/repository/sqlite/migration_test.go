package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"testing/fstest"

	_ "modernc.org/sqlite"

	"github.com/stretchr/testify/require"
)

func TestApplyMigrations_RunsPendingFilesInOrderOnce(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	files := fstest.MapFS{
		"migrations/000001_create_numbers.sql": {Data: []byte(`
create table numbers (value integer not null);
insert into numbers(value) values (1);
`)},
		"migrations/000002_insert_numbers.sql": {Data: []byte(`
insert into numbers(value) values (2);
`)},
	}

	require.NoError(t, applyMigrations(context.Background(), db, files, "migrations"))
	require.NoError(t, applyMigrations(context.Background(), db, files, "migrations"))

	var count int
	require.NoError(t, db.QueryRowContext(context.Background(), `select count(*) from numbers`).Scan(&count))
	require.Equal(t, 2, count)
}

func TestApplyMigrations_FailsOnInvalidSQL(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	files := fstest.MapFS{
		"migrations/000001_bad.sql": {Data: []byte(`this is not sql;`)},
	}

	err = applyMigrations(context.Background(), db, files, "migrations")
	require.Error(t, err)
	require.Contains(t, err.Error(), "000001_bad.sql")
}

func TestApplyBootstrapSQL_UsesEmbeddedBootstrapAssetPragmas(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "bootstrap.db")
	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	require.NoError(t, applyBootstrapSQL(context.Background(), db, sqlFiles, "bootstrap/connection.sql"))

	var journalMode string
	require.NoError(t, db.QueryRowContext(context.Background(), `pragma journal_mode`).Scan(&journalMode))
	require.Equal(t, "wal", journalMode)

	var busyTimeout int
	require.NoError(t, db.QueryRowContext(context.Background(), `pragma busy_timeout`).Scan(&busyTimeout))
	require.Equal(t, 5000, busyTimeout)

	var synchronous int
	require.NoError(t, db.QueryRowContext(context.Background(), `pragma synchronous`).Scan(&synchronous))
	require.Equal(t, 1, synchronous)

	var foreignKeys int
	require.NoError(t, db.QueryRowContext(context.Background(), `pragma foreign_keys`).Scan(&foreignKeys))
	require.Equal(t, 1, foreignKeys)
}

func TestColumnSupportsConflictTarget_RequiresExactSingleColumnConstraint(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	_, err = db.ExecContext(context.Background(), `
create table hook_sessions_good_pk (
  task_id text primary key,
  session_id text not null default ''
);
create table hook_sessions_good_unique (
  task_id text not null,
  session_id text not null default ''
);
create unique index idx_hook_sessions_good_unique_task_id on hook_sessions_good_unique(task_id);
create table hook_sessions_composite_pk (
  task_id text not null,
  session_id text not null default '',
  primary key (task_id, session_id)
);
create table observer_summaries_partial_unique (
  task_id text not null,
  updated_at text not null default ''
);
create unique index idx_observer_summaries_partial_task_id on observer_summaries_partial_unique(task_id)
where updated_at <> '';
`)
	require.NoError(t, err)

	ok, err := columnSupportsConflictTarget(context.Background(), db, "hook_sessions_good_pk", "task_id")
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = columnSupportsConflictTarget(context.Background(), db, "hook_sessions_good_unique", "task_id")
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = columnSupportsConflictTarget(context.Background(), db, "hook_sessions_composite_pk", "task_id")
	require.NoError(t, err)
	require.False(t, ok)

	ok, err = columnSupportsConflictTarget(context.Background(), db, "observer_summaries_partial_unique", "task_id")
	require.NoError(t, err)
	require.False(t, ok)
}

func TestColumnIsExactIntegerPrimaryKey_RequiresRowIDCompatibleShape(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	_, err = db.ExecContext(context.Background(), `
create table events_good (
  id integer primary key,
  payload text not null default ''
);
create table events_missing_pk (
  id integer not null,
  payload text not null default ''
);
create table events_wrong_type (
  id text primary key,
  payload text not null default ''
);
create table events_composite_pk (
  id integer not null,
  task_id text not null,
  primary key (id, task_id)
);
`)
	require.NoError(t, err)

	ok, err := columnIsExactIntegerPrimaryKey(context.Background(), db, "events_good", "id")
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = columnIsExactIntegerPrimaryKey(context.Background(), db, "events_missing_pk", "id")
	require.NoError(t, err)
	require.False(t, ok)

	ok, err = columnIsExactIntegerPrimaryKey(context.Background(), db, "events_wrong_type", "id")
	require.NoError(t, err)
	require.False(t, ok)

	ok, err = columnIsExactIntegerPrimaryKey(context.Background(), db, "events_composite_pk", "id")
	require.NoError(t, err)
	require.False(t, ok)
}
