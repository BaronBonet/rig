# Worktree Post-Creation Setup Script Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `seed.setup_script` field to `agent.yaml` that runs a version-controlled shell script in new worktrees after seeding, before tmux launch.

**Architecture:** Extend `SeedConfig` with a `SetupScript` field. Add a new `SetupScriptRunner` port interface and filesystem adapter that executes the script with live-streamed output. Wire the runner into the service between seed and bootstrap steps.

**Tech Stack:** Go, testify, mockery (for mock generation), bash (script execution)

---

### Task 1: Add `SetupScript` to the domain type

**Files:**
- Modify: `internal/core/ports.go:16-18` (SeedConfig struct)

- [ ] **Step 1: Write the failing test**

In `internal/core/service_new_test.go`, add a test that verifies the setup script runner is called during task creation, after seeding but before the tmux session starts:

```go
func TestServiceCreateTaskWithProgress_RunsSetupScriptAfterSeedingBeforeTmux(t *testing.T) {
	svc := newTestService(t)
	svc.configRepo.repoConfig = RepoConfig{
		Seed: SeedConfig{
			Copy:        []string{".env"},
			SetupScript: "scripts/setup.sh",
		},
	}

	var events []TaskProgress
	task, err := svc.service.CreateTaskWithProgress(t.Context(), NewTaskInput{
		Cwd:                  "/tmp/repo",
		Prompt:               "run setup script",
		ConfirmedDisplayName: "run setup script",
	}, CreateTaskOptions{}, func(event TaskProgress) {
		events = append(events, event)
	})

	require.NoError(t, err)
	require.Equal(t, TaskStatusRunning, task.Status)
	require.True(t, svc.setupRunner.runCalled)
	require.Equal(t, "/tmp/repo", svc.setupRunner.runRepoRoot)
	require.Equal(t, "/tmp/repo-run-setup-script", svc.setupRunner.runWorktreePath)
	require.Equal(t, "scripts/setup.sh", svc.setupRunner.runScriptPath)
	require.True(t, svc.setupRunner.ranAfterSeed)
	require.True(t, svc.setupRunner.ranBeforeSession)
	require.Equal(t, []TaskProgressStep{
		TaskProgressNameSelected,
		TaskProgressWorktreeCreating,
		TaskProgressWorkspaceSeeding,
		TaskProgressWorkspaceSeeded,
		TaskProgressSetupScriptRunning,
		TaskProgressTmuxStarting,
		TaskProgressAgentLaunching,
		TaskProgressTaskCreated,
	}, progressSteps(events))
	require.Equal(t, "Running setup script...", events[4].Message)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/core/ -run TestServiceCreateTaskWithProgress_RunsSetupScriptAfterSeedingBeforeTmux -v`
Expected: FAIL — `SetupScript` field doesn't exist on `SeedConfig`, `TaskProgressSetupScriptRunning` undefined, `setupRunner` field doesn't exist on test harness.

- [ ] **Step 3: Add SetupScript field to SeedConfig**

In `internal/core/ports.go`, update:

```go
type SeedConfig struct {
	Copy        []string
	SetupScript string
}
```

- [ ] **Step 4: Add the SetupScriptRunner port interface**

In `internal/core/ports.go`, add after the `WorkspaceSeeder` interface:

```go
type RunSetupScriptInput struct {
	RepoRoot     string
	WorktreePath string
	ScriptPath   string
}

type SetupScriptRunner interface {
	RunSetupScript(ctx context.Context, in RunSetupScriptInput, output func(string)) error
	ValidateSetupScript(ctx context.Context, repoRoot string, scriptPath string) error
}
```

- [ ] **Step 5: Add the progress step constant**

In `internal/core/domain.go`, add:

```go
TaskProgressSetupScriptRunning TaskProgressStep = "setup_script_running"
```

- [ ] **Step 6: Commit**

```bash
git add internal/core/ports.go internal/core/domain.go
git commit -m "feat: add SetupScript field to SeedConfig and SetupScriptRunner port"
```

---

### Task 2: Wire the SetupScriptRunner into the service

**Files:**
- Modify: `internal/core/service.go:34-51` (Service struct and NewService)
- Modify: `internal/core/service.go:88-272` (CreateTaskWithProgress)
- Modify: `internal/core/service.go:634-679` (Doctor)
- Modify: `internal/core/test_helpers_test.go` (test harness)

