package tui

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"rig/internal/core"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

func TestModel_InitLoadsAllTasksAcrossRepos(t *testing.T) {
	frontend := newStubFrontend()
	frontend.listTasks = []*core.Task{
		{
			ID:          "task-1",
			RepoName:    "repo-a",
			DisplayName: "first task",
			Provider:    core.ProviderCodex,
		},
		{
			ID:          "task-2",
			RepoName:    "repo-b",
			DisplayName: "second task",
			Provider:    core.ProviderCodex,
		},
	}

	m := newModel(frontend)
	cmd := m.Init()
	require.NotNil(t, cmd)

	msg := runCmd(t, cmd)
	next, _ := m.Update(msg)

	got, ok := next.(model)
	require.True(t, ok)
	require.Len(t, got.rows, 2)
	require.Equal(t, []string{"task-1", "task-2"}, []string{got.rows[0].task.ID, got.rows[1].task.ID})
	require.Equal(t, 1, frontend.listTasksCalls)
}

func TestModel_ViewRendersTaskMetadata(t *testing.T) {
	frontend := newStubFrontend()
	frontend.listTasks = []*core.Task{
		{
			ID:          "task-1",
			RepoName:    "repo-a",
			DisplayName: "first task",
			BranchName:  "feat/first-task",
			Provider:    core.ProviderCodex,
			CreatedAt:   time.Now().Add(-15 * time.Minute),
		},
	}

	m := newModel(frontend)
	msg := runCmd(t, m.Init())
	next, _ := m.Update(msg)

	got, ok := next.(model)
	require.True(t, ok)

	view := stripANSI(got.View().Content)
	require.Contains(t, view, "RIG")
	require.Contains(t, view, "n new   r refresh   x clean   q quit")
	require.Contains(t, view, "first task")
	require.Contains(t, view, "repo-a")
	require.Contains(t, view, "feat/first-task")
	require.Contains(t, view, "codex")
	require.Contains(t, view, "WORKSPACE")
	require.Contains(t, view, "SESSION")
	require.Contains(t, view, "15m")
}

func TestModel_AfterLoadRequestsLatestStatusAndSubscriptionsForEachTask(t *testing.T) {
	frontend := newStubFrontend()
	frontend.listTasks = []*core.Task{
		{ID: "task-1", RepoName: "repo-a", DisplayName: "first task", Provider: core.ProviderCodex},
		{ID: "task-2", RepoName: "repo-b", DisplayName: "second task", Provider: core.ProviderCodex},
	}
	frontend.subscribeTaskStatus = map[string]chan core.TaskStatusUpdate{
		"task-1": make(chan core.TaskStatusUpdate, 1),
		"task-2": make(chan core.TaskStatusUpdate, 1),
	}

	m := newModel(frontend)
	loadMsg := runCmd(t, m.Init())
	next, cmd := m.Update(loadMsg)
	require.NotNil(t, cmd)

	_, ok := next.(model)
	require.True(t, ok)

	msgs := runBatchCmd(t, cmd)
	require.Len(t, msgs, 4)
	require.Equal(t, []string{"task-1", "task-2"}, frontend.latestTaskStatusCalls)
	require.Equal(t, []string{"task-1", "task-2"}, frontend.subscribeTaskStatusCalls)
}

