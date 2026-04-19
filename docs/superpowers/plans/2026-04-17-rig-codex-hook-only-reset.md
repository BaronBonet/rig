# Rig Codex Hook-Only Reset Implementation Plan

> Historical implementation plan. Superseded by later status-stream cleanup
> work. Any `PermissionRequest` references here are retained for context only.

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a new standalone Go CLI repo that launches Codex in the current repository, persists hook-driven task state in SQLite, and exposes `start`, `status`, and `watch` commands without tmux, worktrees, or a TUI.

**Architecture:** Create a sibling repository at `/Users/ebon/personal_software/rig-codex` with four focused packages: `cmd/rig` for Cobra wiring, `internal/app` for orchestration and state derivation, `internal/codex` for hook bootstrap and payload decoding, and `internal/store/sqlite` for migrations and persistence. Runtime truth comes only from Codex hooks plus explicit process-exit finalization.

**Tech Stack:** Go, Cobra, SQLite, `database/sql`, `modernc.org/sqlite`, `testing`, `testify/require`

---

## File Map

| Action | Path | Responsibility |
|--------|------|----------------|
| Create | `/Users/ebon/personal_software/rig-codex/go.mod` | Define new module and dependencies |
| Create | `/Users/ebon/personal_software/rig-codex/README.md` | Document setup and CLI usage |
| Create | `/Users/ebon/personal_software/rig-codex/cmd/rig/main.go` | Entrypoint wiring |
| Create | `/Users/ebon/personal_software/rig-codex/internal/app/types.go` | Domain models and status enums |
| Create | `/Users/ebon/personal_software/rig-codex/internal/app/service.go` | Start, status, watch, and hook ingest orchestration |
| Create | `/Users/ebon/personal_software/rig-codex/internal/app/state.go` | Hook-to-display-state derivation |
| Create | `/Users/ebon/personal_software/rig-codex/internal/app/service_test.go` | Service behavior tests |
| Create | `/Users/ebon/personal_software/rig-codex/internal/app/state_test.go` | State derivation tests |
| Create | `/Users/ebon/personal_software/rig-codex/internal/codex/bootstrap.go` | Write `.codex/hooks.json` and forwarder script |
| Create | `/Users/ebon/personal_software/rig-codex/internal/codex/forward-to-rig.sh.tmpl` | Hook forwarder template |
| Create | `/Users/ebon/personal_software/rig-codex/internal/codex/decode.go` | Decode Codex hook payloads |
| Create | `/Users/ebon/personal_software/rig-codex/internal/codex/bootstrap_test.go` | Bootstrap tests |
| Create | `/Users/ebon/personal_software/rig-codex/internal/codex/decode_test.go` | Hook decode tests |
| Create | `/Users/ebon/personal_software/rig-codex/internal/store/sqlite/repository.go` | Task, event, and runtime-state persistence |
| Create | `/Users/ebon/personal_software/rig-codex/internal/store/sqlite/migrations.go` | Embedded schema bootstrap |
| Create | `/Users/ebon/personal_software/rig-codex/internal/store/sqlite/schema.sql` | Minimal SQLite schema |
| Create | `/Users/ebon/personal_software/rig-codex/internal/store/sqlite/repository_test.go` | SQLite repository tests |
| Create | `/Users/ebon/personal_software/rig-codex/internal/cli/root.go` | Cobra root and shared dependency wiring |
| Create | `/Users/ebon/personal_software/rig-codex/internal/cli/start.go` | `rig start` command |
| Create | `/Users/ebon/personal_software/rig-codex/internal/cli/status.go` | `rig status` command |
| Create | `/Users/ebon/personal_software/rig-codex/internal/cli/watch.go` | `rig watch` command |
| Create | `/Users/ebon/personal_software/rig-codex/internal/cli/hook.go` | Hidden `rig hook ingest` command |
| Create | `/Users/ebon/personal_software/rig-codex/internal/cli/root_test.go` | CLI integration tests |

---

### Task 1: Scaffold The New Repository

**Files:**
- Create: `/Users/ebon/personal_software/rig-codex/go.mod`
- Create: `/Users/ebon/personal_software/rig-codex/README.md`
- Create: `/Users/ebon/personal_software/rig-codex/cmd/rig/main.go`
- Create: `/Users/ebon/personal_software/rig-codex/internal/cli/root.go`