- [ ] **Step 1: Add setupRunner to the Service struct and NewService**

In `internal/core/service.go`, add the field to the Service struct:

```go
type Service struct {
	tasks      TaskRepository
	hooks      HookObservabilityRepository
	observers  ObserverRuntimeRepository
	repo       RepoClient
	session    SessionClient
	providers  map[string]ProviderClient
	repoConfig RepoConfigLoader
	workspace  WorkspaceSeeder
	bootstrap  TaskWorkspaceBootstrapper
	setupRunner SetupScriptRunner
	cfg        Config

	usageReader SessionUsageReader

	prChecker PRStatusChecker
	prCacheTTL time.Duration
	prCache    map[string]prCacheEntry
	prCacheMu  sync.Mutex
}
```

Update the `NewService` function signature and body:

```go
func NewService(
	tasks TaskRepository,
	hooks HookObservabilityRepository,
	observers ObserverRuntimeRepository,
	repo RepoClient,
	session SessionClient,
	providers map[string]ProviderClient,
	repoConfig RepoConfigLoader,
	workspace WorkspaceSeeder,
	bootstrap TaskWorkspaceBootstrapper,
	setupRunner SetupScriptRunner,
	cfg Config,
) *Service {
	return &Service{
		tasks:       tasks,
		hooks:       hooks,
		observers:   observers,
		repo:        repo,
		session:     session,
		providers:   providers,
		repoConfig:  repoConfig,
		workspace:   workspace,
		bootstrap:   bootstrap,
		setupRunner: setupRunner,
		cfg:         cfg,
	}
}
```

- [ ] **Step 2: Add setup script validation in CreateTaskWithProgress**

In `internal/core/service.go`, after the `ValidateSeedPaths` block (after line 107) and before the naming step, add:

```go
	if repoConfig.Seed.SetupScript != "" {
		if err := s.setupRunner.ValidateSetupScript(ctx, repoCtx.Root, repoConfig.Seed.SetupScript); err != nil {
			return nil, fmt.Errorf("setup script: %w", err)
		}
	}
```

- [ ] **Step 3: Add setup script execution in CreateTaskWithProgress**

In `internal/core/service.go`, after the seed workspace block (after line 209) and before the bootstrap block, add:

```go
	if repoConfig.Seed.SetupScript != "" {
		emitTaskProgress(progress, TaskProgress{
			Step:    TaskProgressSetupScriptRunning,
			Message: "Running setup script...",
			Task:    cloneTask(task),
		})

		err := s.setupRunner.RunSetupScript(ctx, RunSetupScriptInput{
			RepoRoot:     task.RepoRoot,
			WorktreePath: task.WorktreePath,
			ScriptPath:   repoConfig.Seed.SetupScript,
		}, func(line string) {
			emitTaskProgress(progress, TaskProgress{
				Step:    TaskProgressSetupScriptRunning,
				Message: line,
				Task:    cloneTask(task),
			})
		})
		if err != nil {
			return s.markBroken(ctx, task, fmt.Errorf("setup script: %w", err))
		}
	}
```

- [ ] **Step 4: Add setup script validation in Doctor**

In `internal/core/service.go`, in the `Doctor` method, after the seed path validation block (after the `for _, path := range repoConfig.Seed.Copy` loop around line 671), add:

```go
				if repoConfig.Seed.SetupScript != "" {
					if err := s.setupRunner.ValidateSetupScript(ctx, repoCtx.Root, repoConfig.Seed.SetupScript); err != nil {
						result.Failures = append(result.Failures, "config: "+err.Error())
					} else {
						result.Notes = append(result.Notes, "config: setup script ok: "+repoConfig.Seed.SetupScript)
					}
				}
```

- [ ] **Step 5: Add SetupScriptRunner to .mockery.yaml**

In `.mockery.yaml`, under `agent/internal/core`, add:

```yaml
      SetupScriptRunner:
```

- [ ] **Step 6: Regenerate mocks**

Run: `make generate`

- [ ] **Step 7: Update the test harness**

In `internal/core/test_helpers_test.go`, add the setup runner state and wiring:

Add the state struct:

```go
type setupScriptRunnerState struct {
	validateErr      error
	runErr           error
	validateRepoRoot string
	validateScript   string
	runCalled        bool
	runRepoRoot      string
	runWorktreePath  string
	runScriptPath    string
	ranAfterSeed     bool
	ranBeforeSession bool
}
```