func TestModel_LatestStatusSeedUpdatesRenderedPhase(t *testing.T) {
	frontend := newStubFrontend()
	frontend.listTasks = []*core.Task{
		{ID: "task-1", RepoName: "repo-a", DisplayName: "first task", Provider: core.ProviderCodex},
	}
	frontend.latestTaskStatus = map[string]*core.TaskStatusUpdate{
		"task-1": {
			TaskID: "task-1",
			Phase:  core.TaskStatusPhaseWorking,
		},
	}
	frontend.subscribeTaskStatus = map[string]chan core.TaskStatusUpdate{
		"task-1": make(chan core.TaskStatusUpdate, 1),
	}

	m := newModel(frontend)
	loadMsg := runCmd(t, m.Init())
	next, cmd := m.Update(loadMsg)
	require.NotNil(t, cmd)

	got, ok := next.(model)
	require.True(t, ok)

	msgs := runBatchCmd(t, cmd)
	latestMsg := requireMsgType[latestTaskStatusLoadedMsg](t, msgs)

	next, _ = got.Update(latestMsg)
	got, ok = next.(model)
	require.True(t, ok)
	require.NotNil(t, got.rows[0].status)
	require.Equal(t, core.TaskStatusPhaseWorking, got.rows[0].status.Phase)
	require.Contains(t, stripANSI(got.View().Content), "working")
}

func TestModel_TaskRowUpdatesWhenSubscriptionUpdateArrives(t *testing.T) {
	frontend := newStubFrontend()
	frontend.listTasks = []*core.Task{
		{ID: "task-1", RepoName: "repo-a", DisplayName: "first task", Provider: core.ProviderCodex},
	}
	updates := make(chan core.TaskStatusUpdate, 1)
	frontend.subscribeTaskStatus = map[string]chan core.TaskStatusUpdate{
		"task-1": updates,
	}

	m := newModel(frontend)
	loadMsg := runCmd(t, m.Init())
	next, cmd := m.Update(loadMsg)
	require.NotNil(t, cmd)

	got, ok := next.(model)
	require.True(t, ok)

	msgs := runBatchCmd(t, cmd)
	subscribeMsg := requireMsgType[taskStatusSubscriptionReadyMsg](t, msgs)

	next, waitCmd := got.Update(subscribeMsg)
	got, ok = next.(model)
	require.True(t, ok)
	require.NotNil(t, waitCmd)

	updates <- core.TaskStatusUpdate{
		TaskID: "task-1",
		Phase:  core.TaskStatusPhaseWaitingForInput,
	}

	updateMsg := runCmd(t, waitCmd)
	next, nextCmd := got.Update(updateMsg)
	got, ok = next.(model)
	require.True(t, ok)
	require.NotNil(t, nextCmd)
	require.NotNil(t, got.rows[0].status)
	require.Equal(t, core.TaskStatusPhaseWaitingForInput, got.rows[0].status.Phase)
	require.Contains(t, stripANSI(got.View().Content), "needs input")
}

func TestModel_StatusEnrichmentFailuresDoNotCollapseListView(t *testing.T) {
	frontend := newStubFrontend()
	frontend.listTasks = []*core.Task{
		{ID: "task-1", RepoName: "repo-a", DisplayName: "first task", Provider: core.ProviderCodex},
		{ID: "task-2", RepoName: "repo-b", DisplayName: "second task", Provider: core.ProviderCodex},
	}
	frontend.latestTaskStatus = map[string]*core.TaskStatusUpdate{
		"task-2": {
			TaskID: "task-2",
			Phase:  core.TaskStatusPhaseWorking,
		},
	}
	frontend.latestTaskStatusErr = map[string]error{
		"task-1": errors.New("latest status unavailable"),
	}
	frontend.subscribeTaskStatus = map[string]chan core.TaskStatusUpdate{
		"task-1": make(chan core.TaskStatusUpdate, 1),
	}
	frontend.subscribeTaskStatusErr = map[string]error{
		"task-2": errors.New("subscription unavailable"),
	}

	m := newModel(frontend)
	loadMsg := runCmd(t, m.Init())
	next, cmd := m.Update(loadMsg)
	require.NotNil(t, cmd)

	got, ok := next.(model)
	require.True(t, ok)

	for _, msg := range runBatchCmd(t, cmd) {
		next, _ = got.Update(msg)
		got, ok = next.(model)
		require.True(t, ok)
	}

	require.NoError(t, got.err)
	require.Len(t, got.rows, 2)
	require.Nil(t, got.rows[0].status)
	require.NotNil(t, got.rows[1].status)
	require.Equal(t, core.TaskStatusPhaseWorking, got.rows[1].status.Phase)

	view := stripANSI(got.View().Content)
	require.Contains(t, view, "first task")
	require.Contains(t, view, "second task")
	require.Contains(t, view, "working")
	require.NotContains(t, view, "latest status unavailable")
	require.NotContains(t, view, "subscription unavailable")
}

