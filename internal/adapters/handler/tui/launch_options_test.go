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