Add to `testServiceHarness`:

```go
type testServiceHarness struct {
	// ... existing fields ...

	setupRunnerMock *MockSetupScriptRunner
	setupRunner     setupScriptRunnerState
}
```

In `newTestService`, add initialization:

```go
	h := &testServiceHarness{
		// ... existing fields ...
		setupRunnerMock: NewMockSetupScriptRunner(t),
		// ...
	}
```

Add ordering hooks — update the existing `startHook` to also track setup runner ordering:

```go
	h.sessionClient.startHook = func() {
		h.workspaceSeeder.seededBeforeSession = h.workspaceSeeder.seedCalled
		h.setupRunner.ranBeforeSession = h.setupRunner.runCalled
	}
```

Wire the mock, add a new function:

```go
func wireSetupScriptRunnerMock(h *testServiceHarness) {
	h.setupRunnerMock.EXPECT().RunSetupScript(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, in RunSetupScriptInput, output func(string)) error {
			h.setupRunner.runCalled = true
			h.setupRunner.runRepoRoot = in.RepoRoot
			h.setupRunner.runWorktreePath = in.WorktreePath
			h.setupRunner.runScriptPath = in.ScriptPath
			h.setupRunner.ranAfterSeed = h.workspaceSeeder.seedCalled
			if h.setupRunner.runErr != nil {
				return h.setupRunner.runErr
			}
			return nil
		}).Maybe()
	h.setupRunnerMock.EXPECT().ValidateSetupScript(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, repoRoot string, scriptPath string) error {
			h.setupRunner.validateRepoRoot = repoRoot
			h.setupRunner.validateScript = scriptPath
			return h.setupRunner.validateErr
		}).Maybe()
}
```

Call `wireSetupScriptRunnerMock(h)` in `newTestService`.

Update `NewService` call in `newTestService` to include the new parameter:

```go
	h.service = NewService(
		h.taskRepoMock,
		nil,
		nil,
		h.repoClientMock,
		h.sessionClientMock,
		map[string]ProviderClient{
			"codex": h.providerRepoMock,
		},
		h.configRepoMock,
		h.workspaceSeederMock,
		nil,
		h.setupRunnerMock,
		Config{Provider: "codex"},
	)
```

- [ ] **Step 8: Fix all other NewService call sites**

Every call to `NewService` across the codebase needs the new `setupRunner` parameter. Search for all `NewService(` calls and add `nil` (or the mock) as the new parameter in position after `bootstrap` and before `cfg`.

Run: `grep -rn 'NewService(' internal/ cmd/`

For each call site, add the `setupRunner` parameter.

- [ ] **Step 9: Run tests to verify the new test passes**

Run: `go test ./internal/core/ -run TestServiceCreateTaskWithProgress_RunsSetupScriptAfterSeedingBeforeTmux -v`
Expected: PASS

- [ ] **Step 10: Run all core tests to verify no regressions**

Run: `go test ./internal/core/ -v`
Expected: All pass. The existing tests should continue to work because the mock's `.Maybe()` allows it to not be called.

- [ ] **Step 11: Commit**

```bash
git add internal/core/service.go internal/core/test_helpers_test.go internal/core/service_new_test.go .mockery.yaml internal/core/mock_setup_script_runner.go
git commit -m "feat: wire SetupScriptRunner into service and test harness"
```

---

### Task 3: Add tests for setup script failure and validation

**Files:**
- Modify: `internal/core/service_new_test.go`
- Modify: `internal/core/service_doctor_test.go`

- [ ] **Step 1: Write test for setup script failure aborting task creation**

In `internal/core/service_new_test.go`:

```go
func TestServiceCreateTaskWithProgress_MarksBrokenWhenSetupScriptFails(t *testing.T) {
	svc := newTestService(t)
	svc.configRepo.repoConfig = RepoConfig{
		Seed: SeedConfig{
			SetupScript: "scripts/setup.sh",
		},
	}
	svc.setupRunner.runErr = errors.New("exit status 1")

	task, err := svc.service.CreateTaskWithProgress(t.Context(), NewTaskInput{
		Cwd:                  "/tmp/repo",
		Prompt:               "setup fails",
		ConfirmedDisplayName: "setup fails",
	}, CreateTaskOptions{}, nil)

	require.Error(t, err)
	require.Equal(t, TaskStatusBroken, task.Status)
	require.Contains(t, task.LastError, "setup script")
	require.Nil(t, svc.sessionClient.startedTask)
}
```