func TestModel_InitUsesLifecycleContextForInitialLoad(t *testing.T) {
	frontend := newStubFrontend()
	m := newModel(frontend)

	cmd := m.Init()
	require.NotNil(t, cmd)

	m.cancelStatus()
	runCmd(t, cmd)

	require.NotNil(t, frontend.listTasksContext)
	require.ErrorIs(t, frontend.listTasksContext.Err(), context.Canceled)
}

func TestModel_KeyAEntersPromptMode(t *testing.T) {
	frontend := newStubFrontend()
	m := newLoadedModel(frontend)

	next, cmd := m.Update(tea.KeyPressMsg{Text: "a"})

	require.Nil(t, cmd)

	got, ok := next.(model)
	require.True(t, ok)
	require.Equal(t, modePromptInput, got.mode)
	require.Empty(t, got.prompt)
}

func TestModel_KeyNEntersPromptMode(t *testing.T) {
	frontend := newStubFrontend()
	m := newLoadedModel(frontend)

	next, cmd := m.Update(tea.KeyPressMsg{Text: "n"})

	require.Nil(t, cmd)

	got, ok := next.(model)
	require.True(t, ok)
	require.Equal(t, modePromptInput, got.mode)
	require.Empty(t, got.prompt)
}

func TestModel_EnterOpensSelectedTaskAndKeepsRigRunningOnSuccess(t *testing.T) {
	frontend := newStubFrontend()
	frontend.listTasks = []*core.Task{
		{ID: "task-1", DisplayName: "first task", TmuxSession: "repo_task_1", Provider: core.ProviderCodex},
		{ID: "task-2", DisplayName: "second task", TmuxSession: "repo_task_2", Provider: core.ProviderCodex},
	}

	m := newLoadedModel(frontend)
	m.selected = 1

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)

	pending, ok := next.(model)
	require.True(t, ok)

	msg := runCmd(t, cmd)
	next, follow := pending.Update(msg)
	require.Nil(t, follow)

	got, ok := next.(model)
	require.True(t, ok)
	require.Equal(t, modeBrowse, got.mode)
	require.NoError(t, got.err)
	require.NotNil(t, frontend.openedTask)
	require.Equal(t, "task-2", frontend.openedTask.ID)
	require.Equal(t, 1, frontend.openTaskSessionCalls)
}

func TestModel_OpenTaskFailureShowsErrorAndStaysInList(t *testing.T) {
	frontend := newStubFrontend()
	frontend.listTasks = []*core.Task{
		{ID: "task-1", DisplayName: "first task", TmuxSession: "repo_task_1", Provider: core.ProviderCodex},
	}
	frontend.openTaskSessionErr = errors.New("open failed")

	m := newLoadedModel(frontend)

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)

	pending, ok := next.(model)
	require.True(t, ok)

	msg := runCmd(t, cmd)
	next, follow := pending.Update(msg)
	require.Nil(t, follow)

	got, ok := next.(model)
	require.True(t, ok)
	require.Equal(t, modeBrowse, got.mode)
	require.ErrorContains(t, got.err, "open failed")
	require.Equal(t, 1, frontend.openTaskSessionCalls)
}

