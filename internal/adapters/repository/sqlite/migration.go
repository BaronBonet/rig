package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

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

func seedLegacyMigrationState(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `
create table if not exists schema_migrations (
  version text primary key,
  applied_at text not null
)`); err != nil {
		return fmt.Errorf("create schema_migrations table: %w", err)
	}

	var total int
	if err := db.QueryRowContext(ctx, `select count(*) from schema_migrations`).Scan(&total); err != nil {
		return fmt.Errorf("count schema_migrations rows: %w", err)
	}
	if total > 0 {
		return nil
	}

	tasksExists, err := tableExists(ctx, db, "tasks")
	if err != nil {
		return fmt.Errorf("check tasks table: %w", err)
	}
	if !tasksExists {
		return nil
	}

	versions := []string{"000001_initial_tasks_and_events"}

	hasRepoName, err := columnExists(ctx, db, "tasks", "repo_name")
	if err != nil {
		return fmt.Errorf("check tasks.repo_name column: %w", err)
	}
	if hasRepoName {
		versions = append(versions, "000002_add_task_metadata_columns")
	}

	hookTablesExist, err := tableExists(ctx, db, "task_hook_events")
	if err != nil {
		return fmt.Errorf("check task_hook_events table: %w", err)
	}
	if hookTablesExist {
		versions = append(versions, "000003_add_hook_observability_tables")
	}

	for _, version := range versions {
		if _, err := db.ExecContext(ctx, `
insert into schema_migrations(version, applied_at)
values (?, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
on conflict(version) do nothing`, version); err != nil {
			return fmt.Errorf("seed migration %s: %w", version, err)
		}
	}

	return nil
}

func applyMigrations(ctx context.Context, db *sql.DB, files fs.FS, dir string) error {
	if _, err := db.ExecContext(ctx, `
create table if not exists schema_migrations (
  version text primary key,
  applied_at text not null
)`); err != nil {
		return fmt.Errorf("create schema_migrations table: %w", err)
	}

	entries, err := fs.ReadDir(files, dir)
	if err != nil {
		return fmt.Errorf("read migrations dir %s: %w", dir, err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		version := strings.TrimSuffix(name, filepath.Ext(name))
		var exists int
		if err := db.QueryRowContext(ctx, `select count(*) from schema_migrations where version = ?`, version).Scan(&exists); err != nil {
			return fmt.Errorf("check migration %s: %w", name, err)
		}
		if exists > 0 {
			continue
		}

		content, err := fs.ReadFile(files, filepath.Join(dir, name))
		if err != nil {
			return fmt.Errorf("read migration %s: %w", filepath.Join(dir, name), err)
		}

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, string(content)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, `
insert into schema_migrations(version, applied_at) values (?, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`, version); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", name, err)
		}
	}

	return nil
}
