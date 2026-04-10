# SQLite Sqlc Migration Design

## Goal

Replace all raw SQL currently embedded in the SQLite repository Go code with checked-in SQL files and `sqlc`-generated query bindings, generated through `make generate`, while also replacing the current ad hoc schema/bootstrap flow with explicit versioned SQLite migrations.

## Constraints

- Every repository SQL statement should move out of Go source.
- Generated `sqlc` output should be part of the build workflow and should not be committed.
- The resulting workflow should feel similar to `/Users/ericbonet/software/the_forum/fws-facade`, but adapted for an embedded SQLite database instead of a live Postgres-backed analyzer setup.
- Existing repository behavior must remain intact:
  - task create, update, get, list
  - event append
  - hook event ingestion and listing
  - hook session summary loading and listing
  - observer summary loading, upsert, and subscription-triggered publication
  - startup schema initialization and legacy column/data backfill

## Current State

`internal/adapters/repository/sqlite/repository.go` and `internal/adapters/repository/sqlite/hook_observability.go` currently mix several responsibilities:

- SQLite connection setup
- schema creation and incremental `ALTER TABLE` backfills
- all repository reads and writes
- transaction orchestration for hook ingestion
- row scanning and domain mapping
- in-memory subscriber management

The raw SQL is spread across inline `ExecContext`, `QueryContext`, and `QueryRowContext` calls. Schema evolution is implicit in Go startup code instead of being represented as explicit, versioned SQL artifacts.

## Options Considered

### 1. Query-only sqlc migration

Move repository read/write queries to `sqlc`, but keep `initSchema`, `ALTER TABLE`, and bootstrap PRAGMAs as inline Go strings.

Pros:

- smallest code change
- lowest short-term migration effort

Cons:

- leaves a meaningful amount of SQL in Go
- preserves the current schema drift risk
- does not improve migration clarity

### 2. sqlc plus schema snapshot only

Move all repository queries to `sqlc` and maintain one checked-in `schema.sql` used by both runtime setup and `sqlc`.

Pros:

- simple to understand
- good fit for small SQLite setups

Cons:

- weak schema history
- easy for runtime schema changes and generation schema to diverge
- no explicit migration record

### 3. Recommended: sqlc plus versioned SQLite migrations

Move all repository queries into `sqlc` query files and introduce ordered migration SQL files as the single schema source of truth. Runtime startup applies unapplied migrations. `sqlc` reads the same migration directory as schema input.

Pros:

- removes all application SQL from Go
- gives explicit schema history
- aligns generation and runtime schema sources
- stays lightweight for SQLite

Cons:

- more upfront restructuring
- requires a small local migration runner

## Recommended Design

### File Layout

Restructure `internal/adapters/repository/sqlite` around these artifacts:

- `migrations/`
  - ordered SQL files such as `000001_initial.sql`, `000002_task_repo_name.sql`, `000003_hook_observability.sql`
  - includes both table/index creation and the current legacy evolution steps now expressed as explicit schema changes or backfill DML
- `bootstrap/`
  - a checked-in SQL file for connection-scoped PRAGMA statements executed immediately after opening the SQLite connection
- `queries/`
  - checked-in named SQL queries consumed by `sqlc`
- `generated/`
  - local build artifact output directory for `sqlc`
  - ignored by git
- `sqlc.yaml`
  - SQLite-targeted `sqlc` config colocated with the adapter

### Generation Workflow

`make generate` will be extended so it runs `sqlc` generation before the existing mock generation step.

Expected flow:

1. verify dependencies
2. remove stale generated SQLite query output
3. run `go tool sqlc generate -f internal/adapters/repository/sqlite/sqlc.yaml`
4. run existing generation steps such as `go tool mockery`

Generated Go files under `internal/adapters/repository/sqlite/generated/` will not be committed and must be added to `.gitignore`.

### Migration Strategy

Introduce a lightweight in-process migration runner owned by the SQLite adapter.

Design details:

- create a `schema_migrations` table tracking applied migration version and applied timestamp
- load migration files from the checked-in `migrations/` directory in lexicographic order
- execute each unapplied migration inside a transaction where SQLite supports it
- fail repository initialization if any migration fails

This replaces:

- `initSchema`
- `addColumnIfMissing`
- `columnNameFromAlter`
- `hasColumn`

The goal is to make schema state explicit instead of inferred from startup code.

### Bootstrap SQL

The current connection startup PRAGMAs move out of Go string literals into a checked-in bootstrap SQL file executed during initialization:

- `pragma journal_mode = wal`
- `pragma busy_timeout = 5000`
- `pragma synchronous = normal`
- `pragma foreign_keys = on`