func TestModel_CreateTaskFromPromptAppendsTaskAndStartsStatusTracking(t *testing.T) {
	frontend := newStubFrontend()
	frontend.listTasks = []*core.Task{
		{ID: "task-1", DisplayName: "first task", RepoName: "repo-a", Provider: core.ProviderCodex},
		{ID: "task-2", DisplayName: "second task", RepoName: "repo-b", Provider: core.ProviderCodex},
	}
	frontend.createdTask = &core.Task{
		ID:          "task-3",
		DisplayName: "new task",
		RepoName:    "repo-c",
		Provider:    core.ProviderCodex,
	}
	frontend.createTaskEvents = []core.TaskCreateEvent{
		{Progress: &core.TaskCreateProgressEvent{Step: core.TaskCreateProgressSuggestingName}},
		{Task: frontend.createdTask},
	}
	frontend.latestTaskStatus = map[string]*core.TaskStatusUpdate{
		"task-3": {
			TaskID: "task-3",
			Phase:  core.TaskStatusPhaseWorking,
		},
	}
	frontend.subscribeTaskStatus = map[string]chan core.TaskStatusUpdate{
		"task-1": make(chan core.TaskStatusUpdate, 1),
		"task-2": make(chan core.TaskStatusUpdate, 1),
		"task-3": make(chan core.TaskStatusUpdate, 1),
	}

	m := newLoadedModel(frontend)
	m.mode = modePromptInput
	m.prompt = "fix the retry loop"

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)

	submitted, ok := next.(model)
	require.True(t, ok)
	require.Equal(t, modePromptInput, submitted.mode)
	require.Equal(t, "fix the retry loop", submitted.prompt)
	require.True(t, submitted.createPending)

	initialMsgs := runBatchCmd(t, cmd)
	createEvent := requireMsgType[taskCreateEventMsg](t, initialMsgs)
	requireMsgType[shimmerTickMsg](t, initialMsgs)
	require.NotNil(t, createEvent.event.Progress)
	require.Equal(t, core.TaskCreateProgressSuggestingName, createEvent.event.Progress.Step)

	next, follow := submitted.Update(createEvent)
	require.NotNil(t, follow)

	got, ok := next.(model)
	require.True(t, ok)
	require.True(t, got.createPending)
	require.Equal(t, core.TaskCreateProgressSuggestingName, got.createActive)
	require.Contains(t, stripANSI(got.View().Content), "Suggesting name")

	taskCreated := runCmd(t, follow)
	next, follow = got.Update(taskCreated)
	require.NotNil(t, follow)

	got, ok = next.(model)
	require.True(t, ok)
	require.Len(t, got.rows, 3)
	require.Equal(t, modeBrowse, got.mode)
	require.Empty(t, got.prompt)
	require.False(t, got.createPending)
	require.Equal(t, "task-3", got.rows[len(got.rows)-1].task.ID)
	require.Equal(t, "fix the retry loop", frontend.createInput.Prompt)
	require.Equal(t, core.ProviderCodex, frontend.createInput.Provider)
	require.Equal(t, 1, frontend.createTaskStreamCalls)

	frontend.listTasks = append(frontend.listTasks, frontend.createdTask)
	msgs := runBatchCmd(t, follow)
	require.Len(t, msgs, 3)
	tasksLoaded := requireMsgType[tasksLoadedMsg](t, msgs)
	next, _ = got.Update(tasksLoaded)
	got, ok = next.(model)
	require.True(t, ok)
	require.Len(t, got.rows, 3)
	require.Equal(t, []string{"task-3"}, frontend.latestTaskStatusCalls)
	require.Equal(t, []string{"task-3"}, frontend.subscribeTaskStatusCalls)
	require.Equal(t, 1, frontend.listTasksCalls)

	latestMsg := requireMsgType[latestTaskStatusLoadedMsg](t, msgs)
	next, _ = got.Update(latestMsg)
	got, ok = next.(model)
	require.True(t, ok)
	require.NotNil(t, got.rows[2].status)
	require.Equal(t, core.TaskStatusPhaseWorking, got.rows[2].status.Phase)
}

