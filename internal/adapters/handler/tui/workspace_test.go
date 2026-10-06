package tui

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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

func TestComposer_PullRequestsAreUnavailableOutsideGit(t *testing.T) {
	frontend := newFrontendHarness()
	m := composerIn(t, frontend, t.TempDir())
	require.NotContains(t, stripANSI(m.View().Content), "ctrl+p")

	next, cmd := m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	m, ok := next.(model)

	require.True(t, ok)
	require.Nil(t, cmd)
	require.Equal(t, modePromptInput, m.mode)
	require.ErrorContains(t, m.draft.err, "need a git repository")
}

func TestTaskInFolder_KeepsEachClientsTasksApart(t *testing.T) {
	project := "/src/project/code"

	require.False(t, taskInFolder(&core.Task{RepoRoot: "/src/work/code", WorktreePath: "/src/work/code"}, project))
	require.False(
		t,
		taskInFolder(&core.Task{RepoRoot: "/src/project/code-old"}, project),
		"a sibling sharing the prefix",
	)
	require.True(t, taskInFolder(&core.Task{RepoRoot: project, WorktreePath: project}, project))
	require.True(t, taskInFolder(&core.Task{
		RepoRoot: project + "/api-server", WorktreePath: project + "/api-server_search",
	}, project), "a worktree task of a repo below the folder")
	require.True(t, taskInFolder(&core.Task{RepoRoot: project + "/api-server"}, project+"/api-server/app"),
		"rig launched inside the task's repository")
	require.True(t, taskInFolder(&core.Task{}, project), "a task that names no folder is never hidden")
}

func dashboardIn(t *testing.T, frontend *frontendHarness, folder string) model {
	t.Helper()
	m, loadMsg := initModel(t, newModel(frontend.mock, folder, ""))
	next, _ := m.Update(loadMsg)
	m, ok := next.(model)
	require.True(t, ok)
	return m
}

func clientTasksFixture() []*core.Task {
	return []*core.Task{
		{ID: "work-1", DisplayName: "background-worker", RepoName: "code",
			RepoRoot: "/src/work/code", WorktreePath: "/src/work/code", WorkspaceKind: core.WorkspaceKindFolder},
		{ID: "lic-1", DisplayName: "search-integration", RepoName: "code",
			RepoRoot: "/src/project/code", WorktreePath: "/src/project/code", WorkspaceKind: core.WorkspaceKindFolder},
		{ID: "lic-2", DisplayName: "fix date format", RepoName: "api-server",
			RepoRoot: "/src/project/code/api-server", WorktreePath: "/src/project/code/api-server_fix"},
	}
}

func rowIDs(m model) []string {
	ids := make([]string, 0, len(m.rows))
	for _, row := range m.rows {
		ids = append(ids, taskID(row.task))
	}
	return ids
}

func TestDashboard_ListsOnlyTheLaunchFoldersTasksUntilFIsPressed(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = clientTasksFixture()

	m := dashboardIn(t, frontend, "/src/project/code")
	require.ElementsMatch(t, []string{"lic-1", "lic-2"}, rowIDs(m))
	view := stripANSI(m.View().Content)
	require.Contains(t, view, "1 task in other folders hidden. Press f to show all folders.")
	require.NotContains(t, view, "background-worker")

	next, cmd := m.Update(tea.KeyPressMsg{Code: 'f', Text: "f"})
	m, _ = next.(model)
	next, _ = m.Update(runCmd(t, cmd))
	m, _ = next.(model)
	require.ElementsMatch(t, []string{"work-1", "lic-1", "lic-2"}, rowIDs(m))
	require.Contains(t, stripANSI(m.View().Content), "Showing all folders. Press f to show only /src/project/code.")

	next, cmd = m.Update(tea.KeyPressMsg{Code: 'f', Text: "f"})
	m, _ = next.(model)
	next, _ = m.Update(runCmd(t, cmd))
	m, _ = next.(model)
	require.ElementsMatch(t, []string{"lic-1", "lic-2"}, rowIDs(m))
}

func TestDashboard_SaysNothingAboutFoldersWhenEveryTaskIsHere(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = clientTasksFixture()[1:]

	m := dashboardIn(t, frontend, "/src/project/code")

	require.Len(t, m.rows, 2)
	require.NotContains(t, stripANSI(m.View().Content), "other folders")
}

func TestDashboard_WithoutALaunchFolderListsEveryTask(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = clientTasksFixture()

	m := dashboardIn(t, frontend, "")

	require.Len(t, m.rows, 3)
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'f', Text: "f"})
	require.Nil(t, cmd)
}

func TestDashboard_TheFolderLineFitsTheScreen(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = clientTasksFixture()[:1]
	for index := range 20 {
		suffix := strconv.Itoa(index)
		frontend.listTasks = append(frontend.listTasks, &core.Task{
			ID: "task-" + suffix, DisplayName: "task " + suffix, RepoName: "code",
			RepoRoot: "/src/project/code", BranchName: "feat/task-" + suffix, WorktreePath: "/src/task-" + suffix,
		})
	}
	m := dashboardIn(t, frontend, "/src/project/code")
	m.width, m.height, m.selected = 60, 18, 15

	view := m.View().Content
	require.Contains(t, stripANSI(view), "1 task in other folders")
	require.LessOrEqual(t, len(strings.Split(view, "\n")), m.height)
	for _, line := range strings.Split(view, "\n")[1:] {
		require.LessOrEqual(t, lipgloss.Width(line), m.totalWidth(), stripANSI(line))
	}
}

func TestDashboard_PagingMatchesTheRowsShownUnderTheFolderLine(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = clientTasksFixture()[:1]
	for index := range 30 {
		suffix := strconv.Itoa(index)
		frontend.listTasks = append(frontend.listTasks, &core.Task{
			ID: "task-" + suffix, DisplayName: "row " + suffix, RepoName: "code", RepoRoot: "/src/project/code",
		})
	}
	m := dashboardIn(t, frontend, "/src/project/code")
	m.width = 100
	for height := 12; height <= 20; height++ {
		m.height = height
		shown := 0
		for _, line := range strings.Split(stripANSI(m.View().Content), "\n") {
			if strings.Contains(line, "row ") {
				shown++
			}
		}
		require.Equal(t, shown, m.taskListPageSize(), "height %d", height)
	}
}

func TestDashboard_TheFolderLineIsCutToANarrowScreen(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = clientTasksFixture()
	m := dashboardIn(t, frontend, "/src/project/code")
	m.width = 40

	for _, line := range strings.Split(m.View().Content, "\n") {
		if strings.Contains(stripANSI(line), "other folders") {
			require.LessOrEqual(t, lipgloss.Width(line), m.totalWidth())
			return
		}
	}
	t.Fatal("folder line not shown")
}
