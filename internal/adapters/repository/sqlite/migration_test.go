package sqlite

import (
	"context"
	"database/sql"
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

func TestApplyBootstrapSQL_ExecutesStatements(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	files := fstest.MapFS{
		"bootstrap/connection.sql": {Data: []byte(`pragma foreign_keys = on;`)},
	}

	require.NoError(t, applyBootstrapSQL(context.Background(), db, files, "bootstrap/connection.sql"))

	var enabled int
	require.NoError(t, db.QueryRowContext(context.Background(), `pragma foreign_keys`).Scan(&enabled))
	require.Equal(t, 1, enabled)
}
