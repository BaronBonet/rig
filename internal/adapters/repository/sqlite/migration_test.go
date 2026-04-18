package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"log"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	_ "modernc.org/sqlite"

	"github.com/stretchr/testify/require"
)

func TestApplyGooseMigrations_RunsPendingFilesInOrderOnce(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	files := fstest.MapFS{
		"migrations/000001_create_numbers.sql": {Data: []byte(`
-- +goose Up
create table numbers (value integer not null);
insert into numbers(value) values (1);
-- +goose Down
`)},
		"migrations/000002_insert_numbers.sql": {Data: []byte(`
-- +goose Up
insert into numbers(value) values (2);
-- +goose Down
`)},
	}

	require.NoError(t, applyGooseMigrations(context.Background(), db, files, "migrations"))
	require.NoError(t, applyGooseMigrations(context.Background(), db, files, "migrations"))

	var count int
	require.NoError(t, db.QueryRowContext(context.Background(), `select count(*) from numbers`).Scan(&count))
	require.Equal(t, 2, count)
}

func TestApplyGooseMigrations_DoesNotEmitLogs(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	files := fstest.MapFS{
		"migrations/000001_create_numbers.sql": {Data: []byte(`
-- +goose Up
create table numbers (value integer not null);
-- +goose Down
`)},
	}

	originalWriter := log.Writer()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(originalWriter) })

	require.NoError(t, applyGooseMigrations(context.Background(), db, files, "migrations"))
	require.NoError(t, applyGooseMigrations(context.Background(), db, files, "migrations"))
	require.Empty(t, strings.TrimSpace(buf.String()))
}

func TestApplyGooseMigrations_FailsOnInvalidSQL(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	files := fstest.MapFS{
		"migrations/000001_bad.sql": {Data: []byte(`
-- +goose Up
this is not sql;
-- +goose Down
`)},
	}

	err = applyGooseMigrations(context.Background(), db, files, "migrations")
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

func TestApplyGooseMigrations_TaskSchemaDoesNotIncludeLastError(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	require.NoError(t, applyGooseMigrations(context.Background(), db, sqlFiles, "migrations"))

	rows, err := db.QueryContext(context.Background(), `pragma table_info(tasks)`)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, rows.Close()) })

	for rows.Next() {
		var (
			cid        int
			name       string
			columnType string
			notNull    int
			dfltValue  sql.NullString
			pk         int
		)
		require.NoError(t, rows.Scan(&cid, &name, &columnType, &notNull, &dfltValue, &pk))
		require.NotEqual(t, "last_error", name)
	}
	require.NoError(t, rows.Err())
}
