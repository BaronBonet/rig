package cli

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"agent/internal/core"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestModelUpdate_JAndKChangeSelectedRow(t *testing.T) {
	m := newLoadedTUIModel(t, NewMockTaskService(t), tuiTask("task-one"), tuiTask("task-two"), tuiTask("task-three"))

	m, _ = updateTUIModel(t, m, keyRunes("j"))
	require.Equal(t, 1, m.selected)

	m, _ = updateTUIModel(t, m, keyRunes("k"))
	require.Equal(t, 0, m.selected)
}

func TestModelUpdate_GAndGJumpToBounds(t *testing.T) {
	m := newLoadedTUIModel(t, NewMockTaskService(t), tuiTask("task-one"), tuiTask("task-two"), tuiTask("task-three"))

	m, _ = updateTUIModel(t, m, keyRunes("G"))
	require.Equal(t, 2, m.selected)

	m, _ = updateTUIModel(t, m, keyRunes("g"))
	require.Equal(t, 0, m.selected)
}

func TestModelUpdate_XEntersConfirmationMode(t *testing.T) {
	m := newLoadedTUIModel(t, NewMockTaskService(t), tuiTask("task-one"))

	m, cmd := updateTUIModel(t, m, keyRunes("x"))
	require.Equal(t, tuiModeCleanupConfirm, m.mode)
	require.Nil(t, cmd)
}

func TestModelUpdate_NEntersPromptEntryMode(t *testing.T) {
	m := newLoadedTUIModel(t, NewMockTaskService(t), tuiTask("task-one"))

	m, cmd := updateTUIModel(t, m, keyRunes("n"))
	require.Equal(t, tuiModePromptInput, m.mode)
	require.True(t, m.promptInput.Focused())
	require.Nil(t, cmd)
}

func TestModelUpdate_CreateFlowSuggestsNameThenCreatesTask(t *testing.T) {
	service := NewMockTaskService(t)
	existing := tuiTask("existing-task")
	existing.RepoRoot = "/tmp/repo"
	other := tuiTask("other-task")
	other.RepoRoot = "/tmp/other-repo"
	m := newLoadedTUIModel(t, service, existing, other)

	m, _ = updateTUIModel(t, m, keyRunes("n"))
	m.promptInput.SetValue("add billing retry flow")

	service.EXPECT().
		SuggestTaskName(mock.Anything, "add billing retry flow", "codex").
		Return("billing retry flow", nil).
		Once()
	m, suggestCmd := updateTUIModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, suggestCmd)

	m.selected = 1
	suggestMsg := suggestCmd()
	m, _ = updateTUIModel(t, m, suggestMsg)
	require.Equal(t, tuiModeNameConfirm, m.mode)
	require.Equal(t, "billing retry flow", m.nameInput.Value())

	service.EXPECT().
		CreateTaskWithProgress(
			mock.Anything,
			core.NewTaskInput{
				Cwd:                  "/tmp/repo",
				Prompt:               "add billing retry flow",
				ConfirmedDisplayName: "billing retry flow",
				Provider:             "codex",
			},
			core.CreateTaskOptions{OpenSession: false},
			mock.Anything,
		).
		Return(tuiTask("billing-retry-flow"), nil).
		Once()
	service.EXPECT().
		OpenTask(mock.Anything, "billing-retry-flow").
		Return(nil).
		Once()
	service.EXPECT().
		ListTaskViews(mock.Anything).
		Return(taskViews(existing, other, tuiTask("billing-retry-flow")), nil).
		Once()
	m, createCmd := updateTUIModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, createCmd)
	require.True(t, m.busy)

	createMsg := createCmd()
	m, refreshCmd := updateTUIModel(t, m, createMsg)
	require.Equal(t, tuiModeList, m.mode)
	require.NotNil(t, refreshCmd)
	require.Contains(t, taskSlugs(m.tasks), "billing-retry-flow")

	openMsg := refreshCmd()
	m, followup := updateTUIModel(t, m, openMsg)
	require.NotNil(t, followup)
	refreshMsg := followup()
	m, _ = updateTUIModel(t, m, refreshMsg)
	require.False(t, m.busy)
}

