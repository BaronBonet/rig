package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"testing/fstest"

	"github.com/pressly/goose/v3"
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

func applyGooseMigrations(ctx context.Context, db *sql.DB, files fs.FS, dir string) error {
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

func applyMigrations(ctx context.Context, db *sql.DB, files fs.FS, dir string) error {
	entries, err := fs.ReadDir(files, dir)
	if err != nil {
		return fmt.Errorf("read migrations dir %s: %w", dir, err)
	}

	gooseFiles := make(fstest.MapFS, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		path := dir + "/" + entry.Name()
		content, err := fs.ReadFile(files, path)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", path, err)
		}

		gooseFiles[path] = &fstest.MapFile{
			Data: []byte("-- +goose Up\n" + string(content) + "\n-- +goose Down\n"),
		}
	}

	return applyGooseMigrations(ctx, db, gooseFiles, dir)
}

func schemaObjectExists(ctx context.Context, db *sql.DB, objectType, name string) (bool, error) {
	var count int
	err := db.QueryRowContext(
		ctx,
		`select count(*) from sqlite_master where type = ? and name = ?`,
		objectType,
		name,
	).Scan(&count)
	return count > 0, err
}

func tableExists(ctx context.Context, db *sql.DB, table string) (bool, error) {
	return schemaObjectExists(ctx, db, "table", table)
}

func indexExists(ctx context.Context, db *sql.DB, index string) (bool, error) {
	return schemaObjectExists(ctx, db, "index", index)
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

func columnSupportsConflictTarget(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	rows, err := db.QueryContext(ctx, `pragma table_info(`+table+`)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()

	primaryKeyCount := 0
	targetPrimaryKeyOrder := 0
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
		if pk > 0 {
			primaryKeyCount++
		}
		if name == column {
			targetPrimaryKeyOrder = pk
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	if targetPrimaryKeyOrder == 1 && primaryKeyCount == 1 {
		return true, nil
	}

	indexRows, err := db.QueryContext(ctx, `pragma index_list(`+table+`)`)
	if err != nil {
		return false, err
	}
	defer indexRows.Close()

	type indexInfo struct {
		name    string
		unique  int
		partial int
	}

	var indexes []indexInfo
	for indexRows.Next() {
		var (
			seq     int
			name    string
			unique  int
			origin  string
			partial int
		)
		if err := indexRows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			return false, err
		}
		indexes = append(indexes, indexInfo{name: name, unique: unique, partial: partial})
	}
	if err := indexRows.Err(); err != nil {
		return false, err
	}

	for _, index := range indexes {
		if index.unique != 1 || index.partial != 0 {
			continue
		}

		columnName, singleColumn, err := uniqueIndexColumn(ctx, db, index.name)
		if err != nil {
			return false, err
		}
		if singleColumn && columnName == column {
			return true, nil
		}
	}

	return false, nil
}

func columnIsExactIntegerPrimaryKey(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	rows, err := db.QueryContext(ctx, `pragma table_info(`+table+`)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()

	primaryKeyCount := 0
	targetIsIntegerPrimaryKey := false
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
		if pk > 0 {
			primaryKeyCount++
		}
		if name == column && pk == 1 && strings.EqualFold(strings.TrimSpace(colType), "integer") {
			targetIsIntegerPrimaryKey = true
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}

	return targetIsIntegerPrimaryKey && primaryKeyCount == 1, nil
}

func uniqueIndexColumn(ctx context.Context, db *sql.DB, index string) (string, bool, error) {
	rows, err := db.QueryContext(ctx, `pragma index_info(`+index+`)`)
	if err != nil {
		return "", false, err
	}
	defer rows.Close()

	columnCount := 0
	columnName := ""
	for rows.Next() {
		var (
			seqno int
			cid   int
			name  string
		)
		if err := rows.Scan(&seqno, &cid, &name); err != nil {
			return "", false, err
		}
		columnCount++
		columnName = name
	}
	if err := rows.Err(); err != nil {
		return "", false, err
	}

	return columnName, columnCount == 1, nil
}

func allTablesExist(ctx context.Context, db *sql.DB, tables ...string) (bool, error) {
	sortedTables := slices.Clone(tables)
	slices.Sort(sortedTables)

	for _, table := range sortedTables {
		exists, err := tableExists(ctx, db, table)
		if err != nil {
			return false, err
		}
		if !exists {
			return false, nil
		}
	}

	return true, nil
}

func allIndexesExist(ctx context.Context, db *sql.DB, indexes ...string) (bool, error) {
	sortedIndexes := slices.Clone(indexes)
	slices.Sort(sortedIndexes)

	for _, index := range sortedIndexes {
		exists, err := indexExists(ctx, db, index)
		if err != nil {
			return false, err
		}
		if !exists {
			return false, nil
		}
	}

	return true, nil
}

func allColumnsExist(ctx context.Context, db *sql.DB, table string, columns ...string) (bool, error) {
	sortedColumns := slices.Clone(columns)
	slices.Sort(sortedColumns)

	for _, column := range sortedColumns {
		exists, err := columnExists(ctx, db, table, column)
		if err != nil {
			return false, err
		}
		if !exists {
			return false, nil
		}
	}

	return true, nil
}
