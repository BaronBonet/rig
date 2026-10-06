package tui

import (
	"testing"

	"github.com/BaronBonet/rig/internal/core"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

func launchSettingsFixture() *core.LaunchSettings {
	return &core.LaunchSettings{
		Options: map[core.Provider]core.ProviderLaunchOptions{
			core.ProviderCodex:  {Models: []string{"gpt-5-codex", "gpt-5"}, Efforts: []string{"low", "high"}},
			core.ProviderClaude: {Models: []string{"opus", "sonnet"}, Efforts: []string{"high", "max"}, Handoff: true},
		},
		Defaults: map[core.Provider]core.LaunchOptions{
			core.ProviderCodex: {Model: "gpt-5", Effort: "high"},
		},
	}
}

func ctrl(code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code, Mod: tea.ModCtrl}
}

func TestModel_InitLoadsLaunchSettings(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.launchSettings = launchSettingsFixture()
	m := newTestModel(frontend.mock)

	msgs := runBatchCmd(t, m.Init())
	loaded := requireMsgType[launchSettingsLoadedMsg](t, msgs)
	next, _ := m.Update(loaded)

	got := asModel(t, next)
	require.Equal(t, frontend.launchSettings, got.launchSettings)
	require.Equal(t, 1, frontend.launchSettingsCalls)
}

func TestModel_ComposerPreselectsTheProvidersLastLaunchOptions(t *testing.T) {
	frontend := newFrontendHarness()
	m := newLoadedModel(frontend)
	m.launchSettings = launchSettingsFixture()

	m = pressKeys(t, m, tea.KeyPressMsg{Text: "n"})

	require.Equal(t, modePromptInput, m.mode)
	require.Equal(t, "gpt-5", m.draft.model)
	require.Equal(t, "high", m.draft.effort)
	view := stripANSI(m.View().Content)
	require.Contains(t, view, "model     gpt-5")
	require.Contains(t, view, "effort    high")
	require.Contains(t, view, "ctrl+t cycle")
	require.Contains(t, view, "ctrl+r cycle")
}

func TestModel_CtrlTAndCtrlRCycleModelAndEffortAndSubmitSendsThem(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.createTaskEvents = []core.TaskCreateEvent{
		{Task: &core.Task{ID: "task-9", DisplayName: "new task", Provider: core.ProviderCodex}},
	}
	m := newLoadedModel(frontend)
	m.launchSettings = launchSettingsFixture()
	m = pressKeys(t, m, tea.KeyPressMsg{Text: "n"})
	m = pressKeys(t, m, typed("fix the retry loop")...)

	// gpt-5 → default → gpt-5-codex; high → default → low.
	m = pressKeys(t, m, ctrl('t'))
	require.Empty(t, m.draft.model)
	require.Contains(t, stripANSI(m.View().Content), "model     default")
	m = pressKeys(t, m, ctrl('t'), ctrl('r'), ctrl('r'))
	require.Equal(t, "gpt-5-codex", m.draft.model)
	require.Equal(t, "low", m.draft.effort)
	require.Equal(t, "fix the retry loop", m.draft.prompt, "ctrl+t and ctrl+r never reach the text box")

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	submitted := asModel(t, next)
	runBatchCmd(t, cmd)

	require.Equal(t, "gpt-5-codex", frontend.createInput.Model)
	require.Equal(t, "low", frontend.createInput.Effort)
	require.Equal(t, core.LaunchOptions{Model: "gpt-5-codex", Effort: "low"},
		submitted.launchSettings.Defaults[core.ProviderCodex], "the next draft preselects what was just launched")
}

