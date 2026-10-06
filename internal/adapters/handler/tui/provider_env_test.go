package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/BaronBonet/rig/internal/core"
)

// windowEnv is the provider configuration of the terminal this rig window
// runs in, which the shared daemon cannot see.
var windowEnv = core.ProviderEnv{"CLAUDE_CONFIG_DIR": "/home/me/.claude-work", "CODEX_HOME": ""}

func modelInWindow(frontend *frontendHarness) model {
	m := newLoadedModel(frontend)
	m.providerEnv = windowEnv
	return m
}

func TestModel_ListsAndImportsSessionsWithTheWindowsProviderEnv(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.importableSessions = []core.ProviderSessionSummary{
		{Provider: core.ProviderClaude, SessionID: "sess-1", Title: "fix-date-format", Cwd: "/tmp/repo"},
	}
	frontend.importTask = &core.Task{ID: "task-imported", WorkspaceKind: core.WorkspaceKindFolder}
	m := modelInWindow(frontend)

	runCmd(t, m.importHintCmd())
	require.Equal(t, windowEnv, frontend.importableSessionsEnv, "the empty-dashboard count")
	frontend.importableSessionsEnv = nil

	next, cmd := m.Update(tea.KeyPressMsg{Code: 'i', Text: "i"})
	m, _ = next.(model)
	next, _ = m.Update(runCmd(t, cmd))
	m, _ = next.(model)
	require.Equal(t, windowEnv, frontend.importableSessionsEnv, "the import picker")

	_, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runBatchCmd(t, cmd)
	require.Equal(t, windowEnv, frontend.importEnv)
}

func TestModel_CreatesTasksWithTheWindowsProviderEnv(t *testing.T) {
	frontend := newFrontendHarness()
	m := modelInWindow(frontend)
	next, _ := m.enterPromptInputMode("triage flaky specs")
	prompt, ok := next.(model)
	require.True(t, ok)

	submitComposer(t, prompt)

	require.Equal(t, windowEnv, frontend.createInput.ProviderEnv)
}

func TestModel_CreatesPullRequestTasksWithTheWindowsProviderEnv(t *testing.T) {
	frontend := newFrontendHarness()
	m := modelInWindow(frontend)
	m.mode = modePRPicker
	m.draft.repoRoot = "/tmp/repo"
	m.draft.prs = []core.RepoPullRequest{
		{Number: 42, Title: "Auth rewrite", BranchName: "feat/auth", State: core.PRStateDraft},
	}

	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runBatchCmd(t, cmd)

	require.NotNil(t, frontend.createInput.Source.PullRequest)
	require.Equal(t, windowEnv, frontend.createInput.ProviderEnv)
}