func TestModelUpdate_CreateFlowWithoutTasksUsesModelCwdFallback(t *testing.T) {
	service := NewMockTaskService(t)
	m := newTUIModel(service, "/tmp/fallback-repo", "codex", "", nil)
	m.loading = false

	m, _ = updateTUIModel(t, m, keyRunes("n"))
	m.promptInput.SetValue("add billing retry flow")

	service.EXPECT().
		SuggestTaskName(mock.Anything, "add billing retry flow", "codex").
		Return("billing retry flow", nil).
		Once()
	m, suggestCmd := updateTUIModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, suggestCmd)

	suggestMsg := suggestCmd()
	m, _ = updateTUIModel(t, m, suggestMsg)
	require.Equal(t, tuiModeNameConfirm, m.mode)

	service.EXPECT().
		CreateTaskWithProgress(
			mock.Anything,
			core.NewTaskInput{
				Cwd:                  "/tmp/fallback-repo",
				Prompt:               "add billing retry flow",
				ConfirmedDisplayName: "billing retry flow",
				Provider:             "codex",
			},
			core.CreateTaskOptions{OpenSession: false},
			mock.Anything,
		).
		Return(tuiTask("billing-retry-flow"), nil).
		Once()
	service.EXPECT().
		OpenTask(mock.Anything, "billing-retry-flow").
		Return(nil).
		Once()
	service.EXPECT().
		ListTaskViews(mock.Anything).
		Return(taskViews(tuiTask("billing-retry-flow")), nil).
		Once()
	m, createCmd := updateTUIModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, createCmd)

	createMsg := createCmd()
	m, refreshCmd := updateTUIModel(t, m, createMsg)
	require.Equal(t, tuiModeList, m.mode)
	require.NotNil(t, refreshCmd)
	require.Contains(t, taskSlugs(m.tasks), "billing-retry-flow")

	openMsg := refreshCmd()
	m, followup := updateTUIModel(t, m, openMsg)
	require.NotNil(t, followup)
	refreshMsg := followup()
	m, _ = updateTUIModel(t, m, refreshMsg)
	require.False(t, m.busy)
}

func TestModelUpdate_CreateFlowUsesConfiguredDefaultProvider(t *testing.T) {
	service := NewMockTaskService(t)
	existing := tuiTask("existing-task")
	existing.RepoRoot = "/tmp/repo"
	m := newLoadedTUIModelWithProvider(t, service, "claude", existing)

	m, _ = updateTUIModel(t, m, keyRunes("n"))
	m.promptInput.SetValue("add billing retry flow")

	service.EXPECT().
		SuggestTaskName(mock.Anything, "add billing retry flow", "claude").
		Return("billing retry flow", nil).
		Once()
	m, suggestCmd := updateTUIModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, suggestCmd)

	suggestMsg := suggestCmd()
	m, _ = updateTUIModel(t, m, suggestMsg)
	require.Equal(t, "claude", m.provider)

	service.EXPECT().
		CreateTaskWithProgress(
			mock.Anything,
			core.NewTaskInput{
				Cwd:                  "/tmp/repo",
				Prompt:               "add billing retry flow",
				ConfirmedDisplayName: "billing retry flow",
				Provider:             "claude",
			},
			core.CreateTaskOptions{OpenSession: false},
			mock.Anything,
		).
		Return(tuiTask("billing-retry-flow"), nil).
		Once()
	service.EXPECT().
		OpenTask(mock.Anything, "billing-retry-flow").
		Return(nil).
		Once()
	service.EXPECT().
		ListTaskViews(mock.Anything).
		Return(taskViews(existing, tuiTask("billing-retry-flow")), nil).
		Once()
	m, createCmd := updateTUIModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, createCmd)

	createMsg := createCmd()
	m, followup := updateTUIModel(t, m, createMsg)
	require.NotNil(t, followup)
	openMsg := followup()
	m, refreshAfterOpen := updateTUIModel(t, m, openMsg)
	require.NotNil(t, refreshAfterOpen)
	refreshMsg := refreshAfterOpen()
	m, _ = updateTUIModel(t, m, refreshMsg)
	require.Equal(t, "claude", m.createInput.Provider)
	require.False(t, m.busy)
}

func TestModelUpdate_SuggestNameFailureReturnsToPromptModeAndRendersError(t *testing.T) {
	service := NewMockTaskService(t)
	service.EXPECT().
		SuggestTaskName(mock.Anything, "add billing retry flow", "codex").
		Return("", errors.New("suggest failed")).
		Once()
	m := newLoadedTUIModel(t, service, tuiTask("existing-task"))

	m, _ = updateTUIModel(t, m, keyRunes("n"))
	m.promptInput.SetValue("add billing retry flow")
	m, suggestCmd := updateTUIModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, suggestCmd)
	require.True(t, m.busy)

	msg := suggestCmd()
	m, followup := updateTUIModel(t, m, msg)
	require.Nil(t, followup)
	require.Equal(t, tuiModePromptInput, m.mode)
	require.False(t, m.busy)
	require.True(t, m.promptInput.Focused())
	require.Contains(t, stripANSI(m.View().Content), "suggest failed")
}

func TestModelUpdate_CreateFailureReturnsToNameConfirmModeAndRendersError(t *testing.T) {
	service := NewMockTaskService(t)
	existing := tuiTask("existing-task")
	existing.RepoRoot = "/tmp/repo"
	m := newLoadedTUIModel(t, service, existing)

	m, _ = updateTUIModel(t, m, keyRunes("n"))
	m.promptInput.SetValue("add billing retry flow")

	service.EXPECT().
		SuggestTaskName(mock.Anything, "add billing retry flow", "codex").
		Return("billing retry flow", nil).
		Once()
	m, suggestCmd := updateTUIModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	msg := suggestCmd()
	m, _ = updateTUIModel(t, m, msg)

	service.EXPECT().
		CreateTaskWithProgress(
			mock.Anything,
			core.NewTaskInput{
				Cwd:                  "/tmp/repo",
				Prompt:               "add billing retry flow",
				ConfirmedDisplayName: "billing retry flow",
				Provider:             "codex",
			},
			core.CreateTaskOptions{OpenSession: false},
			mock.Anything,
		).
		Return(nil, errors.New("create failed")).
		Once()
	m, createCmd := updateTUIModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, createCmd)
	require.True(t, m.busy)

	createMsg := createCmd()
	m, followup := updateTUIModel(t, m, createMsg)
	require.Nil(t, followup)
	require.Equal(t, tuiModeNameConfirm, m.mode)
	require.False(t, m.busy)
	require.True(t, m.nameInput.Focused())
	require.Contains(t, stripANSI(m.View().Content), "create failed")
}

