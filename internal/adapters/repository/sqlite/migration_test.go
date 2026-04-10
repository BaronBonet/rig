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