func TestModel_TabToAnotherProviderPreselectsThatProvidersOptions(t *testing.T) {
	frontend := multiProviderFrontend()
	m := newLoadedModel(frontend)
	m.providerSetup = frontend.providerSetup
	m.launchSettings = launchSettingsFixture()

	m = pressKeys(t, m, tea.KeyPressMsg{Text: "n"})
	require.Equal(t, "gpt-5", m.draft.model)

	m = pressKeys(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	require.Equal(t, core.ProviderClaude, m.effectiveCreateProvider())
	require.Empty(t, m.draft.model, "claude has no recorded defaults")
	require.Empty(t, m.draft.effort)

	m = pressKeys(t, m, ctrl('t'))
	require.Equal(t, "opus", m.draft.model, "cycling follows the selected provider's choices")
}

func TestNextChoice_WrapsAndRecoversFromUnknownValues(t *testing.T) {
	choices := []string{"", "opus", "sonnet"}

	require.Equal(t, "opus", nextChoice(choices, ""))
	require.Equal(t, "sonnet", nextChoice(choices, "opus"))
	require.Empty(t, nextChoice(choices, "sonnet"))
	require.Empty(t, nextChoice(choices, "retired-model"))
	require.Empty(t, nextChoice(nil, "opus"))
}

func TestLaunchOptionsText_NamesModelAndEffort(t *testing.T) {
	require.Empty(t, launchOptionsText(core.LaunchOptions{}))
	require.Equal(t, "opus", launchOptionsText(core.LaunchOptions{Model: "opus"}))
	require.Equal(t, "max effort", launchOptionsText(core.LaunchOptions{Effort: "max"}))
	require.Equal(t, "opus · max effort", launchOptionsText(core.LaunchOptions{Model: "opus", Effort: "max"}))
}

func TestModel_KeyNOpensANewSessionComposerForTheSelectedTask(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{{
		ID:             "task-1",
		DisplayName:    "billing retry",
		Prompt:         "add billing retry flow",
		Provider:       core.ProviderCodex,
		CreationStatus: core.TaskCreationStatusReady,
		Model:          "gpt-5",
		Effort:         "low",
	}}
	m := newLoadedModel(frontend)
	m.launchSettings = launchSettingsFixture()

	m = pressKeys(t, m, tea.KeyPressMsg{Text: "N"})

	require.Equal(t, modePromptInput, m.mode)
	require.NotNil(t, m.draft.forTask)
	require.Equal(t, "task-1", m.draft.forTask.ID)
	require.Empty(t, m.draft.prompt, "the box takes the user's own instruction")
	require.Equal(t, "gpt-5", m.draft.model, "the task's own options come first")
	require.Equal(t, "low", m.draft.effort)
	require.False(t, m.draft.handoff, "codex cannot write a handoff note")
	view := stripANSI(m.View().Content)
	require.Contains(t, view, "new session · billing retry")
	require.Contains(t, view, "handoff   not available for this provider")
	require.Contains(
		t,
		view,
		"Continue the task \"billing retry\" in a fresh session.",
		"the continuation is previewed",
	)
	require.Contains(t, view, "Original ask:")
	require.Contains(t, view, "First recover")
	require.Contains(t, view, "where the work stands")
	require.NotContains(t, view, "workspace ")
	require.Contains(t, view, "enter start session")

	m = pressKeys(t, m, ctrl('o'), ctrl('p'))
	require.Equal(t, modePromptInput, m.mode, "no workspace or pull request choice for a new session")
	require.False(t, m.draft.inFolder)
}

func TestModel_NewSessionSubmitStartsASessionOfTheTaskWithAHandoff(t *testing.T) {
	frontend := multiProviderFrontend()
	frontend.listTasks = []*core.Task{{
		ID:             "task-1",
		DisplayName:    "billing retry",
		Provider:       core.ProviderClaude,
		CreationStatus: core.TaskCreationStatusReady,
	}}
	frontend.newSessionEvents = []core.TaskCreateEvent{
		{Progress: &core.TaskCreateProgressEvent{Step: core.TaskCreateProgressWritingHandoff}},
		{Task: &core.Task{
			ID:             "task-1",
			DisplayName:    "billing retry",
			Provider:       core.ProviderClaude,
			CreationStatus: core.TaskCreationStatusReady,
			Model:          "opus",
		}},
	}
	m := newLoadedModel(frontend)
	m.providerSetup = frontend.providerSetup
	m.launchSettings = launchSettingsFixture()

	m = pressKeys(t, m, tea.KeyPressMsg{Text: "N"})
	require.True(t, m.draft.handoff, "claude writes a handoff note by default")
	view := stripANSI(m.View().Content)
	require.Contains(t, view, "handoff   note from the previous session")
	require.Contains(t, view, "Read the", "the preview follows the toggle")
	require.Contains(t, view, "handoff note from the previous session before doing anything else:")
	m = pressKeys(t, m, ctrl('g'))
	require.False(t, m.draft.handoff)
	require.Contains(t, stripANSI(m.View().Content), "First recover")
	m = pressKeys(t, m, ctrl('g'), ctrl('t'))
	require.Equal(t, "opus", m.draft.model)
	m = pressKeys(t, m, typed("add the search page next")...)

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	submitted := asModel(t, next)
	require.Equal(t, modeBrowse, submitted.mode)
	require.Equal(t, opCreating, submitted.pending)
	require.True(t, submitted.create.newSession)

	msgs := runBatchCmd(t, cmd)
	event := requireMsgType[taskCreateEventMsg](t, msgs)
	require.Equal(t, core.NewTaskSessionInput{
		TaskID:   "task-1",
		Prompt:   "add the search page next",
		Provider: core.ProviderClaude,
		Model:    "opus",
		Handoff:  true,
	}, frontend.newSessionInput)
	require.Zero(t, frontend.createTaskStreamCalls)

	next, follow := submitted.Update(event)
	got := asModel(t, next)
	require.Contains(t, stripANSI(got.View().Content), "Writing handoff from the previous session")

	next, _ = got.Update(runCmd(t, follow))
	got = asModel(t, next)
	require.Equal(t, opNone, got.pending)
	require.Len(t, got.rows, 1)
	require.Equal(t, "opus", got.rows[0].task.Model)
	require.Contains(t, stripANSI(got.View().Content), "launch    opus")
}

func TestModel_KeyNRefusesATaskThatIsNotReady(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{{
		ID:             "task-1",
		DisplayName:    "billing retry",
		Provider:       core.ProviderCodex,
		CreationStatus: core.TaskCreationStatusFailed,
		CreationStep:   core.TaskCreateProgressStartingSession,
	}}
	m := newLoadedModel(frontend)

	m = pressKeys(t, m, tea.KeyPressMsg{Text: "N"})

	require.Equal(t, modeBrowse, m.mode)
	require.ErrorContains(t, m.err, "not ready")
}

func TestModel_NewSessionWithoutAnInstructionStillStarts(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{{
		ID:             "task-1",
		DisplayName:    "billing retry",
		Provider:       core.ProviderCodex,
		CreationStatus: core.TaskCreationStatusReady,
	}}
	frontend.newSessionEvents = []core.TaskCreateEvent{{Task: frontend.listTasks[0]}}
	m := newLoadedModel(frontend)

	m = pressKeys(t, m, tea.KeyPressMsg{Text: "N"})
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	submitted := asModel(t, next)
	require.NotNil(t, cmd, "the continuation prompt alone is a prompt")
	runBatchCmd(t, cmd)

	require.Equal(t, opCreating, submitted.pending)
	require.Equal(t, core.NewTaskSessionInput{
		TaskID:   "task-1",
		Provider: core.ProviderCodex,
	}, frontend.newSessionInput)
}