func TestModelUpdate_CreateFailureWithPersistedTaskReturnsToListModeAndPreservesError(t *testing.T) {
	service := NewMockTaskService(t)
	existing := tuiTask("existing-task")
	existing.RepoRoot = "/tmp/repo"
	m := newLoadedTUIModel(t, service, existing)

	m, _ = updateTUIModel(t, m, keyRunes("n"))
	m.promptInput.SetValue("add billing retry flow")

	service.EXPECT().
		SuggestTaskName(mock.Anything, "add billing retry flow", "codex").
		Return("billing retry flow", nil).
		Once()
	m, suggestCmd := updateTUIModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	msg := suggestCmd()
	m, _ = updateTUIModel(t, m, msg)

	service.EXPECT().
		CreateTaskWithProgress(
			mock.Anything,
			core.NewTaskInput{
				Cwd:                  "/tmp/repo",
				Prompt:               "add billing retry flow",
				ConfirmedDisplayName: "billing retry flow",
				Provider:             "codex",
			},
			core.CreateTaskOptions{OpenSession: false},
			mock.Anything,
		).
		Return(tuiTask("billing-retry-flow"), errors.New("create failed after persist")).
		Once()
	m, createCmd := updateTUIModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, createCmd)
	require.True(t, m.busy)

	createMsg := createCmd()
	m, followup := updateTUIModel(t, m, createMsg)
	require.Nil(t, followup)
	require.Equal(t, tuiModeList, m.mode)
	require.False(t, m.busy)
	view := stripANSI(m.View().Content)
	require.NotContains(t, view, "Confirm Task Name")
	require.Contains(t, view, "create failed after persist")
	require.Contains(t, view, "billing-retry-flow")

	_, duplicateCmd := updateTUIModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, duplicateCmd)
}

func TestModelUpdate_PromptModeEscapeReturnsToListMode(t *testing.T) {
	m := newLoadedTUIModel(t, NewMockTaskService(t), tuiTask("existing-task"))

	m, _ = updateTUIModel(t, m, keyRunes("n"))
	m, cmd := updateTUIModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	require.Equal(t, tuiModeList, m.mode)
	require.Nil(t, cmd)
}

func TestModelUpdate_NameConfirmModeEscapeReturnsToListMode(t *testing.T) {
	service := NewMockTaskService(t)
	existing := tuiTask("existing-task")
	existing.RepoRoot = "/tmp/repo"
	m := newLoadedTUIModel(t, service, existing)

	m, _ = updateTUIModel(t, m, keyRunes("n"))
	m.promptInput.SetValue("add billing retry flow")
	service.EXPECT().
		SuggestTaskName(mock.Anything, "add billing retry flow", "codex").
		Return("billing retry flow", nil).
		Once()
	m, suggestCmd := updateTUIModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	msg := suggestCmd()
	m, _ = updateTUIModel(t, m, msg)

	m, cmd := updateTUIModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	require.Equal(t, tuiModeList, m.mode)
	require.Nil(t, cmd)
}

func TestModelUpdate_ConfirmationCancelKeysDismissWithoutQuitting(t *testing.T) {
	tests := []struct {
		name string
		key  tea.KeyMsg
	}{
		{name: "n", key: keyRunes("n")},
		{name: "escape", key: tea.KeyPressMsg{Code: tea.KeyEscape}},
		{name: "q", key: keyRunes("q")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newLoadedTUIModel(t, NewMockTaskService(t), tuiTask("task-one"))
			m, _ = updateTUIModel(t, m, keyRunes("x"))

			m, cmd := updateTUIModel(t, m, tt.key)
			require.Equal(t, tuiModeList, m.mode)
			require.Nil(t, cmd)
		})
	}
}

func TestModelUpdate_YDispatchesCleanupAndRefreshesList(t *testing.T) {
	service := NewMockTaskService(t)
	tasks := []*core.Task{tuiTask("task-one"), tuiTask("task-two")}
	m := newLoadedTUIModel(t, service, tasks...)
	m, _ = updateTUIModel(t, m, keyRunes("j"))
	m, _ = updateTUIModel(t, m, keyRunes("x"))

	service.EXPECT().
		DeleteTaskResources(mock.Anything, "task-two").
		Return(tuiTask("task-two"), nil).
		Once()
	m, cleanupCmd := updateTUIModel(t, m, keyRunes("y"))
	require.Equal(t, tuiModeList, m.mode)
	require.True(t, m.busy)
	require.NotNil(t, cleanupCmd)

	msg := cleanupCmd()
	_, ok := msg.(cleanupFinishedMsg)
	require.True(t, ok)

	service.EXPECT().
		ListTaskViews(mock.Anything).
		Return(taskViews(tasks...), nil).
		Once()
	m, refreshCmd := updateTUIModel(t, m, msg)
	require.True(t, m.busy)
	require.NotNil(t, refreshCmd)

	refreshMsg := refreshCmd()
	_, ok = refreshMsg.(tasksLoadedMsg)
	require.True(t, ok)
}

