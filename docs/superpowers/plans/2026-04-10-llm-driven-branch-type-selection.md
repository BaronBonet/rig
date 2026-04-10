# LLM-Driven Branch Type Selection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the hardcoded `feat/` branch prefix with an LLM-determined branch type based on conventional commit conventions.

**Architecture:** Change `ProviderClient.SuggestTaskName` to return a `TaskSuggestion` struct (name + branch type). Both Claude and Codex providers use a shared prompt from a new `internal/pkg/prompts` package (embedded `.md` file). The service uses the returned branch type to build the branch name. Fallback defaults to `feat` when the LLM fails or returns an unrecognized type.

**Tech Stack:** Go, `go:embed`, testify, mockery (for regenerating mocks)

---

### File Structure

- **Create:** `internal/pkg/prompts/suggest_task.md` — shared prompt markdown
- **Create:** `internal/pkg/prompts/prompts.go` — embeds the `.md` file, exposes `SuggestTaskPrompt` string
- **Modify:** `internal/core/domain.go` — add `TaskSuggestion` struct and `ValidBranchTypes`
- **Modify:** `internal/core/ports.go:120` — change `SuggestTaskName` return type from `(string, error)` to `(TaskSuggestion, error)`
- **Modify:** `internal/core/service.go:56-67,142-143` — update `SuggestTaskName` and `CreateTaskWithProgress` to use `TaskSuggestion`
- **Modify:** `internal/adapters/client/claude/repository.go:47-81` — use shared prompt, parse JSON with branch type
- **Modify:** `internal/adapters/client/codex/repository.go:39-77` — use shared prompt, parse response with branch type
- **Modify:** `internal/adapters/handler/cli/root.go:21` — update `TaskService` interface
- **Modify:** `internal/adapters/observability/observer/tmuxwatcher_test.go:215-217` — update stub
- **Regenerate:** mocks via `make` (mockery)
- **Modify:** test files to use `TaskSuggestion` instead of plain strings

---

### Task 1: Create the shared prompts package

**Files:**
- Create: `internal/pkg/prompts/suggest_task.md`
- Create: `internal/pkg/prompts/prompts.go`
- Test: `internal/pkg/prompts/prompts_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/pkg/prompts/prompts_test.go
package prompts

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSuggestTaskPrompt_IsNonEmpty(t *testing.T) {
	require.NotEmpty(t, SuggestTaskPrompt)
}

func TestSuggestTaskPrompt_ContainsBranchTypeInstruction(t *testing.T) {
	require.True(t, strings.Contains(SuggestTaskPrompt, "branch_type"))
}

func TestSuggestTaskPrompt_ContainsNameInstruction(t *testing.T) {
	require.True(t, strings.Contains(SuggestTaskPrompt, "name"))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/ericbonet/software/tmux-llm-llm-driven-branch-type-selection && go test ./internal/pkg/prompts/ -v`
Expected: FAIL — package does not exist yet

- [ ] **Step 3: Create the prompt markdown file**

```markdown
<!-- internal/pkg/prompts/suggest_task.md -->
You are a task naming and classification assistant.

Given a task description, respond with ONLY a JSON object (no markdown, no explanation):

{"branch_type": "<type>", "name": "<short title>"}

Rules for "branch_type" — choose the most appropriate conventional commit type:
- feat: a new feature or capability
- fix: a bug fix
- chore: maintenance, dependency updates, config changes
- refactor: code restructuring without behavior change
- docs: documentation only
- test: adding or updating tests only
- style: formatting, whitespace, linting (no logic change)
- perf: performance improvement
- ci: CI/CD pipeline changes
- build: build system or external dependency changes

Rules for "name":
- 3-5 words, no quotes
- Describe the work, not the type (the type is in branch_type)
```

- [ ] **Step 4: Create the Go embed file**

```go
// internal/pkg/prompts/prompts.go
package prompts

import _ "embed"

//go:embed suggest_task.md
var SuggestTaskPrompt string
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /Users/ericbonet/software/tmux-llm-llm-driven-branch-type-selection && go test ./internal/pkg/prompts/ -v`
Expected: PASS — all 3 tests pass

- [ ] **Step 6: Commit**

```bash
git add internal/pkg/prompts/
git commit -m "feat: add shared prompts package with suggest_task prompt"
```