- [ ] **Step 2: Run test to verify it passes**

Run: `go test ./internal/core/ -run TestServiceCreateTaskWithProgress_MarksBrokenWhenSetupScriptFails -v`
Expected: PASS

- [ ] **Step 3: Write test for setup script validation failure before task creation**

In `internal/core/service_new_test.go`:

```go
func TestServiceCreateTaskWithProgress_FailsBeforeCreatingTaskWhenSetupScriptValidationFails(t *testing.T) {
	svc := newTestService(t)
	svc.configRepo.repoConfig = RepoConfig{
		Seed: SeedConfig{
			SetupScript: "scripts/setup.sh",
		},
	}
	svc.setupRunner.validateErr = errors.New("script not found")

	task, err := svc.service.CreateTaskWithProgress(t.Context(), NewTaskInput{
		Cwd:                  "/tmp/repo",
		Prompt:               "validate fails",
		ConfirmedDisplayName: "validate fails",
	}, CreateTaskOptions{}, nil)

	require.Error(t, err)
	require.Nil(t, task)
	require.EqualError(t, err, "setup script: script not found")
	require.Nil(t, svc.taskRepo.createdTask)
	require.Nil(t, svc.repoClient.createdTask)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/core/ -run TestServiceCreateTaskWithProgress_FailsBeforeCreatingTaskWhenSetupScriptValidationFails -v`
Expected: PASS

- [ ] **Step 5: Write test for setup script without copy paths**

In `internal/core/service_new_test.go`:

```go
func TestServiceCreateTaskWithProgress_RunsSetupScriptEvenWithoutCopyPaths(t *testing.T) {
	svc := newTestService(t)
	svc.configRepo.repoConfig = RepoConfig{
		Seed: SeedConfig{
			SetupScript: "scripts/setup.sh",
		},
	}

	task, err := svc.service.CreateTaskWithProgress(t.Context(), NewTaskInput{
		Cwd:                  "/tmp/repo",
		Prompt:               "setup only",
		ConfirmedDisplayName: "setup only",
	}, CreateTaskOptions{}, nil)

	require.NoError(t, err)
	require.Equal(t, TaskStatusRunning, task.Status)
	require.True(t, svc.setupRunner.runCalled)
	require.Equal(t, "scripts/setup.sh", svc.setupRunner.runScriptPath)
}
```

- [ ] **Step 6: Run test to verify it passes**

Run: `go test ./internal/core/ -run TestServiceCreateTaskWithProgress_RunsSetupScriptEvenWithoutCopyPaths -v`
Expected: PASS

- [ ] **Step 7: Write doctor test for valid setup script**

In `internal/core/service_doctor_test.go`:

```go
func TestServiceDoctor_ReportsValidSetupScriptAsNote(t *testing.T) {
	svc := newTestService(t)
	svc.configRepo.repoConfig = RepoConfig{
		Exists: true,
		Seed: SeedConfig{
			SetupScript: "scripts/setup.sh",
		},
	}

	result, err := svc.service.Doctor(t.Context(), "/tmp/repo")
	require.NoError(t, err)
	require.Contains(t, result.Notes, "config: setup script ok: scripts/setup.sh")
	require.Empty(t, result.Failures)
}
```

- [ ] **Step 8: Write doctor test for invalid setup script**

In `internal/core/service_doctor_test.go`:

```go
func TestServiceDoctor_ReportsInvalidSetupScriptAsFailure(t *testing.T) {
	svc := newTestService(t)
	svc.configRepo.repoConfig = RepoConfig{
		Exists: true,
		Seed: SeedConfig{
			SetupScript: "scripts/missing.sh",
		},
	}
	svc.setupRunner.validateErr = errors.New("setup script \"scripts/missing.sh\" not found")

	result, err := svc.service.Doctor(t.Context(), "/tmp/repo")
	require.NoError(t, err)
	require.Contains(t, result.Failures, "config: setup script \"scripts/missing.sh\" not found")
}
```

- [ ] **Step 9: Run all core tests**

Run: `go test ./internal/core/ -v`
Expected: All pass.

- [ ] **Step 10: Commit**

