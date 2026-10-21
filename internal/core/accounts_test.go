package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func licentiamAccountsFixture() []AccountConfig {
	return []AccountConfig{
		{Provider: ProviderClaude, Name: "personal", Dir: "/home/me/.claude"},
		{
			Provider: ProviderClaude,
			Name:     "licentiam",
			Dir:      "/home/me/.claude-licentiam",
			Folders:  []string{"/home/me/dev/licentiam"},
		},
		{Provider: ProviderClaude, Name: "wef", Dir: "/home/me/.claude-wef", Folders: []string{"/home/me/dev/wef"}},
		{Provider: ProviderCodex, Name: "personal", Dir: "/home/me/.codex"},
	}
}

func TestTaskServiceListProviderAccounts_OffersTheWindowsAccountFirstThenTheFoldersOthers(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerConfig.setup = &ProviderSetup{
		Configured: []Provider{ProviderCodex, ProviderClaude},
		Default:    ProviderClaude,
	}
	svc.providerConfig.accounts = licentiamAccountsFixture()
	window := ProviderEnv{"CLAUDE_CONFIG_DIR": "/home/me/.claude-licentiam", "CODEX_HOME": ""}

	accounts, err := svc.service.ListProviderAccounts(t.Context(), "/home/me/dev/licentiam/code", window)

	require.NoError(t, err)
	require.Equal(t, []ProviderAccount{
		{
			Provider: ProviderCodex,
			Name:     "personal",
			Dir:      "/home/me/.codex",
			Env:      ProviderEnv{"CODEX_HOME": ""},
			Window:   true,
		},
		{
			Provider: ProviderClaude,
			Name:     "licentiam",
			Dir:      "/home/me/.claude-licentiam",
			Env:      ProviderEnv{"CLAUDE_CONFIG_DIR": "/home/me/.claude-licentiam"},
			Window:   true,
		},
		{
			Provider: ProviderClaude,
			Name:     "personal",
			Dir:      "/home/me/.claude",
			Env:      ProviderEnv{"CLAUDE_CONFIG_DIR": ""},
		},
	}, accounts, "wef's account is offered only in its own folder")
}

func TestTaskServiceListProviderAccounts_NamesAnUnconfiguredWindowAccountByItsDirectory(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerConfig.setup = &ProviderSetup{Configured: []Provider{ProviderClaude}, Default: ProviderClaude}
	svc.providerConfig.accounts = licentiamAccountsFixture()

	accounts, err := svc.service.ListProviderAccounts(
		t.Context(),
		"/home/me/dev/wef",
		ProviderEnv{"CLAUDE_CONFIG_DIR": "/home/me/.claude-other"},
	)

	require.NoError(t, err)
	require.Len(t, accounts, 3, "only the configured provider's accounts")
	require.Equal(t, "/home/me/.claude-other", accounts[0].Name)
	require.True(t, accounts[0].Window)
	require.Equal(t, []string{"personal", "wef"}, []string{accounts[1].Name, accounts[2].Name})
}

func TestTaskServiceListProviderAccounts_RequiresProviderSetup(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerConfig.setup = nil

	_, err := svc.service.ListProviderAccounts(t.Context(), "/home/me", nil)

	require.ErrorIs(t, err, ErrProviderSetupRequired)
}

func TestTaskServiceGetAccountUsage_AsksTheAccountsProvider(t *testing.T) {
	svc := newTestTaskService(t)
	want := &AccountUsage{Plan: "max", Limits: []UsageLimit{{Name: "5h", UsedPercent: 42}}}
	svc.claudeRepo.accountUsage = map[string]*AccountUsage{"/home/me/.claude-licentiam": want}
	account := ProviderAccount{
		Provider: ProviderClaude,
		Dir:      "/home/me/.claude-licentiam",
		Env:      ProviderEnv{"CLAUDE_CONFIG_DIR": "/home/me/.claude-licentiam"},
	}

	usage, err := svc.service.GetAccountUsage(t.Context(), account)

	require.NoError(t, err)
	require.Same(t, want, usage)
	require.Equal(t, account.Env, svc.claudeRepo.accountUsageEnv)
}