func TestModel_CreateTaskReloadsAuthoritativeTaskSnapshotWhenCreateResponseIsPartial(t *testing.T) {
	frontend := newStubFrontend()
	frontend.listTasks = []*core.Task{
		{ID: "task-1", DisplayName: "first task", RepoName: "repo-a", Provider: core.ProviderCodex},
	}
	frontend.createdTask = &core.Task{
		ID:       "task-2",
		Prompt:   "testing if new rig things work",
		Provider: core.ProviderCodex,
	}
	frontend.createTaskEvents = []core.TaskCreateEvent{
		{Progress: &core.TaskCreateProgressEvent{Step: core.TaskCreateProgressSuggestingName}},
		{Task: frontend.createdTask},
	}
	frontend.latestTaskStatus = map[string]*core.TaskStatusUpdate{
		"task-2": {
			TaskID: "task-2",
			Phase:  core.TaskStatusPhaseStarting,
		},
	}
	frontend.subscribeTaskStatus = map[string]chan core.TaskStatusUpdate{
		"task-2": make(chan core.TaskStatusUpdate, 1),
	}

	m := newLoadedModel(frontend)
	m.mode = modePromptInput
	m.prompt = "testing if new rig things work"

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	submitted, ok := next.(model)
	require.True(t, ok)

	initialMsgs := runBatchCmd(t, cmd)
	progressMsg := requireMsgType[taskCreateEventMsg](t, initialMsgs)
	next, follow := submitted.Update(progressMsg)
	require.NotNil(t, follow)

	taskCreated := runCmd(t, follow)
	next, follow = submitted.Update(taskCreated)
	require.NotNil(t, follow)

	pendingReload, ok := next.(model)
	require.True(t, ok)
	require.Equal(t, "task-2", pendingReload.rows[len(pendingReload.rows)-1].task.ID)

	frontend.listTasks = []*core.Task{
		{ID: "task-1", DisplayName: "first task", RepoName: "repo-a", Provider: core.ProviderCodex},
		{
			ID:           "task-2",
			DisplayName:  "verify new rig behavior",
			Prompt:       "testing if new rig things work",
			RepoName:     "rig",
			BranchName:   "feat/verify-new-rig-behavior",
			WorktreePath: "/tmp/rig-verify-new-rig-behavior",
			Provider:     core.ProviderCodex,
		},
	}

	followMsgs := runBatchCmd(t, follow)
	tasksLoaded := requireMsgType[tasksLoadedMsg](t, followMsgs)
	next, _ = pendingReload.Update(tasksLoaded)
	reloaded, ok := next.(model)
	require.True(t, ok)

	selected := reloaded.selectedRow()
	require.NotNil(t, selected)
	require.NotNil(t, selected.task)
	require.Equal(t, "task-2", selected.task.ID)
	require.Equal(t, "verify new rig behavior", selected.task.DisplayName)
	require.Equal(t, "rig", selected.task.RepoName)
	require.Equal(t, "feat/verify-new-rig-behavior", selected.task.BranchName)
	require.Equal(t, "/tmp/rig-verify-new-rig-behavior", selected.task.WorktreePath)

	view := stripANSI(reloaded.View().Content)
	require.Contains(t, view, "verify new rig behavior")
	require.Contains(t, view, "feat/verify-new-rig-behavior")
	require.Contains(t, view, "testing if new rig things work")
}

func TestModel_EnterWithBlankPromptDoesNothing(t *testing.T) {
	frontend := newStubFrontend()
	m := newLoadedModel(frontend)
	m.mode = modePromptInput
	m.prompt = "   "

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	require.Nil(t, cmd)

	got, ok := next.(model)
	require.True(t, ok)
	require.Equal(t, modePromptInput, got.mode)
	require.Equal(t, "   ", got.prompt)
	require.False(t, got.createPending)
	require.Zero(t, frontend.createTaskStreamCalls)
}

