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