func TestModelUpdate_RTriggersRefreshCommand(t *testing.T) {
	service := NewMockTaskService(t)
	tasks := []*core.Task{tuiTask("task-one")}
	m := newLoadedTUIModel(t, service, tasks...)

	service.EXPECT().
		ListTaskViews(mock.Anything).
		Return(taskViews(tasks...), nil).
		Once()
	m, cmd := updateTUIModel(t, m, keyRunes("r"))
	require.NotNil(t, cmd)
	require.True(t, m.busy)
	require.True(t, m.loading)

	msg := cmd()
	_, ok := msg.(tasksLoadedMsg)
	require.True(t, ok)
}

func TestModelUpdate_IgnoresStaleTasksLoadedMessages(t *testing.T) {
	service := NewMockTaskService(t)
	existing := tuiTask("existing-task")
	existing.RepoRoot = "/tmp/repo"
	newTask := tuiTask("billing-retry-flow")
	newTask.RepoRoot = "/tmp/repo-billing-retry-flow"
	m := newLoadedTUIModel(t, service, existing)

	m, followup := updateTUIModel(t, m, createFinishedMsg{task: newTask})
	require.NotNil(t, followup)
	require.Contains(t, taskSlugs(m.tasks), "billing-retry-flow")

	m, cmd := updateTUIModel(t, m, tasksLoadedMsg{
		requestID: 1,
		views:     taskViews(existing),
	})
	require.Nil(t, cmd)
	require.Contains(t, taskSlugs(m.tasks), "billing-retry-flow")
}

func TestModelUpdate_ObserverTaskUpdatedDoesNotTriggerFullRefresh(t *testing.T) {
	service := NewMockTaskService(t)
	task := tuiTask("billing-retry-flow")
	m := newLoadedTUIModelWithViews(t, service, taskViewWithObserver(task, nil, &core.ObserverSummary{
		TaskID:          task.ID,
		DisplayStatus:   core.DisplayStatusWorking,
		DisplayActivity: core.DisplayActivityNone,
		ProcessAlive:    true,
	}))
	updates := make(chan core.ObserverTaskUpdate)
	m.observerUpdates = updates

	m, cmd := updateTUIModel(t, m, observerTaskUpdatedMsg{update: core.ObserverTaskUpdate{
		TaskID:          task.ID,
		DisplayStatus:   core.DisplayStatusNeedsInput,
		DisplayActivity: core.DisplayActivityCommand,
		LastActivityAt:  time.Date(2026, 4, 9, 17, 30, 0, 0, time.UTC),
	}})
	require.NotNil(t, cmd)
	require.Equal(t, core.DisplayStatusNeedsInput, m.selectedTaskView().Observer.DisplayStatus)
	require.Equal(t, core.DisplayActivityCommand, m.selectedTaskView().Observer.DisplayActivity)
	require.False(t, m.loading)
}

func TestModelUpdate_EnterDispatchesOpenAndKeepsTUIOpen(t *testing.T) {
	service := NewMockTaskService(t)
	tasks := []*core.Task{tuiTask("task-one"), tuiTask("task-two")}
	m := newLoadedTUIModel(t, service, tasks...)
	m, _ = updateTUIModel(t, m, keyRunes("j"))

	service.EXPECT().
		OpenTask(mock.Anything, "task-two").
		Return(nil).
		Once()
	service.EXPECT().
		ListTaskViews(mock.Anything).
		Return(taskViews(tasks...), nil).
		Once()
	m, cmd := updateTUIModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	require.True(t, m.busy)

	msg := cmd()
	m, cmd = updateTUIModel(t, m, msg)
	require.NotNil(t, cmd)

	msg = cmd()
	m, cmd = updateTUIModel(t, m, msg)
	require.Nil(t, cmd)
	require.False(t, m.busy)
}

func TestModelUpdate_EnterFailureRendersInlineErrorAndKeepsTUIOpen(t *testing.T) {
	service := NewMockTaskService(t)
	tasks := []*core.Task{tuiTask("task-one"), tuiTask("task-two")}
	m := newLoadedTUIModel(t, service, tasks...)
	m, _ = updateTUIModel(t, m, keyRunes("j"))

	service.EXPECT().
		OpenTask(mock.Anything, "task-two").
		Return(errors.New("open failed")).
		Once()
	m, cmd := updateTUIModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	require.True(t, m.busy)

	msg := cmd()
	m, followup := updateTUIModel(t, m, msg)
	require.Nil(t, followup)
	require.False(t, m.busy)
	require.Contains(t, stripANSI(m.View().Content), "open failed")

	m, _ = updateTUIModel(t, m, keyRunes("k"))
	require.Equal(t, 0, m.selected)
}

