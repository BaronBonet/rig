package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/BaronBonet/rig/internal/core"
)

func TestComposer_ModifiedEnterBreaksTheLineAndPlainEnterSubmits(t *testing.T) {
	frontend := newFrontendHarness()
	m := composerIn(t, frontend, t.TempDir())

	m = pressKeys(t, m, tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt})
	m = pressKeys(t, m, typed("add the endpoint first")...)
	m = pressKeys(t, m, tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift})
	m = pressKeys(t, m, typed("then update the ui")...)
	require.Equal(t, modePromptInput, m.mode, "a modified enter does not submit")
	require.Zero(t, frontend.createTaskStreamCalls)
	require.Contains(t, stripANSI(m.View().Content), "alt+enter newline")

	submitComposer(t, m)
	require.Equal(t, "triage flaky specs\nadd the endpoint first\nthen update the ui", frontend.createInput.Prompt)
}

func TestComposer_PromptRunsPastTheBoxHeight(t *testing.T) {
	m := composerIn(t, newFrontendHarness(), t.TempDir())

	lines := []string{"triage flaky specs"}
	for i := range promptInputMaxHeight + 3 {
		m = pressKeys(t, m, tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt})
		line := "step " + string(rune('a'+i))
		m = pressKeys(t, m, typed(line)...)
		lines = append(lines, line)
	}

	require.Equal(t, strings.Join(lines, "\n"), m.draft.prompt)
}

func TestNewSessionComposer_ModifiedEnterBreaksTheInstruction(t *testing.T) {
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
	m = pressKeys(t, m, typed("add the search page")...)
	m = pressKeys(t, m, tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt})
	m = pressKeys(t, m, typed("then the detail page")...)
	require.Contains(t, stripANSI(m.View().Content), "alt+enter newline")

	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runBatchCmd(t, cmd)
	require.Equal(t, "add the search page\nthen the detail page", frontend.newSessionInput.Prompt)
}
