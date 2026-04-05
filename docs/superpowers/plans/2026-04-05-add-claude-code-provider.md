# Add Claude Code Provider Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Claude Code as a second provider alongside Codex, with per-task provider selection in the TUI and a `--provider` CLI flag.

**Architecture:** Rename the Codex-specific `CodexRepository` interface to a generic `ProviderRepository`, create a parallel Claude Code adapter, and wire provider selection through Config, CLI flag, and TUI picker. All providers implement the same 3-method interface.

**Tech Stack:** Go, Cobra, BubbleTea, lipgloss, testify

---

## File Structure

| Action | File | Responsibility |
|--------|------|----------------|
| Modify | `internal/core/ports.go` | Rename `CodexRepository` → `ProviderRepository` |
| Modify | `internal/core/config.go` | Add `Provider` field to `Config` |
| Modify | `internal/core/task.go` | (no changes — `Provider` field already exists) |
| Modify | `internal/core/progress.go` | Rename `TaskProgressCodexLaunching` → `TaskProgressAgentLaunching` |
| Modify | `internal/core/service.go` | Rename `codex` field → `provider`, use `NewTaskInput.Provider`, dynamic progress messages |
| Modify | `internal/core/fakes_test.go` | Rename `fakeCodexRepository` → `fakeProviderRepository`, rename `codexRepo` → `providerRepo` |
| Modify | `internal/core/service_new_test.go` | Update references from `codexRepo` → `providerRepo` |
| Modify | `internal/core/service_doctor_test.go` | Update references from `codexRepo` → `providerRepo` |
| Create | `internal/adapters/repository/claude/repository.go` | Claude Code adapter implementing `ProviderRepository` |
| Create | `internal/adapters/repository/claude/repository_test.go` | Tests for Claude adapter |
| Modify | `internal/adapters/handler/cli/new.go` | Add `--provider` flag |
| Modify | `internal/adapters/handler/cli/tui_model.go` | Add provider picker to prompt input view |
| Modify | `internal/adapters/handler/cli/tui_style.go` | Add provider icons |
| Modify | `internal/adapters/handler/cli/root.go` | Update description |
| Modify | `cmd/agent/main.go` | Switch on provider to instantiate correct adapter |

---

### Task 1: Rename `CodexRepository` to `ProviderRepository`

**Files:**
- Modify: `internal/core/ports.go:75-79`
- Modify: `internal/core/service.go:31,39,201-213,349-368,382-384`
- Modify: `internal/core/progress.go:7`
- Modify: `internal/core/fakes_test.go:11,17-18,31,39,266-287`
- Modify: `internal/core/service_new_test.go:13,42,72`
- Modify: `internal/core/service_doctor_test.go:12`

- [ ] **Step 1: Rename the interface in ports.go**

In `internal/core/ports.go`, rename the interface:

```go
type ProviderRepository interface {
	ProposeTaskName(ctx context.Context, prompt string) (string, error)
	BuildLaunchCommand(task *Task) ([]string, error)
	IsAvailable(ctx context.Context) error
}
```

- [ ] **Step 2: Rename the field and constructor param in service.go**

In `internal/core/service.go`, rename the `codex` field to `provider` in the `Service` struct:

```go
type Service struct {
	tasks      TaskRepository
	git        GitRepository
	tmux       TmuxRepository
	provider   ProviderRepository
	repoConfig RepoConfigRepository
	workspace  WorkspaceSeeder
	clock      timeutil.Clock
	cfg        Config
}
```

Update the `NewService` constructor parameter name:

```go
func NewService(
	tasks TaskRepository,
	git GitRepository,
	tmux TmuxRepository,
	provider ProviderRepository,
	repoConfig RepoConfigRepository,
	workspace WorkspaceSeeder,
	clock timeutil.Clock,
	cfg Config,
) *Service {
	return &Service{
		tasks:      tasks,
		git:        git,
		tmux:       tmux,
		provider:   provider,
		repoConfig: repoConfig,
		workspace:  workspace,
		clock:      clock,
		cfg:        cfg,
	}
}
```

- [ ] **Step 3: Update all `s.codex` references in service.go**

Replace all `s.codex.` with `s.provider.` in `service.go`. There are three call sites:

1. `SuggestTaskName`: `s.provider.ProposeTaskName(ctx, prompt)`
2. `createTask`: `s.provider.BuildLaunchCommand(task)`
3. `Doctor`: `s.provider.IsAvailable(ctx)`

Also in `Doctor`, update the failure message from `"codex: "` to `"provider: "`:

