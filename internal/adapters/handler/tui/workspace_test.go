package tui

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/BaronBonet/rig/internal/core"
)

func composerIn(t *testing.T, frontend *frontendHarness, cwd string) model {
	t.Helper()
	m := newLoadedModel(frontend)
	m.launchCwd = cwd
	next, _ := m.enterPromptInputMode("triage flaky specs")
	got, ok := next.(model)
	require.True(t, ok)
	return got
}

func submitComposer(t *testing.T, m model) {
	t.Helper()
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	runBatchCmd(t, cmd)
}

func TestComposer_CtrlOSwitchesANewTaskToRunInTheRepositoryFolder(t *testing.T) {
	repo := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(repo, ".git"), 0o755))
	frontend := newFrontendHarness()
	m := composerIn(t, frontend, repo)
	require.Contains(t, stripANSI(m.View().Content), "workspace new worktree")

	next, _ := m.Update(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	m, ok := next.(model)
	require.True(t, ok)
	require.Contains(t, stripANSI(m.View().Content), "workspace this folder")

	submitComposer(t, m)
	require.Equal(t, core.WorkspaceKindFolder, frontend.createInput.Workspace)
	require.Equal(t, repo, frontend.createInput.Cwd)
}

func TestComposer_NewWorktreeIsTheDefaultInsideARepository(t *testing.T) {
	repo := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(repo, ".git"), 0o755))
	frontend := newFrontendHarness()

	submitComposer(t, composerIn(t, frontend, filepath.Join(repo)))

	require.Empty(t, frontend.createInput.Workspace)
}

func TestComposer_OutsideGitTheFolderIsTheOnlyWorkspace(t *testing.T) {
	folder := t.TempDir()
	frontend := newFrontendHarness()
	m := composerIn(t, frontend, folder)
	require.Contains(t, stripANSI(m.View().Content), "workspace this folder  ·  not a git repository")

	next, _ := m.Update(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	m, ok := next.(model)
	require.True(t, ok)
	require.Contains(t, stripANSI(m.View().Content), "workspace this folder")

	submitComposer(t, m)
	require.Equal(t, core.WorkspaceKindFolder, frontend.createInput.Workspace)
}

func TestRepoHeader_NamesFolderTasksByTheirPath(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	frontend := newFrontendHarness()
	m := newLoadedModel(frontend)

	header := m.renderRepoHeader(&core.Task{
		RepoName:      "code",
		RepoRoot:      filepath.Join(home, "dev", "project", "code"),
		WorkspaceKind: core.WorkspaceKindFolder,
	}, 80)

	require.Equal(t, "~/dev/project/code", stripANSI(header))
}
