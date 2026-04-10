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

func allTablesExist(ctx context.Context, db *sql.DB, tables ...string) (bool, error) {
	for _, table := range tables {
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
	for _, index := range indexes {
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
	for _, column := range columns {
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

func tableColumnState(ctx context.Context, db *sql.DB, table string, columns ...string) (bool, bool, error) {
	exists, err := tableExists(ctx, db, table)
	if err != nil {
		return false, false, err
	}
	if !exists {
		return false, false, nil
	}

	complete, err := allColumnsExist(ctx, db, table, columns...)
	if err != nil {
		return false, false, err
	}

	return true, complete, nil
}

func seedLegacyMigrationState(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `
create table if not exists schema_migrations (
  version text primary key,
  applied_at text not null
)`); err != nil {
		return fmt.Errorf("create schema_migrations table: %w", err)
	}

	tasksExists, tasksComplete, err := tableColumnState(
		ctx,
		db,
		"tasks",
		"id",
		"prompt",
		"display_name",
		"slug",
		"repo_root",
		"base_branch",
		"branch_name",
		"worktree_path",
		"tmux_session",
		"provider",
		"status",
		"worktree_exists",
		"branch_exists",
		"session_exists",
		"last_error",
		"created_at",
		"updated_at",
		"last_reconciled_at",
	)
	if err != nil {
		return fmt.Errorf("check initial tasks table: %w", err)
	}
	if tasksExists && !tasksComplete {
		return fmt.Errorf("incomplete managed schema for tasks table")
	}

	eventsExists, eventsComplete, err := tableColumnState(
		ctx,
		db,
		"events",
		"id",
		"task_id",
		"event_type",
		"payload",
		"created_at",
	)
	if err != nil {
		return fmt.Errorf("check initial events table: %w", err)
	}
	if eventsExists && !eventsComplete {
		return fmt.Errorf("incomplete managed schema for events table")
	}
	initialTasksAndEventsComplete := tasksComplete && eventsComplete

	taskMetadataColumnsComplete, err := allColumnsExist(
		ctx,
		db,
		"tasks",
		"repo_name",
		"agent_window_name",
		"editor_window_name",
		"agent_window_exists",
		"editor_window_exists",
	)
	if err != nil {
		return fmt.Errorf("check task metadata columns: %w", err)
	}

	hookEventsExists, hookEventColumnsComplete, err := tableColumnState(
		ctx,
		db,
		"task_hook_events",
		"id",
		"task_id",
		"session_id",
		"turn_id",
		"event_name",
		"occurred_at",
		"raw_payload_json",
		"last_assistant_message",
		"prompt_preview",
		"command_preview",
		"command_result_preview",
		"tool_use_id",
	)
	if err != nil {
		return fmt.Errorf("check task_hook_events columns: %w", err)
	}
	if hookEventsExists && !hookEventColumnsComplete {
		return fmt.Errorf("incomplete managed schema for task_hook_events table")
	}

	hookSessionsExists, hookSessionColumnsComplete, err := tableColumnState(
		ctx,
		db,
		"task_hook_sessions",
		"task_id",
		"session_id",
		"model",
		"cwd",
		"transcript_path",
		"start_source",
		"current_turn_id",
		"last_event_name",
		"runtime_phase",
		"started_at",
		"last_activity_at",
		"last_stop_at",
		"last_prompt_preview",
		"last_command_preview",
		"last_command_result_preview",
		"last_assistant_message",
		"command_count",
		"updated_at",
	)
	if err != nil {
		return fmt.Errorf("check task_hook_sessions columns: %w", err)
	}
	if hookSessionsExists && !hookSessionColumnsComplete {
		return fmt.Errorf("incomplete managed schema for task_hook_sessions table")
	}

	observerSummariesExists, observerSummaryColumnsComplete, err := tableColumnState(
		ctx,
		db,
		"task_observer_summaries",
		"task_id",
		"display_status",
		"display_activity",
		"process_alive",
		"last_runtime_observed_at",
		"updated_at",
	)
	if err != nil {
		return fmt.Errorf("check task_observer_summaries columns: %w", err)
	}
	if observerSummariesExists && !observerSummaryColumnsComplete {
		return fmt.Errorf("incomplete managed schema for task_observer_summaries table")
	}

	hookObservabilityIndexesComplete, err := allIndexesExist(
		ctx,
		db,
		"idx_task_hook_events_task_occurred_at",
		"idx_task_hook_sessions_session_id",
	)
	if err != nil {
		return fmt.Errorf("check hook observability indexes: %w", err)
	}
	hookObservabilityComplete := hookEventColumnsComplete &&
		hookSessionColumnsComplete &&
		observerSummaryColumnsComplete &&
		hookObservabilityIndexesComplete

	managedMigrations := []struct {
		version  string
		complete bool
	}{
		{version: "000001_initial_tasks_and_events", complete: initialTasksAndEventsComplete},
		{version: "000002_add_task_metadata_columns", complete: taskMetadataColumnsComplete},
		{version: "000003_add_hook_observability_tables", complete: hookObservabilityComplete},
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin schema_migrations normalization: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `delete from schema_migrations where version = ?`, "000001_sqlc_bootstrap"); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("remove legacy bootstrap migration row: %w", err)
	}

	for _, migration := range managedMigrations {
		if !migration.complete {
			if _, err := tx.ExecContext(ctx, `delete from schema_migrations where version = ?`, migration.version); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("remove stale migration %s: %w", migration.version, err)
			}
			continue
		}

		if _, err := tx.ExecContext(ctx, `
insert into schema_migrations(version, applied_at)
values (?, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
on conflict(version) do nothing`, migration.version); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("seed migration %s: %w", migration.version, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit schema_migrations normalization: %w", err)
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
