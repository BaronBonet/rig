package tui

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/BaronBonet/rig/internal/core"
)

// setupFinishes describes the two ways setup runs: `rig setup`, which quits
// once setup finishes, and the setup the TUI starts with, which goes on to
// the task list.
var setupFinishes = []struct {
	name      string
	setupOnly bool
}{
	{name: "rig setup", setupOnly: true},
	{name: "setup in the TUI", setupOnly: false},
}

// repoDirsStep returns a model on the repo directories step, launched from
// /tmp/repo, once it has loaded the directories it starts with.
func repoDirsStep(t *testing.T, frontend *frontendHarness, setupOnly bool) model {
	t.Helper()
	m := newLoadedModel(frontend)
	m.setupOnly = setupOnly
	m.mode = modeProviderSetup

	next, cmd := m.enterRepoDirsSetupMode()
	got := asModel(t, next)
	require.Equal(t, modeRepoDirsSetup, got.mode)
	next, _ = got.Update(runCmd(t, cmd))
	return asModel(t, next)
}

func typeText(t *testing.T, m model, text string) model {
	t.Helper()
	for _, r := range text {
		m = pressKey(t, m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return m
}

// requireSetupFinished checks that setup finished the way it should after
// the repo directories step: rig setup quits, and setup in the TUI goes on
// to the task list, reloading it.
func requireSetupFinished(t *testing.T, setupOnly bool, got model, cmd tea.Cmd) {
	t.Helper()
	if setupOnly {
		require.Equal(t, tea.Quit(), runCmd(t, cmd))
		return
	}
	require.Equal(t, modeBrowse, got.mode)
	require.Equal(t, repoDirsFormState{}, got.repoDirsForm)
	require.IsType(t, tasksLoadedMsg{}, runCmd(t, cmd))
}

func TestModel_SavedProviderSetupOpensTheRepoDirsStep(t *testing.T) {
	tests := []struct {
		name      string
		setupOnly bool
		setup     *core.ProviderSetup
	}{
		{
			name:      "rig setup",
			setupOnly: true,
			setup: &core.ProviderSetup{
				Configured: []core.Provider{core.ProviderCodex},
				Default:    core.ProviderCodex,
			},
		},
		{name: "first-run setup", setup: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			frontend := newFrontendHarness()
			frontend.providerSetup = tt.setup
			frontend.detections = []core.ProviderDetection{{Provider: core.ProviderCodex, Ready: true}}
			m := newModel(frontend.mock, "/work/backend", "")
			m.setupOnly = tt.setupOnly

			next, cmd := m.Update(providerSetupLoadedMsg{setup: tt.setup})
			got := asModel(t, next)
			require.Equal(t, modeProviderSetup, got.mode)
			next, _ = got.Update(runCmd(t, cmd))
			got = asModel(t, next)
			next, saveCmd := got.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			got = asModel(t, next)
			next, cmd = got.Update(runCmd(t, saveCmd))
			got = asModel(t, next)

			require.NotNil(t, frontend.savedProviderSetup)
			require.Equal(t, modeRepoDirsSetup, got.mode)
			require.IsType(t, repoDirsLoadedMsg{}, runCmd(t, cmd))
			require.Equal(t, 1, frontend.getRepoDirsCalls)
			require.Equal(t, []string{"/work/backend"}, frontend.suggestRepoDirCwds)
		})
	}
}

// Setup is required only for providers: a user who has one is never sent
// into setup, whether or not they have repo directories.
func TestModel_StartWithProviderSetupDoesNotOpenTheRepoDirsStep(t *testing.T) {
	frontend := newFrontendHarness()
	m := newModel(frontend.mock, "/work/backend", "")

	next, cmd := m.Update(providerSetupLoadedMsg{setup: frontend.providerSetup})
	got := asModel(t, next)

	require.Nil(t, cmd)
	require.Equal(t, modeBrowse, got.mode)
	require.Zero(t, frontend.getRepoDirsCalls)
}

func TestModel_RepoDirsStepStartsWithTheUsersRepoDirs(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.repoDirs = []string{"/work", "/code"}
	frontend.repoDirSuggestion = "/tmp"

	got := repoDirsStep(t, frontend, true)

	require.Equal(t, "/work\n/code", got.repoDirsForm.input.Value())
	require.True(t, got.repoDirsForm.input.Focused())
	require.Empty(t, frontend.suggestRepoDirCwds)
}

func TestModel_RepoDirsStepStartsWithTheSuggestionWithoutRepoDirs(t *testing.T) {
	tests := []struct {
		name       string
		suggestion string
		suggestErr error
		want       string
	}{
		{name: "a suggestion", suggestion: "/tmp", want: "/tmp"},
		{name: "no suggestion", want: ""},
		// The suggestion only prefills the step.
		{name: "the suggestion fails", suggestErr: errors.New("daemon unavailable"), want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			frontend := newFrontendHarness()
			frontend.repoDirSuggestion = tt.suggestion
			frontend.suggestRepoDirErr = tt.suggestErr

			got := repoDirsStep(t, frontend, true)

			require.Equal(t, []string{"/tmp/repo"}, frontend.suggestRepoDirCwds)
			require.Equal(t, tt.want, got.repoDirsForm.input.Value())
			require.True(t, got.repoDirsForm.input.Focused())
			require.NoError(t, got.repoDirsForm.err)
		})
	}
}

