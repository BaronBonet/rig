# TUI Provider Badge Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make every visible TUI task row show its provider badge (`codex` or `claude`) at all times, while preserving the existing structural status and optional runtime badge rendering.

**Architecture:** Keep the change inside the TUI presentation layer. First extend the row-rendering tests to assert provider badges on rows with and without runtime state, then update the list row formatter in `internal/adapters/handler/cli/tui_model.go` to render the provider badge before the structural status badge on every row.

**Tech Stack:** Go, Bubble Tea, Lip Gloss, Testify

---

### Task 1: Add Failing TUI Coverage For Provider Badges

**Files:**
- Modify: `internal/adapters/handler/cli/tui_model_test.go`
- Test: `internal/adapters/handler/cli/tui_model_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestModelView_ShowsProviderBadgeOnEveryTaskRow(t *testing.T) {
	codexTask := tuiTask("task-codex")
	codexTask.DisplayName = "codex task"
	codexTask.Provider = "codex"
	codexTask.Status = core.TaskStatusRunning
	codexTask.RuntimeState = core.RuntimeStateNone

	claudeTask := tuiTask("task-claude")
	claudeTask.DisplayName = "claude task"
	claudeTask.Provider = "claude"
	claudeTask.Status = core.TaskStatusDegraded
	claudeTask.RuntimeState = core.RuntimeStateNeedsInput

	m := newLoadedTUIModel(t, &fakeTUIService{}, codexTask, claudeTask)
	view := stripANSI(m.View())
	rows := strings.Split(view, "\n")

	requireLineContains := func(name, want string) {
		t.Helper()
		for _, row := range rows {
			if strings.Contains(row, name) {
				require.Contains(t, row, want)
				return
			}
		}
		t.Fatalf("did not find row for %q in view:\n%s", name, view)
	}

	requireLineContains("codex task", "⚡ codex")
	requireLineContains("codex task", "● running")
	requireLineContains("claude task", "✦ claude")
	requireLineContains("claude task", "◐ degraded")
	requireLineContains("claude task", "◐ needs input")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/handler/cli -run TestModelView_ShowsProviderBadgeOnEveryTaskRow -count=1`
Expected: FAIL because the current row formatter does not include `⚡ codex` or `✦ claude` in task rows.

- [ ] **Step 3: Write minimal implementation**

```go
for i, task := range m.tasks {
	icon, style := statusStyle(string(task.Status))
	provider := dimStyle.Render(providerIcon(task.Provider) + " " + emptyFallback(task.Provider, "codex"))
	status := style.Render(icon + " " + string(task.Status))
	runtime := ""
	if task.RuntimeState != core.RuntimeStateNone {
		runtimeIcon, runtimeStyle := runtimeStateStyle(string(task.RuntimeState))
		runtime = "  " + runtimeStyle.Render(runtimeIcon+" "+strings.ReplaceAll(string(task.RuntimeState), "_", " "))
	}

	if i == m.selected {
		name := iconSelected + " " + task.DisplayName
		row := fmt.Sprintf("%-40s %s  %s%s", name, provider, status, runtime)
		b.WriteString(selectedRowStyle.Render(row) + "\n")
	} else {
		name := "  " + task.DisplayName
		row := fmt.Sprintf("%-40s %s  %s%s", name, provider, status, runtime)
		b.WriteString(normalRowStyle.Render(row) + "\n")
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/handler/cli -run TestModelView_ShowsProviderBadgeOnEveryTaskRow -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/handler/cli/tui_model.go internal/adapters/handler/cli/tui_model_test.go
git commit -m "feat: show provider badges in tui rows"
```

### Task 2: Protect Existing Runtime Badge Rendering

**Files:**
- Modify: `internal/adapters/handler/cli/tui_model_test.go`
- Test: `internal/adapters/handler/cli/tui_model_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestModelView_ProviderBadgeCoexistsWithRuntimeBadge(t *testing.T) {
	task := tuiTask("task-running")
	task.DisplayName = "running task"
	task.Provider = "claude"
	task.Status = core.TaskStatusDegraded
	task.RuntimeState = core.RuntimeStateFinished

	m := newLoadedTUIModel(t, &fakeTUIService{}, task)
	view := stripANSI(m.View())

	require.Contains(t, view, "running task")
	require.Contains(t, view, "✦ claude")
	require.Contains(t, view, "◐ degraded")
	require.Contains(t, view, "○ finished")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapters/handler/cli -run TestModelView_ProviderBadgeCoexistsWithRuntimeBadge -count=1`
Expected: FAIL because the current row formatter does not render provider badges alongside runtime badges.

- [ ] **Step 3: Write minimal implementation**

```go
provider := dimStyle.Render(providerIcon(task.Provider) + " " + emptyFallback(task.Provider, "codex"))
row := fmt.Sprintf("%-40s %s  %s%s", name, provider, status, runtime)
```

Keep the existing runtime badge branch unchanged so provider is additive and does not replace the runtime badge.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapters/handler/cli -run TestModelView_ProviderBadgeCoexistsWithRuntimeBadge -count=1`
Expected: PASS

- [ ] **Step 5: Run broader TUI verification**

Run: `go test ./internal/adapters/handler/cli -count=1`
Expected: PASS with all CLI TUI tests green.

- [ ] **Step 6: Commit**

```bash
git add internal/adapters/handler/cli/tui_model.go internal/adapters/handler/cli/tui_model_test.go
git commit -m "test: cover tui provider row badges"
```