func TestModel_CreateTaskFailureKeepsPromptRecoverableAndPreservesListView(t *testing.T) {
	frontend := newStubFrontend()
	frontend.listTasks = []*core.Task{
		{ID: "task-1", DisplayName: "first task", RepoName: "repo-a", Provider: core.ProviderCodex},
		{ID: "task-2", DisplayName: "second task", RepoName: "repo-b", Provider: core.ProviderCodex},
	}
	frontend.createTaskErr = errors.New("create failed")
	frontend.createTaskEvents = []core.TaskCreateEvent{
		{Progress: &core.TaskCreateProgressEvent{Step: core.TaskCreateProgressCreatingWorktree}},
		{Err: errors.New("create failed")},
	}

	m := newLoadedModel(frontend)
	m.mode = modePromptInput
	m.prompt = "fix the retry loop"

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)

	pending, ok := next.(model)
	require.True(t, ok)
	require.True(t, pending.createPending)

	initialMsgs := runBatchCmd(t, cmd)
	progressMsg := requireMsgType[taskCreateEventMsg](t, initialMsgs)
	requireMsgType[shimmerTickMsg](t, initialMsgs)
	require.NotNil(t, progressMsg.event.Progress)

	next, follow := pending.Update(progressMsg)
	require.NotNil(t, follow)

	withProgress, ok := next.(model)
	require.True(t, ok)
	require.Equal(t, core.TaskCreateProgressCreatingWorktree, withProgress.createActive)
	require.Contains(t, stripANSI(withProgress.View().Content), "Creating worktree")

	createFailed := runCmd(t, follow)
	next, follow = withProgress.Update(createFailed)
	require.Nil(t, follow)

	got, ok := next.(model)
	require.True(t, ok)
	require.Equal(t, modePromptInput, got.mode)
	require.Equal(t, "fix the retry loop", got.prompt)
	require.False(t, got.createPending)
	require.ErrorContains(t, got.createErr, "create failed")
	require.NoError(t, got.err)
	require.Len(t, got.rows, 2)
	require.Empty(t, frontend.latestTaskStatusCalls)
	require.Empty(t, frontend.subscribeTaskStatusCalls)
	require.Equal(t, core.TaskCreateProgressCreatingWorktree, got.createActive)

	view := stripANSI(got.View().Content)
	require.Contains(t, view, "RIG")
	require.Contains(t, view, "new task")
	require.Contains(t, view, "Enter task prompt.")
	require.Contains(t, view, "provider  codex")
	require.Contains(t, view, "fix the retry loop")
	require.Contains(t, view, "Creating worktree")
	require.Contains(t, view, "create failed")
	require.NotContains(t, view, "Loading tasks...")
}

func TestModel_PendingCreateStillAllowsQuitKeys(t *testing.T) {
	frontend := newStubFrontend()
	m := newLoadedModel(frontend)
	m.mode = modePromptInput
	m.prompt = "fix the retry loop"
	m.createPending = true

	for _, msg := range []tea.KeyPressMsg{
		{Text: "q"},
		{Code: 'c', Mod: tea.ModCtrl},
	} {
		next, cmd := m.Update(msg)
		require.NotNil(t, cmd)

		got, ok := next.(model)
		require.True(t, ok)
		require.True(t, got.createPending)

		quitMsg := runCmd(t, cmd)
		_, ok = quitMsg.(tea.QuitMsg)
		require.True(t, ok)
	}
}

func TestModel_ShimmerTickAdvancesAndReschedulesWhilePending(t *testing.T) {
	frontend := newStubFrontend()
	m := newLoadedModel(frontend)
	m.createPending = true

	next, cmd := m.Update(shimmerTickMsg{})
	require.NotNil(t, cmd)

	got, ok := next.(model)
	require.True(t, ok)
	require.Equal(t, 1, got.shimmerTick)

	msg := runCmd(t, cmd)
	_, ok = msg.(shimmerTickMsg)
	require.True(t, ok)
}

func TestModel_EscCancelsPromptMode(t *testing.T) {
	frontend := newStubFrontend()
	m := newLoadedModel(frontend)
	m.mode = modePromptInput
	m.prompt = "fix the retry loop"

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	require.Nil(t, cmd)

	got, ok := next.(model)
	require.True(t, ok)
	require.Equal(t, modeBrowse, got.mode)
	require.Empty(t, got.prompt)
	require.False(t, got.createPending)
	require.NoError(t, got.createErr)
	require.Zero(t, frontend.createTaskStreamCalls)
}