func TestModelUpdate_QQuitsFromNormalMode(t *testing.T) {
	m := newLoadedTUIModel(t, NewMockTaskService(t), tuiTask("task-one"))

	_, cmd := updateTUIModel(t, m, keyRunes("q"))
	require.NotNil(t, cmd)

	msg := cmd()
	_, ok := msg.(tea.QuitMsg)
	require.True(t, ok)
}

func TestModelUpdate_BusyStateBlocksOverlappingActions(t *testing.T) {
	service := NewMockTaskService(t)
	tasks := []*core.Task{tuiTask("task-one"), tuiTask("task-two")}
	m := newLoadedTUIModel(t, service, tasks...)
	m, _ = updateTUIModel(t, m, keyRunes("x"))
	m, cleanupCmd := updateTUIModel(t, m, keyRunes("y"))
	require.NotNil(t, cleanupCmd)
	require.True(t, m.busy)

	selected := m.selected
	m, cmd := updateTUIModel(t, m, keyRunes("j"))
	require.Equal(t, selected, m.selected)
	require.Nil(t, cmd)

	m, cmd = updateTUIModel(t, m, keyRunes("r"))
	require.Nil(t, cmd)
}

func TestModelUpdate_RefreshInFlightBlocksOverlappingActions(t *testing.T) {
	service := NewMockTaskService(t)
	tasks := []*core.Task{tuiTask("task-one"), tuiTask("task-two")}
	m := newLoadedTUIModel(t, service, tasks...)
	m, refreshCmd := updateTUIModel(t, m, keyRunes("r"))
	require.NotNil(t, refreshCmd)
	require.True(t, m.busy)

	selected := m.selected
	m, cmd := updateTUIModel(t, m, keyRunes("j"))
	require.Equal(t, selected, m.selected)
	require.Nil(t, cmd)

	m, cmd = updateTUIModel(t, m, keyRunes("x"))
	require.Equal(t, tuiModeList, m.mode)
	require.Nil(t, cmd)
}

func TestModelUpdate_MainListViewRendersControlCenterDetails(t *testing.T) {
	service := NewMockTaskService(t)
	task := tuiTask("billing-retry-flow")
	task.DisplayName = "billing retry flow"
	task.Status = core.TaskStatusRunning
	task.RepoName = "tmux-llm"
	task.SessionExists = true
	task.AgentWindowExists = true
	task.EditorWindowExists = false
	task.WorktreeExists = false
	task.BranchName = "feat/billing-retry-flow"

	m := newLoadedTUIModel(t, service, task)
	view := stripANSI(m.View().Content)

	require.Contains(t, view, "Control Center")
	require.Contains(t, view, "TASK")
	require.Contains(t, view, "PROVIDER")
	require.Contains(t, view, "STATUS")
	require.Contains(t, view, "billing retry flow")
	require.Contains(t, view, "running")
}

func TestModelView_PrefersRuntimeBadgesOnSeparateTaskRows(t *testing.T) {
	running := tuiTask("task-running")
	running.DisplayName = "running task"
	running.Status = core.TaskStatusDegraded
	running.RuntimeState = core.RuntimeStateRunning
	running.BranchName = "branch-running"

	needsInput := tuiTask("task-needs-input")
	needsInput.DisplayName = "needs input task"
	needsInput.Status = core.TaskStatusDegraded
	needsInput.RuntimeState = core.RuntimeStateNeedsInput
	needsInput.BranchName = "branch-needs-input"

	finished := tuiTask("task-finished")
	finished.DisplayName = "finished task"
	finished.Status = core.TaskStatusDegraded
	finished.RuntimeState = core.RuntimeStateFinished
	finished.BranchName = "branch-finished"

	m := newLoadedTUIModel(t, NewMockTaskService(t), running, needsInput, finished)
	view := stripANSI(m.View().Content)

	require.Contains(t, view, "running task")
	require.Contains(t, view, "needs input task")
	require.Contains(t, view, "finished task")

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

	requireLineContains("running task", "● running")
	requireLineContains("needs input task", "◐ needs input")
	requireLineContains("finished task", "○ finished")
	requireLineContains("running task", "running task")
	requireLineContains("needs input task", "needs input task")
	requireLineContains("finished task", "finished task")

	for _, row := range rows {
		if strings.Contains(row, "running task") {
			require.NotContains(t, row, "degraded")
		}
		if strings.Contains(row, "needs input task") {
			require.NotContains(t, row, "degraded")
		}
		if strings.Contains(row, "finished task") {
			require.NotContains(t, row, "degraded")
		}
	}
}

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

	m := newLoadedTUIModel(t, NewMockTaskService(t), codexTask, claudeTask)
	view := stripANSI(m.View().Content)
	rows := strings.Split(view, "\n")

	requireLineOrdered := func(name, first, second string) {
		t.Helper()
		for _, row := range rows {
			if strings.Contains(row, name) {
				firstIndex := strings.Index(row, first)
				secondIndex := strings.Index(row, second)
				require.NotEqual(t, -1, firstIndex, "row %q missing %q", row, first)
				require.NotEqual(t, -1, secondIndex, "row %q missing %q", row, second)
				require.Less(t, firstIndex, secondIndex, "row %q has %q after %q", row, first, second)
				return
			}
		}
		t.Fatalf("did not find row for %q in view:\n%s", name, view)
	}

	requireLineOrdered("codex task", "⚡ codex", "● running")
	requireLineOrdered("claude task", "✦ claude", "◐ needs input")
}