- [ ] **Step 1: Create the failing CLI smoke test**

Create `internal/cli/root_test.go` with a basic test that constructs the root command and expects subcommands `start`, `status`, `watch`, and hidden `hook` to exist.

- [ ] **Step 2: Run the CLI package tests to verify failure**

Run: `cd /Users/ebon/personal_software/rig-codex && go test ./internal/cli`
Expected: FAIL because the repo and command wiring do not exist yet.

- [ ] **Step 3: Create the module and command skeleton**

Add `go.mod`, `cmd/rig/main.go`, and `internal/cli/root.go` with a minimal Cobra root command and placeholder subcommands that return `ErrNotImplemented`.

- [ ] **Step 4: Add a minimal README**

Write `README.md` describing the repo goal, prerequisites (`git`, `codex`), and planned commands.

- [ ] **Step 5: Run the CLI package tests to verify they pass**

Run: `cd /Users/ebon/personal_software/rig-codex && go test ./internal/cli`
Expected: PASS with the command-shape test green.

- [ ] **Step 6: Commit**

```bash
git -C /Users/ebon/personal_software/rig-codex add .
git -C /Users/ebon/personal_software/rig-codex commit -m "chore: scaffold rig-codex CLI repo"
```

---

### Task 2: Add Codex Hook Bootstrap And Payload Decode

**Files:**
- Create: `/Users/ebon/personal_software/rig-codex/internal/codex/bootstrap.go`
- Create: `/Users/ebon/personal_software/rig-codex/internal/codex/forward-to-rig.sh.tmpl`
- Create: `/Users/ebon/personal_software/rig-codex/internal/codex/decode.go`
- Create: `/Users/ebon/personal_software/rig-codex/internal/codex/bootstrap_test.go`
- Create: `/Users/ebon/personal_software/rig-codex/internal/codex/decode_test.go`

- [ ] **Step 1: Write failing bootstrap tests**

Add tests asserting bootstrap creates `.codex/hooks.json` and `.codex/hooks/forward-to-rig.sh` in a temp repo and that the generated script invokes `rig hook ingest`.

- [ ] **Step 2: Write failing decode tests**

