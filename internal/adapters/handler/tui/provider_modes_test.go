package tui

import (
	"errors"
	"testing"

	"github.com/BaronBonet/rig/internal/core"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

func multiProviderFrontend() *frontendHarness {
	frontend := newFrontendHarness()
	frontend.providerSetup = &core.ProviderSetup{
		Configured: []core.Provider{core.ProviderCodex, core.ProviderClaude},
		Default:    core.ProviderCodex,
	}
	return frontend
}

func asModel(t *testing.T, next tea.Model) model {
	t.Helper()
	got, ok := next.(model)
	require.True(t, ok)
	return got
}

func TestModel_MissingProviderSetupGatesTUIBehindSetup(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.providerSetup = nil
	frontend.detections = []core.ProviderDetection{
		{Provider: core.ProviderCodex, Ready: true},
		{Provider: core.ProviderClaude, Ready: false, Detail: "claude binary not found"},
	}

	m := newModel(frontend.mock)
	msgs := runBatchCmd(t, m.Init())
	setupMsg := requireMsgType[providerSetupLoadedMsg](t, msgs)

	next, cmd := m.Update(setupMsg)
	got := asModel(t, next)
	require.Equal(t, modeProviderSetup, got.mode)
	require.NotNil(t, cmd)

	next, _ = got.Update(runCmd(t, cmd))
	got = asModel(t, next)
	require.Equal(t, 1, frontend.detectProvidersCalls)
	require.False(t, got.setupForm.detecting)

	view := stripANSI(got.View().Content)
	require.Contains(t, view, "Provider setup")
	require.Contains(t, view, "codex")
	require.Contains(t, view, "unavailable")
	require.Contains(t, view, "claude binary not found")
	// Only the detected provider is preselected.
	require.True(t, got.setupForm.rows[0].enabled)
	require.False(t, got.setupForm.rows[1].enabled)
}

func TestModel_ProviderSetupSaveRequiresAtLeastOneProvider(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.providerSetup = nil
	frontend.detections = []core.ProviderDetection{
		{Provider: core.ProviderCodex, Ready: true},
	}

	m := newLoadedModel(frontend)
	m.providerSetup = nil
	next, cmd := m.enterProviderSetupMode()
	got := asModel(t, next)
	next, _ = got.Update(runCmd(t, cmd))
	got = asModel(t, next)

	// Toggle codex off, then try to save.
	next, _ = got.Update(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	got = asModel(t, next)
	require.False(t, got.setupForm.rows[0].enabled)

	next, saveCmd := got.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	got = asModel(t, next)
	require.Nil(t, saveCmd)
	require.ErrorContains(t, got.setupForm.err, "at least one provider")
	require.Nil(t, frontend.savedProviderSetup)
}

func TestModel_ProviderSetupSavesSelectionAndDefault(t *testing.T) {
	frontend := multiProviderFrontend()
	frontend.detections = []core.ProviderDetection{
		{Provider: core.ProviderCodex, Ready: true},
		{Provider: core.ProviderClaude, Ready: true},
	}

	m := newLoadedModel(frontend)
	m.providerSetup = frontend.providerSetup
	next, cmd := m.enterProviderSetupMode()
	got := asModel(t, next)
	next, _ = got.Update(runCmd(t, cmd))
	got = asModel(t, next)

	// Rerunning setup preserves the existing choices.
	require.True(t, got.setupForm.rows[0].enabled)
	require.True(t, got.setupForm.rows[1].enabled)
	require.Equal(t, core.ProviderCodex, got.setupForm.defaultProvider)

	// Move to claude and make it the default.
	next, _ = got.Update(tea.KeyPressMsg{Text: "j"})
	got = asModel(t, next)
	next, _ = got.Update(tea.KeyPressMsg{Text: "d"})
	got = asModel(t, next)
	require.Equal(t, core.ProviderClaude, got.setupForm.defaultProvider)

	next, saveCmd := got.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	got = asModel(t, next)
	require.True(t, got.setupForm.saving)
	require.NotNil(t, saveCmd)

	next, _ = got.Update(runCmd(t, saveCmd))
	got = asModel(t, next)
	require.Equal(t, modeBrowse, got.mode)
	require.NotNil(t, frontend.savedProviderSetup)
	require.Equal(t, core.ProviderClaude, frontend.savedProviderSetup.Default)
	require.Equal(
		t,
		[]core.Provider{core.ProviderCodex, core.ProviderClaude},
		frontend.savedProviderSetup.Configured,
	)
	require.Equal(t, core.ProviderClaude, got.providerSetup.Default)
}

func TestModel_ProviderSetupSaveFailureShowsError(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.detections = []core.ProviderDetection{{Provider: core.ProviderCodex, Ready: true}}
	frontend.saveProviderSetupErr = errors.New("install codex hooks: permission denied")

	m := newLoadedModel(frontend)
	next, cmd := m.enterProviderSetupMode()
	got := asModel(t, next)
	next, _ = got.Update(runCmd(t, cmd))
	got = asModel(t, next)

	next, saveCmd := got.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	got = asModel(t, next)
	next, _ = got.Update(runCmd(t, saveCmd))
	got = asModel(t, next)

	require.Equal(t, modeProviderSetup, got.mode)
	require.ErrorContains(t, got.setupForm.err, "permission denied")
}

func TestModel_TabCyclesConfiguredProvidersWhileComposing(t *testing.T) {
	frontend := multiProviderFrontend()
	m := newLoadedModel(frontend)
	m.providerSetup = frontend.providerSetup
	m.mode = modePromptInput

	require.Equal(t, core.ProviderCodex, m.effectiveCreateProvider())

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	got := asModel(t, next)
	require.Equal(t, core.ProviderClaude, got.effectiveCreateProvider())

	view := stripANSI(got.View().Content)
	require.Contains(t, view, "claude")
	require.Contains(t, view, "tab")

	next, _ = got.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	got = asModel(t, next)
	require.Equal(t, core.ProviderCodex, got.effectiveCreateProvider())
}

func TestModel_TabIsANoOpWithASingleConfiguredProvider(t *testing.T) {
	frontend := newFrontendHarness()
	m := newLoadedModel(frontend)
	m.mode = modePromptInput

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	got := asModel(t, next)

	require.Equal(t, core.ProviderCodex, got.effectiveCreateProvider())
	view := stripANSI(got.View().Content)
	require.NotContains(t, view, "tab cycle")
}

func TestModel_SubmitPromptUsesSelectedProvider(t *testing.T) {
	frontend := multiProviderFrontend()
	frontend.createTaskEvents = []core.TaskCreateEvent{
		{Task: &core.Task{ID: "task-9", DisplayName: "new task", Provider: core.ProviderClaude}},
	}

	m := newLoadedModel(frontend)
	m.providerSetup = frontend.providerSetup
	m.mode = modePromptInput
	m.draft.prompt = "add billing retry flow"

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	got := asModel(t, next)

	_, cmd := got.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	runBatchCmd(t, cmd)

	require.Equal(t, core.ProviderClaude, frontend.createInput.Provider)
}

func TestModel_PRPickerUsesSelectedProvider(t *testing.T) {
	frontend := multiProviderFrontend()
	frontend.createTaskEvents = []core.TaskCreateEvent{
		{Task: &core.Task{ID: "task-9", DisplayName: "auth rewrite", Provider: core.ProviderClaude}},
	}

	m := newLoadedModel(frontend)
	m.providerSetup = frontend.providerSetup
	m.mode = modePRPicker
	m.draft.repoRoot = "/tmp/repo"
	m.draft.prs = []core.RepoPullRequest{{Number: 42, Title: "Auth rewrite", BranchName: "feat/auth"}}

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	got := asModel(t, next)

	_, cmd := got.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	runBatchCmd(t, cmd)

	require.Equal(t, core.ProviderClaude, frontend.createInput.Provider)
	require.NotNil(t, frontend.createInput.Source.PullRequest)
}

func TestModel_SwitchProviderListsOnlyOtherConfiguredProviders(t *testing.T) {
	frontend := multiProviderFrontend()
	frontend.listTasks = []*core.Task{
		{ID: "task-1", DisplayName: "first task", RepoName: "repo-a", Provider: core.ProviderCodex},
	}

	m := newLoadedModel(frontend)
	m.providerSetup = frontend.providerSetup

	next, _ := m.Update(tea.KeyPressMsg{Text: "p"})
	got := asModel(t, next)

	require.Equal(t, modeSwitchProvider, got.mode)
	require.Equal(t, []core.Provider{core.ProviderClaude}, got.providerSwitch.options)

	view := stripANSI(got.View().Content)
	require.Contains(t, view, "Switch this task to:")
	require.Contains(t, view, "claude")
}

func TestModel_SwitchProviderReportsWhenNoAlternativeIsConfigured(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{
		{ID: "task-1", DisplayName: "first task", RepoName: "repo-a", Provider: core.ProviderCodex},
	}

	m := newLoadedModel(frontend)

	next, _ := m.Update(tea.KeyPressMsg{Text: "p"})
	got := asModel(t, next)

	require.Equal(t, modeBrowse, got.mode)
	require.ErrorContains(t, got.err, "no configured alternative provider")
}

func TestModel_SwitchProviderSuccessUpdatesDisplayedActiveProvider(t *testing.T) {
	frontend := multiProviderFrontend()
	frontend.listTasks = []*core.Task{
		{ID: "task-1", DisplayName: "first task", RepoName: "repo-a", Provider: core.ProviderCodex},
	}
	frontend.switchTaskResult = &core.Task{
		ID:          "task-1",
		DisplayName: "first task",
		RepoName:    "repo-a",
		Provider:    core.ProviderClaude,
	}

	m := newLoadedModel(frontend)
	m.providerSetup = frontend.providerSetup

	next, _ := m.Update(tea.KeyPressMsg{Text: "p"})
	got := asModel(t, next)
	next, cmd := got.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	got = asModel(t, next)
	require.Equal(t, opSwitching, got.pending)
	require.NotNil(t, cmd)

	msgs := runBatchCmd(t, cmd)
	switchedMsg := requireMsgType[taskProviderSwitchedMsg](t, msgs)
	next, _ = got.Update(switchedMsg)
	got = asModel(t, next)

	require.Equal(t, "task-1", frontend.switchedTaskID)
	require.Equal(t, core.ProviderClaude, frontend.switchedProvider)
	require.Equal(t, modeBrowse, got.mode)
	require.NoError(t, got.err)
	require.Equal(t, core.ProviderClaude, got.rows[0].task.Provider)

	view := stripANSI(got.View().Content)
	require.Contains(t, view, "claude")
}

func TestModel_SwitchProviderRefusalPreservesActiveProvider(t *testing.T) {
	frontend := multiProviderFrontend()
	frontend.listTasks = []*core.Task{
		{ID: "task-1", DisplayName: "first task", RepoName: "repo-a", Provider: core.ProviderCodex},
	}
	frontend.switchTaskErr = errors.New("provider session is still running: exit codex first")

	m := newLoadedModel(frontend)
	m.providerSetup = frontend.providerSetup

	next, _ := m.Update(tea.KeyPressMsg{Text: "p"})
	got := asModel(t, next)
	next, cmd := got.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	got = asModel(t, next)

	msgs := runBatchCmd(t, cmd)
	switchedMsg := requireMsgType[taskProviderSwitchedMsg](t, msgs)
	next, _ = got.Update(switchedMsg)
	got = asModel(t, next)

	require.Equal(t, modeBrowse, got.mode)
	require.ErrorContains(t, got.err, "still running")
	require.Equal(t, core.ProviderCodex, got.rows[0].task.Provider)
}

func TestModel_SetupOnlyModeQuitsAfterSaving(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.detections = []core.ProviderDetection{{Provider: core.ProviderCodex, Ready: true}}

	m := newModel(frontend.mock)
	m.setupOnly = true

	msgs := runBatchCmd(t, m.Init())
	setupMsg := requireMsgType[providerSetupLoadedMsg](t, msgs)
	next, cmd := m.Update(setupMsg)
	got := asModel(t, next)
	require.Equal(t, modeProviderSetup, got.mode)

	next, _ = got.Update(runCmd(t, cmd))
	got = asModel(t, next)

	next, saveCmd := got.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	got = asModel(t, next)
	next, quitCmd := got.Update(runCmd(t, saveCmd))
	_ = next

	require.NotNil(t, frontend.savedProviderSetup)
	require.NotNil(t, quitCmd)
	require.Equal(t, tea.Quit(), quitCmd())
}

func TestProviderStyleDistinguishesClaudeFromCodex(t *testing.T) {
	claude := providerStyle(string(core.ProviderClaude))
	codex := providerStyle(string(core.ProviderCodex))

	require.Equal(t, colorClaude, claude.GetForeground())
	require.Equal(t, colorCodex, codex.GetForeground())
	require.NotEqual(t, claude.GetForeground(), codex.GetForeground())
}

func TestModel_StatusFromAdoptedProviderReloadsTasks(t *testing.T) {
	frontend := multiProviderFrontend()
	frontend.listTasks = []*core.Task{
		{ID: "task-1", DisplayName: "first task", RepoName: "repo-a", Provider: core.ProviderClaude},
	}

	m := newLoadedModel(frontend)
	m.rows[0].task = &core.Task{
		ID: "task-1", DisplayName: "first task", RepoName: "repo-a", Provider: core.ProviderCodex,
	}
	updates := make(chan core.TaskStatusUpdate, 1)

	// The daemon adopted a manually launched claude session: the live status
	// carries claude while the row still shows codex.
	next, cmd := m.Update(taskStatusUpdatedMsg{
		taskID: "task-1",
		update: core.TaskStatusUpdate{
			TaskID:       "task-1",
			Provider:     core.ProviderClaude,
			Phase:        core.TaskStatusPhaseStarting,
			RawEventName: "SessionStart",
		},
		updates: updates,
	})
	got := asModel(t, next)
	require.NotNil(t, cmd)

	close(updates)
	msgs := runBatchCmd(t, cmd)
	loadedMsg := requireMsgType[tasksLoadedMsg](t, msgs)
	next, _ = got.Update(loadedMsg)
	got = asModel(t, next)

	require.Equal(t, core.ProviderClaude, got.rows[0].task.Provider)
}

func TestModel_StatusFromActiveProviderDoesNotReloadTasks(t *testing.T) {
	frontend := multiProviderFrontend()
	frontend.listTasks = []*core.Task{
		{ID: "task-1", DisplayName: "first task", RepoName: "repo-a", Provider: core.ProviderCodex},
	}

	m := newLoadedModel(frontend)
	updates := make(chan core.TaskStatusUpdate, 1)

	_, cmd := m.Update(taskStatusUpdatedMsg{
		taskID: "task-1",
		update: core.TaskStatusUpdate{
			TaskID:   "task-1",
			Provider: core.ProviderCodex,
			Phase:    core.TaskStatusPhaseWorking,
		},
		updates: updates,
	})
	require.NotNil(t, cmd)

	close(updates)
	for _, msg := range runBatchCmd(t, cmd) {
		_, isLoaded := msg.(tasksLoadedMsg)
		require.False(t, isLoaded, "status from the active provider must not trigger a task reload")
	}
}

func TestModel_ActivityRefreshTickReloadsSelectedTaskActivity(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{
		{ID: "task-1", DisplayName: "first task", RepoName: "repo-a", Provider: core.ProviderClaude},
	}

	m := newLoadedModel(frontend)

	next, cmd := m.Update(activityRefreshTickMsg{})
	require.NotNil(t, cmd)
	got := asModel(t, next)

	msgs := runBatchCmd(t, cmd)
	requireMsgType[taskActivityLoadedMsg](t, msgs)
	requireMsgType[taskTokenUsageLoadedMsg](t, msgs)
	// The tick re-arms itself so activity keeps refreshing.
	requireMsgType[activityRefreshTickMsg](t, msgs)
	require.Contains(t, frontend.getTaskActivityCalls, "task-1:6")
	require.Contains(t, frontend.getTaskTokenUsageCalls, "task-1")
	_ = got
}

func TestModel_ActivityRefreshTickRearmsWithoutASelectedTask(t *testing.T) {
	frontend := newFrontendHarness()
	m := newLoadedModel(frontend)

	_, cmd := m.Update(activityRefreshTickMsg{})
	require.NotNil(t, cmd)

	msg := runCmd(t, cmd)
	_, isTick := msg.(activityRefreshTickMsg)
	require.True(t, isTick)
	require.Empty(t, frontend.getTaskActivityCalls)
}