func TestModelView_ProviderBadgeCoexistsWithRuntimeBadge(t *testing.T) {
	task := tuiTask("task-running")
	task.DisplayName = "running task"
	task.Provider = "claude"
	task.Status = core.TaskStatusDegraded
	task.RuntimeState = core.RuntimeStateFinished

	m := newLoadedTUIModel(t, NewMockTaskService(t), task)
	view := stripANSI(m.View().Content)
	for _, row := range strings.Split(view, "\n") {
		if !strings.Contains(row, "running task") {
			continue
		}

		require.Contains(t, row, "✦ claude")
		require.Contains(t, row, "○ finished")
		return
	}

	t.Fatalf("did not find row for %q in view:\n%s", "running task", view)
}

func TestModelView_TaskRowsUseObserverStatusAndHookPreview(t *testing.T) {
	service := NewMockTaskService(t)
	task := tuiTask("billing-retry-flow")
	task.DisplayName = "billing retry flow"

	service.EXPECT().
		GetTaskHookEvents(mock.Anything, task.ID, 5).
		Return(nil, nil).
		Once()
	m := newLoadedTUIModelWithViews(t, service, taskViewWithObserver(task, &core.HookSessionSummary{
		TaskID:          task.ID,
		RuntimePhase:    core.HookRuntimePhaseRunningCommand,
		LastCommandText: "go test ./internal/adapters/handler/cli -count=1",
	}, &core.ObserverSummary{
		TaskID:          task.ID,
		DisplayStatus:   core.DisplayStatusWorking,
		DisplayActivity: core.DisplayActivityCommand,
		ProcessAlive:    true,
	}))
	view := stripANSI(m.View().Content)
	rows := strings.Split(view, "\n")

	for _, row := range rows {
		if !strings.Contains(row, "billing retry flow") {
			continue
		}

		require.Contains(t, row, "working · command")
		require.Contains(t, row, "go test ./internal/adapters/handler/cli -count=1")
		require.NotContains(t, row, "◐ degraded")
		return
	}

	t.Fatalf("did not find row for %q in view:\n%s", "billing retry flow", view)
}

func TestModelView_SelectedTaskDetailShowsHookMetadataAndRecentEvents(t *testing.T) {
	service := NewMockTaskService(t)
	task := tuiTask("billing-retry-flow")
	task.DisplayName = "billing retry flow"
	task.Provider = "codex"
	task.RepoName = "tmux-llm-v1-refactor-brainstorm"
	task.RepoRoot = "/tmp/repo"
	task.WorktreePath = "/tmp/repo/.branches/billing-retry-flow"
	task.TmuxSession = "repo-billing-retry-flow"
	task.BranchName = "feat/billing-retry-flow"
	summary := &core.HookSessionSummary{
		TaskID:               task.ID,
		SessionID:            "sess-123",
		Model:                "gpt-5",
		Cwd:                  "/tmp/repo",
		TranscriptPath:       "/tmp/repo/transcripts/sess-123.jsonl",
		StartSource:          "UserPromptSubmit",
		LastEventName:        "PostToolUse",
		RuntimePhase:         core.HookRuntimePhaseRunningCommand,
		LastCommandText:      "go test ./internal/adapters/handler/cli -count=1",
		LastActivityAt:       time.Date(2026, 4, 8, 10, 3, 0, 0, time.UTC),
		LastAssistantMessage: "working on cli hook detail view",
	}
	observerSummary := &core.ObserverSummary{
		TaskID:                task.ID,
		DisplayStatus:         core.DisplayStatusWorking,
		DisplayActivity:       core.DisplayActivityCommand,
		ProcessAlive:          true,
		LastRuntimeObservedAt: time.Date(2026, 4, 8, 10, 3, 0, 0, time.UTC),
	}

	service.EXPECT().
		GetTaskHookEvents(mock.Anything, task.ID, 5).
		Return([]core.HookEvent{
			{
				TaskID:      task.ID,
				EventName:   "PostToolUse",
				CommandText: "go test ./internal/adapters/handler/cli -count=1",
				OccurredAt:  time.Date(2026, 4, 8, 10, 2, 0, 0, time.UTC),
			},
			{
				TaskID:               task.ID,
				EventName:            "Stop",
				CommandResultText:    "PASS",
				LastAssistantMessage: "tests complete",
				OccurredAt:           time.Date(2026, 4, 8, 10, 3, 0, 0, time.UTC),
			},
		}, nil).
		Once()
	m := newLoadedTUIModelWithViews(t, service, taskViewWithObserver(task, summary, observerSummary))

	view := stripANSI(m.View().Content)
	require.Contains(t, view, "Selected Task")
	require.Contains(t, view, "Session Activity")
	require.Contains(t, view, "Status: working · command")
	require.Contains(t, view, "Session ID: sess-123")
	require.Contains(t, view, "Model: gpt-5")
	require.Contains(t, view, "Process: connected")
	require.Contains(t, view, "Last Activity: 2026-04-08 12:03:00")
	require.Contains(t, view, "Recent Hook Events")
	require.Contains(t, view, "PostToolUse")
	require.Contains(t, view, "go test ./internal/adapters/handler/cli -count=1")
	require.Contains(t, view, "Stop")
	require.Contains(t, view, "PASS")
}