Add tests for `SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `PermissionRequest`, and `Stop` payload decoding into a normalized hook event struct.

- [ ] **Step 3: Run the codex package tests to verify failure**

Run: `cd /Users/ebon/personal_software/rig-codex && go test ./internal/codex`
Expected: FAIL because bootstrap and decode code do not exist yet.

- [ ] **Step 4: Implement bootstrap**

Port the current repo's narrow Codex hook bootstrap logic into `bootstrap.go`, but remove all Claude handling and any dependency on the old repo structure.

- [ ] **Step 5: Implement payload decoding**

Write `decode.go` to parse stdin JSON into a normalized event struct containing event name, task/session identifiers, cwd, prompt text, command text, command result text, assistant message, and raw payload.

- [ ] **Step 6: Run the codex package tests to verify they pass**

Run: `cd /Users/ebon/personal_software/rig-codex && go test ./internal/codex`
Expected: PASS for bootstrap and decode tests.

- [ ] **Step 7: Commit**

```bash
git -C /Users/ebon/personal_software/rig-codex add internal/codex
git -C /Users/ebon/personal_software/rig-codex commit -m "feat: add codex hook bootstrap and decode"
```

---

### Task 3: Build The SQLite Schema And Repository

**Files:**
- Create: `/Users/ebon/personal_software/rig-codex/internal/store/sqlite/schema.sql`
- Create: `/Users/ebon/personal_software/rig-codex/internal/store/sqlite/migrations.go`
- Create: `/Users/ebon/personal_software/rig-codex/internal/store/sqlite/repository.go`
- Create: `/Users/ebon/personal_software/rig-codex/internal/store/sqlite/repository_test.go`
- Create: `/Users/ebon/personal_software/rig-codex/internal/app/types.go`

- [ ] **Step 1: Write the failing repository tests**

Add tests for:

- creating a task with `launch_state=starting`
- marking a repo-root task as active
- appending a hook event
- upserting the latest runtime state
- finalizing a task with exit code and end time
- selecting the latest task for a repo

- [ ] **Step 2: Run the SQLite package tests to verify failure**

Run: `cd /Users/ebon/personal_software/rig-codex && go test ./internal/store/sqlite`
Expected: FAIL because schema and repository code do not exist yet.

- [ ] **Step 3: Define the domain types**

Add `internal/app/types.go` with task records, normalized hook event records, runtime-state records, launch-state enums, and display-status enums shared by the repository and service.

- [ ] **Step 4: Implement the schema and bootstrap**

Add `schema.sql` and `migrations.go` so the repository can initialize the `tasks`, `hook_events`, and `task_runtime_state` tables on first open.

- [ ] **Step 5: Implement the repository**

Write `repository.go` with focused methods for:

- open and migrate database
- create task
- set active task for repo root
- resolve active task by repo root or cwd
- insert hook event
- upsert runtime state
- update task launch state on start and exit
- fetch latest task and latest runtime state for a repo

- [ ] **Step 6: Run the SQLite package tests to verify they pass**

Run: `cd /Users/ebon/personal_software/rig-codex && go test ./internal/store/sqlite`
Expected: PASS for repository behavior tests.

- [ ] **Step 7: Commit**

```bash
git -C /Users/ebon/personal_software/rig-codex add internal/store/sqlite internal/app/types.go
git -C /Users/ebon/personal_software/rig-codex commit -m "feat: add sqlite task and hook store"
```

---

### Task 4: Add Runtime State Derivation And Hook Ingest Service

**Files:**
- Create: `/Users/ebon/personal_software/rig-codex/internal/app/state.go`
- Create: `/Users/ebon/personal_software/rig-codex/internal/app/service.go`
- Create: `/Users/ebon/personal_software/rig-codex/internal/app/state_test.go`
- Create: `/Users/ebon/personal_software/rig-codex/internal/app/service_test.go`

- [ ] **Step 1: Write the failing state-derivation tests**

Add table-driven tests that verify:

- `PermissionRequest` => `needs_input`
- `PreToolUse` => `working` with `command`
- `UserPromptSubmit`, `PostToolUse`, `SessionStart` => `working`
- `Stop` => `needs_input`
- no hook data yet => `starting`
- process exit => `finished`

- [ ] **Step 2: Write the failing service tests**

Add service tests for:

- ingesting a decoded hook event persists the raw event and updates latest runtime state
- unresolved repo-root hook ingestion logs and returns nil error
- Codex process finalization sets launch state `exited` or `failed` and runtime state `finished`

- [ ] **Step 3: Run the app package tests to verify failure**

Run: `cd /Users/ebon/personal_software/rig-codex && go test ./internal/app`
Expected: FAIL because state and service logic do not exist yet.

- [ ] **Step 4: Implement state derivation**

Write `state.go` so display status is derived only from hook events and explicit process finalization. Do not add `disconnected`.

- [ ] **Step 5: Implement the service**

Write `service.go` with orchestration methods for:

- start task record creation
- hook ingestion from decoded payloads
- process-exit finalization
- status lookup for the current repo
- watch polling loop support

- [ ] **Step 6: Run the app package tests to verify they pass**

Run: `cd /Users/ebon/personal_software/rig-codex && go test ./internal/app`
Expected: PASS for state and service tests.

- [ ] **Step 7: Commit**

```bash
git -C /Users/ebon/personal_software/rig-codex add internal/app
git -C /Users/ebon/personal_software/rig-codex commit -m "feat: add hook ingestion and runtime state service"
```

---

### Task 5: Implement `rig start`

**Files:**
- Create: `/Users/ebon/personal_software/rig-codex/internal/cli/start.go`
- Modify: `/Users/ebon/personal_software/rig-codex/internal/cli/root.go`
- Modify: `/Users/ebon/personal_software/rig-codex/internal/app/service.go`
- Modify: `/Users/ebon/personal_software/rig-codex/internal/cli/root_test.go`

- [ ] **Step 1: Write the failing start-command tests**

Add CLI tests that verify `rig start "prompt"`:

- fails outside a git repo
- fails when `codex` is unavailable
- creates a task and bootstraps hooks before launch
- finalizes the task when the Codex command exits

- [ ] **Step 2: Run the CLI package tests to verify failure**

Run: `cd /Users/ebon/personal_software/rig-codex && go test ./internal/cli`
Expected: FAIL because `start` is only a placeholder.

- [ ] **Step 3: Implement `rig start`**

Replace the placeholder with real logic that:

- detects repo root from the current working directory
- ensures the SQLite store is open
- creates and activates a task
- bootstraps `.codex/hooks`
- runs `codex <prompt>` attached to stdio
- records process-exit finalization

- [ ] **Step 4: Run the CLI package tests to verify they pass**

Run: `cd /Users/ebon/personal_software/rig-codex && go test ./internal/cli`
Expected: PASS for `start` command behavior.

- [ ] **Step 5: Commit**

```bash
git -C /Users/ebon/personal_software/rig-codex add internal/cli/start.go internal/cli/root.go internal/app/service.go internal/cli/root_test.go
git -C /Users/ebon/personal_software/rig-codex commit -m "feat: add codex start command"
```

---

### Task 6: Implement `rig status`, `rig watch`, And Hidden Hook Ingest

**Files:**
- Create: `/Users/ebon/personal_software/rig-codex/internal/cli/status.go`
- Create: `/Users/ebon/personal_software/rig-codex/internal/cli/watch.go`
- Create: `/Users/ebon/personal_software/rig-codex/internal/cli/hook.go`
- Modify: `/Users/ebon/personal_software/rig-codex/internal/cli/root.go`
- Modify: `/Users/ebon/personal_software/rig-codex/internal/cli/root_test.go`

- [ ] **Step 1: Write the failing status, watch, and hook tests**

Add CLI tests that verify:

- `rig status` prints the latest task state for the current repo
- `rig watch --once` prints a single snapshot for scripting tests
- `rig hook ingest <event-name>` reads stdin JSON and persists the decoded event

- [ ] **Step 2: Run the CLI package tests to verify failure**

Run: `cd /Users/ebon/personal_software/rig-codex && go test ./internal/cli`
Expected: FAIL because these commands are still placeholders or missing.

- [ ] **Step 3: Implement the commands**

Add:

- `status.go` for current-repo state rendering
- `watch.go` with a small poll loop and a `--once` mode for tests
- `hook.go` with hidden ingestion wired to stdin payloads

- [ ] **Step 4: Run the CLI package tests to verify they pass**

Run: `cd /Users/ebon/personal_software/rig-codex && go test ./internal/cli`
Expected: PASS for status, watch, and hook command tests.

- [ ] **Step 5: Commit**

```bash
git -C /Users/ebon/personal_software/rig-codex add internal/cli/status.go internal/cli/watch.go internal/cli/hook.go internal/cli/root.go internal/cli/root_test.go
git -C /Users/ebon/personal_software/rig-codex commit -m "feat: add status watch and hook commands"
```

---

### Task 7: End-To-End Verification And Documentation

**Files:**
- Modify: `/Users/ebon/personal_software/rig-codex/README.md`
- Verify: `/Users/ebon/personal_software/rig-codex/...`

- [ ] **Step 1: Update README with real usage**

Document:

- required binaries
- default SQLite location
- `rig start`
- `rig status`
- `rig watch`
- how Codex hooks are bootstrapped

- [ ] **Step 2: Run focused unit tests**

Run: `cd /Users/ebon/personal_software/rig-codex && go test ./internal/codex ./internal/store/sqlite ./internal/app ./internal/cli`
Expected: PASS across all focused packages.

- [ ] **Step 3: Run full repository tests**

Run: `cd /Users/ebon/personal_software/rig-codex && go test ./...`
Expected: PASS for the full repo.

- [ ] **Step 4: Build the binary**

Run: `cd /Users/ebon/personal_software/rig-codex && go build ./cmd/rig`
Expected: successful build of the CLI binary.

- [ ] **Step 5: Smoke-test the hook ingest path**

Run a manual local flow in a disposable test repo:

1. `rig start "say hi"`
2. from another terminal, run `rig status`
3. confirm hook-driven state transitions away from `starting`

Expected: persisted state shows `working`, `needs_input`, or `finished` based on actual Codex hook traffic, with no `disconnected` status.

- [ ] **Step 6: Commit**

```bash
git -C /Users/ebon/personal_software/rig-codex add README.md
git -C /Users/ebon/personal_software/rig-codex commit -m "docs: finalize rig-codex usage and verification"
```