```go
if err := s.provider.IsAvailable(ctx); err != nil {
	result.Failures = append(result.Failures, "provider: "+err.Error())
}
```

- [ ] **Step 4: Rename progress constant**

In `internal/core/progress.go`, rename:

```go
TaskProgressAgentLaunching TaskProgressStep = "agent_launching"
```

Update the reference in `service.go` from `TaskProgressCodexLaunching` to `TaskProgressAgentLaunching`. Update the message from `"Launching Codex..."` to `"Launching agent..."`. Also update the error message in `createTask` from `"build codex launch command"` to `"build launch command"` and `"launch codex"` to `"launch agent"`.

- [ ] **Step 5: Rename fakes in fakes_test.go**

In `internal/core/fakes_test.go`:

Rename the struct and all references:

```go
type fakeProviderRepository struct {
	isAvailableErr error
	proposeErr     error
	proposedName   string
	launchCommand  []string
}

func (f *fakeProviderRepository) ProposeTaskName(context.Context, string) (string, error) {
	if f.proposeErr != nil {
		return "", f.proposeErr
	}

	return f.proposedName, nil
}
func (f *fakeProviderRepository) BuildLaunchCommand(task *Task) ([]string, error) {
	if len(f.launchCommand) > 0 {
		return append([]string(nil), f.launchCommand...), nil
	}

	return []string{"codex", task.Prompt}, nil
}
func (f *fakeProviderRepository) IsAvailable(context.Context) error { return f.isAvailableErr }
```

In `testServiceHarness`, rename:

```go
type testServiceHarness struct {
	service      *Service
	taskRepo     *fakeTaskRepository
	gitRepo      *fakeGitRepository
	tmuxRepo     *fakeTmuxRepository
	providerRepo *fakeProviderRepository
	configRepo   *fakeRepoConfigRepository
	workspaceSeeder *fakeWorkspaceSeeder
}
```

In `newTestService`, rename:

```go
providerRepo := &fakeProviderRepository{}
```

And update the return struct field and `NewService` call to use `providerRepo`.

- [ ] **Step 6: Update service_new_test.go references**

Replace all `svc.codexRepo` with `svc.providerRepo` in `internal/core/service_new_test.go`. There are references on these lines:

- `svc.codexRepo.proposedName` → `svc.providerRepo.proposedName`
- `svc.codexRepo.proposeErr` → `svc.providerRepo.proposeErr`
- `svc.codexRepo.launchCommand` → `svc.providerRepo.launchCommand`

Also update `TaskProgressCodexLaunching` → `TaskProgressAgentLaunching` in the progress step assertions.

- [ ] **Step 7: Update service_doctor_test.go references**

Replace `svc.codexRepo` with `svc.providerRepo` in `internal/core/service_doctor_test.go`:

```go
svc.providerRepo.isAvailableErr = errors.New("missing codex")
```

Update the assertion from `"codex: missing codex"` to `"provider: missing codex"`.

- [ ] **Step 8: Run tests to verify rename**

Run: `cd /Users/ericbonet/software/tmux-llm-add-claude-code && go test ./internal/core/... -v -count=1`

Expected: All tests pass.

- [ ] **Step 9: Commit**

```bash
git add internal/core/
git commit -m "refactor: rename CodexRepository to ProviderRepository"
```

---

### Task 2: Add `Provider` field to Config and `NewTaskInput`

**Files:**
- Modify: `internal/core/config.go:8-15,17-25`
- Modify: `internal/core/service.go:22-25,105-122`

- [ ] **Step 1: Add Provider to Config**

In `internal/core/config.go`, add the field and default:

```go
type Config struct {
	BaseBranch     string
	DatabasePath   string
	WorktreeMode   string
	CodexBinary    string
	ClaudeBinary   string
	Provider       string
	AttachOnNew    bool
	NonInteractive bool
}

func DefaultConfig() Config {
	return Config{
		BaseBranch:   "main",
		DatabasePath: defaultDatabasePath(),
		WorktreeMode: "sibling",
		CodexBinary:  "codex",
		ClaudeBinary: "claude",
		Provider:     "codex",
		AttachOnNew:  true,
	}
}
```

- [ ] **Step 2: Add Provider to NewTaskInput**

In `internal/core/service.go`, add `Provider` to the input struct:

```go
type NewTaskInput struct {
	Cwd                  string
	Prompt               string
	ConfirmedDisplayName string
	Provider             string
}
```

- [ ] **Step 3: Use input Provider in createTask**

