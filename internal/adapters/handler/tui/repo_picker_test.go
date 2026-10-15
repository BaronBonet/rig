package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/BaronBonet/rig/internal/core"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestModel_RepoPickerCreatesTheTaskInThePickedRepo(t *testing.T) {
	now := time.Now()
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{
		{ID: "a", RepoRoot: "/src/repo-a", RepoName: "repo-a", CreatedAt: now.Add(-2 * time.Hour)},
		{ID: "b", RepoRoot: "/src/repo-b", RepoName: "repo-b", CreatedAt: now.Add(-3 * time.Hour)},
	}
	frontend.createTaskEvents = []core.TaskCreateEvent{{Task: &core.Task{ID: "new"}}}
	frontend.mock.EXPECT().ListRepos(mock.Anything).Return([]core.KnownRepo{
		{Name: "repo-a", Root: "/src/repo-a"},
		{Name: "repo-b", Root: "/src/repo-b"},
		{Name: "repo-c", Root: "/src/repo-c"},
	}, nil)

	m := newLoadedModel(frontend)
	m.width = 120
	// repo-b's agent worked last, though repo-a's task is newer.
	m.rows[1].status = &core.TaskStatusUpdate{TaskID: "b", ObservedAt: now.Add(-time.Minute)}

	m = press(t, m, tea.KeyPressMsg{Text: "n"})
	require.Contains(t, stripANSI(m.View().Content), "Creating in repo  ctrl+n change repo")
	m.draft.prompt = "fix the retry loop"

	m = press(t, m, tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl})
	require.Equal(t, modeRepoPicker, m.mode)
	require.Equal(t, []string{"repo-b", "repo-a", "repo-c"}, repoNames(m.draft.repos))

	m = press(t, m, tea.KeyPressMsg{Text: "j"})
	m = press(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, modePromptInput, m.mode)
	require.Equal(t, "fix the retry loop", m.draft.prompt)
	require.Contains(t, stripANSI(m.View().Content), "Creating in repo-a")

	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runBatchCmd(t, cmd)
	require.Equal(t, "/src/repo-a", frontend.createInput.Cwd)
}

func repoNames(repos []core.KnownRepo) []string {
	names := make([]string, 0, len(repos))
	for _, repo := range repos {
		names = append(names, repo.Name)
	}
	return names
}

func TestRepoPickerView_ScrollsToKeepTheCursorInView(t *testing.T) {
	m := newLoadedModel(newFrontendHarness())
	m.width, m.height = 80, 10
	m.mode = modeRepoPicker
	m.draft.reposLoaded = true
	for i := range 20 {
		m.draft.repos = append(m.draft.repos, core.KnownRepo{Name: fmt.Sprintf("repo-%02d", i), Root: "/src"})
	}
	m.draft.repoSelected = 15

	view := stripANSI(m.View().Content)
	require.LessOrEqual(t, strings.Count(view, "\n")+1, 10)
	require.Contains(t, view, "repo-15")
	require.NotContains(t, view, "repo-00")
	require.Contains(t, view, taskListScrollbarThumb)
}

func TestPromptInputView_NamesTheRepoOfTheWorktreeRigWasLaunchedIn(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{
		{ID: "a", RepoRoot: "/src/rig", RepoName: "rig", WorktreePath: "/src/rig_fix-flaky-test"},
	}
	m := newLoadedModel(frontend)
	m.width = 120
	m.launchCwd = "/src/rig_fix-flaky-test/internal"

	m = press(t, m, tea.KeyPressMsg{Text: "n"})
	require.Contains(t, stripANSI(m.View().Content), "Creating in rig  ctrl+n change repo")
}