func TestAccountConfigOfferedIn(t *testing.T) {
	account := AccountConfig{Folders: []string{"/home/me/dev/licentiam"}}

	require.True(t, account.OfferedIn("/home/me/dev/licentiam"))
	require.True(t, account.OfferedIn("/home/me/dev/licentiam/code/app"))
	require.False(t, account.OfferedIn("/home/me/dev/licentiam-old"))
	require.False(t, account.OfferedIn("/home/me/dev"))
	require.True(t, AccountConfig{}.OfferedIn("/anywhere"), "an account without folders is offered everywhere")
}

func TestProviderEnvSelectsAndWith(t *testing.T) {
	window := ProviderEnv{"CLAUDE_CONFIG_DIR": "/home/me/.claude-licentiam", "CODEX_HOME": ""}
	personal := ProviderEnv{"CLAUDE_CONFIG_DIR": ""}

	require.True(t, window.Selects(ProviderEnv{"CLAUDE_CONFIG_DIR": "/home/me/.claude-licentiam"}))
	require.False(t, window.Selects(personal))
	require.False(t, ProviderEnv(nil).Selects(personal), "an env that decides nothing selects no account")

	moved := window.With(personal)
	require.Equal(t, ProviderEnv{"CLAUDE_CONFIG_DIR": "", "CODEX_HOME": ""}, moved)
	require.Equal(t, "/home/me/.claude-licentiam", window["CLAUDE_CONFIG_DIR"], "the window's env is left as it was")
}

func TestTaskServiceNewTaskSession_UnderAnotherAccountEndsTheSessionAndKeepsTheAccount(t *testing.T) {
	svc := newTestTaskService(t)
	task := readyTaskFixture()
	task.Model, task.Effort = "gpt-5", "high"
	task.ProviderEnv = ProviderEnv{"CODEX_HOME": "", "CLAUDE_CONFIG_DIR": ""}
	svc.taskRepo.listTasks = []*Task{task}
	svc.providerRepo.clearCommand = "/clear"
	svc.sessionClient.inspectState = TaskSessionRuntimeState{Exists: true, ActiveCommands: []string{"codex"}}
	svc.sessionClient.onSubmit = func(*Task, string) {
		svc.sessionClient.inspectState = idlePane()
	}
	work := ProviderEnv{"CODEX_HOME": "/home/me/.codex-work", "CLAUDE_CONFIG_DIR": ""}

	updated, err := svc.service.NewTaskSessionWithProgress(t.Context(), NewTaskSessionInput{
		TaskID:      "task-1",
		Prompt:      "continue",
		Model:       "gpt-5",
		Effort:      "high",
		ProviderEnv: work,
	}, nil)

	require.NoError(t, err)
	require.Equal(t, []string{"/exit"}, svc.sessionClient.submittedInputs,
		"a running process keeps the account it was launched under")
	require.Equal(t, work, svc.sessionClient.startedTask.ProviderEnv)
	require.Equal(t, work, svc.providerRepo.sessionEnvEnv, "the new account's environment is prepared")
	require.Equal(t, work, updated.ProviderEnv)
	require.Equal(t, work, svc.taskRepo.updatedTask.ProviderEnv, "the task keeps running under the new account")
}

func TestTaskServiceNewTaskSession_InANewWindowRunsTheChildUnderTheChosenAccount(t *testing.T) {
	svc := newTestTaskService(t)
	task := readyTaskFixture()
	task.ProviderEnv = ProviderEnv{"CODEX_HOME": ""}
	svc.taskRepo.listTasks = []*Task{task}
	svc.sessionClient.inspectState = TaskSessionRuntimeState{Exists: true, ActiveCommands: []string{"codex"}}
	work := ProviderEnv{"CODEX_HOME": "/home/me/.codex-work"}

	child, err := svc.service.NewTaskSessionWithProgress(t.Context(), NewTaskSessionInput{
		TaskID:      "task-1",
		Prompt:      "add the search page",
		NewWindow:   true,
		ProviderEnv: work,
	}, nil)

	require.NoError(t, err)
	require.Equal(t, work, child.ProviderEnv)
	require.Equal(t, work, svc.sessionClient.startedTask.ProviderEnv)
	require.Equal(t, work, svc.providerRepo.suggestEnv, "the child's name is suggested under its own account")
}