```bash
git add internal/core/service_new_test.go internal/core/service_doctor_test.go
git commit -m "test: add setup script service-level tests"
```

---

### Task 4: Parse `setup_script` from agent.yaml

**Files:**
- Modify: `internal/adapters/filesystem/agentconfig/repository.go:50-108` (parseSeedCopy → parseSeed)
- Modify: `internal/adapters/filesystem/agentconfig/repository_test.go`

- [ ] **Step 1: Write failing tests for setup_script parsing**

In `internal/adapters/filesystem/agentconfig/repository_test.go`, add:

```go
func TestRepositoryLoadRepoConfig_ParsesSetupScript(t *testing.T) {
	t.Run("valid setup_script is returned in config", func(t *testing.T) {
		repoRoot := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "agent.yaml"), []byte(`
seed:
  setup_script: scripts/setup.sh
`), 0o644))

		repo := NewLoader()

		cfg, err := repo.LoadRepoConfig(t.Context(), repoRoot)
		require.NoError(t, err)
		require.True(t, cfg.Exists)
		require.Equal(t, "scripts/setup.sh", cfg.Seed.SetupScript)
	})

	t.Run("setup_script with copy paths", func(t *testing.T) {
		repoRoot := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "agent.yaml"), []byte(`
seed:
  copy:
    - .env
  setup_script: scripts/setup.sh
`), 0o644))

		repo := NewLoader()

		cfg, err := repo.LoadRepoConfig(t.Context(), repoRoot)
		require.NoError(t, err)
		require.True(t, cfg.Exists)
		require.Equal(t, []string{".env"}, cfg.Seed.Copy)
		require.Equal(t, "scripts/setup.sh", cfg.Seed.SetupScript)
	})

	t.Run("empty setup_script is treated as absent", func(t *testing.T) {
		repoRoot := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "agent.yaml"), []byte(`
seed:
  setup_script: ""
`), 0o644))

		repo := NewLoader()

		cfg, err := repo.LoadRepoConfig(t.Context(), repoRoot)
		require.NoError(t, err)
		require.True(t, cfg.Exists)
		require.Empty(t, cfg.Seed.SetupScript)
	})

	t.Run("non-string setup_script returns an error", func(t *testing.T) {
		repoRoot := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "agent.yaml"), []byte(`
seed:
  setup_script: [a, b]
`), 0o644))

		repo := NewLoader()

		_, err := repo.LoadRepoConfig(t.Context(), repoRoot)
		require.Error(t, err)
		require.ErrorContains(t, err, "setup_script must be a string")
	})

	t.Run("null setup_script returns an error", func(t *testing.T) {
		repoRoot := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "agent.yaml"), []byte(`
seed:
  setup_script: null
`), 0o644))

		repo := NewLoader()

		_, err := repo.LoadRepoConfig(t.Context(), repoRoot)
		require.Error(t, err)
		require.ErrorContains(t, err, "setup_script must be a string")
	})

	t.Run("setup_script with path traversal returns an error", func(t *testing.T) {
		repoRoot := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "agent.yaml"), []byte(`
seed:
  setup_script: ../evil.sh
`), 0o644))

		repo := NewLoader()

		_, err := repo.LoadRepoConfig(t.Context(), repoRoot)
		require.Error(t, err)
		require.ErrorContains(t, err, "must not contain path traversal")
	})

	t.Run("setup_script with absolute path returns an error", func(t *testing.T) {
		repoRoot := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "agent.yaml"), []byte(`
seed:
  setup_script: /tmp/evil.sh
`), 0o644))

		repo := NewLoader()

		_, err := repo.LoadRepoConfig(t.Context(), repoRoot)
		require.Error(t, err)
		require.ErrorContains(t, err, "must be repo-relative")
	})

	t.Run("setup_script with glob pattern returns an error", func(t *testing.T) {
		repoRoot := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "agent.yaml"), []byte(`
seed:
  setup_script: "scripts/*.sh"
`), 0o644))

		repo := NewLoader()

		_, err := repo.LoadRepoConfig(t.Context(), repoRoot)
		require.Error(t, err)
		require.ErrorContains(t, err, "must not contain glob characters")
	})
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/adapters/filesystem/agentconfig/ -run TestRepositoryLoadRepoConfig_ParsesSetupScript -v`
Expected: FAIL — `setup_script` is an unknown key.

