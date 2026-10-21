package tui

import (
	"errors"
	"testing"
	"time"

	"github.com/BaronBonet/rig/internal/core"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

var (
	personalCodex = core.ProviderAccount{
		Provider: core.ProviderCodex, Name: "personal", Dir: "/home/me/.codex",
		Env: core.ProviderEnv{"CODEX_HOME": ""}, Window: true,
	}
	workCodex = core.ProviderAccount{
		Provider: core.ProviderCodex, Name: "work", Dir: "/home/me/.codex-work",
		Env: core.ProviderEnv{"CODEX_HOME": "/home/me/.codex-work"},
	}
	accountsWindowEnv = core.ProviderEnv{"CODEX_HOME": "", "CLAUDE_CONFIG_DIR": "/home/me/.claude-wef"}
)

func accountsFrontend() *frontendHarness {
	frontend := newFrontendHarness()
	frontend.accounts = []core.ProviderAccount{personalCodex, workCodex}
	return frontend
}

func newAccountsModel(frontend *frontendHarness) model {
	m := newLoadedModel(frontend)
	m.providerEnv = accountsWindowEnv
	m.accounts = frontend.accounts
	return m
}

// applyAccountMsgs runs cmd and feeds the account messages it produces back
// into the model, leaving out commands that would tick or block.
func applyAccountMsgs(t *testing.T, m model, cmd tea.Cmd) model {
	t.Helper()
	if cmd == nil {
		return m
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, inner := range msg {
			m = applyAccountMsgs(t, m, inner)
		}
	case accountUsageMsg, providerAccountsLoadedMsg:
		next, nextCmd := m.Update(msg)
		m = applyAccountMsgs(t, asModel(t, next), nextCmd)
	}
	return m
}

func TestModel_ComposerRunsTheTaskUnderTheWindowsAccountUnlessAnotherIsChosen(t *testing.T) {
	for name, tc := range map[string]struct {
		keys    []tea.KeyPressMsg
		wantEnv core.ProviderEnv
	}{
		"window's account": {wantEnv: accountsWindowEnv},
		"another account": {
			keys:    []tea.KeyPressMsg{shiftTab()},
			wantEnv: core.ProviderEnv{"CODEX_HOME": "/home/me/.codex-work", "CLAUDE_CONFIG_DIR": "/home/me/.claude-wef"},
		},
		"round the accounts": {keys: []tea.KeyPressMsg{shiftTab(), shiftTab()}, wantEnv: accountsWindowEnv},
	} {
		t.Run(name, func(t *testing.T) {
			frontend := accountsFrontend()
			frontend.createTaskEvents = []core.TaskCreateEvent{{Task: &core.Task{ID: "task-9"}}}
			m := newAccountsModel(frontend)
			m = pressKeys(t, m, tea.KeyPressMsg{Text: "n"})
			m = pressKeys(t, m, typed("fix the retry loop")...)
			m = pressKeys(t, m, tc.keys...)
			require.Equal(t, "fix the retry loop", m.draft.prompt, "shift+tab never reaches the text box")

			_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			runBatchCmd(t, cmd)

			require.Equal(t, tc.wantEnv, frontend.createInput.ProviderEnv)
		})
	}
}

func TestModel_ComposerShowsTheAccountWithItsUsage(t *testing.T) {
	frontend := accountsFrontend()
	frontend.accountUsage = map[string]*core.AccountUsage{
		workCodex.Dir: {Plan: "plus", Limits: []core.UsageLimit{
			{Name: "5h", UsedPercent: 42.4, ResetsAt: time.Now().Add(time.Hour)},
			{Name: "week", UsedPercent: 91},
		}},
	}
	m := newAccountsModel(frontend)

	next, cmd := m.Update(tea.KeyPressMsg{Text: "n"})
	m = asModel(t, next)
	require.Contains(t, stripANSI(m.View().Content), "account   personal  reading usage...  ·  shift+tab cycle")
	m = applyAccountMsgs(t, m, cmd)
	require.ElementsMatch(t, []string{personalCodex.Dir, workCodex.Dir}, frontend.accountUsageCalls,
		"opening the composer reads the usage of the provider's accounts")

	m = pressKeys(t, m, shiftTab())

	require.Contains(t, stripANSI(m.View().Content), "account   work  5h 42% · week 91%  ·  shift+tab cycle")
}

func TestModel_NewSessionKeepsTheTasksAccountUnlessAnotherIsChosen(t *testing.T) {
	task := &core.Task{
		ID: "task-1", Provider: core.ProviderCodex, CreationStatus: core.TaskCreationStatusReady,
		ProviderEnv: core.ProviderEnv{"CODEX_HOME": "/home/me/.codex-work", "CLAUDE_CONFIG_DIR": ""},
	}
	for name, tc := range map[string]struct {
		keys    []tea.KeyPressMsg
		wantEnv core.ProviderEnv
	}{
		"the task's account": {wantEnv: nil},
		"another account": {
			keys:    []tea.KeyPressMsg{shiftTab()},
			wantEnv: core.ProviderEnv{"CODEX_HOME": "", "CLAUDE_CONFIG_DIR": ""},
		},
	} {
		t.Run(name, func(t *testing.T) {
			frontend := accountsFrontend()
			frontend.listTasks = []*core.Task{task}
			frontend.newSessionEvents = []core.TaskCreateEvent{{Task: task}}
			m := newAccountsModel(frontend)
			m = pressKeys(t, m, tea.KeyPressMsg{Text: "N"})
			require.Contains(t, stripANSI(m.View().Content), "account   work", "the task's account, not the window's")
			m = pressKeys(t, m, tc.keys...)

			_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			runBatchCmd(t, cmd)

			require.Equal(t, tc.wantEnv, frontend.newSessionInput.ProviderEnv)
		})
	}
}

func TestModel_TaskDetailsNameTheTasksAccount(t *testing.T) {
	frontend := accountsFrontend()
	frontend.listTasks = []*core.Task{{
		ID: "task-1", DisplayName: "retry loop", Provider: core.ProviderCodex,
		ProviderEnv: core.ProviderEnv{"CODEX_HOME": "/home/me/.codex-work"},
	}}
	m := newAccountsModel(frontend)

	require.Contains(t, stripANSI(m.View().Content), "account   work")
}

func TestModel_KeyUShowsEveryAccountsUsage(t *testing.T) {
	t.Setenv("HOME", "/home/me")
	frontend := accountsFrontend()
	frontend.accountUsage = map[string]*core.AccountUsage{
		personalCodex.Dir: {Plan: "plus", Limits: []core.UsageLimit{
			{Name: "5h", UsedPercent: 12, ResetsAt: time.Now().Add(time.Hour)},
			{Name: "week", UsedPercent: 30, ResetsAt: time.Now().Add(-time.Minute)},
		}},
	}
	frontend.accountUsageErr = map[string]error{workCodex.Dir: errors.New("not logged in")}
	m := newAccountsModel(frontend)

	next, cmd := m.Update(tea.KeyPressMsg{Text: "u"})
	m = applyAccountMsgs(t, asModel(t, next), cmd)

	require.Equal(t, modeAccounts, m.mode)
	require.Equal(t, "/tmp/repo", frontend.accountsFolder, "the accounts of the window's folder")
	require.Equal(t, accountsWindowEnv, frontend.accountsEnv)
	view := stripANSI(m.View().Content)
	require.Contains(t, view, "personal  ~/.codex")
	require.Contains(t, view, "default here")
	require.Contains(t, view, "plus · 5h 12% resets ")
	require.Contains(t, view, "week 0%", "a window that has started over since the reading is unused")
	require.Contains(t, view, "work      ~/.codex-work")
	require.Contains(t, view, "usage unavailable: not logged in")

	calls := len(frontend.accountUsageCalls)
	next, cmd = m.Update(tea.KeyPressMsg{Text: "r"})
	m = applyAccountMsgs(t, asModel(t, next), cmd)
	require.Len(t, frontend.accountUsageCalls, calls+2, "refresh reads every account again")

	m = pressKeys(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	require.Equal(t, modeBrowse, m.mode)
}

func TestModel_ComposerOffersTheChosenAccountsModels(t *testing.T) {
	frontend := accountsFrontend()
	frontend.accounts[1].Options = core.ProviderLaunchOptions{Models: []string{"gpt-5-mini"}, Efforts: []string{"low"}}
	m := newAccountsModel(frontend)
	m.launchSettings = launchSettingsFixture()
	m = pressKeys(t, m, tea.KeyPressMsg{Text: "n"})
	require.Equal(t, "gpt-5", m.draft.model, "the window's account offers its last model")

	m = pressKeys(t, m, shiftTab())
	require.Empty(t, m.draft.model, "the chosen account's plan does not offer gpt-5")
	require.Empty(t, m.draft.effort)

	m = pressKeys(t, m, ctrl('t'), ctrl('r'))
	require.Equal(t, "gpt-5-mini", m.draft.model)
	require.Equal(t, "low", m.draft.effort)
}

// shiftTab cycles the composer's account. It reaches rig inside tmux, where
// vim-tmux-navigator style bindings take ctrl+h/j/k/l.
func shiftTab() tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
}