func TestModel_KeyXEntersCleanupConfirmMode(t *testing.T) {
	frontend := newStubFrontend()
	frontend.listTasks = []*core.Task{{ID: "task-1", DisplayName: "first task"}}

	m := newLoadedModel(frontend)
	next, cmd := m.Update(tea.KeyPressMsg{Text: "x"})

	require.Nil(t, cmd)

	got, ok := next.(model)
	require.True(t, ok)
	require.Equal(t, modeCleanupConfirm, got.mode)
}

func TestModel_ConfirmCleanupDeletesTaskAndRemovesRow(t *testing.T) {
	frontend := newStubFrontend()
	frontend.listTasks = []*core.Task{
		{ID: "task-1", DisplayName: "first task", Provider: core.ProviderCodex},
		{ID: "task-2", DisplayName: "second task", Provider: core.ProviderCodex},
	}

	m := newLoadedModel(frontend)
	m.selected = 1
	m.mode = modeCleanupConfirm

	next, cmd := m.Update(tea.KeyPressMsg{Text: "y"})
	require.NotNil(t, cmd)

	pending, ok := next.(model)
	require.True(t, ok)
	require.True(t, pending.deletePending)
	require.Equal(t, modeCleanupConfirm, pending.mode)

	initialMsgs := runBatchCmd(t, cmd)
	taskDeleted := requireMsgType[taskDeletedMsg](t, initialMsgs)
	requireMsgType[shimmerTickMsg](t, initialMsgs)

	next, follow := pending.Update(taskDeleted)
	require.Nil(t, follow)

	got, ok := next.(model)
	require.True(t, ok)
	require.False(t, got.deletePending)
	require.Equal(t, modeBrowse, got.mode)
	require.Len(t, got.rows, 1)
	require.Equal(t, "task-1", got.rows[0].task.ID)
	require.Equal(t, 0, got.selected)
	require.Equal(t, []string{"task-2"}, frontend.deleteTaskIDs)
}

func TestModel_CleanupFailurePreservesRowsAndShowsError(t *testing.T) {
	frontend := newStubFrontend()
	frontend.listTasks = []*core.Task{
		{ID: "task-1", DisplayName: "first task", Provider: core.ProviderCodex},
	}
	frontend.deleteTaskErr = errors.New("cleanup failed")

	m := newLoadedModel(frontend)
	m.mode = modeCleanupConfirm

	next, cmd := m.Update(tea.KeyPressMsg{Text: "y"})
	require.NotNil(t, cmd)

	pending, ok := next.(model)
	require.True(t, ok)
	require.True(t, pending.deletePending)

	initialMsgs := runBatchCmd(t, cmd)
	taskDeleted := requireMsgType[taskDeletedMsg](t, initialMsgs)
	requireMsgType[shimmerTickMsg](t, initialMsgs)

	next, _ = pending.Update(taskDeleted)

	got, ok := next.(model)
	require.True(t, ok)
	require.False(t, got.deletePending)
	require.Equal(t, modeBrowse, got.mode)
	require.Len(t, got.rows, 1)
	require.ErrorContains(t, got.err, "cleanup failed")
}

func newLoadedModel(frontend *stubFrontend) model {
	return model{
		frontend:      frontend,
		statusContext: context.Background(),
		rows:          rowsFromTasks(frontend.listTasks),
		mode:          modeBrowse,
	}
}

func runCmd(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	require.NotNil(t, cmd)
	return cmd()
}

func runBatchCmd(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	msg := runCmd(t, cmd)
	batch, ok := msg.(tea.BatchMsg)
	require.True(t, ok)

	msgs := make([]tea.Msg, 0, len(batch))
	for _, batchCmd := range batch {
		msgs = append(msgs, runCmd(t, batchCmd))
	}
	return msgs
}