- [ ] **Step 3: Update the parser to accept and parse setup_script**

In `internal/adapters/filesystem/agentconfig/repository.go`, rename `parseSeedCopy` to `parseSeed` and update it to return both copy paths and setup script. Update the `LoadRepoConfig` method accordingly.

Update `validateAllowedKeys` call for "seed" to allow both "copy" and "setup_script":

```go
	if err := validateAllowedKeys(seedNode, "seed", "copy", "setup_script"); err != nil {
		return nil, err
	}
```

After parsing `copyNode`, add parsing for `setup_script`:

```go
	setupScriptNode, ok, err := lookupMapping(seedNode, "setup_script")
	if err != nil {
		return core.RepoConfig{}, err
	}
	var setupScript string
	if ok {
		if setupScriptNode.Kind != yaml.ScalarNode || setupScriptNode.Tag != "!!str" {
			return core.RepoConfig{}, fmt.Errorf("invalid agent.yaml: seed.setup_script must be a string")
		}
		setupScript = setupScriptNode.Value
		if setupScript != "" {
			if err := validateSeedPath(setupScript); err != nil {
				return core.RepoConfig{}, fmt.Errorf("invalid agent.yaml: seed.setup_script %w", err)
			}
		}
	}
```

Update the return value to include `SetupScript`:

```go
	return core.RepoConfig{
		Exists: true,
		Seed: core.SeedConfig{
			Copy:        copyPaths,
			SetupScript: setupScript,
		},
	}, nil
```

The function signature changes from returning `([]string, error)` to being inlined in `LoadRepoConfig` or returning the full seed config. The cleanest approach is to change `parseSeedCopy` into a `parseSeed` function that returns `(core.SeedConfig, error)` and update `LoadRepoConfig` to use it.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/adapters/filesystem/agentconfig/ -v`
Expected: All pass (both existing and new tests).

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/filesystem/agentconfig/repository.go internal/adapters/filesystem/agentconfig/repository_test.go
git commit -m "feat: parse setup_script from agent.yaml"
```

---

### Task 5: Implement the SetupScriptRunner filesystem adapter

**Files:**
- Create: `internal/adapters/filesystem/setupscript/runner.go`
- Create: `internal/adapters/filesystem/setupscript/runner_test.go`

- [ ] **Step 1: Write the failing test for validation**

Create `internal/adapters/filesystem/setupscript/runner_test.go`:

```go
package setupscript

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunner_ValidateSetupScript(t *testing.T) {
	t.Run("valid script passes validation", func(t *testing.T) {
		repoRoot := t.TempDir()
		scriptDir := filepath.Join(repoRoot, "scripts")
		require.NoError(t, os.MkdirAll(scriptDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(scriptDir, "setup.sh"), []byte("#!/bin/bash\necho hi"), 0o755))

		runner := NewRunner()

		err := runner.ValidateSetupScript(t.Context(), repoRoot, "scripts/setup.sh")
		require.NoError(t, err)
	})

	t.Run("missing script fails validation", func(t *testing.T) {
		repoRoot := t.TempDir()

		runner := NewRunner()

		err := runner.ValidateSetupScript(t.Context(), repoRoot, "scripts/setup.sh")
		require.Error(t, err)
		require.ErrorContains(t, err, "not found")
	})

	t.Run("directory instead of file fails validation", func(t *testing.T) {
		repoRoot := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(repoRoot, "scripts", "setup.sh"), 0o755))

		runner := NewRunner()

		err := runner.ValidateSetupScript(t.Context(), repoRoot, "scripts/setup.sh")
		require.Error(t, err)
		require.ErrorContains(t, err, "not a file")
	})

	t.Run("symlink fails validation", func(t *testing.T) {
		repoRoot := t.TempDir()
		scriptDir := filepath.Join(repoRoot, "scripts")
		require.NoError(t, os.MkdirAll(scriptDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(scriptDir, "real.sh"), []byte("#!/bin/bash"), 0o755))
		require.NoError(t, os.Symlink(filepath.Join(scriptDir, "real.sh"), filepath.Join(scriptDir, "setup.sh")))

		runner := NewRunner()

		err := runner.ValidateSetupScript(t.Context(), repoRoot, "scripts/setup.sh")
		require.Error(t, err)
		require.ErrorContains(t, err, "symlink")
	})

	t.Run("script escaping repo root fails validation", func(t *testing.T) {
		repoRoot := t.TempDir()

		runner := NewRunner()

		err := runner.ValidateSetupScript(t.Context(), repoRoot, "../escape.sh")
		require.Error(t, err)
		require.ErrorContains(t, err, "escapes")
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/filesystem/setupscript/ -run TestRunner_ValidateSetupScript -v`
Expected: FAIL — package doesn't exist.