In `internal/core/service.go`, in the `createTask` method, replace the hardcoded `Provider: "codex"` with:

```go
provider := input.Provider
if provider == "" {
	provider = s.cfg.Provider
}
```

Then use `provider` when building the task:

```go
Provider: provider,
```

Also update the progress message to use the provider name:

```go
emitTaskProgress(progress, TaskProgress{
	Step:    TaskProgressAgentLaunching,
	Message: fmt.Sprintf("Launching %s...", task.Provider),
	Task:    cloneTask(task),
})
```

And the event:

```go
_ = s.tasks.AppendEvent(ctx, task.ID, "agent_launch_requested", strings.Join(command, " "))
```

- [ ] **Step 4: Run tests**

Run: `cd /Users/ericbonet/software/tmux-llm-add-claude-code && go test ./internal/core/... -v -count=1`

Expected: All tests pass. (Tests that don't set `input.Provider` will get the default empty string which falls back to `s.cfg.Provider` which is empty in test config, producing `""` — but the Task.Provider field is only used for display/persistence, not dispatch, so tests still pass.)

- [ ] **Step 5: Commit**

```bash
git add internal/core/
git commit -m "feat: add Provider field to Config and NewTaskInput"
```

---

### Task 3: Create Claude Code adapter

**Files:**
- Create: `internal/adapters/repository/claude/repository.go`
- Create: `internal/adapters/repository/claude/repository_test.go`

- [ ] **Step 1: Write failing tests for the Claude adapter**

Create `internal/adapters/repository/claude/repository_test.go`:

```go
package claude

import (
	"testing"

	"agent/internal/core"
	"agent/internal/pkg/execx"

	"github.com/stretchr/testify/require"
)

func TestRepositoryBuildLaunchCommand_IncludesPrompt(t *testing.T) {
	repo := NewRepository(execx.NewFakeRunner(nil), "claude")

	cmd, err := repo.BuildLaunchCommand(&core.Task{
		Prompt: "add billing retry flow",
	})
	require.NoError(t, err)
	require.Equal(t, "claude", cmd[0])
	require.Equal(t, "add billing retry flow", cmd[len(cmd)-1])
}

func TestRepositoryProposeTaskName_ParsesJSONOutput(t *testing.T) {
	runner := execx.NewFakeRunner([]execx.Result{
		{Stdout: `{"type":"result","subtype":"success","cost_usd":0.002,"duration_ms":1500,"duration_api_ms":1200,"is_error":false,"num_turns":1,"result":"Billing Retry Flow","session_id":"abc123","total_cost_usd":0.002}` + "\n"},
	})
	repo := NewRepository(runner, "claude")

	name, err := repo.ProposeTaskName(t.Context(), "add billing retry flow")
	require.NoError(t, err)
	require.Equal(t, "Billing Retry Flow", name)
	require.Equal(t, "claude", runner.Calls[0].Name)
	require.Contains(t, runner.Calls[0].Args, "-p")
	require.Contains(t, runner.Calls[0].Args, "--output-format")
	require.Contains(t, runner.Calls[0].Args, "json")
}

func TestRepositoryProposeTaskName_StripsMarkdownTicks(t *testing.T) {
	runner := execx.NewFakeRunner([]execx.Result{
		{Stdout: `{"type":"result","subtype":"success","result":"Migrate to ` + "`sqlc`" + `","is_error":false}` + "\n"},
	})
	repo := NewRepository(runner, "claude")

	name, err := repo.ProposeTaskName(t.Context(), "switch sqlite to sqlc")
	require.NoError(t, err)
	require.Equal(t, "Migrate to sqlc", name)
}

func TestRepositoryProposeTaskName_ReturnsErrorOnEmptyResult(t *testing.T) {
	runner := execx.NewFakeRunner([]execx.Result{
		{Stdout: `{"type":"result","subtype":"success","result":"","is_error":false}` + "\n"},
	})
	repo := NewRepository(runner, "claude")

	_, err := repo.ProposeTaskName(t.Context(), "do something")
	require.Error(t, err)
}

func TestRepositoryProposeTaskName_ReturnsErrorOnAPIError(t *testing.T) {
	runner := execx.NewFakeRunner([]execx.Result{
		{Stdout: `{"type":"result","subtype":"error","result":"API error","is_error":true}` + "\n"},
	})
	repo := NewRepository(runner, "claude")

	_, err := repo.ProposeTaskName(t.Context(), "do something")
	require.Error(t, err)
}

func TestRepositoryIsAvailable_CallsClaudeVersion(t *testing.T) {
	runner := execx.NewFakeRunner([]execx.Result{
		{Stdout: "1.0.0\n"},
	})
	repo := NewRepository(runner, "claude")

	err := repo.IsAvailable(t.Context())
	require.NoError(t, err)
	require.Equal(t, "claude", runner.Calls[0].Name)
	require.Equal(t, []string{"--version"}, runner.Calls[0].Args)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/ericbonet/software/tmux-llm-add-claude-code && go test ./internal/adapters/repository/claude/... -v -count=1`

Expected: Compilation failure — package doesn't exist yet.

- [ ] **Step 3: Implement the Claude adapter**

Create `internal/adapters/repository/claude/repository.go`:

```go
package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"agent/internal/core"
	"agent/internal/pkg/execx"
)

type Repository struct {
	runner execx.Runner
	binary string
}

func NewRepository(runner execx.Runner, binary string) *Repository {
	if binary == "" {
		binary = "claude"
	}

	return &Repository{
		runner: runner,
		binary: binary,
	}
}

func (r *Repository) IsAvailable(ctx context.Context) error {
	_, err := r.runner.Run(ctx, "", r.binary, "--version")
	return err
}

type claudeResult struct {
	Result  string `json:"result"`
	IsError bool   `json:"is_error"`
}

func (r *Repository) ProposeTaskName(ctx context.Context, prompt string) (string, error) {
	result, err := r.runner.Run(
		ctx,
		"",
		r.binary,
		"-p",
		"--output-format", "json",
		"Reply with only a short task title: "+prompt,
	)
	if err != nil {
		return "", fmt.Errorf("claude exec failed: %w", err)
	}

	var parsed claudeResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(result.Stdout)), &parsed); err != nil {
		return "", fmt.Errorf("claude: failed to parse JSON output: %w", err)
	}

	if parsed.IsError {
		return "", fmt.Errorf("claude returned error: %s", parsed.Result)
	}

	title := normalizeTitle(parsed.Result)
	if title == "" {
		return "", fmt.Errorf("claude did not return a usable task title")
	}

	return title, nil
}

func (r *Repository) BuildLaunchCommand(task *core.Task) ([]string, error) {
	return []string{r.binary, task.Prompt}, nil
}

func normalizeTitle(raw string) string {
	line := strings.TrimSpace(raw)
	line = strings.ReplaceAll(line, "`", "")
	line = strings.Trim(line, "[]")
	line = strings.Trim(line, ":")
	line = strings.TrimSpace(line)

	if line == "" {
		return ""
	}

	if !containsLetter(line) {
		return ""
	}

	return line
}