This preserves the “no raw SQL in Go” requirement while keeping connection configuration separate from schema migrations and from `sqlc` query generation.

### Repository Structure

`Repository` should keep:

- `db *sql.DB`
- `queries *generated.Queries`
- subscriber state and mutexes
- path/init error fields

The repository methods become thin orchestration and domain-mapping layers over generated query methods.

Examples:

- `CreateTask` -> generated insert query
- `UpdateTask` -> generated update query
- `GetTask` -> generated lookup query
- `ListTasks` -> generated list query
- `AppendEvent` -> generated insert query
- `UpsertObserverSummary` -> generated upsert query
- `ListObserverSummaries` -> generated list query plus optional Go-side filtering if needed
- `ListHookSessionSummaries` -> generated list query plus optional Go-side filtering if needed
- `ListHookEvents` -> generated list query with limit variants as needed

### Hook Ingestion Transaction Flow

`IngestHookEvent` keeps its current transaction-oriented orchestration, but the SQL steps move to generated methods.

Expected shape:

1. resolve task ID via generated lookup queries
2. begin transaction
3. create tx-bound generated query helper with `queries.WithTx(tx)`
4. load previous hook session summary
5. load previous observer summary
6. insert hook event
7. upsert hook session summary
8. upsert observer summary
9. commit
10. publish in-memory updates

This preserves current behavior while removing inline SQL.

### Query Design Notes

The adapter currently has a few dynamic query cases:

- lookup by either task ID or slug
- optional `limit` for hook events
- optional filtering by a provided set of task IDs for summary list methods

Preferred handling:

- use explicit named queries for stable cases
- for optional `limit`, define separate bounded and unbounded query methods if that keeps `sqlc` simpler
- for task-ID subset filtering, prefer whichever of these is simplest and most reliable with SQLite `sqlc` generation during implementation:
  - `sqlc.slice`-style list expansion if supported cleanly for the SQLite target in this repo
  - otherwise list all summaries in SQL and filter by task ID in Go for the small caller-side subsets currently used

The implementation should optimize for correctness and maintainability over forcing every dynamic shape into a complex SQL trick.

### Domain Mapping

Generated row types should not leak into core domain code.

The repository layer remains responsible for:

- converting SQLite integer booleans to Go booleans
- converting stored RFC3339 timestamps to `time.Time`
- converting stored status/activity/runtime strings into core enums
- trimming previews and deriving aggregate summaries exactly as today

Where practical, SQL should store the same primitives as today to minimize behavioral risk during migration.

### Testing Impact

Repository behavior should remain covered by existing tests, but the migration requires focused updates:

- tests should continue exercising repository behavior rather than generated internals
- add migration-runner tests for:
  - initial database creation
  - applying pending migrations in order
  - idempotent startup when all migrations are already applied
  - failure on invalid migration SQL
- update any tests that currently inspect schema creation side effects through `initSchema`
- keep hook ingestion tests centered on persisted results and published summaries

## Out of Scope

- switching away from SQLite
- introducing an external migration binary or service
- changing repository behavior or domain model shape beyond what is necessary for the migration
- broad refactoring of subscriber/publisher logic unrelated to SQL removal

## Implementation Outline

1. Add `sqlc` as a Go tool dependency and add a SQLite `sqlc.yaml`.
2. Add `.gitignore` coverage for SQLite generated output.
3. Create migration SQL files representing the current schema and evolution.
4. Replace startup schema helpers with a migration runner plus bootstrap SQL executor.
5. Create checked-in query SQL files for every current repository statement.
6. Generate SQLite query bindings through `make generate`.
7. Refactor repository methods to use generated queries.
8. Update tests to cover migration execution and preserve repository behavior.

## Risks And Mitigations

### Dynamic filtering queries may not map cleanly to SQLite sqlc

Mitigation:

Prefer simple generated queries and small Go-side filtering where needed.

### Existing implicit backfill behavior may be lost during migration extraction

Mitigation:

Represent legacy changes explicitly in ordered migration files and add migration tests covering old-database upgrade paths.

### Build failures if generated code is absent

Mitigation:

Make `generate` part of the normal developer workflow, keep generated output gitignored, and ensure CI/test/lint paths run generation first where needed.

## Success Criteria

The migration is complete when all of the following are true:

- no repository SQL statements remain embedded in Go under `internal/adapters/repository/sqlite`
- SQLite schema evolution is represented by checked-in migration files
- `make generate` produces SQLite `sqlc` output locally
- generated SQLite output is ignored by git
- repository tests pass against the migrated implementation
- runtime behavior is unchanged from the current repository contract
