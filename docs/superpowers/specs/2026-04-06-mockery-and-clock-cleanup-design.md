# Mockery And Clock Cleanup Design

## Summary

Replace handwritten test doubles across the repo with `mockery`-generated mocks for real interface seams, and remove the custom `timeutil.Clock` abstraction from production code. Keep generated mocks out of git, install `mockery` through the existing dependency tooling, and make tests rely on `time.Now()` with time-window assertions rather than frozen timestamps.

## Goals

- remove repo-wide handwritten fake/mock implementations that exist only for tests
- standardize interface-boundary mocking around `mockery`
- keep generated mocks out of version control
- align dependency installation with the existing `make dependencies install` flow
- simplify production code by deleting `internal/pkg/timeutil/clock.go`
- keep test behavior reliable after switching from exact fake-clock timestamps to time-window assertions

## Non-Goals

- introducing new production interfaces only to make mocking possible
- changing runtime behavior outside what is needed to remove the clock abstraction
- refactoring package boundaries unrelated to test infrastructure cleanup
- checking generated mock files into the repository

## Design

### Testing Strategy

Use generated mocks only at actual interface seams already present in production code.

- if production code already depends on an interface, tests should use a generated `mockery` mock for that interface
- if a test is exercising a concrete internal helper and no shared interface exists, keep the test concrete instead of adding a new abstraction just for tests
- remove handwritten shared fake files such as `internal/core/fakes_test.go`
- replace ad hoc service fakes in CLI tests with generated mocks for `TaskService`

This keeps the production design honest. The cleanup should remove test scaffolding, not push more testing concerns into runtime packages.

### Mockery Configuration

Mirror the style used in `../the_forum/fws-facade`.

- add a root `.mockery.yaml`
- use:
  - `filename: "mock_{{ snakecase .InterfaceName }}.go"`
  - `mockname: "Mock{{.InterfaceName}}"`
  - `inpackage: true`
  - `with-expecter: true`
  - explicit `packages` and `interfaces` lists
- keep generation package-local rather than writing to a shared central mocks package
- ignore generated mock files through `.gitignore`

The repo should have an explicit generation workflow:

- `make dependencies install` installs `mockery` through `scripts/dependencies/install.sh`
- `make generate` runs the repo generation step
- `go test ./...` assumes mocks have already been generated

### Expected Mock Coverage

This pass should cover the handwritten doubles that currently stand in for interface seams.

Primary targets:

- `internal/core`
  - `TaskRepository`
  - `RepoConfigRepository`
  - `WorkspaceSeeder`
  - `RepoClient`
  - `SessionClient`
  - `ProviderClient`
- `internal/adapters/handler/cli`
  - `TaskService`
- `internal/pkg/execx`
  - `Runner`
- `internal/adapters/client/tmux`
  - package-local interface seams such as `controlPipe` and `controlPipeFactory` when `mockery` can generate them cleanly in-package

Handwritten test fakes that exist only to satisfy those interfaces should be removed. Small concrete test helpers that are not standing in for a production interface do not need to be forced into `mockery`.

### Clock Removal

The custom clock abstraction should be deleted rather than replaced.

- remove `internal/pkg/timeutil/clock.go`
- remove the `clock` dependency from `internal/core.Service`
- replace `s.clock.Now().UTC()` with `time.Now().UTC()`
- simplify service construction so callers no longer pass a clock implementation

Tests should stop asserting exact fake-clock equality. Instead:

- capture `before := time.Now().UTC()`
- execute the operation under test
- capture `after := time.Now().UTC()`
- assert timestamps fall within `[before, after]` where exact value matters
- otherwise assert simpler invariants such as:
  - non-zero timestamps
  - `UpdatedAt` changed
  - `UpdatedAt` is after or equal to `CreatedAt`
  - reconciliation or cleanup timestamps moved forward

This removes a production dependency that exists only to support tests.

### Tooling

The repository should follow the existing dependency-install pattern rather than introducing a one-off command.

- update `scripts/dependencies/install.sh` so `make dependencies install` installs `mockery`
- keep the generated files out of git
- add or update repo generation wiring in `Makefile`
- document the expectation that contributors run `make generate` before `go test ./...` when mocks are missing or interfaces change

## Affected Areas

- `internal/core`
- `internal/adapters/handler/cli`
- `internal/adapters/client/tmux`
- `internal/pkg/execx`
- `cmd/agent`
- `scripts/dependencies/install.sh`
- `Makefile`
- `.gitignore`
- `.mockery.yaml`

## Risks

- mock generation can add friction if the workflow is not obvious or the generated files are placed inconsistently
- converting exact clock-based assertions to time windows can make tests flaky if the assertions are too loose or too strict
- package-local interfaces in tmux tests may need careful handling so generation works without reshaping production code

## Mitigations

- keep the `mockery` config explicit and package-local
- mirror the existing `fws-facade` config style to reduce surprise
- use tight but realistic time-window assertions with `before` and `after` captured immediately around the operation
- avoid introducing new interfaces just to satisfy generation tooling

## Acceptance Criteria

- handwritten interface-based test doubles are removed across the repo
- generated mocks are produced by `mockery`, not committed to git
- `make dependencies install` installs `mockery`
- `make generate` regenerates mocks successfully
- `internal/pkg/timeutil/clock.go` is gone
- `internal/core` no longer depends on a clock interface
- the full test suite passes after generation with real-time assertions