func containsLetter(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}

	return false
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/ericbonet/software/tmux-llm-add-claude-code && go test ./internal/adapters/repository/claude/... -v -count=1`

Expected: All 6 tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/repository/claude/
git commit -m "feat: add Claude Code provider adapter"
```

---

### Task 4: Wire provider selection in `main.go`

**Files:**
- Modify: `cmd/agent/main.go:9-18,129-150`

- [ ] **Step 1: Import the Claude adapter and add provider switch**

In `cmd/agent/main.go`, add the import:

```go
clauderepo "agent/internal/adapters/repository/claude"
```

In `newService`, replace the hardcoded Codex instantiation with a switch:

```go
func (r *runtimeService) newService(withSQLite bool) (*core.Service, error) {
	var taskRepo core.TaskRepository = noopTaskRepository{}
	if withSQLite {
		sqliteRepo, err := sqliterepo.NewRepository(r.cfg.DatabasePath)
		if err != nil {
			return nil, err
		}

		taskRepo = sqliteRepo
	}

	var providerRepo core.ProviderRepository
	switch r.cfg.Provider {
	case "claude":
		providerRepo = clauderepo.NewRepository(r.runner, r.cfg.ClaudeBinary)
	default:
		providerRepo = codexrepo.NewRepository(r.runner, r.cfg.CodexBinary)
	}

	return core.NewService(
		taskRepo,
		gitrepo.NewRepository(r.runner),
		tmuxrepo.NewRepository(r.runner),
		providerRepo,
		agentconfigrepo.NewRepository(),
		workspacerepo.NewRepository(),
		timeutil.RealClock{},
		r.cfg,
	), nil
}
```

- [ ] **Step 2: Run full test suite**

Run: `cd /Users/ericbonet/software/tmux-llm-add-claude-code && go test ./... -count=1`

Expected: All tests pass.