---

### Task 2: Add `TaskSuggestion` struct and valid branch types to domain

**Files:**
- Modify: `internal/core/domain.go`
- Test: `internal/core/task_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/core/task_test.go`:

```go
func TestTaskSuggestion_DefaultBranchType(t *testing.T) {
	s := TaskSuggestion{Name: "billing retry flow"}
	require.Equal(t, "feat", s.BranchTypeOrDefault())
}

func TestTaskSuggestion_ValidBranchType(t *testing.T) {
	s := TaskSuggestion{Name: "billing retry flow", BranchType: "fix"}
	require.Equal(t, "fix", s.BranchTypeOrDefault())
}

func TestTaskSuggestion_InvalidBranchTypeFallsBackToFeat(t *testing.T) {
	s := TaskSuggestion{Name: "billing retry flow", BranchType: "banana"}
	require.Equal(t, "feat", s.BranchTypeOrDefault())
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/ericbonet/software/tmux-llm-llm-driven-branch-type-selection && go test ./internal/core/ -run TestTaskSuggestion -v`
Expected: FAIL — `TaskSuggestion` undefined

- [ ] **Step 3: Implement TaskSuggestion in domain.go**

Add to `internal/core/domain.go` (after the `Task` struct):

```go
var validBranchTypes = map[string]bool{
	"feat":     true,
	"fix":      true,
	"chore":    true,
	"refactor": true,
	"docs":     true,
	"test":     true,
	"style":    true,
	"perf":     true,
	"ci":       true,
	"build":    true,
}

type TaskSuggestion struct {
	Name       string `json:"name"`
	BranchType string `json:"branch_type"`
}

func (s TaskSuggestion) BranchTypeOrDefault() string {
	if s.BranchType != "" && validBranchTypes[s.BranchType] {
		return s.BranchType
	}
	return "feat"
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/ericbonet/software/tmux-llm-llm-driven-branch-type-selection && go test ./internal/core/ -run TestTaskSuggestion -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/core/domain.go internal/core/task_test.go
git commit -m "feat: add TaskSuggestion struct with branch type validation"
```

---

### Task 3: Update `ProviderClient` interface and regenerate mocks

**Files:**
- Modify: `internal/core/ports.go:120`
- Regenerate: mocks via mockery

- [ ] **Step 1: Update the interface**

In `internal/core/ports.go`, change line 120:

```go
// Before:
SuggestTaskName(ctx context.Context, prompt string) (string, error)

// After:
SuggestTaskName(ctx context.Context, prompt string) (TaskSuggestion, error)
```

- [ ] **Step 2: Regenerate mocks**

Run: `cd /Users/ericbonet/software/tmux-llm-llm-driven-branch-type-selection && make mocks`