func TestModel_RepoDirsStepShowsWhyTheRepoDirsCouldNotLoad(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.getRepoDirsErr = errors.New("read the repo directories: decode user config")

	got := repoDirsStep(t, frontend, true)

	require.Empty(t, got.repoDirsForm.input.Value())
	require.Contains(t, stripANSI(got.View().Content), "Error: read the repo directories: decode user config")
}

func TestModel_RepoDirsStepInsertsANewLineWithoutSaving(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{
		{Code: 'j', Mod: tea.ModCtrl},
		{Code: tea.KeyEnter, Mod: tea.ModShift},
		{Code: tea.KeyEnter, Mod: tea.ModAlt},
	} {
		t.Run(key.String(), func(t *testing.T) {
			frontend := newFrontendHarness()
			frontend.repoDirs = []string{"/work"}
			got := repoDirsStep(t, frontend, true)

			got = pressKey(t, got, key)

			require.Equal(t, "/work\n", got.repoDirsForm.input.Value())
			require.False(t, got.repoDirsForm.saving)
			require.Empty(t, frontend.savedRepoDirs)
		})
	}
}

func TestModel_RepoDirsStepSavesTheEditedLinesAndFinishesSetup(t *testing.T) {
	for _, tt := range setupFinishes {
		t.Run(tt.name, func(t *testing.T) {
			frontend := newFrontendHarness()
			frontend.repoDirSuggestion = "/work"
			got := repoDirsStep(t, frontend, tt.setupOnly)

			got = pressKey(t, got, tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
			got = typeText(t, got, "~/code")
			next, saveCmd := got.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			got = asModel(t, next)
			require.True(t, got.repoDirsForm.saving)
			require.Contains(t, stripANSI(got.View().Content), "Saving repo directories...")
			next, cmd := got.Update(runCmd(t, saveCmd))
			got = asModel(t, next)

			require.Equal(t, [][]string{{"/work", "~/code"}}, frontend.savedRepoDirs)
			requireSetupFinished(t, tt.setupOnly, got, cmd)
		})
	}
}

func TestModel_RepoDirsStepKeepsTheUserThereWhenSavingFails(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.saveRepoDirsErr = errors.New(`repo directory "code" must be an absolute path or start with ~/`)
	got := repoDirsStep(t, frontend, true)
	got.width = 120

	got = typeText(t, got, "code")
	next, saveCmd := got.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	got = asModel(t, next)
	next, cmd := got.Update(runCmd(t, saveCmd))
	got = asModel(t, next)

	require.Nil(t, cmd)
	require.Equal(t, modeRepoDirsSetup, got.mode)
	require.False(t, got.repoDirsForm.saving)
	require.Equal(t, "code", got.repoDirsForm.input.Value())
	require.Contains(
		t,
		stripANSI(got.View().Content),
		`Error: repo directory "code" must be an absolute path or start with ~/`,
	)

	// Fixing the directory clears the error.
	got = pressKey(t, got, tea.KeyPressMsg{Code: tea.KeyHome})
	got = typeText(t, got, "/")
	require.Equal(t, "/code", got.repoDirsForm.input.Value())
	require.NoError(t, got.repoDirsForm.err)
}

func TestModel_EscSkipsTheRepoDirsStepWithoutSaving(t *testing.T) {
	for _, tt := range setupFinishes {
		t.Run(tt.name, func(t *testing.T) {
			frontend := newFrontendHarness()
			frontend.repoDirs = []string{"/work"}
			got := repoDirsStep(t, frontend, tt.setupOnly)
			got = typeText(t, got, "/code")

			next, cmd := got.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
			got = asModel(t, next)

			require.Empty(t, frontend.savedRepoDirs)
			requireSetupFinished(t, tt.setupOnly, got, cmd)
		})
	}
}

// Saving before the step has loaded would replace the user's repo
// directories with none.
func TestModel_RepoDirsStepDoesNotSaveBeforeItLoads(t *testing.T) {
	frontend := newFrontendHarness()
	m := newLoadedModel(frontend)
	m.mode = modeProviderSetup
	next, _ := m.enterRepoDirsSetupMode()
	got := asModel(t, next)

	next, cmd := got.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	got = asModel(t, next)

	require.Nil(t, cmd)
	require.False(t, got.repoDirsForm.saving)
	require.Contains(t, stripANSI(got.View().Content), "Loading repo directories...")
}

func TestModel_RepoDirsStepExplainsTheDirectoriesAndShowsItsKeys(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.repoDirs = []string{"/work"}
	got := repoDirsStep(t, frontend, true)
	got.width = 200

	view := stripANSI(got.View().Content)

	require.Contains(t, view, "Repo directories")
	require.Contains(t, view, "Optional. Rig lists the git repositories directly inside these directories, "+
		"so agents can find a repository by name. Worktrees are skipped. One path per line, absolute or "+
		"starting with ~/.")
	require.Contains(t, view, "┃ /work")
	require.Contains(t, view, "enter save · ctrl+j new line · esc skip")
}

// The box shows six lines at a time, and scrolls through any more.
func TestModel_RepoDirsStepTakesMoreLinesThanItsBoxShows(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.repoDirs = []string{"/a", "/b", "/c", "/d", "/e", "/f"}
	got := repoDirsStep(t, frontend, true)

	got = pressKey(t, got, tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
	got = typeText(t, got, "/g")

	require.Equal(t, "/a\n/b\n/c\n/d\n/e\n/f\n/g", got.repoDirsForm.input.Value())
	require.Contains(t, stripANSI(got.View().Content), "┃ /g")
}

// Leaving the step while it loads, then running setup again, leaves the
// first load's reply in flight; it must not fill the step the second load
// opened.
func TestModel_RepoDirsStepIgnoresTheReplyToAnEarlierLoad(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.detections = []core.ProviderDetection{{Provider: core.ProviderCodex, Ready: true}}
	m := newLoadedModel(frontend)
	m.mode = modeProviderSetup
	next, firstLoad := m.enterRepoDirsSetupMode()
	got := pressKey(t, asModel(t, next), tea.KeyPressMsg{Code: tea.KeyEscape})
	require.Equal(t, modeBrowse, got.mode)

	next, cmd := got.Update(tea.KeyPressMsg{Code: 'S', Text: "S"})
	got = asModel(t, next)
	require.Equal(t, modeProviderSetup, got.mode)
	next, _ = got.Update(runCmd(t, cmd))
	got = asModel(t, next)
	next, cmd = got.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	got = asModel(t, next)
	next, secondLoad := got.Update(runCmd(t, cmd))
	got = asModel(t, next)
	require.Equal(t, modeRepoDirsSetup, got.mode)

	frontend.repoDirs = []string{"/old"}
	stale := runCmd(t, firstLoad)
	frontend.repoDirs = []string{"/work"}
	fresh := runCmd(t, secondLoad)

	next, cmd = got.Update(stale)
	got = asModel(t, next)
	require.Nil(t, cmd)
	require.True(t, got.repoDirsForm.loading)
	require.Empty(t, got.repoDirsForm.input.Value())

	next, _ = got.Update(fresh)
	got = asModel(t, next)
	require.Equal(t, "/work", got.repoDirsForm.input.Value())

	// Nor does it replace what the user has typed since.
	got = pressKey(t, got, tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
	got = typeText(t, got, "/code")
	next, cmd = got.Update(stale)
	got = asModel(t, next)

	require.Nil(t, cmd)
	require.Equal(t, "/work\n/code", got.repoDirsForm.input.Value())
}

func TestModel_RepoDirsStepIgnoresALoadReplyAfterTheUserLeft(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.repoDirs = []string{"/work"}
	m := newLoadedModel(frontend)
	m.mode = modeProviderSetup
	next, load := m.enterRepoDirsSetupMode()
	got := pressKey(t, asModel(t, next), tea.KeyPressMsg{Code: tea.KeyEscape})

	next, cmd := got.Update(runCmd(t, load))
	got = asModel(t, next)

	require.Nil(t, cmd)
	require.Equal(t, modeBrowse, got.mode)
	require.Equal(t, repoDirsFormState{}, got.repoDirsForm)
}

func TestModel_RepoDirsStepIgnoresASaveReplyAfterTheUserLeft(t *testing.T) {
	for _, reply := range []repoDirsSavedMsg{{}, {err: errors.New("permission denied")}} {
		frontend := newFrontendHarness()
		got := repoDirsStep(t, frontend, false)
		got = pressKey(t, got, tea.KeyPressMsg{Code: tea.KeyEscape})
		require.Equal(t, modeBrowse, got.mode)

		next, cmd := got.Update(reply)
		got = asModel(t, next)

		require.Nil(t, cmd)
		require.Equal(t, modeBrowse, got.mode)
		require.NoError(t, got.err)
		require.Equal(t, repoDirsFormState{}, got.repoDirsForm)
	}
}

func TestModel_RepoDirsStepIgnoresKeysWhileSaving(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.repoDirs = []string{"/work"}
	got := repoDirsStep(t, frontend, true)
	next, saveCmd := got.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	got = asModel(t, next)
	require.True(t, got.repoDirsForm.saving)

	for _, msg := range []tea.Msg{
		tea.KeyPressMsg{Code: tea.KeyEscape},
		tea.KeyPressMsg{Code: tea.KeyEnter},
		tea.KeyPressMsg{Code: 'x', Text: "x"},
		tea.PasteMsg{Content: "/code"},
	} {
		next, cmd := got.Update(msg)
		got = asModel(t, next)
		require.Nil(t, cmd)
	}

	require.Equal(t, modeRepoDirsSetup, got.mode)
	require.True(t, got.repoDirsForm.saving)
	require.Equal(t, "/work", got.repoDirsForm.input.Value())
	require.Empty(t, frontend.savedRepoDirs)
	next, cmd := got.Update(runCmd(t, saveCmd))
	require.Equal(t, [][]string{{"/work"}}, frontend.savedRepoDirs)
	requireSetupFinished(t, true, asModel(t, next), cmd)
}

// When the user's repo directories cannot be loaded, the step starts empty,
// and saving it as it is would delete them unseen.
func TestModel_RepoDirsStepSkipsAnUneditedStepAfterALoadFailure(t *testing.T) {
	for _, tt := range setupFinishes {
		t.Run(tt.name, func(t *testing.T) {
			frontend := newFrontendHarness()
			frontend.getRepoDirsErr = errors.New("read the repo directories: decode user config")
			got := repoDirsStep(t, frontend, tt.setupOnly)

			next, cmd := got.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			got = asModel(t, next)

			require.Empty(t, frontend.savedRepoDirs)
			requireSetupFinished(t, tt.setupOnly, got, cmd)
		})
	}
}

func TestModel_RepoDirsStepSavesDirectoriesTypedAfterALoadFailure(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.getRepoDirsErr = errors.New("read the repo directories: decode user config")
	got := repoDirsStep(t, frontend, true)
	got = typeText(t, got, "/work")

	next, saveCmd := got.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	got = asModel(t, next)
	next, cmd := got.Update(runCmd(t, saveCmd))

	require.Equal(t, [][]string{{"/work"}}, frontend.savedRepoDirs)
	requireSetupFinished(t, true, asModel(t, next), cmd)
}

// The TUI's first start with no provider setup runs setup, the provider
// step and then the repo directories step, and goes on to the task list.
func TestModel_FirstRunSetupGoesThroughBothStepsToTheTaskList(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.providerSetup = nil
	frontend.detections = []core.ProviderDetection{{Provider: core.ProviderCodex, Ready: true}}
	frontend.repoDirSuggestion = "/work"
	frontend.listTasks = []*core.Task{
		{ID: "task-1", DisplayName: "first task", RepoName: "backend", Provider: core.ProviderCodex},
	}
	m := newModel(frontend.mock, "/work/backend", "")

	msgs := runBatchCmd(t, m.Init())
	next, cmd := m.Update(requireMsgType[providerSetupLoadedMsg](t, msgs))
	got := asModel(t, next)
	next, _ = got.Update(requireMsgType[tasksLoadedMsg](t, msgs))
	got = asModel(t, next)
	require.Equal(t, modeProviderSetup, got.mode)

	next, _ = got.Update(runCmd(t, cmd))
	got = asModel(t, next)
	next, cmd = got.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	got = asModel(t, next)
	next, cmd = got.Update(runCmd(t, cmd))
	got = asModel(t, next)
	require.Equal(t, modeRepoDirsSetup, got.mode)
	require.Equal(t, &core.ProviderSetup{
		Configured: []core.Provider{core.ProviderCodex},
		Default:    core.ProviderCodex,
	}, frontend.savedProviderSetup)

	next, _ = got.Update(runCmd(t, cmd))
	got = asModel(t, next)
	require.Equal(t, "/work", got.repoDirsForm.input.Value())
	next, cmd = got.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	got = asModel(t, next)
	next, cmd = got.Update(runCmd(t, cmd))
	got = asModel(t, next)
	require.Equal(t, [][]string{{"/work"}}, frontend.savedRepoDirs)
	require.Equal(t, modeBrowse, got.mode)
	require.True(t, got.loading)

	next, _ = got.Update(runCmd(t, cmd))
	got = asModel(t, next)
	require.False(t, got.loading)
	require.Contains(t, stripANSI(got.View().Content), "first task")
}