- [ ] **Step 3: Commit**

```bash
git add cmd/agent/main.go
git commit -m "feat: wire provider selection in main.go"
```

---

### Task 5: Add `--provider` flag to `agent new` CLI

**Files:**
- Modify: `internal/adapters/handler/cli/new.go:16-17,28-33`
- Modify: `internal/adapters/handler/cli/root.go:31`

- [ ] **Step 1: Add the --provider flag**

In `internal/adapters/handler/cli/new.go`, add the flag variable and pass it through the input:

```go
func newNewCommand(deps Dependencies) *cobra.Command {
	var nonInteractive bool
	var jsonOutput bool
	var provider string

	cmd := &cobra.Command{
		Use:   "new <prompt>",
		Short: "Create a new task session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps.Service == nil {
				return fmt.Errorf("service not configured")
			}

			prompt := args[0]
			input := core.NewTaskInput{
				Cwd:      deps.Cwd,
				Prompt:   prompt,
				Provider: provider,
			}

			if !nonInteractive {
				if _, err := fmt.Fprintln(cmd.ErrOrStderr(), "Naming task..."); err != nil {
					return err
				}
				suggested, err := deps.Service.SuggestTaskName(context.Background(), prompt)
				if err != nil {
					return err
				}

				if _, err = fmt.Fprintf(cmd.OutOrStdout(), "Proposed name [%s] (press Enter to accept or type a replacement): ", suggested); err != nil {
					return err
				}

				line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
				if err != nil && err.Error() != "EOF" {
					return err
				}

				line = strings.TrimSpace(line)
				if line == "" || strings.EqualFold(line, "y") || strings.EqualFold(line, "yes") {
					line = suggested
				}
				input.ConfirmedDisplayName = line
			}

			task, err := deps.Service.CreateTaskWithProgress(
				context.Background(),
				input,
				core.CreateTaskOptions{OpenSession: !jsonOutput},
				func(event core.TaskProgress) {
					if strings.TrimSpace(event.Message) == "" {
						return
					}
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(), event.Message)
				},
			)
			if err != nil {
				return err
			}

			if jsonOutput {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(task)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&nonInteractive, "non-interactive", false, "accept the suggested name without prompting")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "print the created task as JSON")
	cmd.Flags().StringVar(&provider, "provider", "", "provider to use (codex, claude)")

	return cmd
}
```

- [ ] **Step 2: Update root command description**

In `internal/adapters/handler/cli/root.go`, update the short description:

```go
Short: "Manage task worktrees and tmux sessions for agent-driven work",
```

- [ ] **Step 3: Run tests**

Run: `cd /Users/ericbonet/software/tmux-llm-add-claude-code && go test ./internal/adapters/handler/cli/... -v -count=1`

Expected: All tests pass.

- [ ] **Step 4: Commit**

```bash
git add internal/adapters/handler/cli/new.go internal/adapters/handler/cli/root.go
git commit -m "feat: add --provider flag to agent new command"
```

---

### Task 6: Add provider picker to TUI

**Files:**
- Modify: `internal/adapters/handler/cli/tui_style.go:16-33`
- Modify: `internal/adapters/handler/cli/tui_model.go:23-35,62-80,246-249,283-300,392-401`

- [ ] **Step 1: Add provider icons to tui_style.go**

In `internal/adapters/handler/cli/tui_style.go`, add provider icons to the icons section:

```go
iconProviderCodex  = "⚡"
iconProviderClaude = "✦"
```

- [ ] **Step 2: Add provider state to the TUI model**

In `internal/adapters/handler/cli/tui_model.go`, add a `provider` field and a providers list to the model:

```go
var availableProviders = []string{"codex", "claude"}

type model struct {
	service            TaskService
	tasks              []*core.Task
	selected           int
	loading            bool
	busy               bool
	mode               tuiMode
	promptInput        textinput.Model
	nameInput          textinput.Model
	defaultCreationCwd string
	createInput        core.NewTaskInput
	provider           string
	err                error
}
```

In `newTUIModel`, initialize the provider:

```go
func newTUIModel(service TaskService, defaultCreationCwd string) model {
	promptInput := textinput.New()
	promptInput.Prompt = titleStyle.Render("❯") + " "
	promptInput.Placeholder = "Describe the task to create"
	promptInput.Focus()

	nameInput := textinput.New()
	nameInput.Prompt = titleStyle.Render("❯") + " "
	nameInput.Placeholder = "Confirm or edit the suggested task name"

	return model{
		service:            service,
		loading:            true,
		mode:               tuiModeList,
		promptInput:        promptInput,
		nameInput:          nameInput,
		defaultCreationCwd: emptyFallback(defaultCreationCwd, "."),
		provider:           "codex",
	}
}
```

