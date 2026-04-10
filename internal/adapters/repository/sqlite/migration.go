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
		return err
	}
	if _, err := db.ExecContext(ctx, string(content)); err != nil {
		return fmt.Errorf("apply bootstrap %s: %w", path, err)
	}
	return nil
}

func applyMigrations(ctx context.Context, db *sql.DB, files fs.FS, dir string) error {
	if _, err := db.ExecContext(ctx, `
create table if not exists schema_migrations (
  version text primary key,
  applied_at text not null
)`); err != nil {
		return err
	}

	entries, err := fs.ReadDir(files, dir)
	if err != nil {
		return err
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
			return err
		}
		if exists > 0 {
			continue
		}

		content, err := fs.ReadFile(files, filepath.Join(dir, name))
		if err != nil {
			return err
		}

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(content)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, `
insert into schema_migrations(version, applied_at) values (?, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`, version); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}

	return nil
}