- [ ] **Step 3: Implement the runner with validation**

Create `internal/adapters/filesystem/setupscript/runner.go`:

```go
package setupscript

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"agent/internal/core"
)

type Runner struct{}

func NewRunner() *Runner {
	return &Runner{}
}

func (r *Runner) ValidateSetupScript(_ context.Context, repoRoot string, scriptPath string) error {
	absPath := filepath.Join(repoRoot, scriptPath)

	rel, err := filepath.Rel(repoRoot, absPath)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("setup script %q escapes repo root", scriptPath)
	}

	info, err := os.Lstat(absPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("setup script %q not found", scriptPath)
		}
		return err
	}

	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("setup script %q is a symlink", scriptPath)
	}
	if info.IsDir() {
		return fmt.Errorf("setup script %q is not a file", scriptPath)
	}

	return nil
}

func (r *Runner) RunSetupScript(ctx context.Context, in core.RunSetupScriptInput, output func(string)) error {
	return nil // placeholder for next step
}
```

- [ ] **Step 4: Run validation tests**

Run: `go test ./internal/adapters/filesystem/setupscript/ -run TestRunner_ValidateSetupScript -v`
Expected: PASS

- [ ] **Step 5: Write the failing test for script execution**

Add to `internal/adapters/filesystem/setupscript/runner_test.go`:

```go
func TestRunner_RunSetupScript(t *testing.T) {
	t.Run("runs script in worktree directory and streams output", func(t *testing.T) {
		repoRoot := t.TempDir()
		worktreePath := t.TempDir()

		scriptDir := filepath.Join(repoRoot, "scripts")
		require.NoError(t, os.MkdirAll(scriptDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(scriptDir, "setup.sh"), []byte(`#!/bin/bash
echo "line one"
echo "line two"
pwd
`), 0o755))

		runner := NewRunner()

		var lines []string
		err := runner.RunSetupScript(t.Context(), core.RunSetupScriptInput{
			RepoRoot:     repoRoot,
			WorktreePath: worktreePath,
			ScriptPath:   "scripts/setup.sh",
		}, func(line string) {
			lines = append(lines, line)
		})

		require.NoError(t, err)
		require.Contains(t, lines, "line one")
		require.Contains(t, lines, "line two")
		require.Contains(t, lines, worktreePath)
	})

	t.Run("returns error when script exits non-zero", func(t *testing.T) {
		repoRoot := t.TempDir()
		worktreePath := t.TempDir()

		scriptDir := filepath.Join(repoRoot, "scripts")
		require.NoError(t, os.MkdirAll(scriptDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(scriptDir, "setup.sh"), []byte(`#!/bin/bash
echo "about to fail"
exit 1
`), 0o755))

		runner := NewRunner()

		var lines []string
		err := runner.RunSetupScript(t.Context(), core.RunSetupScriptInput{
			RepoRoot:     repoRoot,
			WorktreePath: worktreePath,
			ScriptPath:   "scripts/setup.sh",
		}, func(line string) {
			lines = append(lines, line)
		})

		require.Error(t, err)
		require.Contains(t, lines, "about to fail")
	})

	t.Run("script reads from repo root but runs in worktree", func(t *testing.T) {
		repoRoot := t.TempDir()
		worktreePath := t.TempDir()

		markerPath := filepath.Join(worktreePath, "marker.txt")

		scriptDir := filepath.Join(repoRoot, "scripts")
		require.NoError(t, os.MkdirAll(scriptDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(scriptDir, "setup.sh"), []byte(`#!/bin/bash
touch marker.txt
`), 0o755))

		runner := NewRunner()

		err := runner.RunSetupScript(t.Context(), core.RunSetupScriptInput{
			RepoRoot:     repoRoot,
			WorktreePath: worktreePath,
			ScriptPath:   "scripts/setup.sh",
		}, func(string) {})

		require.NoError(t, err)
		_, err = os.Stat(markerPath)
		require.NoError(t, err, "marker.txt should be created in worktree")
	})
}
```

