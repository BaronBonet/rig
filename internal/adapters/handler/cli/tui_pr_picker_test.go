package cli

import (
	"testing"

	"rig/internal/core"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestModelUpdate_CtrlPEntersPRPickerForSelectedRepo(t *testing.T) {
	service := NewMockTaskService(t)
	existing := tuiTask("existing-task")
	existing.RepoRoot = "/tmp/repo"
	existing.RepoName = "repo"

	service.EXPECT().
		ListRepoPullRequests(mock.Anything, "/tmp/repo").
		Return([]core.RepoPullRequest{
			{Number: 42, Title: "Auth rewrite", BranchName: "feat/auth", State: core.PRStateDraft},
		}, nil).
		Once()

	m := newLoadedTUIModel(t, service, existing)
	m, cmd := updateTUIModel(t, m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	require.Equal(t, tuiModePRPicker, m.mode)
	require.NotNil(t, cmd)

	msg := cmd()
	loaded, ok := msg.(repoPRsLoadedMsg)
	require.True(t, ok)
	require.Equal(t, "repo", loaded.repoName)

	m, _ = updateTUIModel(t, m, loaded)
	require.Equal(t, tuiModePRPicker, m.mode)
	require.Equal(t, "repo", m.prPickerRepoName)
	require.Len(t, m.prPickerRows, 1)
}

func TestPRPickerView_ShowsDuplicateRowsAsDisabled(t *testing.T) {
	m := newLoadedTUIModel(t, NewMockTaskService(t), tuiTask("existing-task"))
	m.mode = tuiModePRPicker
	m.prPickerRepoName = "repo"
	m.prPickerRows = []core.RepoPullRequest{
		{Number: 42, Title: "Auth rewrite", BranchName: "feat/auth", State: core.PRStateDraft, HasExistingTask: true},
	}

	view := stripANSI(m.View().Content)
	require.Contains(t, view, "PRs: repo")
	require.Contains(t, view, "Auth rewrite")
	require.Contains(t, view, "already has workspace")
}

func TestModelUpdate_PRPickerEnterCreatesTaskFromSelectedPR(t *testing.T) {
	service := NewMockTaskService(t)
	existing := tuiTask("existing-task")
	existing.RepoRoot = "/tmp/repo"
	existing.RepoName = "repo"
	m := newLoadedTUIModel(t, service, existing)
	m.mode = tuiModePRPicker
	m.prPickerRepoRoot = "/tmp/repo"
	m.prPickerRepoName = "repo"
	m.prPickerRows = []core.RepoPullRequest{
		{Number: 42, Title: "Auth rewrite", BranchName: "feat/auth", State: core.PRStateDraft},
	}

	service.EXPECT().
		CreateTaskFromPRWithProgress(
			mock.Anything,
			core.CreateTaskFromPRInput{
				RepoRoot: "/tmp/repo",
				PR: core.RepoPullRequest{
					Number:     42,
					Title:      "Auth rewrite",
					BranchName: "feat/auth",
					State:      core.PRStateDraft,
				},
				Provider: "codex",
			},
			core.CreateTaskOptions{OpenSession: false},
			mock.Anything,
		).
		Return(tuiTask("pr-42-auth-rewrite"), nil).
		Once()

	m, cmd := updateTUIModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	require.Equal(t, tuiModeList, m.mode)
	require.True(t, m.createInFlight)

	createMsg := executeBatchUntil[createFinishedMsg](t, cmd)
	m, _ = updateTUIModel(t, m, createMsg)
	require.False(t, m.createInFlight)
	require.Equal(t, "pr-42-auth-rewrite", m.selectedTask().Slug)
}

func TestModelUpdate_PRPickerEscapeClearsDuplicateSelectionError(t *testing.T) {
	m := newLoadedTUIModel(t, NewMockTaskService(t), tuiTask("existing-task"))
	m.mode = tuiModePRPicker
	m.prPickerRepoRoot = "/tmp/repo"
	m.prPickerRepoName = "repo"
	m.prPickerRows = []core.RepoPullRequest{
		{Number: 42, Title: "Auth rewrite", BranchName: "feat/auth", State: core.PRStateDraft, HasExistingTask: true},
	}

	m, _ = updateTUIModel(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.EqualError(t, m.err, "PR already has workspace")
	require.Equal(t, tuiModePRPicker, m.mode)

	m, _ = updateTUIModel(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	require.Equal(t, tuiModeList, m.mode)
	require.NoError(t, m.err)

	view := stripANSI(m.View().Content)
	require.NotContains(t, view, "PR already has workspace")
}
