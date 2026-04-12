package cli

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"rig/internal/core"

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
		Return(core.TaskSuggestion{Name: "billing retry flow", BranchType: "feat"}, nil).
		Once()
	m, suggestCmd := updateTUIModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, suggestCmd)

	m.selected = 1
	suggestMsg := executeBatchUntil[suggestNameFinishedMsg](t, suggestCmd)
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
				ConfirmedBranchType:  "feat",
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

	createMsg := executeBatchUntil[createFinishedMsg](t, createCmd)
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
		Return(core.TaskSuggestion{Name: "billing retry flow", BranchType: "feat"}, nil).
		Once()
	m, suggestCmd := updateTUIModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, suggestCmd)

	suggestMsg := executeBatchUntil[suggestNameFinishedMsg](t, suggestCmd)
	m, _ = updateTUIModel(t, m, suggestMsg)
	require.Equal(t, tuiModeNameConfirm, m.mode)

	service.EXPECT().
		CreateTaskWithProgress(
			mock.Anything,
			core.NewTaskInput{
				Cwd:                  "/tmp/fallback-repo",
				Prompt:               "add billing retry flow",
				ConfirmedDisplayName: "billing retry flow",
				ConfirmedBranchType:  "feat",
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

	createMsg := executeBatchUntil[createFinishedMsg](t, createCmd)
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
		Return(core.TaskSuggestion{Name: "billing retry flow", BranchType: "feat"}, nil).
		Once()
	m, suggestCmd := updateTUIModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, suggestCmd)

	suggestMsg := executeBatchUntil[suggestNameFinishedMsg](t, suggestCmd)
	m, _ = updateTUIModel(t, m, suggestMsg)
	require.Equal(t, "claude", m.provider)

	service.EXPECT().
		CreateTaskWithProgress(
			mock.Anything,
			core.NewTaskInput{
				Cwd:                  "/tmp/repo",
				Prompt:               "add billing retry flow",
				ConfirmedDisplayName: "billing retry flow",
				ConfirmedBranchType:  "feat",
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

	createMsg := executeBatchUntil[createFinishedMsg](t, createCmd)
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
		Return(core.TaskSuggestion{}, errors.New("suggest failed")).
		Once()
	m := newLoadedTUIModel(t, service, tuiTask("existing-task"))

	m, _ = updateTUIModel(t, m, keyRunes("n"))
	m.promptInput.SetValue("add billing retry flow")
	m, suggestCmd := updateTUIModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, suggestCmd)
	require.True(t, m.busy)

	msg := executeBatchUntil[suggestNameFinishedMsg](t, suggestCmd)
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
		Return(core.TaskSuggestion{Name: "billing retry flow", BranchType: "feat"}, nil).
		Once()
	m, suggestCmd := updateTUIModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	msg := executeBatchUntil[suggestNameFinishedMsg](t, suggestCmd)
	m, _ = updateTUIModel(t, m, msg)

	service.EXPECT().
		CreateTaskWithProgress(
			mock.Anything,
			core.NewTaskInput{
				Cwd:                  "/tmp/repo",
				Prompt:               "add billing retry flow",
				ConfirmedDisplayName: "billing retry flow",
				ConfirmedBranchType:  "feat",
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

	createMsg := executeBatchUntil[createFinishedMsg](t, createCmd)
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
		Return(core.TaskSuggestion{Name: "billing retry flow", BranchType: "feat"}, nil).
		Once()
	m, suggestCmd := updateTUIModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	msg := executeBatchUntil[suggestNameFinishedMsg](t, suggestCmd)
	m, _ = updateTUIModel(t, m, msg)

	service.EXPECT().
		CreateTaskWithProgress(
			mock.Anything,
			core.NewTaskInput{
				Cwd:                  "/tmp/repo",
				Prompt:               "add billing retry flow",
				ConfirmedDisplayName: "billing retry flow",
				ConfirmedBranchType:  "feat",
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

	createMsg := executeBatchUntil[createFinishedMsg](t, createCmd)
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
		Return(core.TaskSuggestion{Name: "billing retry flow", BranchType: "feat"}, nil).
		Once()
	m, suggestCmd := updateTUIModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	msg := executeBatchUntil[suggestNameFinishedMsg](t, suggestCmd)
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

	service.EXPECT().InvalidatePRCache().Once()
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

func TestModelUpdate_HookTaskUpdatedDoesNotTriggerFullRefresh(t *testing.T) {
	service := NewMockTaskService(t)
	task := tuiTask("billing-retry-flow")
	m := newLoadedTUIModelWithViews(t, service, taskView(task, nil))
	updates := make(chan core.HookSessionSummary)
	m.hookUpdates = updates

	m, cmd := updateTUIModel(t, m, hookTaskUpdatedMsg{update: core.HookSessionSummary{
		TaskID:               task.ID,
		RuntimePhase:         core.HookRuntimePhaseIdle,
		LastPromptText:       "fix the billing retry flow",
		LastAssistantMessage: "Updated the retry loop and tests",
	}})
	require.NotNil(t, cmd)
	require.Equal(t, "fix the billing retry flow", m.selectedTaskView().HookSession.LastPromptText)
	require.Equal(t, "Updated the retry loop and tests", m.selectedTaskView().HookSession.LastAssistantMessage)
	require.False(t, m.loading)
}

func TestModelUpdate_HookTaskUpdated_UserPromptSubmitReplacesPriorTurnPreview(t *testing.T) {
	service := NewMockTaskService(t)
	task := tuiTask("billing-retry-flow")
	m := newLoadedTUIModelWithViews(t, service, taskView(task, &core.HookSessionSummary{
		TaskID:                task.ID,
		LastEventName:         "Stop",
		LastPromptText:        "first prompt",
		LastAssistantMessage:  "first answer",
		LastCommandText:       "go test ./...",
		LastCommandResultText: "PASS",
	}))
	updates := make(chan core.HookSessionSummary)
	m.hookUpdates = updates

	m, cmd := updateTUIModel(t, m, hookTaskUpdatedMsg{update: core.HookSessionSummary{
		TaskID:         task.ID,
		LastEventName:  "UserPromptSubmit",
		RuntimePhase:   core.HookRuntimePhasePrompted,
		LastPromptText: "second prompt",
	}})

	require.NotNil(t, cmd)
	require.Equal(t, "second prompt", m.selectedTaskView().HookSession.LastPromptText)
	require.Equal(t, "", m.selectedTaskView().HookSession.LastAssistantMessage)
	require.Equal(t, "", m.selectedTaskView().HookSession.LastCommandText)
	require.Equal(t, "", m.selectedTaskView().HookSession.LastCommandResultText)
}

func TestModelUpdate_ObserverTaskUpdatedPreservesHookSession(t *testing.T) {
	service := NewMockTaskService(t)
	task := tuiTask("billing-retry-flow")
	m := newLoadedTUIModelWithViews(t, service, taskView(task, &core.HookSessionSummary{
		TaskID:               task.ID,
		LastPromptText:       "fix the billing retry flow",
		LastAssistantMessage: "Updated the retry loop and tests",
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
	require.Equal(t, "fix the billing retry flow", m.selectedTaskView().HookSession.LastPromptText)
	require.Equal(t, "Updated the retry loop and tests", m.selectedTaskView().HookSession.LastAssistantMessage)
	require.Equal(t, core.DisplayStatusNeedsInput, m.selectedTaskView().Observer.DisplayStatus)
}

func TestModelUpdate_ObserverTaskUpdatedAppliesHookSessionFromSocket(t *testing.T) {
	service := NewMockTaskService(t)
	task := tuiTask("billing-retry-flow")
	m := newLoadedTUIModelWithViews(t, service, taskViewWithObserver(task, nil, &core.ObserverSummary{
		TaskID:        task.ID,
		DisplayStatus: core.DisplayStatusWorking,
		ProcessAlive:  true,
	}))
	updates := make(chan core.ObserverTaskUpdate)
	m.observerUpdates = updates

	m, cmd := updateTUIModel(t, m, observerTaskUpdatedMsg{update: core.ObserverTaskUpdate{
		TaskID:          task.ID,
		DisplayStatus:   core.DisplayStatusWorking,
		DisplayActivity: core.DisplayActivityCommand,
		LastActivityAt:  time.Date(2026, 4, 9, 17, 30, 0, 0, time.UTC),
		HookSession: &core.HookSessionSummary{
			TaskID:               task.ID,
			LastEventName:        "Stop",
			LastPromptText:       "fix the billing retry flow",
			LastAssistantMessage: "Updated the retry loop and tests",
		},
	}})
	require.NotNil(t, cmd)
	require.Equal(t, "fix the billing retry flow", m.selectedTaskView().HookSession.LastPromptText)
	require.Equal(t, "Updated the retry loop and tests", m.selectedTaskView().HookSession.LastAssistantMessage)
	require.Equal(t, "Stop", m.selectedTaskView().HookSession.LastEventName)
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
	m, _ = updateTUIModel(t, m, msg)
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
	service.EXPECT().InvalidatePRCache().Once()
	service.EXPECT().ListTaskViews(mock.Anything).Return(taskViews(tasks...), nil).Maybe()
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

func TestModelUpdate_MainListViewRendersHeaderAndTaskRows(t *testing.T) {
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

	require.Contains(t, view, "RIG")
	require.Contains(t, view, "n new")
	require.Contains(t, view, "q quit")
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

func TestModelView_ShowsProviderOnSecondLine(t *testing.T) {
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

	// In the two-line layout, line 1 has task name + status, line 2 has provider.
	// Find the line after the task name line and check for provider.
	requireNextLineContains := func(name, want string) {
		t.Helper()
		for i, row := range rows {
			if strings.Contains(row, name) && i+1 < len(rows) {
				require.Contains(t, rows[i+1], want,
					"line after %q should contain %q, got %q", name, want, rows[i+1])
				return
			}
		}
		t.Fatalf("did not find row for %q in view:\n%s", name, view)
	}

	requireNextLineContains("codex task", "codex")
	requireNextLineContains("claude task", "claude")

	// Status is still on the same line as the task name
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
	requireLineContains("codex task", "● running")
	requireLineContains("claude task", "◐ needs input")
}

func TestModelView_ProviderBadgeCoexistsWithRuntimeBadge(t *testing.T) {
	task := tuiTask("task-running")
	task.DisplayName = "running task"
	task.Provider = "claude"
	task.Status = core.TaskStatusDegraded
	task.RuntimeState = core.RuntimeStateFinished

	m := newLoadedTUIModel(t, NewMockTaskService(t), task)
	view := stripANSI(m.View().Content)
	rows := strings.Split(view, "\n")

	// Line 1 has task name + status, line 2 has provider
	foundName := false
	for i, row := range rows {
		if !strings.Contains(row, "running task") {
			continue
		}
		foundName = true
		require.Contains(t, row, "○ finished", "status should be on name line")
		require.Less(t, i+1, len(rows), "expected a second line after task name")
		require.Contains(t, rows[i+1], "claude", "provider should be on the second line")
		break
	}

	require.True(t, foundName, "did not find row for %q in view:\n%s", "running task", view)
}

func TestModelView_TaskRowsUseObserverStatus(t *testing.T) {
	service := NewMockTaskService(t)
	task := tuiTask("billing-retry-flow")
	task.DisplayName = "billing retry flow"

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
		require.Contains(t, row, "working")
		return
	}
	t.Fatalf("did not find row for %q in view:\n%s", "billing retry flow", view)
}

func TestModelView_OverviewRowsShowElapsedTimeWithoutClockIcon(t *testing.T) {
	service := NewMockTaskService(t)
	task := tuiTask("auth-rewrite")
	task.DisplayName = "auth rewrite"

	summary := &core.HookSessionSummary{
		TaskID:    task.ID,
		StartedAt: time.Now().Add(-15 * time.Minute),
	}

	m := newLoadedTUIModelWithViews(t, service, taskView(task, summary))
	view := stripANSI(m.View().Content)
	lines := strings.Split(view, "\n")

	// Elapsed time should appear on the same line as the task name (line 1 of the row)
	for _, row := range lines {
		if !strings.Contains(row, "auth rewrite") {
			continue
		}
		require.Contains(t, row, "15m")
		require.NotContains(t, row, "\U0001F550") // old clock emoji icon should not appear
		return
	}

	t.Fatalf("did not find row for %q in view:\n%s", "auth rewrite", view)
}

func TestTaskElapsed_PrefersCodexTaskCreatedAtOverHookSessionStart(t *testing.T) {
	task := tuiTask("codex-task")
	task.Provider = "codex"
	task.CreatedAt = time.Now().Add(-45 * time.Minute)

	view := taskView(task, &core.HookSessionSummary{
		TaskID:    task.ID,
		StartedAt: time.Now().Add(-15 * time.Minute),
	})

	require.Equal(t, "45m", taskElapsed(view))
}

func TestModelView_DetailPanelShowsGitAndSessionColumns(t *testing.T) {
	service := NewMockTaskService(t)
	task := tuiTask("auth-rewrite")
	task.DisplayName = "auth rewrite"
	task.Provider = "codex"
	task.RepoName = "tmux-llm"
	task.BranchName = "feat/auth-rewrite"

	summary := &core.HookSessionSummary{
		TaskID:               task.ID,
		StartedAt:            time.Now().Add(-2*time.Hour - 13*time.Minute),
		LastPromptText:       "refactor the token validation to use JWT",
		LastAssistantMessage: "Updated validateToken() to use jwt.Parse",
		LastEventName:        "PostToolUse",
	}
	observerSummary := &core.ObserverSummary{
		TaskID:        task.ID,
		DisplayStatus: core.DisplayStatusWorking,
		ProcessAlive:  true,
	}

	m := newLoadedTUIModelWithViews(t, service,
		taskViewWithObserver(task, summary, observerSummary),
	)
	view := stripANSI(m.View().Content)

	// Workspace column
	require.Contains(t, view, "WORKSPACE")
	require.Contains(t, view, "feat/auth-rewrite")
	require.Contains(t, view, "tmux-llm")

	// Session column
	require.Contains(t, view, "SESSION")
	require.Contains(t, view, "2h 13m")
	require.Contains(t, view, "total")

	// Removed fields should NOT appear
	require.NotContains(t, view, "connected")
	require.NotContains(t, view, "Selected Task")
	require.NotContains(t, view, "Session Activity")
	require.NotContains(t, view, "Recent Hook Events")
	require.NotContains(t, view, "Session ID")
	require.NotContains(t, view, "Transcript")
	require.NotContains(t, view, "Start Source")
}

func TestModelView_SelectedTaskDetailShowsFallbackWhenHookDataMissing(t *testing.T) {
	service := NewMockTaskService(t)
	task := tuiTask("billing-retry-flow")
	task.DisplayName = "billing retry flow"
	task.Provider = "claude"
	task.RepoName = "tmux-llm"
	task.BranchName = "feat/billing-retry-flow"

	m := newLoadedTUIModel(t, service, task)
	view := stripANSI(m.View().Content)

	require.Contains(t, view, "WORKSPACE")
	require.Contains(t, view, "SESSION")
	require.Contains(t, view, "feat/billing-retry-flow")
	require.Contains(t, view, "tmux-llm")
	require.NotContains(t, view, "connected")
}

func TestCurrentTurnLLMActions_IgnoresEventsFromOtherSessions(t *testing.T) {
	events := []core.HookEvent{
		{
			EventName:   "PostToolUse",
			SessionID:   "sess-child",
			TurnID:      "turn-child",
			CommandText: "sed -n '1,120p' internal/core/task.go",
		},
		{
			EventName:            "Stop",
			SessionID:            "sess-child",
			TurnID:               "turn-child",
			LastAssistantMessage: "Implemented Task 1 in the child agent",
		},
		{
			EventName:   "PostToolUse",
			SessionID:   "sess-parent",
			TurnID:      "turn-parent",
			CommandText: "go test ./internal/adapters/handler/cli -count=1",
		},
		{
			EventName:  "UserPromptSubmit",
			SessionID:  "sess-parent",
			TurnID:     "turn-parent",
			PromptText: "fix the billing retry flow",
		},
	}

	actions := currentTurnLLMActions(events, "sess-parent", "turn-parent", 5)
	require.Equal(t, []string{"go test ./internal/adapters/handler/cli -count=1"}, actions)
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
	require.Contains(t, view, "RIG")
	require.Contains(t, view, "cleanup")
	require.Contains(t, view, "tmux session and worktree will be deleted")
	require.Contains(t, view, "branch will be kept")
	require.NotContains(t, view, "Confirm Cleanup")
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

func TestModelInit_SubscribesToHookUpdates(t *testing.T) {
	service := NewMockTaskService(t)
	hookUpdates := make(chan core.HookSessionSummary)

	service.EXPECT().
		ListTaskViews(mock.Anything).
		Return([]*core.TaskView{}, nil).
		Maybe()
	service.EXPECT().
		SubscribeTaskHookUpdates(mock.Anything).
		Return((<-chan core.HookSessionSummary)(hookUpdates), func() {}, nil).
		Once()

	cmd := newTUIModel(service, "/tmp/default", "codex", "", nil).Init()
	require.NotNil(t, cmd)

	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	require.True(t, ok)
	for _, sub := range batch {
		if sub != nil {
			_ = sub()
		}
	}
}

func TestModelView_PRStatusShownInDetailPanel(t *testing.T) {
	service := NewMockTaskService(t)
	task := tuiTask("auth-rewrite")
	task.RepoName = "tmux-llm"
	task.RepoRoot = "/tmp/repo"
	task.BranchName = "feat/auth-rewrite"

	service.EXPECT().
		GetPRStatus(mock.Anything, "/tmp/repo", "feat/auth-rewrite").
		Return(&core.PRStatus{State: core.PRStateOpen, Number: 42}, nil).
		Maybe()

	view := &core.TaskView{Task: task}
	m := newLoadedTUIModelWithViews(t, service, view)

	// Simulate PR status response
	m, _ = updateTUIModel(t, m, prStatusLoadedMsg{
		taskID: task.ID,
		status: &core.PRStatus{State: core.PRStateOpen, Number: 42},
	})

	rendered := stripANSI(m.View().Content)
	require.Contains(t, rendered, "#42 open")
}

func TestModelView_PRStatusShownInOverviewRows(t *testing.T) {
	service := NewMockTaskService(t)

	openTask := tuiTask("auth-rewrite")
	openTask.DisplayName = "auth rewrite"
	openTask.Provider = "codex"

	mergedTask := tuiTask("billing-retry")
	mergedTask.DisplayName = "billing retry"
	mergedTask.Provider = "claude"

	m := newLoadedTUIModelWithViews(t, service,
		&core.TaskView{
			Task: openTask,
			PR:   &core.PRStatus{State: core.PRStateOpen, Number: 42},
		},
		&core.TaskView{
			Task: mergedTask,
			PR:   &core.PRStatus{State: core.PRStateMerged, Number: 43},
		},
	)

	view := stripANSI(m.View().Content)
	rows := strings.Split(view, "\n")

	// In two-line layout, PR info is on the second line (same as provider).
	// Find the line after the task name and check for the PR icon.
	requireNextLineContains := func(name, want string) {
		t.Helper()
		for i, row := range rows {
			if strings.Contains(row, name) && i+1 < len(rows) {
				require.Contains(t, rows[i+1], want,
					"line after %q should contain %q, got %q", name, want, rows[i+1])
				return
			}
		}
		t.Fatalf("did not find row for %q in view:\n%s", name, view)
	}

	requireNextLineContains("auth rewrite", iconPROpen)
	requireNextLineContains("billing retry", iconPRMerged)
}

func TestModelUpdate_RefreshInvalidatesPRCache(t *testing.T) {
	service := NewMockTaskService(t)
	task := tuiTask("auth-rewrite")
	m := newLoadedTUIModel(t, service, task)

	service.EXPECT().InvalidatePRCache().Once()
	service.EXPECT().ListTaskViews(mock.Anything).Return(nil, nil).Maybe()

	m, cmd := updateTUIModel(t, m, keyRunes("r"))
	require.NotNil(t, cmd)
	require.True(t, m.busy)
}

func TestRefreshTasksCmd_ConvertsPanicsToErrorMessage(t *testing.T) {
	service := NewMockTaskService(t)
	service.EXPECT().
		ListTaskViews(mock.Anything).
		Run(func(context.Context) {
			panic("refresh exploded")
		}).
		Return(nil, nil).
		Once()

	cmd := refreshTasksCmd(service, 7)

	require.NotPanics(t, func() {
		msg := cmd()
		asyncErr, ok := msg.(asyncErrMsg)
		require.True(t, ok)
		require.ErrorContains(t, asyncErr.err, "refreshTasksCmd panicked: refresh exploded")
	})
}

func TestModelUpdate_ShimmerTickIncrementsCounter(t *testing.T) {
	m := newLoadedTUIModel(t, NewMockTaskService(t), tuiTask("task-one"))

	m.mode = tuiModePromptInput
	m.creationProgress = core.TaskProgressNaming
	m.shimmerTick = 3

	m, cmd := updateTUIModel(t, m, shimmerTickMsg{})
	require.Equal(t, 4, m.shimmerTick)
	require.NotNil(t, cmd, "should return another tick command to keep animation going")
}

func TestModelUpdate_ShimmerTickIgnoredWhenNoProgress(t *testing.T) {
	m := newLoadedTUIModel(t, NewMockTaskService(t), tuiTask("task-one"))

	m.shimmerTick = 0
	m.creationProgress = ""

	m, cmd := updateTUIModel(t, m, shimmerTickMsg{})
	require.Equal(t, 0, m.shimmerTick, "tick should not increment when no progress active")
	require.Nil(t, cmd, "should not schedule another tick")
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

	// Allow PR status fetches that may be triggered during task load.
	service.EXPECT().
		GetPRStatus(mock.Anything, mock.Anything, mock.Anything).
		Return(&core.PRStatus{State: core.PRStateNone}, nil).
		Maybe()

	// Allow recent-events fetches that may be triggered during task load or navigation.
	service.EXPECT().
		GetTaskHookEvents(mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil).
		Maybe()

	next, cmd := newTUIModel(
		service,
		"/tmp/default",
		provider,
		"",
		nil,
	).Update(tasksLoadedMsg{requestID: 1, views: views})
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

func executeBatchUntil[T tea.Msg](t *testing.T, cmd tea.Cmd) T {
	t.Helper()
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, sub := range batch {
			result := sub()
			if result == nil {
				continue
			}
			if typed, ok := result.(T); ok {
				return typed
			}
		}
	}
	if typed, ok := msg.(T); ok {
		return typed
	}
	t.Fatalf("expected message of type %T not found in batch", *new(T))
	return *new(T)
}

func TestNameConfirmView_ShowsCheckmarkRecap(t *testing.T) {
	m := newLoadedTUIModel(t, NewMockTaskService(t), tuiTask("task-one"))

	m.mode = tuiModeNameConfirm
	m.createInput.Prompt = "add dark mode toggle to settings page"
	m.provider = "codex"
	m.nameInput.SetValue("dark-mode-settings-toggle")

	view := stripANSI(m.nameConfirmView())

	require.Contains(t, view, "RIG")
	require.Contains(t, view, "new task")
	require.Contains(t, view, "✔")
	require.Contains(t, view, "add dark mode toggle to settings page")
	require.Contains(t, view, "codex")
	require.Contains(t, view, "Name:")
	require.Contains(t, view, "enter")
	require.Contains(t, view, "create")
}

func TestNameConfirmView_ShowsProgressStepsDuringCreation(t *testing.T) {
	m := newLoadedTUIModel(t, NewMockTaskService(t), tuiTask("task-one"))

	m.mode = tuiModeNameConfirm
	m.createInput.Prompt = "add dark mode toggle"
	m.provider = "codex"
	m.nameInput.SetValue("dark-mode-toggle")
	m.busy = true
	m.creationProgress = core.TaskProgressAgentLaunching
	m.creationSteps = []string{"Creating worktree...", "Starting session...", "Launching agent..."}
	m.shimmerTick = 5

	view := stripANSI(m.nameConfirmView())

	// Completed steps should show checkmarks.
	require.Contains(t, view, "Creating worktree...")
	require.Contains(t, view, "Starting session...")
	// Active step should be visible.
	require.Contains(t, view, "Launching agent...")
}

func TestPromptInputView_ShowsShimmerDuringNameSuggestion(t *testing.T) {
	m := newLoadedTUIModel(t, NewMockTaskService(t), tuiTask("task-one"))

	m.mode = tuiModePromptInput
	m.createInput.Prompt = "add dark mode toggle"
	m.creationProgress = core.TaskProgressNaming
	m.shimmerTick = 5

	view := stripANSI(m.promptInputView())
	require.Contains(t, view, "RIG")
	require.Contains(t, view, "new task")
	require.Contains(t, view, "Suggesting name...")
}

func TestPromptInputView_NoShimmerWhenNotBusy(t *testing.T) {
	m := newLoadedTUIModel(t, NewMockTaskService(t), tuiTask("task-one"))

	m.mode = tuiModePromptInput
	m.creationProgress = ""

	view := stripANSI(m.promptInputView())
	require.Contains(t, view, "RIG")
	require.Contains(t, view, "new task")
	require.NotContains(t, view, "Suggesting name...")
}

func TestCompactCount_FormatsTokenCounts(t *testing.T) {
	require.Equal(t, "500", compactCount(500))
	require.Equal(t, "25.2k", compactCount(25200))
	require.Equal(t, "1.5m", compactCount(1500000))
}

func TestModelView_OverviewRowsDoNotShowTokenUsage(t *testing.T) {
	service := NewMockTaskService(t)
	task := tuiTask("auth-rewrite")
	task.DisplayName = "auth rewrite"

	view := &core.TaskView{
		Task: task,
		HookSession: &core.HookSessionSummary{
			TaskID:    task.ID,
			StartedAt: time.Now().Add(-15 * time.Minute),
		},
		TokenUsage: &core.SessionTokenUsage{
			TotalTokens: 25200,
		},
	}

	m := newLoadedTUIModelWithViews(t, service, view)
	rendered := stripANSI(m.listView())

	// Find the task row line and verify it has elapsed time but not token usage.
	for _, line := range strings.Split(rendered, "\n") {
		if strings.Contains(line, "auth rewrite") {
			require.Contains(t, line, "15m")
			require.NotContains(t, line, "tok")
			return
		}
	}
	t.Fatal("task row not found in overview")
}

func TestModelView_DetailPanelShowsTokenUsageSubSection(t *testing.T) {
	service := NewMockTaskService(t)
	task := tuiTask("auth-rewrite")
	task.DisplayName = "auth rewrite"

	m := newLoadedTUIModelWithViews(t, service, &core.TaskView{
		Task: task,
		HookSession: &core.HookSessionSummary{
			TaskID:    task.ID,
			StartedAt: time.Now().Add(-2 * time.Hour),
		},
		TokenUsage: &core.SessionTokenUsage{
			InputTokens:              24000,
			CacheCreationInputTokens: 8000,
			CachedInputTokens:        5900000,
			OutputTokens:             1200,
			TotalTokens:              5925200,
		},
	})

	rendered := stripANSI(m.View().Content)
	require.Contains(t, rendered, "tokens")
	require.Contains(t, rendered, "in    24.0k")
	require.Contains(t, rendered, "5.9m cached")
	require.Contains(t, rendered, "new cache")
	require.Contains(t, rendered, "8.0k new cache")
	require.Contains(t, rendered, "out   1.2k")
}

func TestModelView_DetailPanelShowsTokenUsageWithoutCacheSplit(t *testing.T) {
	service := NewMockTaskService(t)
	task := tuiTask("codex-task")
	task.DisplayName = "codex task"

	m := newLoadedTUIModelWithViews(t, service, &core.TaskView{
		Task: task,
		HookSession: &core.HookSessionSummary{
			TaskID:    task.ID,
			StartedAt: time.Now().Add(-10 * time.Minute),
		},
		TokenUsage: &core.SessionTokenUsage{
			InputTokens:           240,
			CachedInputTokens:     80,
			OutputTokens:          25,
			ReasoningOutputTokens: 9,
			TotalTokens:           345,
		},
	})

	rendered := stripANSI(m.View().Content)
	require.Contains(t, rendered, "tokens")
	require.Contains(t, rendered, "in    240")
	require.Contains(t, rendered, "80 cached")
	require.Contains(t, rendered, "out   25")
	require.Contains(t, rendered, "9 reasoning")
	// No new cache line for Codex
	require.NotContains(t, rendered, "new cache")
}