func TestModelView_SelectedTaskDetailShowsFallbackWhenHookDataMissing(t *testing.T) {
	service := NewMockTaskService(t)
	task := tuiTask("billing-retry-flow")
	task.DisplayName = "billing retry flow"
	task.Provider = "claude"
	task.RepoName = "tmux-llm-v1-refactor-brainstorm"
	task.RepoRoot = "/tmp/repo"
	task.WorktreePath = "/tmp/repo/.branches/billing-retry-flow"
	task.TmuxSession = "repo-billing-retry-flow"
	task.BranchName = "feat/billing-retry-flow"
	task.Status = core.TaskStatusDegraded

	m := newLoadedTUIModel(t, service, task)
	view := stripANSI(m.View().Content)

	require.Contains(t, view, "Selected Task")
	require.Contains(t, view, "Session Activity")
	require.Contains(t, view, "No hook activity has been recorded for this task yet.")
	require.Contains(t, view, "Provider: claude")
	require.Contains(t, view, "Branch: feat/billing-retry-flow")
	require.Contains(t, view, "Repo: tmux-llm-v1-refactor-brainstorm")
	require.Contains(t, view, "Status: degraded")
}

func TestTaskStateText_PrefersNeedsInputOverHookActivity(t *testing.T) {
	view := taskViewWithObserver(
		tuiTask("billing-retry-flow"),
		&core.HookSessionSummary{LastCommandText: "go test ./..."},
		&core.ObserverSummary{
			DisplayStatus:   core.DisplayStatusNeedsInput,
			DisplayActivity: core.DisplayActivityCommand,
			ProcessAlive:    true,
		},
	)

	text, _ := taskStateText(view)
	require.Equal(t, "◐ needs input", text)
}

func TestTaskStateText_ShowsWorkingCommandForActiveCommand(t *testing.T) {
	view := taskViewWithObserver(
		tuiTask("billing-retry-flow"),
		nil,
		&core.ObserverSummary{
			DisplayStatus:   core.DisplayStatusWorking,
			DisplayActivity: core.DisplayActivityCommand,
			ProcessAlive:    true,
		},
	)

	text, _ := taskStateText(view)
	require.Equal(t, "◐ working · command", text)
}

func TestTaskStateText_ShowsDisconnectedWhenProcessMissing(t *testing.T) {
	view := taskViewWithObserver(
		tuiTask("billing-retry-flow"),
		nil,
		&core.ObserverSummary{
			DisplayStatus: core.DisplayStatusDisconnected,
			ProcessAlive:  false,
		},
	)

	text, _ := taskStateText(view)
	require.Equal(t, "○ disconnected", text)
}

func TestModelView_OmitsRuntimeBadgeWhenRuntimeStateIsEmpty(t *testing.T) {
	task := tuiTask("billing-retry-flow")
	task.Status = core.TaskStatusDegraded
	task.RuntimeState = core.RuntimeStateNone

	m := newLoadedTUIModel(t, NewMockTaskService(t), task)
	view := stripANSI(m.View().Content)

	require.Contains(t, view, "◐ degraded")
	require.NotContains(t, view, "● running")
	require.NotContains(t, view, "◐ needs input")
	require.NotContains(t, view, "○ finished")
}

func TestModelUpdate_LoadedTasksHideTasksWithoutLiveResources(t *testing.T) {
	active := tuiTask("active-task")
	hidden := tuiTask("cleaned-task")
	hidden.Status = core.TaskStatusCleaned
	hidden.SessionExists = false
	hidden.WorktreeExists = false

	m := newLoadedTUIModel(t, NewMockTaskService(t), active, hidden)
	view := stripANSI(m.View().Content)

	require.Contains(t, view, "active-task")
	require.NotContains(t, view, "cleaned-task")
}

func TestModelUpdate_ConfirmationViewExplainsDeletionScope(t *testing.T) {
	m := newLoadedTUIModel(t, NewMockTaskService(t), tuiTask("billing-retry-flow"))
	m, _ = updateTUIModel(t, m, keyRunes("x"))

	view := stripANSI(m.View().Content)
	require.Contains(t, view, "tmux session and worktree will be deleted")
	require.Contains(t, view, "branch will be kept")
}

func TestModelUpdate_CleanupFailureRendersInlineErrorAndKeepsTUIUsable(t *testing.T) {
	service := NewMockTaskService(t)
	tasks := []*core.Task{tuiTask("task-one"), tuiTask("task-two")}
	m := newLoadedTUIModel(t, service, tasks...)
	m, _ = updateTUIModel(t, m, keyRunes("x"))

	service.EXPECT().
		DeleteTaskResources(mock.Anything, "task-one").
		Return(nil, errors.New("cleanup failed")).
		Once()
	_, cleanupCmd := updateTUIModel(t, m, keyRunes("y"))
	require.NotNil(t, cleanupCmd)

	msg := cleanupCmd()
	m, _ = updateTUIModel(t, m, msg)
	require.Contains(t, stripANSI(m.View().Content), "cleanup failed")

	m, _ = updateTUIModel(t, m, keyRunes("j"))
	require.Equal(t, 1, m.selected)
	require.Equal(t, tuiModeList, m.mode)
}