(If `make mocks` doesn't exist, run: `go tool mockery --config=.mockery.yaml`)

- [ ] **Step 3: Verify the generated mock compiles**

Run: `cd /Users/ericbonet/software/tmux-llm-llm-driven-branch-type-selection && go build ./internal/core/...`

Note: This will fail because callers haven't been updated yet. That's expected — we fix callers in subsequent tasks.

- [ ] **Step 4: Commit**

```bash
git add internal/core/ports.go internal/core/mock_provider_client.go
git commit -m "feat: change ProviderClient.SuggestTaskName to return TaskSuggestion"
```

---

### Task 4: Update the service layer

**Files:**
- Modify: `internal/core/service.go:56-67` (SuggestTaskName method)
- Modify: `internal/core/service.go:142-143` (CreateTaskWithProgress — branch name construction)
- Modify: `internal/core/test_helpers_test.go` (mock wiring and state)
- Modify: `internal/core/service_new_test.go` (test assertions)

- [ ] **Step 1: Update test helpers to use TaskSuggestion**

In `internal/core/test_helpers_test.go`, change `providerClientState`:

```go
// Before:
type providerClientState struct {
	isAvailableErr error
	suggestErr     error
	suggestedName  string
	launchErr      error
	launchRequest  LaunchRequest
	runtimeState   RuntimeState
}

// After:
type providerClientState struct {
	isAvailableErr    error
	suggestErr        error
	suggestedName     string
	suggestedSuggestion TaskSuggestion
	launchErr         error
	launchRequest     LaunchRequest
	runtimeState      RuntimeState
}
```

In `wireProviderClientMock`, update the `SuggestTaskName` mock:

```go
// Before:
h.providerRepoMock.EXPECT().SuggestTaskName(mock.Anything, mock.Anything).
	RunAndReturn(func(context.Context, string) (string, error) {
		if h.providerRepo.suggestErr != nil {
			return "", h.providerRepo.suggestErr
		}

		return h.providerRepo.suggestedName, nil
	}).Maybe()

// After:
h.providerRepoMock.EXPECT().SuggestTaskName(mock.Anything, mock.Anything).
	RunAndReturn(func(context.Context, string) (TaskSuggestion, error) {
		if h.providerRepo.suggestErr != nil {
			return TaskSuggestion{}, h.providerRepo.suggestErr
		}
		if h.providerRepo.suggestedSuggestion.Name != "" {
			return h.providerRepo.suggestedSuggestion, nil
		}

		return TaskSuggestion{Name: h.providerRepo.suggestedName, BranchType: "feat"}, nil
	}).Maybe()
```

- [ ] **Step 2: Update service.go SuggestTaskName method**

Replace the `SuggestTaskName` method (lines 56-67) with:

```go
func (s *Service) SuggestTaskName(ctx context.Context, prompt string, provider string) (TaskSuggestion, error) {
	repo := s.resolveProvider(provider)
	if repo == nil {
		return TaskSuggestion{Name: fallbackDisplayName(prompt), BranchType: "feat"}, nil
	}
	suggestion, err := repo.SuggestTaskName(ctx, prompt)
	if err == nil && strings.TrimSpace(suggestion.Name) != "" {
		suggestion.Name = strings.TrimSpace(suggestion.Name)
		return suggestion, nil
	}

	return TaskSuggestion{Name: fallbackDisplayName(prompt), BranchType: "feat"}, nil
}
```

- [ ] **Step 3: Update CreateTaskWithProgress to use TaskSuggestion**

In `service.go`, update the naming section (around lines 105-115) and branch name construction (line 143):

```go
// Replace the displayName block (lines 105-115):
var suggestion TaskSuggestion
if strings.TrimSpace(input.ConfirmedDisplayName) != "" {
	suggestion = TaskSuggestion{Name: strings.TrimSpace(input.ConfirmedDisplayName), BranchType: "feat"}
} else {
	emitTaskProgress(progress, TaskProgress{
		Step:    TaskProgressNaming,
		Message: "Naming task...",
	})
	var err error
	suggestion, err = s.SuggestTaskName(ctx, input.Prompt, input.Provider)
	if err != nil {
		return nil, err
	}
}

// Then replace the displayName and BranchName usages below:
// Change: displayName := ... (remove this variable)
// Change all references from displayName to suggestion.Name
// Change line 143 from:
//   BranchName: "feat/" + taskSlug,
// To:
//   BranchName: suggestion.BranchTypeOrDefault() + "/" + taskSlug,
```

Specifically, the task construction block becomes:

```go
	taskSlug := slug.EnsureUnique(slug.FromDisplayName(suggestion.Name), existingSlugs)
	task := &Task{
		ID:          fmt.Sprintf("%d", now.UnixNano()),
		Prompt:      input.Prompt,
		DisplayName: suggestion.Name,
		Slug:        taskSlug,
		RepoRoot:    repoCtx.Root,
		RepoName:    repoCtx.Name,
		BaseBranch:  repoCtx.BaseBranch,
		BranchName:       suggestion.BranchTypeOrDefault() + "/" + taskSlug,
		// ... rest unchanged
	}
```

- [ ] **Step 4: Update test assertions in service_new_test.go**

In `TestServiceCreateTaskWithProgress_CreatesWorktreeSessionAndPersistsTask`, the test sets `ConfirmedDisplayName` so branch type defaults to `feat`. Assertion on line 50 stays the same:

```go
require.Equal(t, "feat/billing-retry-flow", task.BranchName)
```

No change needed for this test — `ConfirmedDisplayName` path defaults to `feat`.

- [ ] **Step 5: Run all core tests**

Run: `cd /Users/ericbonet/software/tmux-llm-llm-driven-branch-type-selection && go test ./internal/core/ -v`
Expected: PASS (the build may still fail due to provider implementations not updated yet — if so, skip to Task 5 and come back)

- [ ] **Step 6: Commit**

```bash
git add internal/core/service.go internal/core/test_helpers_test.go internal/core/service_new_test.go
git commit -m "feat: use TaskSuggestion for branch type in service layer"
```

---

### Task 5: Update Claude provider to use shared prompt and return TaskSuggestion

**Files:**
- Modify: `internal/adapters/client/claude/repository.go:47-81`
- Modify: `internal/adapters/client/claude/repository_test.go`

- [ ] **Step 1: Update the test to expect the new prompt and JSON response**

Replace `TestRepositorySuggestTaskName_DelegatesToClaudeProposal` in `repository_test.go`:

```go
func TestRepositorySuggestTaskName_ReturnsTaskSuggestion(t *testing.T) {
	runner := execx.NewMockRunner(t)
	runner.EXPECT().
		RunWithStdin(mock.Anything, mock.MatchedBy(func(opts execx.RunWithStdinOptions) bool {
			return opts.Stdin == "add billing retry flow" && opts.Name == "claude"
		})).
		Return(execx.Result{
			Stdout: `{"type":"result","subtype":"success","result":"{\"branch_type\":\"feat\",\"name\":\"Billing Retry Flow\"}","is_error":false}` + "\n",
		}, nil).
		Once()
	repo := NewRepository(runner, Config{Binary: "claude"})

	suggestion, err := repo.SuggestTaskName(t.Context(), "add billing retry flow")
	require.NoError(t, err)
	require.Equal(t, "Billing Retry Flow", suggestion.Name)
	require.Equal(t, "feat", suggestion.BranchType)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/ericbonet/software/tmux-llm-llm-driven-branch-type-selection && go test ./internal/adapters/client/claude/ -run TestRepositorySuggestTaskName_ReturnsTaskSuggestion -v`
Expected: FAIL — return type mismatch

- [ ] **Step 3: Update the Claude provider implementation**

In `internal/adapters/client/claude/repository.go`, update imports to include the prompts package and core:

```go
import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"agent/internal/core"
	"agent/internal/pkg/execx"
	"agent/internal/pkg/prompts"
)
```

Replace `ProposeTaskName` method:

```go
func (r *Repository) ProposeTaskName(ctx context.Context, prompt string) (core.TaskSuggestion, error) {
	result, err := r.runner.RunWithStdin(ctx, execx.RunWithStdinOptions{
		Name:  r.binary,
		Stdin: prompt,
		Args: []string{
			"-p",
			"--output-format", "json",
			"--tools", "",
			"--system-prompt", prompts.SuggestTaskPrompt,
		},
	})
	if err != nil {
		return core.TaskSuggestion{}, fmt.Errorf("claude exec failed: %w", err)
	}

	var parsed claudeResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(result.Stdout)), &parsed); err != nil {
		return core.TaskSuggestion{}, fmt.Errorf("claude: failed to parse JSON output: %w", err)
	}

	if parsed.IsError {
		return core.TaskSuggestion{}, fmt.Errorf("claude returned error: %s", parsed.Result)
	}

	var suggestion core.TaskSuggestion
	if err := json.Unmarshal([]byte(parsed.Result), &suggestion); err != nil {
		// Fallback: treat the result as a plain name (backwards compat with older responses)
		title := normalizeTitle(parsed.Result)
		if title == "" {
			return core.TaskSuggestion{}, fmt.Errorf("claude did not return a usable task title")
		}
		return core.TaskSuggestion{Name: title, BranchType: "feat"}, nil
	}

	suggestion.Name = normalizeTitle(suggestion.Name)
	if suggestion.Name == "" {
		return core.TaskSuggestion{}, fmt.Errorf("claude did not return a usable task title")
	}

	return suggestion, nil
}

func (r *Repository) SuggestTaskName(ctx context.Context, prompt string) (core.TaskSuggestion, error) {
	return r.ProposeTaskName(ctx, prompt)
}
```

- [ ] **Step 4: Update remaining Claude tests**

Update `TestRepositoryProposeTaskName_ParsesJSONOutput` to expect `TaskSuggestion` return and use a JSON result containing `{"branch_type":"feat","name":"Billing Retry Flow"}`.

Update `TestRepositoryProposeTaskName_StripsMarkdownTicks` — this tests the fallback path where the LLM returns a plain string (not JSON). Adjust to expect `(core.TaskSuggestion, error)` return:

```go
func TestRepositoryProposeTaskName_FallsBackToPlainTextName(t *testing.T) {
	runner := execx.NewMockRunner(t)
	runner.EXPECT().
		RunWithStdin(mock.Anything, mock.MatchedBy(func(opts execx.RunWithStdinOptions) bool {
			return opts.Stdin == "switch sqlite to sqlc"
		})).
		Return(execx.Result{
			Stdout: `{"type":"result","subtype":"success","result":"Migrate to sqlc","is_error":false}` + "\n",
		}, nil).
		Once()
	repo := NewRepository(runner, Config{Binary: "claude"})

	suggestion, err := repo.ProposeTaskName(t.Context(), "switch sqlite to sqlc")
	require.NoError(t, err)
	require.Equal(t, "Migrate to sqlc", suggestion.Name)
	require.Equal(t, "feat", suggestion.BranchType)
}
```

Update `TestRepositoryProposeTaskName_ReturnsErrorOnEmptyResult` and `TestRepositoryProposeTaskName_ReturnsErrorOnAPIError` to expect `(core.TaskSuggestion, error)` returns — error assertions stay the same.

- [ ] **Step 5: Run Claude provider tests**

Run: `cd /Users/ericbonet/software/tmux-llm-llm-driven-branch-type-selection && go test ./internal/adapters/client/claude/ -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/adapters/client/claude/
git commit -m "feat: update Claude provider to return TaskSuggestion with branch type"
```

---

### Task 6: Update Codex provider to use shared prompt and return TaskSuggestion

**Files:**
- Modify: `internal/adapters/client/codex/repository.go:39-77`
- Modify: `internal/adapters/client/codex/repository_test.go`

- [ ] **Step 1: Update the test**

Replace `TestRepositorySuggestTaskName_DelegatesToCodexProposal`:

```go
func TestRepositorySuggestTaskName_ReturnsTaskSuggestion(t *testing.T) {
	runner := execx.NewMockRunner(t)
	runner.EXPECT().
		Run(mock.Anything, "", "codex", "exec", "--skip-git-repo-check", "--output-last-message", mock.Anything, mock.MatchedBy(func(s string) bool {
			return strings.Contains(s, "add billing retry flow")
		})).
		Return(execx.Result{Stdout: `{"branch_type":"feat","name":"billing retry flow"}` + "\n"}, nil).
		Once()
	repo := NewRepository(runner, Config{Binary: "codex"})

	suggestion, err := repo.SuggestTaskName(t.Context(), "add billing retry flow")
	require.NoError(t, err)
	require.Equal(t, "billing retry flow", suggestion.Name)
	require.Equal(t, "feat", suggestion.BranchType)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/ericbonet/software/tmux-llm-llm-driven-branch-type-selection && go test ./internal/adapters/client/codex/ -run TestRepositorySuggestTaskName_ReturnsTaskSuggestion -v`
Expected: FAIL

- [ ] **Step 3: Update Codex provider implementation**

In `internal/adapters/client/codex/repository.go`, update imports:

```go
import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"unicode"

	"agent/internal/core"
	"agent/internal/pkg/execx"
	"agent/internal/pkg/prompts"
)
```

Replace `ProposeTaskName`:

```go
func (r *Repository) ProposeTaskName(ctx context.Context, prompt string) (core.TaskSuggestion, error) {
	tmpFile, err := os.CreateTemp("", "agent-codex-name-*.txt")
	if err != nil {
		return core.TaskSuggestion{}, err
	}
	tmpPath := tmpFile.Name()
	_ = tmpFile.Close()
	defer func() { _ = os.Remove(tmpPath) }()

	fullPrompt := prompts.SuggestTaskPrompt + "\n\nTask description: " + prompt

	result, err := r.runner.Run(
		ctx,
		"",
		r.binary,
		"exec",
		"--skip-git-repo-check",
		"--output-last-message",
		tmpPath,
		fullPrompt,
	)

	// Try to parse from the output file first
	if fileBytes, readErr := os.ReadFile(tmpPath); readErr == nil {
		if suggestion, ok := parseCodexSuggestion(string(fileBytes)); ok {
			return suggestion, nil
		}
	}

	// Fall back to stdout
	if suggestion, ok := parseCodexSuggestion(result.Stdout); ok {
		return suggestion, nil
	}

	// Fall back to extracting a plain title
	if fileBytes, readErr := os.ReadFile(tmpPath); readErr == nil {
		if title := extractCodexTitle(string(fileBytes)); title != "" {
			return core.TaskSuggestion{Name: title, BranchType: "feat"}, nil
		}
	}
	if title := extractCodexTitle(result.Stdout); title != "" {
		return core.TaskSuggestion{Name: title, BranchType: "feat"}, nil
	}

	if err != nil {
		return core.TaskSuggestion{}, fmt.Errorf("codex exec failed: %w", err)
	}

	return core.TaskSuggestion{}, fmt.Errorf("codex did not return a usable task title")
}

func (r *Repository) SuggestTaskName(ctx context.Context, prompt string) (core.TaskSuggestion, error) {
	return r.ProposeTaskName(ctx, prompt)
}

func parseCodexSuggestion(raw string) (core.TaskSuggestion, bool) {
	lines := strings.Split(raw, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var suggestion core.TaskSuggestion
		if err := json.Unmarshal([]byte(line), &suggestion); err == nil && suggestion.Name != "" {
			suggestion.Name = normalizeCodexTitle(suggestion.Name)
			if suggestion.Name != "" {
				return suggestion, true
			}
		}
	}
	return core.TaskSuggestion{}, false
}
```

- [ ] **Step 4: Update remaining Codex tests**

Update `TestRepositoryProposeTaskName_TrimsRunnerOutput` to expect `(core.TaskSuggestion, error)` return.

Update `TestRepositoryProposeTaskName_ExtractsFinalTitleFromTranscriptOutput` — this tests the fallback path (plain text output from codex). Adjust return type expectations:

```go
func TestRepositoryProposeTaskName_FallsBackToPlainTextTitle(t *testing.T) {
	runner := execx.NewMockRunner(t)
	runner.EXPECT().
		Run(mock.Anything, "", "codex", "exec", "--skip-git-repo-check", "--output-last-message", mock.Anything, mock.Anything).
		Return(execx.Result{Stdout: "billing retry flow\n"}, nil).
		Once()
	repo := NewRepository(runner, Config{Binary: "codex"})

	suggestion, err := repo.ProposeTaskName(t.Context(), "add billing retry flow")
	require.NoError(t, err)
	require.Equal(t, "billing retry flow", suggestion.Name)
	require.Equal(t, "feat", suggestion.BranchType)
}
```

Update `TestRepositoryProposeTaskName_StripsMarkdownTicksFromTitle` similarly.

- [ ] **Step 5: Run Codex provider tests**

Run: `cd /Users/ericbonet/software/tmux-llm-llm-driven-branch-type-selection && go test ./internal/adapters/client/codex/ -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/adapters/client/codex/
git commit -m "feat: update Codex provider to return TaskSuggestion with branch type"
```

---

### Task 7: Update CLI handler interface, observer stub, and regenerate all mocks

**Files:**
- Modify: `internal/adapters/handler/cli/root.go:21`
- Modify: `internal/adapters/observability/observer/tmuxwatcher_test.go:215-217`

- [ ] **Step 1: Update TaskService interface in root.go**

Change line 21:

```go
// Before:
SuggestTaskName(ctx context.Context, prompt string, provider string) (string, error)

// After:
SuggestTaskName(ctx context.Context, prompt string, provider string) (core.TaskSuggestion, error)
```

- [ ] **Step 2: Update the observer stub**

In `internal/adapters/observability/observer/tmuxwatcher_test.go`, change:

```go
// Before:
func (s stubTMuxWatcherProvider) SuggestTaskName(context.Context, string) (string, error) {
	return "", nil
}

// After:
func (s stubTMuxWatcherProvider) SuggestTaskName(context.Context, string) (core.TaskSuggestion, error) {
	return core.TaskSuggestion{}, nil
}
```

- [ ] **Step 3: Regenerate all mocks**

Run: `cd /Users/ericbonet/software/tmux-llm-llm-driven-branch-type-selection && make mocks`

(Or: `go tool mockery --config=.mockery.yaml`)

- [ ] **Step 4: Update TUI model to handle TaskSuggestion**

Check `internal/adapters/handler/cli/tui_model.go` around lines 1265-1271. The `suggestTaskNameCmd` function and `suggestNameFinishedMsg` need to carry a `TaskSuggestion` instead of a plain `string`. Update:

```go
// In tui_model.go, update suggestNameFinishedMsg:
type suggestNameFinishedMsg struct {
	prompt     string
	suggestion core.TaskSuggestion
	err        error
}

// Update suggestTaskNameCmd:
func suggestTaskNameCmd(service TaskService, prompt string, provider string) tea.Cmd {
	return safeCmd("suggestTaskNameCmd", func() tea.Msg {
		suggestion, err := service.SuggestTaskName(context.Background(), prompt, provider)
		return suggestNameFinishedMsg{prompt: prompt, suggestion: suggestion, err: err}
	})
}
```

Then find where `suggestNameFinishedMsg` is handled (where `msg.name` is used) and change it to use `msg.suggestion.Name`.

- [ ] **Step 5: Update TUI model tests**

In `internal/adapters/handler/cli/tui_model_test.go`, all `SuggestTaskName` mock expectations return `(string, error)`. Update them to return `(core.TaskSuggestion, error)`:

```go
// Before:
service.EXPECT().
	SuggestTaskName(mock.Anything, "add billing retry flow", "codex").
	Return("billing retry flow", nil).
	Once()

// After:
service.EXPECT().
	SuggestTaskName(mock.Anything, "add billing retry flow", "codex").
	Return(core.TaskSuggestion{Name: "billing retry flow", BranchType: "feat"}, nil).
	Once()
```

Apply this change to all `SuggestTaskName` mock expectations in the file.

- [ ] **Step 6: Build and test everything**

Run: `cd /Users/ericbonet/software/tmux-llm-llm-driven-branch-type-selection && go test ./... -v`
Expected: PASS — full project compiles and all tests pass

- [ ] **Step 7: Commit**

```bash
git add internal/adapters/handler/cli/ internal/adapters/observability/observer/tmuxwatcher_test.go internal/core/mock_*.go
git commit -m "feat: update CLI and observer interfaces for TaskSuggestion"
```

---

### Task 8: Add integration test for non-feat branch type

**Files:**
- Modify: `internal/core/service_new_test.go`

- [ ] **Step 1: Write a test that verifies a fix/ branch is created**

Add to `internal/core/service_new_test.go`:

```go
func TestServiceCreateTaskWithProgress_UsesLLMSuggestedBranchType(t *testing.T) {
	svc := newTestService(t)
	svc.providerRepo.suggestedSuggestion = TaskSuggestion{
		Name:       "billing retry flow",
		BranchType: "fix",
	}

	task, err := svc.service.CreateTaskWithProgress(t.Context(), NewTaskInput{
		Cwd:    "/tmp/repo",
		Prompt: "fix the billing retry flow",
	}, CreateTaskOptions{}, nil)

	require.NoError(t, err)
	require.Equal(t, "fix/billing-retry-flow", task.BranchName)
	require.Equal(t, "billing retry flow", task.DisplayName)
}
```

- [ ] **Step 2: Run the test**

Run: `cd /Users/ericbonet/software/tmux-llm-llm-driven-branch-type-selection && go test ./internal/core/ -run TestServiceCreateTaskWithProgress_UsesLLMSuggestedBranchType -v`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/core/service_new_test.go
git commit -m "test: add integration test for LLM-driven branch type selection"
```

---

### Task 9: Final verification

- [ ] **Step 1: Run the full test suite**

Run: `cd /Users/ericbonet/software/tmux-llm-llm-driven-branch-type-selection && go test ./... -v`
Expected: ALL PASS

- [ ] **Step 2: Verify no hardcoded feat/ remains in service.go**

Run: `grep -n 'feat/' internal/core/service.go`
Expected: No matches (the TODO comment and hardcoded prefix should both be gone)

- [ ] **Step 3: Verify the build is clean**

Run: `cd /Users/ericbonet/software/tmux-llm-llm-driven-branch-type-selection && go build ./...`
Expected: Clean build, no errors
