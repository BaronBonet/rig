package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sync"

	"github.com/pressly/goose/v3"
)

var gooseMigrationMu sync.Mutex

var legacyTaskMetadataColumns = []struct {
	name      string
	statement string
}{
	{
		name:      "repo_name",
		statement: `ALTER TABLE tasks ADD COLUMN repo_name text NOT NULL DEFAULT ''`,
	},
	{
		name:      "agent_window_name",
		statement: `ALTER TABLE tasks ADD COLUMN agent_window_name text NOT NULL DEFAULT 'agent'`,
	},
	{
		name:      "editor_window_name",
		statement: `ALTER TABLE tasks ADD COLUMN editor_window_name text NOT NULL DEFAULT 'editor'`,
	},
	{
		name:      "agent_window_exists",
		statement: `ALTER TABLE tasks ADD COLUMN agent_window_exists integer NOT NULL DEFAULT 0`,
	},
	{
		name:      "editor_window_exists",
		statement: `ALTER TABLE tasks ADD COLUMN editor_window_exists integer NOT NULL DEFAULT 0`,
	},
}

func tableExists(ctx context.Context, db *sql.DB, table string) (bool, error) {
	var count int
	err := db.QueryRowContext(
		ctx,
		`select count(*) from sqlite_master where type = 'table' and name = ?`,
		table,
	).Scan(&count)
	return count > 0, err
}

func columnExists(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	rows, err := db.QueryContext(ctx, `pragma table_info(`+table+`)`)
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

func applyBootstrapSQL(ctx context.Context, db *sql.DB, files fs.FS, path string) error {
	content, err := fs.ReadFile(files, path)
	if err != nil {
		return fmt.Errorf("read bootstrap %s: %w", path, err)
	}
	if _, err := db.ExecContext(ctx, string(content)); err != nil {
		return fmt.Errorf("apply bootstrap %s: %w", path, err)
	}
	return nil
}

func applyGooseMigrations(ctx context.Context, db *sql.DB, files fs.FS, dir string) error {
	gooseMigrationMu.Lock()
	defer gooseMigrationMu.Unlock()

	migrationsFS, err := fs.Sub(files, dir)
	if err != nil {
		return fmt.Errorf("open goose migrations fs %s: %w", dir, err)
	}

	provider, err := goose.NewProvider(goose.DialectSQLite3, db, migrationsFS)
	if err != nil {
		return fmt.Errorf("create goose provider for %s: %w", dir, err)
	}
	if _, err := provider.Up(ctx); err != nil {
		var partialErr *goose.PartialError
		if errors.As(err, &partialErr) && partialErr.Failed != nil && partialErr.Failed.Source != nil {
			return fmt.Errorf(
				"apply goose migration %s from %s: %w",
				filepath.Base(partialErr.Failed.Source.Path),
				dir,
				err,
			)
		}
		return fmt.Errorf("apply goose migrations from %s: %w", dir, err)
	}
	return nil
}

func repairLegacyTasksSchema(ctx context.Context, db *sql.DB) error {
	tasksExists, err := tableExists(ctx, db, "tasks")
	if err != nil {
		return fmt.Errorf("check tasks table: %w", err)
	}
	if !tasksExists {
		return nil
	}

	for _, column := range legacyTaskMetadataColumns {
		exists, err := columnExists(ctx, db, "tasks", column.name)
		if err != nil {
			return fmt.Errorf("check tasks.%s column: %w", column.name, err)
		}
		if exists {
			continue
		}

		if _, err := db.ExecContext(ctx, column.statement); err != nil {
			return fmt.Errorf("add tasks.%s column: %w", column.name, err)
		}
	}

	return nil
}