func TestModelUpdate_CleanupSuccessRefreshFailureRemovesTaskFromVisibleList(t *testing.T) {
	service := NewMockTaskService(t)
	cleaned := tuiTask("task-one")
	cleaned.Status = core.TaskStatusCleaned
	cleaned.SessionExists = false
	cleaned.WorktreeExists = false
	tasks := []*core.Task{tuiTask("task-one")}
	m := newLoadedTUIModel(t, service, tasks...)
	m, _ = updateTUIModel(t, m, keyRunes("x"))

	service.EXPECT().
		DeleteTaskResources(mock.Anything, "task-one").
		Return(cleaned, nil).
		Once()
	m, cleanupCmd := updateTUIModel(t, m, keyRunes("y"))
	msg := cleanupCmd()

	service.EXPECT().
		ListTaskViews(mock.Anything).
		Return(nil, errors.New("refresh failed")).
		Once()
	m, refreshCmd := updateTUIModel(t, m, msg)
	require.NotNil(t, refreshCmd)

	refreshMsg := refreshCmd()
	m, _ = updateTUIModel(t, m, refreshMsg)

	view := stripANSI(m.View().Content)
	require.NotContains(t, view, "task-one")
	require.Contains(t, view, "refresh failed")
	require.False(t, m.busy)
}

func TestModelView_ShowsLoadingBeforeInitialLoadCompletes(t *testing.T) {
	m := newTUIModel(NewMockTaskService(t), "/tmp/default", "codex", "", nil)
	require.Contains(t, stripANSI(m.View().Content), "Loading tasks")
}

func newLoadedTUIModel(t *testing.T, service *MockTaskService, tasks ...*core.Task) model {
	return newLoadedTUIModelWithProvider(t, service, "codex", tasks...)
}

func newLoadedTUIModelWithProvider(t *testing.T, service *MockTaskService, provider string, tasks ...*core.Task) model {
	t.Helper()

	return newLoadedTUIModelWithProviderAndViews(t, service, provider, taskViews(tasks...)...)
}

func newLoadedTUIModelWithViews(t *testing.T, service *MockTaskService, views ...*core.TaskView) model {
	t.Helper()

	return newLoadedTUIModelWithProviderAndViews(t, service, "codex", views...)
}

func newLoadedTUIModelWithProviderAndViews(
	t *testing.T,
	service *MockTaskService,
	provider string,
	views ...*core.TaskView,
) model {
	t.Helper()

	next, cmd := newTUIModel(service, "/tmp/default", provider, "", nil).Update(tasksLoadedMsg{requestID: 1, views: views})
	m, ok := next.(model)
	require.True(t, ok)
	if cmd == nil {
		return m
	}

	next, followup := m.Update(cmd())
	require.Nil(t, followup)
	m, ok = next.(model)
	require.True(t, ok)
	return m
}

func updateTUIModel(t *testing.T, current model, msg tea.Msg) (model, tea.Cmd) {
	t.Helper()

	next, cmd := current.Update(msg)
	m, ok := next.(model)
	require.True(t, ok)
	return m, cmd
}

func keyRunes(chars string) tea.KeyPressMsg {
	r := []rune(chars)
	return tea.KeyPressMsg{Code: r[0], Text: chars}
}

// stripANSI removes ANSI escape sequences so view assertions can match plain text.
var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string {
	return ansiPattern.ReplaceAllString(s, "")
}

func TestListViewShowsInitialError(t *testing.T) {
	m := newTUIModel(NewMockTaskService(t), "/tmp/default", "codex", "", errors.New("observer unavailable"))
	m.loading = false

	view := stripANSI(m.listView())

	require.Contains(t, view, "observer unavailable")
}

func tuiTask(slug string) *core.Task {
	return &core.Task{
		ID:             slug + "-id",
		Slug:           slug,
		DisplayName:    slug,
		Status:         core.TaskStatusRunning,
		BranchName:     "feat/" + slug,
		TmuxSession:    "repo-" + slug,
		SessionExists:  true,
		WorktreeExists: true,
	}
}

func taskViews(tasks ...*core.Task) []*core.TaskView {
	views := make([]*core.TaskView, 0, len(tasks))
	for _, task := range tasks {
		views = append(views, taskView(task, nil))
	}

	return views
}

func taskView(task *core.Task, hook *core.HookSessionSummary) *core.TaskView {
	return &core.TaskView{
		Task:        task,
		HookSession: hook,
	}
}

func taskViewWithObserver(
	task *core.Task,
	hook *core.HookSessionSummary,
	observer *core.ObserverSummary,
) *core.TaskView {
	return &core.TaskView{
		Task:        task,
		HookSession: hook,
		Observer:    observer,
	}
}

func taskSlugs(tasks []*core.Task) []string {
	slugs := make([]string, 0, len(tasks))
	for _, task := range tasks {
		if task != nil {
			slugs = append(slugs, task.Slug)
		}
	}

	return slugs
}
