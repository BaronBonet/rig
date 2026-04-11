# Add `gh` to Doctor Checks — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Report a note in `agent doctor` when the `gh` CLI is unavailable, so users know PR status checks won't work.

**Architecture:** Add `IsAvailable` to the existing `PRStatusChecker` interface, implement it in the GitHub adapter by running `gh --version`, and call it from `Service.Doctor()` — appending to `Notes` (not `Failures`) on error since `gh` is optional.

**Tech Stack:** Go, testify (require/mock), mockery for mock regeneration.

---

### Task 1: Add `IsAvailable` to `PRStatusChecker` interface and regenerate mocks

**Files:**
- Modify: `internal/core/ports.go:132-134`
- Regenerate: `internal/core/mock_pr_status_checker.go`

- [ ] **Step 1: Add `IsAvailable` to the interface**

In `internal/core/ports.go`, change:

```go
type PRStatusChecker interface {
	CheckPRStatus(ctx context.Context, repoRoot string, branchName string) (*PRStatus, error)
}
```

to:

```go
type PRStatusChecker interface {
	IsAvailable(ctx context.Context) error
	CheckPRStatus(ctx context.Context, repoRoot string, branchName string) (*PRStatus, error)
}
```

- [ ] **Step 2: Regenerate mocks**

Run: `make generate`

Expected: succeeds, `internal/core/mock_pr_status_checker.go` now contains `IsAvailable` method and expecter.

- [ ] **Step 3: Verify build compiles**

Run: `go build ./...`

Expected: FAIL — `internal/adapters/client/github/pr_status.go` does not implement `IsAvailable` yet. This is expected and fixed in Task 2.

- [ ] **Step 4: Commit**

```bash
git add internal/core/ports.go internal/core/mock_pr_status_checker.go
git commit -m "feat: add IsAvailable to PRStatusChecker interface"
```

---

### Task 2: Implement `IsAvailable` in the GitHub adapter

**Files:**
- Modify: `internal/adapters/client/github/pr_status.go`
- Modify: `internal/adapters/client/github/pr_status_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/adapters/client/github/pr_status_test.go`:

```go
func TestGHPRChecker_IsAvailable_Succeeds(t *testing.T) {
	runner := execx.NewMockRunner(t)
	runner.EXPECT().
		Run(mock.Anything, "", "gh", "--version").
		Return(execx.Result{Stdout: "gh version 2.50.0\n"}, nil).
		Once()

	checker := NewPRStatusChecker(runner)
	err := checker.IsAvailable(context.Background())

	require.NoError(t, err)
}

func TestGHPRChecker_IsAvailable_ReturnsError(t *testing.T) {
	runner := execx.NewMockRunner(t)
	runner.EXPECT().
		Run(mock.Anything, "", "gh", "--version").
		Return(execx.Result{}, &execx.CommandError{Err: context.Canceled}).
		Once()

	checker := NewPRStatusChecker(runner)
	err := checker.IsAvailable(context.Background())

	require.Error(t, err)
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/adapters/client/github/... -run TestGHPRChecker_IsAvailable -v`

Expected: FAIL — `IsAvailable` method does not exist on `PRStatusChecker`.

- [ ] **Step 3: Implement `IsAvailable`**

Add to `internal/adapters/client/github/pr_status.go`, after the `NewPRStatusChecker` function:

```go
func (c *PRStatusChecker) IsAvailable(ctx context.Context) error {
	_, err := c.runner.Run(ctx, "", "gh", "--version")
	return err
}
```

- [ ] **Step 4: Run all tests to verify they pass**

Run: `go test ./internal/adapters/client/github/... -v`

Expected: all pass including the two new `IsAvailable` tests.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/client/github/pr_status.go internal/adapters/client/github/pr_status_test.go
git commit -m "feat: implement IsAvailable for gh PRStatusChecker"
```

---

### Task 3: Wire `gh` check into `Service.Doctor()`

**Files:**
- Modify: `internal/core/service.go:634-679` (Doctor method)
- Modify: `internal/core/service_doctor_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `internal/core/service_doctor_test.go`:

```go
func TestServiceDoctor_NotesGHUnavailable(t *testing.T) {
	svc := newTestService(t)
	prChecker := NewMockPRStatusChecker(t)
	prChecker.EXPECT().IsAvailable(mock.Anything).Return(errors.New("gh not found")).Once()
	svc.service.SetPRStatusChecker(prChecker)

	result, err := svc.service.Doctor(t.Context(), "/tmp/repo")
	require.NoError(t, err)
	require.Contains(t, result.Notes, "gh: gh CLI not found, PR status checks will be unavailable")
	require.Empty(t, result.Failures)
}

func TestServiceDoctor_NoNoteWhenGHAvailable(t *testing.T) {
	svc := newTestService(t)
	prChecker := NewMockPRStatusChecker(t)
	prChecker.EXPECT().IsAvailable(mock.Anything).Return(nil).Once()
	svc.service.SetPRStatusChecker(prChecker)

	result, err := svc.service.Doctor(t.Context(), "/tmp/repo")
	require.NoError(t, err)
	for _, note := range result.Notes {
		require.NotContains(t, note, "gh:")
	}
}

func TestServiceDoctor_NoNoteWhenPRCheckerNotSet(t *testing.T) {
	svc := newTestService(t)

	result, err := svc.service.Doctor(t.Context(), "/tmp/repo")
	require.NoError(t, err)
	for _, note := range result.Notes {
		require.NotContains(t, note, "gh:")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/core/... -run TestServiceDoctor_NotesGH -v && go test ./internal/core/... -run TestServiceDoctor_NoNoteWhenGH -v && go test ./internal/core/... -run TestServiceDoctor_NoNoteWhenPR -v`

Expected: `TestServiceDoctor_NotesGHUnavailable` FAILS (no gh note produced). The other two should pass since they assert absence.

- [ ] **Step 3: Add the `gh` check to `Service.Doctor()`**

In `internal/core/service.go`, in the `Doctor` method, add the following block after the provider loop (after line 653, before the `cwd` check):

```go
	if s.prChecker != nil {
		if err := s.prChecker.IsAvailable(ctx); err != nil {
			result.Notes = append(result.Notes, "gh: gh CLI not found, PR status checks will be unavailable")
		}
	}
```

- [ ] **Step 4: Run all doctor tests to verify they pass**

Run: `go test ./internal/core/... -run TestServiceDoctor -v`

Expected: all pass.

- [ ] **Step 5: Run full test suite**

Run: `make test`

Expected: all pass.

- [ ] **Step 6: Commit**

```bash
git add internal/core/service.go internal/core/service_doctor_test.go
git commit -m "feat: report gh CLI availability as note in agent doctor"
```