func requireMsgType[T tea.Msg](t *testing.T, msgs []tea.Msg) T {
	t.Helper()

	for _, msg := range msgs {
		typed, ok := msg.(T)
		if ok {
			return typed
		}
	}

	var zero T
	t.Fatalf("message of type %T not found", zero)
	return zero
}

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string {
	return ansiPattern.ReplaceAllString(s, "")
}

type stubFrontend struct {
	listTasks                []*core.Task
	listTasksContext         context.Context
	listTasksErr             error
	listTasksCalls           int
	openedTask               *core.Task
	openTaskSessionErr       error
	openTaskSessionCalls     int
	createdTask              *core.Task
	createInput              core.CreateTaskInput
	createTaskErr            error
	createTaskCalls          int
	createTaskEvents         []core.TaskCreateEvent
	createTaskStreamErr      error
	createTaskStreamCalls    int
	deleteTaskErr            error
	deleteTaskIDs            []string
	latestTaskStatus         map[string]*core.TaskStatusUpdate
	latestTaskStatusErr      map[string]error
	latestTaskStatusCalls    []string
	subscribeTaskStatus      map[string]chan core.TaskStatusUpdate
	subscribeTaskStatusErr   map[string]error
	subscribeTaskStatusCalls []string
}

func newStubFrontend() *stubFrontend {
	return &stubFrontend{}
}

func (s *stubFrontend) OpenTaskSession(_ context.Context, task *core.Task) error {
	s.openTaskSessionCalls++
	s.openedTask = task
	return s.openTaskSessionErr
}

func (s *stubFrontend) CreateTask(_ context.Context, input core.CreateTaskInput) (*core.Task, error) {
	s.createTaskCalls++
	s.createInput = input
	return s.createdTask, s.createTaskErr
}

func (s *stubFrontend) CreateTaskStream(
	_ context.Context,
	input core.CreateTaskInput,
) (<-chan core.TaskCreateEvent, error) {
	s.createTaskStreamCalls++
	s.createInput = input
	if s.createTaskStreamErr != nil {
		return nil, s.createTaskStreamErr
	}

	events := make(chan core.TaskCreateEvent, len(s.createTaskEvents))
	for _, event := range s.createTaskEvents {
		events <- event
	}
	close(events)
	return events, nil
}

func (s *stubFrontend) DeleteTask(_ context.Context, taskID string) error {
	s.deleteTaskIDs = append(s.deleteTaskIDs, taskID)
	return s.deleteTaskErr
}

func (s *stubFrontend) ListTasks(ctx context.Context) ([]*core.Task, error) {
	s.listTasksCalls++
	s.listTasksContext = ctx
	return s.listTasks, s.listTasksErr
}

func (s *stubFrontend) LatestTaskStatus(_ context.Context, taskID string) (*core.TaskStatusUpdate, error) {
	s.latestTaskStatusCalls = append(s.latestTaskStatusCalls, taskID)
	if s.latestTaskStatusErr != nil && s.latestTaskStatusErr[taskID] != nil {
		return nil, s.latestTaskStatusErr[taskID]
	}
	if s.latestTaskStatus == nil {
		return nil, nil
	}
	return s.latestTaskStatus[taskID], nil
}

func (s *stubFrontend) SubscribeTaskStatus(_ context.Context, taskID string) (<-chan core.TaskStatusUpdate, error) {
	s.subscribeTaskStatusCalls = append(s.subscribeTaskStatusCalls, taskID)
	if s.subscribeTaskStatusErr != nil && s.subscribeTaskStatusErr[taskID] != nil {
		return nil, s.subscribeTaskStatusErr[taskID]
	}
	if s.subscribeTaskStatus == nil {
		ch := make(chan core.TaskStatusUpdate)
		close(ch)
		return ch, nil
	}
	return s.subscribeTaskStatus[taskID], nil
}
