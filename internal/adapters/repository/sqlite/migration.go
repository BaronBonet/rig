package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"sync"

	"github.com/pressly/goose/v3"
)

var gooseMigrationMu sync.Mutex

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

	goose.SetBaseFS(files)
	defer goose.SetBaseFS(nil)

	if err := goose.SetDialect("sqlite"); err != nil {
		return fmt.Errorf("set goose sqlite dialect: %w", err)
	}
	if err := goose.UpContext(ctx, db, dir); err != nil {
		return fmt.Errorf("apply goose migrations from %s: %w", dir, err)
	}
	return nil
}
