# Generate Workflow Design

## Summary

`make generate` currently fails when stale generated mocks remain in the tree. `mockery` loads packages before regeneration, and stale in-package mocks can reference old import paths such as `agent/internal/core`, which prevents package loading and stops generation before those files are replaced.

The repository should adopt the same workflow shape used in the facade project: Go code generation runs through a dedicated script at `scripts/generate/go.sh`, and that script deletes generated artifacts before invoking generators. `make generate` remains available as a convenience wrapper, but generation logic moves out of the `Makefile`.

## Goals

- Make generation reliable even when stale generated files are present.
- Match the facade project structure closely enough that the workflow is familiar and reusable.
- Provide a script entrypoint that CI can invoke directly without depending on `make`.
- Preserve the existing local `make generate` developer command.

## Non-Goals

- Redesign every `make` target in the repository.
- Change generated file contents manually.
- Remove `make generate` as a local convenience command.
- Introduce broader code-generation categories beyond the current `sqlc` and `mockery` steps.

## Proposed Approach

### Entrypoints

Add a new script at `scripts/generate/go.sh` as the canonical Go code generation entrypoint. This script becomes the place where cleanup and generator execution are defined.

Keep `make generate`, but reduce it to:

- `dependencies-check`
- invoke `./scripts/generate/go.sh`

This preserves local ergonomics while making generation independently callable in CI and other scripts.

### Cleanup Phase

Before any generator runs, `scripts/generate/go.sh` must delete current generated outputs that can interfere with package loading:

- `internal/adapters/repository/sqlite/generated/`
- every `mock_*.go` file in the repository

The cleanup happens unconditionally at the start of the script. This mirrors the facade workflow, where generated outputs are removed before regeneration begins.

### Generation Phase

After cleanup, the script runs the current generators in order:

1. `go tool sqlc generate -f ./internal/adapters/repository/sqlite/sqlc.yaml`
2. `go tool mockery --config=.mockery.yaml`

The script should stop on the first failing command and return a non-zero exit code.

### CI Usage

GitHub Actions should call `./scripts/generate/go.sh` directly before linting or testing. This decouples CI generation from `make` and makes the generation contract explicit in workflows.

Expected workflow shape:

- install dependencies
- run `./scripts/generate/go.sh`
- run lint and test commands

This allows future CI changes to reuse the same script entrypoint without reproducing generation steps in workflow YAML.

## Data Flow

1. Developer or CI invokes `./scripts/generate/go.sh` directly, or invokes `make generate`.
2. The script resolves project root and configures `PATH` consistently.
3. The script deletes generated directories and mock files.
4. `sqlc` recreates SQLite generated code.
5. `mockery` loads packages from a clean tree and regenerates mocks.
6. Calling command receives success or failure via exit code.

## Error Handling

- Missing dependencies remain the responsibility of `dependencies-check` and the workflow setup steps.
- The generation script must fail fast on cleanup or generator errors.
- The script should not ignore generator failures or continue after a failed `sqlc` step.
- Cleanup should target only generated artifacts so it cannot delete source files outside the known generated patterns.

## Testing Strategy

### Automated Verification

- Run `./scripts/generate/go.sh` from a tree containing existing generated files and confirm it completes successfully.
- Run `make generate` and confirm it delegates successfully to the script.
- Run the relevant CI-facing commands locally after generation:
  - `make lint-go`
  - `make test`

### Regression Focus

The critical regression to prevent is the current failure mode where stale mocks break package loading before `mockery` can regenerate them. Verification should explicitly confirm generation succeeds from a dirty generated state, not only from a clean checkout.

## Implementation Notes

- The script should follow the facade pattern for determining `PROJECT_ROOT_DIR` and exporting `local/bin` onto `PATH`.
- The first pass should keep behavior narrow: only the existing `sqlc` and `mockery` steps move into the script.
- CI changes should be limited to replacing implicit generation through `make` with explicit invocation of the new script.