- [ ] **Step 3: Add tab key handler for provider cycling in prompt input mode**

In `updatePromptInputKey`, add a `tab` case before the default text input handling:

```go
func (m model) updatePromptInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.mode = tuiModeList
		m.promptInput.Blur()
		return m, nil
	case tea.KeyTab:
		m.provider = nextProvider(m.provider)
		return m, nil
	case tea.KeyEnter:
		prompt := strings.TrimSpace(m.promptInput.Value())
		if prompt == "" {
			return m, nil
		}

		m.err = nil
		m.busy = true
		m.createInput.Prompt = prompt
		m.createInput.Provider = m.provider
		m.promptInput.Blur()
		return m, suggestTaskNameCmd(m.service, prompt)
	}

	var cmd tea.Cmd
	m.promptInput, cmd = m.promptInput.Update(msg)
	return m, cmd
}
```

Add the `nextProvider` helper:

```go
func nextProvider(current string) string {
	for i, p := range availableProviders {
		if p == current {
			return availableProviders[(i+1)%len(availableProviders)]
		}
	}

	return availableProviders[0]
}

func providerIcon(provider string) string {
	switch provider {
	case "claude":
		return iconProviderClaude
	default:
		return iconProviderCodex
	}
}
```

- [ ] **Step 4: Pass provider into createTask input**

In `updateNameConfirmKey`, ensure the provider is carried through:

```go
case tea.KeyEnter:
	name := strings.TrimSpace(m.nameInput.Value())
	if name == "" {
		return m, nil
	}

	m.err = nil
	m.busy = true
	m.nameInput.Blur()
	input := m.createInput
	input.ConfirmedDisplayName = name
	input.Provider = m.provider
	return m, createTaskCmd(m.service, input)
```

- [ ] **Step 5: Update promptInputView to show the provider picker**

Update `promptInputView` to display the current provider:

```go
func (m model) promptInputView() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(iconHeaderCreate+" Create Task") + "\n\n")
	b.WriteString(dimStyle.Render("Enter the task prompt. Press Enter to suggest a name, or Esc to cancel.") + "\n")
	providerLabel := providerIcon(m.provider) + " " + m.provider
	b.WriteString(dimStyle.Render("tab to switch provider: ") + primaryStyle.Render(providerLabel) + "\n\n")
	if m.err != nil {
		b.WriteString(errorStyle.Render("Error: "+m.err.Error()) + "\n\n")
	}
	b.WriteString(m.promptInput.View())
	return b.String()
}
```

- [ ] **Step 6: Also initialize provider when entering prompt mode from list view**

In `updateListKey`, in the `"n"` case, reset provider to default:

```go
case "n":
	m.err = nil
	m.mode = tuiModePromptInput
	m.createInput = core.NewTaskInput{Cwd: m.creationCwd()}
	m.promptInput.SetValue("")
	m.promptInput.Focus()
	m.nameInput.Blur()
	return m, nil
```

No change needed here — `m.provider` persists from initialization or last selection, which is the desired behavior (remembers last choice).

- [ ] **Step 7: Run tests**

Run: `cd /Users/ericbonet/software/tmux-llm-add-claude-code && go test ./internal/adapters/handler/cli/... -v -count=1`

Expected: All tests pass.

- [ ] **Step 8: Commit**

```bash
git add internal/adapters/handler/cli/
git commit -m "feat: add provider picker to TUI prompt input view"
```

---

### Task 7: Run full test suite and verify

**Files:** None (verification only)

- [ ] **Step 1: Run full test suite**

Run: `cd /Users/ericbonet/software/tmux-llm-add-claude-code && go test ./... -v -count=1`

Expected: All tests pass across all packages.

- [ ] **Step 2: Build the binary**

Run: `cd /Users/ericbonet/software/tmux-llm-add-claude-code && go build ./cmd/agent/`

Expected: Clean build, no errors.

- [ ] **Step 3: Verify --provider flag exists**

Run: `cd /Users/ericbonet/software/tmux-llm-add-claude-code && ./agent new --help`

Expected: Output includes `--provider string   provider to use (codex, claude)`.

- [ ] **Step 4: Clean up binary**

Run: `rm -f /Users/ericbonet/software/tmux-llm-add-claude-code/agent`