- [ ] **Step 6: Run test to verify it fails**

Run: `go test ./internal/adapters/filesystem/setupscript/ -run TestRunner_RunSetupScript -v`
Expected: FAIL — `RunSetupScript` returns nil without doing anything.

- [ ] **Step 7: Implement RunSetupScript**

Replace the placeholder `RunSetupScript` in `runner.go`:

```go
func (r *Runner) RunSetupScript(ctx context.Context, in core.RunSetupScriptInput, output func(string)) error {
	scriptAbsPath := filepath.Join(in.RepoRoot, in.ScriptPath)

	cmd := exec.CommandContext(ctx, "bash", scriptAbsPath)
	cmd.Dir = in.WorktreePath

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("setup script: %w", err)
	}
	cmd.Stderr = cmd.Stdout // merge stderr into stdout

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("setup script: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		if output != nil {
			output(scanner.Text())
		}
	}

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("setup script %q failed: %w", in.ScriptPath, err)
	}

	return nil
}
```

Add `"bufio"` and `"os/exec"` to the imports.

- [ ] **Step 8: Run all tests**

Run: `go test ./internal/adapters/filesystem/setupscript/ -v`
Expected: All pass.

- [ ] **Step 9: Commit**

```bash
git add internal/adapters/filesystem/setupscript/
git commit -m "feat: implement SetupScriptRunner filesystem adapter"
```

---

### Task 6: Wire the runner into the application

**Files:**
- Modify: `cmd/agent/main.go` (add runner to dependency construction)

- [ ] **Step 1: Find and read main.go to understand the wiring**

Run: `cat cmd/agent/main.go` (or use Read tool)

Look for where `NewService` is called and where the workspace seeder and bootstrapper are created. The runner needs to be created and passed in the same way.

- [ ] **Step 2: Add the runner import and instantiation**

Add `"agent/internal/adapters/filesystem/setupscript"` to the import block.

Create the runner near the other filesystem adapters:

```go
setupRunner := setupscript.NewRunner()
```

Pass it to `NewService` in the appropriate position (after `bootstrap`, before `cfg`).

- [ ] **Step 3: Build and verify**

Run: `go build ./cmd/agent/`
Expected: Compiles successfully.

- [ ] **Step 4: Commit**

```bash
git add cmd/agent/main.go
git commit -m "feat: wire SetupScriptRunner into application main"
```

---

### Task 7: Add setup script progress label to TUI

**Files:**
- Modify: `internal/adapters/handler/cli/tui_model.go:1379-1396` (progressStepLabel)

- [ ] **Step 1: Add the progress step label**

In `internal/adapters/handler/cli/tui_model.go`, update `progressStepLabel`:

```go
func progressStepLabel(step core.TaskProgressStep) string {
	switch step {
	case core.TaskProgressNaming:
		return "Suggesting name..."
	case core.TaskProgressWorktreeCreating:
		return "Creating worktree..."
	case core.TaskProgressWorkspaceSeeding:
		return "Seeding workspace..."
	case core.TaskProgressSetupScriptRunning:
		return "Running setup script..."
	case core.TaskProgressTmuxStarting:
		return "Starting session..."
	case core.TaskProgressAgentLaunching:
		return "Launching agent..."
	case core.TaskProgressTaskCreated:
		return "Task created"
	default:
		return ""
	}
}
```

- [ ] **Step 2: Build and verify**

Run: `go build ./cmd/agent/`
Expected: Compiles successfully.

- [ ] **Step 3: Run all tests**

Run: `go test ./...`
Expected: All pass.

- [ ] **Step 4: Commit**

```bash
git add internal/adapters/handler/cli/tui_model.go
git commit -m "feat: add setup script progress label to TUI"
```

---

### Task 8: Verify the complete existing test suite passes

- [ ] **Step 1: Run the full test suite**

Run: `go test ./... -v`
Expected: All tests pass.

- [ ] **Step 2: Run the linter**

Run: `make lint-all`
Expected: No lint errors.

- [ ] **Step 3: Fix any issues found**

If there are failures, fix and re-run until clean.

- [ ] **Step 4: Final commit if any fixes were needed**

```bash
git add -A
git commit -m "fix: address lint and test issues"
```
