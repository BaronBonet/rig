package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/BaronBonet/rig/internal/core"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"
)

func multiProviderFrontend() *frontendHarness {
	frontend := newFrontendHarness()
	frontend.providerSetup = &core.ProviderSetup{
		Configured: []core.Provider{core.ProviderCodex, core.ProviderClaude},
		Default:    core.ProviderCodex,
	}
	return frontend
}

func asModel(t *testing.T, next tea.Model) model {
	t.Helper()
	got, ok := next.(model)
	require.True(t, ok)
	return got
}

func TestModel_MissingProviderSetupGatesTUIBehindSetup(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.providerSetup = nil
	frontend.detections = []core.ProviderDetection{
		{Provider: core.ProviderCodex, Ready: true},
		{Provider: core.ProviderClaude, Ready: false, Detail: "claude binary not found"},
	}

	m := newTestModel(frontend.mock)
	msgs := runBatchCmd(t, m.Init())
	setupMsg := requireMsgType[providerSetupLoadedMsg](t, msgs)

	next, cmd := m.Update(setupMsg)
	got := asModel(t, next)
	require.Equal(t, modeProviderSetup, got.mode)
	require.NotNil(t, cmd)

	got = feed(t, got, dataMsgs(t, cmd)...)
	require.Equal(t, 1, frontend.detectProvidersCalls)
	require.False(t, got.setupForm.detecting)

	view := stripANSI(got.View().Content)
	require.Contains(t, view, "Provider setup")
	require.Contains(t, view, "codex")
	require.Contains(t, view, "unavailable")
	require.Contains(t, view, "claude binary not found")
	// Only the detected provider is preselected.
	require.True(t, got.setupForm.rows[0].enabled)
	require.False(t, got.setupForm.rows[1].enabled)
}

func TestModel_ProviderSetupSaveRequiresAtLeastOneProvider(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.providerSetup = nil
	frontend.detections = []core.ProviderDetection{
		{Provider: core.ProviderCodex, Ready: true},
	}

	m := newLoadedModel(frontend)
	m.providerSetup = nil
	next, cmd := m.enterProviderSetupMode()
	got := feed(t, asModel(t, next), dataMsgs(t, cmd)...)

	// Toggle codex off, then try to save.
	next, _ = got.Update(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	got = asModel(t, next)
	require.False(t, got.setupForm.rows[0].enabled)

	next, saveCmd := got.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	got = asModel(t, next)
	require.Nil(t, saveCmd)
	require.ErrorContains(t, got.setupForm.err, "at least one provider")
	require.Nil(t, frontend.savedProviderSetup)
}

func TestModel_ProviderSetupSavesSelectionAndDefault(t *testing.T) {
	frontend := multiProviderFrontend()
	frontend.detections = []core.ProviderDetection{
		{Provider: core.ProviderCodex, Ready: true},
		{Provider: core.ProviderClaude, Ready: true},
	}

	m := newLoadedModel(frontend)
	m.providerSetup = frontend.providerSetup
	next, cmd := m.enterProviderSetupMode()
	got := feed(t, asModel(t, next), dataMsgs(t, cmd)...)

	// Rerunning setup preserves the existing choices.
	require.True(t, got.setupForm.rows[0].enabled)
	require.True(t, got.setupForm.rows[1].enabled)
	require.Equal(t, core.ProviderCodex, got.setupForm.defaultProvider)

	// Move to claude and make it the default.
	next, _ = got.Update(tea.KeyPressMsg{Text: "j"})
	got = asModel(t, next)
	next, _ = got.Update(tea.KeyPressMsg{Text: "d"})
	got = asModel(t, next)
	require.Equal(t, core.ProviderClaude, got.setupForm.defaultProvider)

	next, saveCmd := got.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	got = asModel(t, next)
	require.True(t, got.setupForm.saving)
	require.NotNil(t, saveCmd)

	next, _ = got.Update(runCmd(t, saveCmd))
	got = asModel(t, next)
	require.Equal(t, modeRepoDirsSetup, got.mode)
	require.NotNil(t, frontend.savedProviderSetup)
	require.Equal(t, core.ProviderClaude, frontend.savedProviderSetup.Default)
	require.Equal(
		t,
		[]core.Provider{core.ProviderCodex, core.ProviderClaude},
		frontend.savedProviderSetup.Configured,
	)
	require.Equal(t, core.ProviderClaude, got.providerSetup.Default)
}

func TestModel_ProviderSetupSaveFailureShowsError(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.detections = []core.ProviderDetection{{Provider: core.ProviderCodex, Ready: true}}
	frontend.saveProviderSetupErr = errors.New("install codex hooks: permission denied")

	m := newLoadedModel(frontend)
	next, cmd := m.enterProviderSetupMode()
	got := feed(t, asModel(t, next), dataMsgs(t, cmd)...)

	next, saveCmd := got.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	got = asModel(t, next)
	next, _ = got.Update(runCmd(t, saveCmd))
	got = asModel(t, next)

	require.Equal(t, modeProviderSetup, got.mode)
	require.ErrorContains(t, got.setupForm.err, "permission denied")
}

func TestModel_TabCyclesConfiguredProvidersWhileComposing(t *testing.T) {
	frontend := multiProviderFrontend()
	m := newLoadedModel(frontend)
	m.providerSetup = frontend.providerSetup
	m.mode = modePromptInput

	require.Equal(t, core.ProviderCodex, m.effectiveCreateProvider())

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	got := asModel(t, next)
	require.Equal(t, core.ProviderClaude, got.effectiveCreateProvider())

	view := stripANSI(got.View().Content)
	require.Contains(t, view, "claude")
	require.Contains(t, view, "tab")

	next, _ = got.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	got = asModel(t, next)
	require.Equal(t, core.ProviderCodex, got.effectiveCreateProvider())
}

func TestModel_TabIsANoOpWithASingleConfiguredProvider(t *testing.T) {
	frontend := newFrontendHarness()
	m := newLoadedModel(frontend)
	m.mode = modePromptInput

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	got := asModel(t, next)

	require.Equal(t, core.ProviderCodex, got.effectiveCreateProvider())
	view := stripANSI(got.View().Content)
	require.NotContains(t, view, "tab cycle")
}

func TestModel_SubmitPromptUsesSelectedProvider(t *testing.T) {
	frontend := multiProviderFrontend()
	frontend.createTaskEvents = []core.TaskCreateEvent{
		{Task: &core.Task{ID: "task-9", DisplayName: "new task", Provider: core.ProviderClaude}},
	}

	m := newLoadedModel(frontend)
	m.providerSetup = frontend.providerSetup
	m.mode = modePromptInput
	m.draft.prompt = "add billing retry flow"

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	got := asModel(t, next)

	_, cmd := got.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	runBatchCmd(t, cmd)

	require.Equal(t, core.ProviderClaude, frontend.createInput.Provider)
}

func TestModel_PRPickerUsesSelectedProvider(t *testing.T) {
	frontend := multiProviderFrontend()
	frontend.createTaskEvents = []core.TaskCreateEvent{
		{Task: &core.Task{ID: "task-9", DisplayName: "auth rewrite", Provider: core.ProviderClaude}},
	}

	m := newLoadedModel(frontend)
	m.providerSetup = frontend.providerSetup
	m.mode = modePRPicker
	m.draft.repoRoot = "/tmp/repo"
	m.draft.prs = []core.RepoPullRequest{{Number: 42, Title: "Auth rewrite", BranchName: "feat/auth"}}

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	got := asModel(t, next)

	_, cmd := got.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	runBatchCmd(t, cmd)

	require.Equal(t, core.ProviderClaude, frontend.createInput.Provider)
	require.NotNil(t, frontend.createInput.Source.PullRequest)
}

// Rig no longer starts agents itself: the user splits a pane in the task's
// Session and runs one there, and Rig picks it up at its first hook.
func TestModel_PStartsNoAgent(t *testing.T) {
	frontend := multiProviderFrontend()
	m := agentsTaskModel(frontend)
	m = withAgentSessions(t, m, "task-1", claudeAgent(), codexAgent())

	for name, model := range map[string]model{
		"task row": m,
		"sub-row":  pressKey(t, m, tea.KeyPressMsg{Code: 'j', Text: "j"}),
	} {
		t.Run(name, func(t *testing.T) {
			next, cmd := model.Update(tea.KeyPressMsg{Text: "p"})
			got := asModel(t, next)

			require.Nil(t, cmd)
			require.Equal(t, modeBrowse, got.mode)
			require.Equal(t, model.View().Content, got.View().Content)
			require.Zero(t, frontend.attachTaskSessionCalls)
		})
	}
}

func TestModel_ListKeybindsOfferNoNewAgent(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{
		{ID: "task-1", DisplayName: "first task", RepoName: "repo-a", Provider: core.ProviderCodex},
	}
	m := newLoadedModel(frontend)

	keybinds := stripANSI(m.listKeybindText())
	require.NotContains(t, keybinds, "new agent")
	require.NotContains(t, keybinds, "p ")
	require.Contains(t, keybinds, "n new")
}

var (
	spaceKey = tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	downKey  = tea.KeyPressMsg{Code: 'j', Text: "j"}
	escKey   = tea.KeyPressMsg{Code: tea.KeyEscape}
	setupKey = tea.KeyPressMsg{Code: 'S', Text: "S"}
)

// setupFrontend is launchChoiceFrontend with both providers detected ready.
func setupFrontend(defaults map[core.Provider]core.LaunchChoice) *frontendHarness {
	frontend := launchChoiceFrontend(defaults)
	frontend.detections = []core.ProviderDetection{
		{Provider: core.ProviderCodex, Ready: true},
		{Provider: core.ProviderClaude, Ready: true},
	}
	return frontend
}

// codexOnlySetup configures Codex alone, leaving Claude ready but unconfigured.
func codexOnlySetup(frontend *frontendHarness) *frontendHarness {
	frontend.providerSetup = &core.ProviderSetup{
		Configured: []core.Provider{core.ProviderCodex},
		Default:    core.ProviderCodex,
	}
	return frontend
}

// skipRepoDirs skips the repo directories step that follows a saved provider
// setup, which takes setup in the TUI on to the task list.
func skipRepoDirs(t *testing.T, m model) model {
	t.Helper()
	require.Equal(t, modeRepoDirsSetup, m.mode)
	next, _ := m.Update(escKey)
	return asModel(t, next)
}

// settle applies msgs to m, then the data messages their commands yield, until
// none is left.
func settle(t *testing.T, m model, msgs ...tea.Msg) model {
	t.Helper()
	for len(msgs) > 0 {
		next, cmd := m.Update(msgs[0])
		m = asModel(t, next)
		msgs = append(msgs[1:], dataMsgs(t, cmd)...)
	}
	return m
}

// openSetup opens the provider setup screen from the task list and applies
// what it fetches, and what that fetches in turn.
func openSetup(t *testing.T, m model) model {
	t.Helper()
	next, cmd := m.Update(setupKey)
	got := settle(t, asModel(t, next), dataMsgs(t, cmd)...)
	require.Equal(t, modeProviderSetup, got.mode)
	return got
}

// openSetupAwaitingDefaults opens the provider setup screen from the task
// list and applies the detection and what it fetches, but returns the Launch
// defaults reply unapplied.
func openSetupAwaitingDefaults(t *testing.T, m model) (model, launchDefaultsLoadedMsg) {
	t.Helper()
	next, cmd := m.Update(setupKey)
	msgs := dataMsgs(t, cmd)
	got := settle(t, asModel(t, next), msgsOf[providerDetectionsMsg](msgs)...)
	require.Equal(t, modeProviderSetup, got.mode)
	return got, requireMsgType[launchDefaultsLoadedMsg](t, msgs)
}

// setupRows is the provider setup screen's rows and the lines under them,
// without styling or trailing spaces.
func setupRows(t *testing.T, m model) []string {
	t.Helper()
	var rows []string
	for _, line := range strings.Split(stripANSI(m.View().Content), "\n") {
		line = strings.TrimRight(line, " ")
		if len(rows) == 0 && !isSetupRow(line) {
			continue
		}
		if line == "" {
			break
		}
		rows = append(rows, line)
	}
	require.NotEmpty(t, rows, "the view has no provider setup rows")
	return rows
}

func isSetupRow(line string) bool {
	return strings.HasPrefix(line, "> [") || strings.HasPrefix(line, "  [")
}

// setupLaunchDefault is the model line under provider's setup row, trimmed
// and without its label, or "" when the row has none.
func setupLaunchDefault(t *testing.T, m model, provider core.Provider) string {
	t.Helper()
	rows := setupRows(t, m)
	for i, row := range rows {
		if !strings.Contains(row, "] "+string(provider)+" ") {
			continue
		}
		if i+1 == len(rows) || isSetupRow(rows[i+1]) {
			return ""
		}
		value, labeled := strings.CutPrefix(strings.TrimSpace(rows[i+1]), "model  ")
		require.True(t, labeled, rows[i+1])
		return value
	}
	t.Fatalf("the provider setup screen has no %s row", provider)
	return ""
}

// setupFooter is the provider setup screen's keybind footer.
func setupFooter(t *testing.T, m model) string {
	t.Helper()
	lines := strings.Split(stripANSI(m.View().Content), "\n")
	return lines[len(lines)-1]
}

// setupLaunchDefaults presses key count times on the provider setup screen and
// collects the selected provider's Launch default each time.
func setupLaunchDefaults(t *testing.T, m model, key tea.KeyPressMsg, count int) []string {
	t.Helper()
	row, ok := m.selectedSetupRow()
	require.True(t, ok)
	var values []string
	for range count {
		m = press(t, m, key)
		values = append(values, setupLaunchDefault(t, m, row.provider))
	}
	return values
}

func TestModel_ProviderSetupShowsEachEnabledProvidersLaunchDefault(t *testing.T) {
	frontend := setupFrontend(map[core.Provider]core.LaunchChoice{
		core.ProviderCodex: {Model: "gpt-6-sol", ReasoningLevel: "high"},
	})
	m := openSetup(t, loadedLaunchChoiceModel(t, frontend))

	require.Equal(t, []string{
		"> [x] codex    ready  (default)",
		"       model  GPT-6-Sol · high",
		"  [x] claude   ready",
		"       model  default",
	}, setupRows(t, m))
}

func TestModel_ProviderSetupReadsALaunchDefaultAsTheDraftDoes(t *testing.T) {
	tests := []struct {
		name   string
		choice core.LaunchChoice
		want   string
	}{
		{"a listed model and level", core.LaunchChoice{Model: "gpt-6-sol", ReasoningLevel: "high"}, "GPT-6-Sol · high"},
		{"a model without a level", core.LaunchChoice{Model: "gpt-6-mini"}, "GPT-6-Mini"},
		{"a level the model lacks", core.LaunchChoice{Model: "gpt-6-fast", ReasoningLevel: "high"}, "GPT-6-Fast"},
		{
			"a model the list lacks",
			core.LaunchChoice{Model: "gpt-4-retired", ReasoningLevel: "xhigh"},
			"gpt-4-retired · xhigh",
		},
		{"a level without a model", core.LaunchChoice{ReasoningLevel: "low"}, "default"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			frontend := setupFrontend(map[core.Provider]core.LaunchChoice{core.ProviderCodex: tt.choice})
			m := openSetup(t, loadedLaunchChoiceModel(t, frontend))

			require.Equal(t, tt.want, setupLaunchDefault(t, m, core.ProviderCodex))
		})
	}
}

func TestModel_ProviderSetupShowsNoLaunchDefaultForADisabledOrUnavailableProvider(t *testing.T) {
	frontend := setupFrontend(map[core.Provider]core.LaunchChoice{
		core.ProviderCodex:  {Model: "gpt-6-sol", ReasoningLevel: "high"},
		core.ProviderClaude: {Model: "opus"},
	})
	frontend.detections[1] = core.ProviderDetection{
		Provider: core.ProviderClaude,
		Detail:   "claude binary not found",
	}
	m := openSetup(t, loadedLaunchChoiceModel(t, frontend))
	require.Equal(t, []string{
		"> [x] codex    ready  (default)",
		"       model  GPT-6-Sol · high",
		"  [-] claude   unavailable",
		"       claude binary not found",
	}, setupRows(t, m))

	require.Equal(t, []string{
		"> [ ] codex    ready",
		"  [-] claude   unavailable",
		"       claude binary not found",
	}, setupRows(t, press(t, m, spaceKey)))
}

func TestModel_ProviderSetupShowsWhileLaunchDefaultsAreMissing(t *testing.T) {
	frontend := setupFrontend(map[core.Provider]core.LaunchChoice{
		core.ProviderCodex: {Model: "gpt-6-sol", ReasoningLevel: "high"},
	})
	m := newLoadedModel(frontend)
	m.width = 120
	m, defaults := openSetupAwaitingDefaults(t, m)

	require.Equal(t, []string{
		"> [x] codex    ready  (default)",
		"       model  loading defaults",
		"  [x] claude   ready",
		"       model  loading defaults",
	}, setupRows(t, m))
	failed := feed(t, m, launchDefaultsLoadedMsg{fetch: defaults.fetch, err: errors.New("config unreadable")})
	require.Equal(t, "defaults unavailable", setupLaunchDefault(t, failed, core.ProviderCodex))
	require.Equal(t, "defaults unavailable", setupLaunchDefault(t, failed, core.ProviderClaude))
	require.Equal(t, "GPT-6-Sol · high", setupLaunchDefault(t, feed(t, m, defaults), core.ProviderCodex))

	// A Launch default the user edited shows whether or not the defaults loaded.
	require.Equal(t, "GPT-6-Sol", setupLaunchDefault(t, press(t, failed, ctrlO), core.ProviderCodex))
}

func TestModel_ProviderSetupShowsTheSelectedRowsModelListState(t *testing.T) {
	frontend := setupFrontend(map[core.Provider]core.LaunchChoice{
		core.ProviderCodex: {Model: "gpt-6-sol", ReasoningLevel: "high"},
	})
	m := openSetup(t, loadedLaunchChoiceModel(t, frontend))
	m.providerModels[core.ProviderCodex] = providerModelsState{}
	m.providerModels[core.ProviderClaude] = providerModelsState{}

	require.Equal(t, []string{
		"> [x] codex    ready  (default)",
		"       model  gpt-6-sol · high  (loading models)",
		"  [x] claude   ready",
		"       model  default",
	}, setupRows(t, m))

	// It follows the cursor.
	moved := press(t, m, downKey)
	require.Equal(t, "gpt-6-sol · high", setupLaunchDefault(t, moved, core.ProviderCodex))
	require.Equal(t, "default  (loading models)", setupLaunchDefault(t, moved, core.ProviderClaude))

	m.providerModels[core.ProviderCodex] = providerModelsState{err: errors.New("codex timed out")}
	require.Equal(t, "gpt-6-sol · high  (models unavailable)", setupLaunchDefault(t, m, core.ProviderCodex))

	// An empty list is an answer, as in the task draft.
	m.providerModels[core.ProviderCodex] = providerModelsState{loaded: true}
	require.Equal(t, "gpt-6-sol · high", setupLaunchDefault(t, m, core.ProviderCodex))
}

// The reasoning level is what gets saved, so a narrow terminal cuts the model
// before it, and drops the model list's state before the model has no room.
func TestModel_ProviderSetupCutsTheModelBeforeTheReasoningLevel(t *testing.T) {
	tests := []struct {
		name    string
		width   int
		loading bool
		want    string
	}{
		{"the model is cut", 40, false, "       model  GPT-6-Sol Extended… · high"},
		{
			"the list's state stays while the model has room",
			50,
			true,
			"       model  gpt-6-sol-… · high  (loading models)",
		},
		{"the list's state goes before the model's room", 40, true, "       model  gpt-6-sol-preview · high"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			frontend := setupFrontend(map[core.Provider]core.LaunchChoice{
				core.ProviderCodex: {Model: "gpt-6-sol-preview", ReasoningLevel: "high"},
			})
			frontend.providerModels[core.ProviderCodex] = append(codexModels(), core.ProviderModel{
				ID:              "gpt-6-sol-preview",
				DisplayName:     "GPT-6-Sol Extended Context Research Preview",
				ReasoningLevels: []string{"high"},
			})
			m := openSetup(t, loadedLaunchChoiceModel(t, frontend))
			m.width = tt.width
			if tt.loading {
				m.providerModels = map[core.Provider]providerModelsState{core.ProviderCodex: {}}
			}

			line := setupRows(t, m)[1]
			require.Equal(t, tt.want, line)
			require.LessOrEqual(t, lipgloss.Width(line), tt.width)
		})
	}
}

func TestModel_ProviderSetupKeysCycleTheSelectedRowsLaunchDefault(t *testing.T) {
	open := func() model { return openSetup(t, loadedLaunchChoiceModel(t, setupFrontend(nil))) }

	require.Equal(
		t,
		[]string{"GPT-6-Sol", "GPT-6-Mini", "GPT-6-Fast", "default"},
		setupLaunchDefaults(t, open(), ctrlO, 4),
	)
	require.Equal(
		t,
		[]string{"GPT-6-Sol · low", "GPT-6-Sol · medium", "GPT-6-Sol · high", "GPT-6-Sol"},
		setupLaunchDefaults(t, press(t, open(), ctrlO), ctrlR, 4),
	)
	require.Equal(t, []string{"Opus", "Haiku", "default"}, setupLaunchDefaults(t, press(t, open(), downKey), ctrlO, 3))

	// Each row has its own, which the keys on another row leave alone.
	m := press(t, press(t, press(t, open(), ctrlO), downKey), ctrlO)
	require.Equal(t, "GPT-6-Sol", setupLaunchDefault(t, m, core.ProviderCodex))
	require.Equal(t, "Opus", setupLaunchDefault(t, m, core.ProviderClaude))
}

// The provider setup screen cycles a Launch default by the task draft's rules.
func TestModel_ProviderSetupKeysFollowTheDraftsRules(t *testing.T) {
	tests := []struct {
		name   string
		saved  core.LaunchChoice
		keys   []tea.KeyPressMsg
		want   string
		footer string
	}{
		{
			name:   "without a model there is no reasoning level",
			keys:   []tea.KeyPressMsg{ctrlR},
			want:   "default",
			footer: "space toggle · d default · ctrl+o model · enter save · q quit",
		},
		{
			name:   "a model change keeps a level the new model offers",
			saved:  core.LaunchChoice{Model: "gpt-6-sol", ReasoningLevel: "low"},
			keys:   []tea.KeyPressMsg{ctrlO},
			want:   "GPT-6-Mini · low",
			footer: "space toggle · d default · ctrl+o model · ctrl+r reasoning · enter save · q quit",
		},
		{
			name:   "a model change drops a level the new model lacks",
			saved:  core.LaunchChoice{Model: "gpt-6-sol", ReasoningLevel: "high"},
			keys:   []tea.KeyPressMsg{ctrlO},
			want:   "GPT-6-Mini",
			footer: "space toggle · d default · ctrl+o model · ctrl+r reasoning · enter save · q quit",
		},
		{
			name:   "a dropped level does not come back",
			saved:  core.LaunchChoice{Model: "gpt-6-sol", ReasoningLevel: "high"},
			keys:   []tea.KeyPressMsg{ctrlO, ctrlO, ctrlO, ctrlO},
			want:   "GPT-6-Sol",
			footer: "space toggle · d default · ctrl+o model · ctrl+r reasoning · enter save · q quit",
		},
		{
			name:   "a model without levels takes none",
			saved:  core.LaunchChoice{Model: "gpt-6-fast"},
			keys:   []tea.KeyPressMsg{ctrlR},
			want:   "GPT-6-Fast",
			footer: "space toggle · d default · ctrl+o model · enter save · q quit",
		},
		{
			name:   "a model the list lacks keeps its level",
			saved:  core.LaunchChoice{Model: "gpt-4-retired", ReasoningLevel: "xhigh"},
			keys:   []tea.KeyPressMsg{ctrlR},
			want:   "gpt-4-retired · xhigh",
			footer: "space toggle · d default · ctrl+o model · enter save · q quit",
		},
		{
			name:   "a model the list lacks goes to no model",
			saved:  core.LaunchChoice{Model: "gpt-4-retired", ReasoningLevel: "xhigh"},
			keys:   []tea.KeyPressMsg{ctrlO},
			want:   "default",
			footer: "space toggle · d default · ctrl+o model · enter save · q quit",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			frontend := setupFrontend(map[core.Provider]core.LaunchChoice{core.ProviderCodex: tt.saved})
			m := openSetup(t, loadedLaunchChoiceModel(t, frontend))
			for _, key := range tt.keys {
				m = press(t, m, key)
			}

			require.Equal(t, tt.want, setupLaunchDefault(t, m, core.ProviderCodex))
			require.Equal(t, tt.footer, setupFooter(t, m))
		})
	}
}

func TestModel_ProviderSetupKeysDoNothingWhereTheyHaveNothingToCycle(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(model) model
	}{
		{"a disabled row", func(m model) model { return press(t, m, spaceKey) }},
		{"an unavailable row", func(m model) model {
			m.setupForm.rows[0] = providerSetupRow{provider: core.ProviderCodex, detail: "codex binary not found"}
			return m
		}},
		{"a loading model list", func(m model) model {
			m.providerModels[core.ProviderCodex] = providerModelsState{}
			return m
		}},
		{"an unavailable model list", func(m model) model {
			m.providerModels[core.ProviderCodex] = providerModelsState{err: errors.New("codex timed out")}
			return m
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			frontend := setupFrontend(map[core.Provider]core.LaunchChoice{
				core.ProviderCodex: {Model: "gpt-6-sol", ReasoningLevel: "high"},
			})
			m := tt.mutate(openSetup(t, loadedLaunchChoiceModel(t, frontend)))
			before := m.View().Content
			require.Equal(t, "space toggle · d default · enter save · q quit", setupFooter(t, m))

			m = press(t, press(t, m, ctrlO), ctrlR)
			require.Equal(t, before, m.View().Content)
			require.Empty(t, m.setupForm.edits)
		})
	}
}

// An edit made before the Launch defaults load would start from no choice, and
// enter would save it over the stored one.
func TestModel_ProviderSetupKeysWaitForTheLaunchDefaultsOnAnUneditedRow(t *testing.T) {
	frontend := setupFrontend(map[core.Provider]core.LaunchChoice{
		core.ProviderCodex: {Model: "gpt-6-mini", ReasoningLevel: "low"},
	})
	m := newLoadedModel(frontend)
	m.width = 120
	m, defaults := openSetupAwaitingDefaults(t, m)
	require.Equal(t, "space toggle · d default · enter save · q quit", setupFooter(t, m))

	waiting := press(t, press(t, m, ctrlO), ctrlR)
	require.Equal(t, m.View().Content, waiting.View().Content)
	require.Empty(t, waiting.setupForm.edits)

	// Once they load, the keys move on from the stored default.
	loaded := feed(t, m, defaults)
	require.Equal(
		t,
		"space toggle · d default · ctrl+o model · ctrl+r reasoning · enter save · q quit",
		setupFooter(t, loaded),
	)
	require.Equal(t, "GPT-6-Fast", setupLaunchDefault(t, press(t, loaded, ctrlO), core.ProviderCodex))

	// Once they fail, from no choice, as the task draft does.
	failed := feed(t, m, launchDefaultsLoadedMsg{fetch: defaults.fetch, err: errors.New("config unreadable")})
	require.Equal(t, "GPT-6-Sol", setupLaunchDefault(t, press(t, failed, ctrlO), core.ProviderCodex))
}

func TestModel_ProviderSetupFooterHintsFollowTheSelectedRow(t *testing.T) {
	frontend := setupFrontend(map[core.Provider]core.LaunchChoice{
		core.ProviderCodex: {Model: "gpt-6-sol", ReasoningLevel: "high"},
	})
	m := openSetup(t, loadedLaunchChoiceModel(t, frontend))

	require.Equal(
		t,
		"space toggle · d default · ctrl+o model · ctrl+r reasoning · enter save · q quit",
		setupFooter(t, m),
	)
	m = press(t, m, downKey)
	require.Equal(t, "space toggle · d default · ctrl+o model · enter save · q quit", setupFooter(t, m))
	m = press(t, m, spaceKey)
	require.Equal(t, "space toggle · d default · enter save · q quit", setupFooter(t, m))
}

func TestModel_ProviderSetupFooterWrapsOnANarrowTerminal(t *testing.T) {
	frontend := setupFrontend(map[core.Provider]core.LaunchChoice{
		core.ProviderCodex: {Model: "gpt-6-sol", ReasoningLevel: "high"},
	})
	m := openSetup(t, loadedLaunchChoiceModel(t, frontend))
	m.width = 60

	lines := strings.Split(stripANSI(m.View().Content), "\n")
	require.Equal(t, []string{
		"space toggle · d default · ctrl+o model · ctrl+r reasoning",
		"enter save · q quit",
	}, lines[len(lines)-2:])
}

// Launch defaults loaded earlier may have changed since, as creating a task
// elsewhere saves them.
func TestModel_EnteringProviderSetupFetchesTheLaunchDefaults(t *testing.T) {
	frontend := setupFrontend(nil)
	m := loadedLaunchChoiceModel(t, frontend)
	frontend.launchDefaults = map[core.Provider]core.LaunchChoice{
		core.ProviderCodex: {Model: "gpt-6-sol", ReasoningLevel: "high"},
	}

	next, cmd := m.Update(setupKey)
	msgs := dataMsgs(t, cmd)
	require.Len(t, msgsOf[launchDefaultsLoadedMsg](msgs), 1)
	require.Equal(t, 2, frontend.launchDefaultsCalls)
	_, hasDeadline := frontend.launchDefaultsContext.Deadline()
	require.True(t, hasDeadline)

	m = settle(t, asModel(t, next), msgs...)
	require.Equal(t, "GPT-6-Sol · high", setupLaunchDefault(t, m, core.ProviderCodex))
}

func TestModel_ProviderDetectionsFetchTheModelsOfEachReadyProvider(t *testing.T) {
	frontend := codexOnlySetup(setupFrontend(nil))
	m := loadedLaunchChoiceModel(t, frontend)
	require.Equal(t, []core.Provider{core.ProviderCodex}, frontend.providerModelsCalls)

	// Claude is ready but not configured, and Codex's list loaded already.
	m = openSetup(t, m)
	require.Equal(t, []core.Provider{core.ProviderCodex, core.ProviderClaude}, frontend.providerModelsCalls)
	_, hasDeadline := frontend.providerModelsContexts[1].Deadline()
	require.True(t, hasDeadline)
	m = press(t, press(t, press(t, m, downKey), spaceKey), ctrlO)
	require.Equal(t, "Opus", setupLaunchDefault(t, m, core.ProviderClaude))

	// An unavailable provider's list is not fetched.
	frontend.providerModelsCalls = nil
	frontend.detections[1].Ready = false
	m.providerModels = map[core.Provider]providerModelsState{}
	openSetup(t, press(t, m, escKey))
	require.Equal(t, []core.Provider{core.ProviderCodex}, frontend.providerModelsCalls)
}

func TestModel_ProviderDetectionsFetchAFailedModelListAgain(t *testing.T) {
	frontend := setupFrontend(nil)
	frontend.providerModelsErr = map[core.Provider]error{core.ProviderCodex: errors.New("codex timed out")}
	m := loadedLaunchChoiceModel(t, frontend)

	frontend.providerModelsErr = nil
	frontend.providerModelsCalls = nil
	m = openSetup(t, m)
	require.Equal(t, []core.Provider{core.ProviderCodex}, frontend.providerModelsCalls)
	require.Equal(t, "GPT-6-Sol", setupLaunchDefault(t, press(t, m, ctrlO), core.ProviderCodex))
}

func TestModel_ADetectionReplyAfterTheSetupScreenClosesIsDropped(t *testing.T) {
	frontend := codexOnlySetup(setupFrontend(nil))
	m := loadedLaunchChoiceModel(t, frontend)
	next, cmd := m.Update(setupKey)
	detected := requireMsgType[providerDetectionsMsg](t, dataMsgs(t, cmd))
	m = press(t, asModel(t, next), escKey)
	require.Equal(t, modeBrowse, m.mode)

	next, cmd = m.Update(detected)
	m = asModel(t, next)

	require.Nil(t, cmd)
	require.Empty(t, m.setupForm.rows)
	require.Equal(t, []core.Provider{core.ProviderCodex}, frontend.providerModelsCalls)
}

// The user leaves the setup screen and opens it again before the first
// detection answers.
func TestModel_OnlyTheLatestDetectionReplyBuildsTheSetupRows(t *testing.T) {
	frontend := codexOnlySetup(setupFrontend(nil))
	m := loadedLaunchChoiceModel(t, frontend)
	next, cmd := m.Update(setupKey)
	first := requireMsgType[providerDetectionsMsg](t, dataMsgs(t, cmd))
	next, cmd = asModel(t, press(t, asModel(t, next), escKey)).Update(setupKey)
	m = asModel(t, next)
	latest := dataMsgs(t, cmd)

	m = settle(t, m, first)
	require.True(t, m.setupForm.detecting)
	require.Contains(t, stripANSI(m.View().Content), "Checking provider availability")

	m = settle(t, m, latest...)
	m = press(t, press(t, press(t, m, downKey), spaceKey), ctrlO)
	edited := []string{
		"  [x] codex    ready  (default)",
		"       model  default",
		"> [x] claude   ready",
		"       model  Opus",
	}
	require.Equal(t, edited, setupRows(t, m))
	// Arriving late, it does not rebuild them either.
	require.Equal(t, edited, setupRows(t, settle(t, m, first)))
}

func TestModel_LateLaunchDefaultsFillOnlyTheProviderSetupRowsTheUserHasNotEdited(t *testing.T) {
	frontend := setupFrontend(map[core.Provider]core.LaunchChoice{
		core.ProviderCodex: {Model: "gpt-6-mini", ReasoningLevel: "low"},
	})
	m := loadedLaunchChoiceModel(t, frontend)
	// Claude's Launch default was saved elsewhere since they loaded.
	frontend.launchDefaults = map[core.Provider]core.LaunchChoice{
		core.ProviderCodex:  {Model: "gpt-6-mini", ReasoningLevel: "low"},
		core.ProviderClaude: {Model: "opus", ReasoningLevel: "max"},
	}
	m, late := openSetupAwaitingDefaults(t, m)
	require.Equal(t, "default", setupLaunchDefault(t, m, core.ProviderClaude))

	m = feed(t, press(t, m, ctrlO), late)

	require.Equal(t, []string{
		"> [x] codex    ready  (default)",
		"       model  GPT-6-Fast",
		"  [x] claude   ready",
		"       model  Opus · max",
	}, setupRows(t, m))
}

// The setup screen keeps a level its list dropped from an edit dropped, as
// the task draft does.
func TestModel_AModelListRefreshDropsALevelFromAProviderSetupEdit(t *testing.T) {
	frontend := setupFrontend(nil)
	m := openSetup(t, loadedLaunchChoiceModel(t, frontend))
	m = press(t, press(t, press(t, press(t, m, ctrlO), ctrlR), ctrlR), ctrlR)
	require.Equal(t, "GPT-6-Sol · high", setupLaunchDefault(t, m, core.ProviderCodex))

	refreshed := codexModels()
	refreshed[0].ReasoningLevels = []string{"low", "medium"}
	m = feed(t, m, providerModelsLoadedMsg{provider: core.ProviderCodex, models: refreshed})
	require.Equal(t, "GPT-6-Sol", setupLaunchDefault(t, m, core.ProviderCodex))
	m = feed(t, m, providerModelsLoadedMsg{provider: core.ProviderCodex, models: codexModels()})
	require.Equal(t, "GPT-6-Sol", setupLaunchDefault(t, m, core.ProviderCodex))

	press(t, m, enter)
	require.Equal(
		t,
		map[core.Provider]core.LaunchChoice{core.ProviderCodex: {Model: "gpt-6-sol"}},
		frontend.savedLaunchDefaults,
	)
}

func TestModel_EscDiscardsProviderSetupEdits(t *testing.T) {
	frontend := setupFrontend(map[core.Provider]core.LaunchChoice{
		core.ProviderCodex: {Model: "gpt-6-sol", ReasoningLevel: "high"},
	})
	m := openSetup(t, loadedLaunchChoiceModel(t, frontend))
	m = press(t, press(t, m, ctrlO), escKey)
	require.Equal(t, modeBrowse, m.mode)

	m = openSetup(t, m)

	require.Equal(t, "GPT-6-Sol · high", setupLaunchDefault(t, m, core.ProviderCodex))
	require.Empty(t, frontend.saveCalls)
}

func TestModel_ProviderSetupSavesTheEditedLaunchDefaultsAfterTheSetup(t *testing.T) {
	frontend := setupFrontend(map[core.Provider]core.LaunchChoice{
		core.ProviderCodex:  {Model: "gpt-6-sol", ReasoningLevel: "high"},
		core.ProviderClaude: {Model: "opus", ReasoningLevel: "max"},
	})
	m := openSetup(t, loadedLaunchChoiceModel(t, frontend))

	// Codex moves on to GPT-6-Mini, which lacks high, and Claude back to
	// no choice.
	m = press(t, m, ctrlO)
	m = press(t, press(t, press(t, m, downKey), ctrlO), ctrlO)
	require.Equal(t, "default", setupLaunchDefault(t, m, core.ProviderClaude))

	m = press(t, m, enter)

	require.Equal(t, modeRepoDirsSetup, m.mode)
	require.NoError(t, m.setupForm.err)
	require.Equal(t, []string{"setup", "codex", "claude"}, frontend.saveCalls)
	require.Equal(t, map[core.Provider]core.LaunchChoice{
		core.ProviderCodex:  {Model: "gpt-6-mini"},
		core.ProviderClaude: {},
	}, frontend.savedLaunchDefaults)
	// The setup save installs hooks and runs checks, so it has no deadline;
	// each Launch default save does.
	_, hasDeadline := frontend.saveProviderSetupContext.Deadline()
	require.False(t, hasDeadline)
	require.Len(t, frontend.saveLaunchDefaultContexts, 2)
	for _, ctx := range frontend.saveLaunchDefaultContexts {
		_, hasDeadline := ctx.Deadline()
		require.True(t, hasDeadline)
	}
}

func TestModel_ProviderSetupSavesOnlyLaunchDefaultsEditedForEnabledProviders(t *testing.T) {
	frontend := setupFrontend(map[core.Provider]core.LaunchChoice{
		core.ProviderCodex: {Model: "gpt-6-sol", ReasoningLevel: "high"},
	})
	m := openSetup(t, loadedLaunchChoiceModel(t, frontend))

	// Codex is left alone, and Claude edited, then disabled.
	m = press(t, press(t, press(t, m, downKey), ctrlO), spaceKey)
	m = press(t, m, enter)

	require.Equal(t, modeRepoDirsSetup, m.mode)
	require.Equal(t, []string{"setup"}, frontend.saveCalls)
	require.Empty(t, frontend.savedLaunchDefaults)
}

func TestModel_AFailedProviderSetupSaveSavesNoLaunchDefault(t *testing.T) {
	frontend := setupFrontend(nil)
	frontend.saveProviderSetupErr = errors.New("install codex hooks: permission denied")
	m := openSetup(t, loadedLaunchChoiceModel(t, frontend))

	m = press(t, press(t, m, ctrlO), enter)

	require.Equal(t, modeProviderSetup, m.mode)
	require.ErrorContains(t, m.setupForm.err, "permission denied")
	require.Equal(t, []string{"setup"}, frontend.saveCalls)
	// The edit stays for the next try.
	require.Equal(t, "GPT-6-Sol", setupLaunchDefault(t, m, core.ProviderCodex))
}

// Setup stays open to show a Launch default that failed to save, so enter
// can save it again; the setup itself has saved, so esc leaves.
func TestModel_ALaunchDefaultThatFailsToSaveKeepsProviderSetupOpen(t *testing.T) {
	frontend := setupFrontend(nil)
	frontend.saveLaunchDefaultErr = map[core.Provider]error{core.ProviderCodex: errors.New("config locked")}
	m := openSetup(t, loadedLaunchChoiceModel(t, frontend))

	m = press(t, press(t, press(t, m, ctrlO), downKey), ctrlO)
	m = press(t, m, enter)

	require.Equal(t, modeProviderSetup, m.mode)
	require.Contains(
		t,
		stripANSI(m.View().Content),
		"Error: provider setup saved; save codex launch default: config locked",
	)
	// A failure does not stop the next save.
	require.Equal(t, []string{"setup", "codex", "claude"}, frontend.saveCalls)
	require.Equal(
		t,
		map[core.Provider]core.LaunchChoice{core.ProviderClaude: {Model: "opus"}},
		frontend.savedLaunchDefaults,
	)

	m = press(t, m, escKey)
	require.Equal(t, modeBrowse, m.mode)
	require.Equal(
		t,
		[]string{"claude · tab", "Opus · ctrl+o", "default · ctrl+r"},
		choiceRows(t, press(t, openComposer(t, m), tab)),
	)
}

func TestModel_SetupOnlyModeStaysWhenALaunchDefaultFailsToSave(t *testing.T) {
	frontend := setupFrontend(nil)
	frontend.saveLaunchDefaultErr = map[core.Provider]error{core.ProviderCodex: errors.New("config locked")}
	m := newTestModel(frontend.mock)
	m.setupOnly = true
	m.width = 120
	m = settle(t, m, requireMsgType[providerSetupLoadedMsg](t, runBatchCmd(t, m.Init())))
	m = press(t, press(t, press(t, m, ctrlO), downKey), ctrlO)

	next, saveCmd := m.Update(enter)
	next, cmd := asModel(t, next).Update(runCmd(t, saveCmd))
	m = asModel(t, next)

	require.Equal(t, modeProviderSetup, m.mode)
	require.Contains(
		t,
		stripANSI(m.View().Content),
		"Error: provider setup saved; save codex launch default: config locked",
	)
	requireMsgType[launchDefaultsLoadedMsg](t, dataMsgs(t, cmd))
	require.Equal(t, "GPT-6-Sol", setupLaunchDefault(t, m, core.ProviderCodex))
	require.Equal(t, "Opus", setupLaunchDefault(t, m, core.ProviderClaude))

	// Enter saves the setup again, and only the Launch default that failed.
	frontend.saveLaunchDefaultErr = nil
	next, saveCmd = m.Update(enter)
	next, _ = asModel(t, next).Update(runCmd(t, saveCmd))
	require.Equal(t, modeRepoDirsSetup, asModel(t, next).mode)
	require.Equal(t, []string{"setup", "codex", "claude", "setup", "codex"}, frontend.saveCalls)
	require.Equal(t, map[core.Provider]core.LaunchChoice{
		core.ProviderCodex:  {Model: "gpt-6-sol"},
		core.ProviderClaude: {Model: "opus"},
	}, frontend.savedLaunchDefaults)
}

// The Launch defaults fetch that opening the setup screen starts answers once
// the setup has saved, with the defaults from before the save.
func TestModel_LaunchDefaultsSavedWithTheSetupStartTheNextDraft(t *testing.T) {
	frontend := setupFrontend(map[core.Provider]core.LaunchChoice{
		core.ProviderCodex: {Model: "gpt-6-mini", ReasoningLevel: "minimal"},
	})
	m, stale := openSetupAwaitingDefaults(t, loadedLaunchChoiceModel(t, frontend))

	m = skipRepoDirs(t, press(t, press(t, m, ctrlO), enter))
	require.Equal(t, modeBrowse, m.mode)
	require.Equal(
		t,
		map[core.Provider]core.LaunchChoice{core.ProviderCodex: {Model: "gpt-6-fast"}},
		frontend.savedLaunchDefaults,
	)

	want := []string{"codex · tab", "GPT-6-Fast · ctrl+o", "default"}
	require.Equal(t, want, choiceRows(t, openComposer(t, m)))
	require.Equal(t, want, choiceRows(t, openComposer(t, feed(t, m, stale))))
}

func TestModel_FirstRunSavesLaunchDefaultsAfterTheProviderSetup(t *testing.T) {
	frontend := setupFrontend(nil)
	frontend.providerSetup = nil
	m := newTestModel(frontend.mock)
	m.width = 120

	m = settle(t, m, requireMsgType[providerSetupLoadedMsg](t, runBatchCmd(t, m.Init())))
	require.Equal(t, modeProviderSetup, m.mode)
	// Nothing is configured yet, and each ready provider lists its models.
	require.ElementsMatch(t, []core.Provider{core.ProviderCodex, core.ProviderClaude}, frontend.providerModelsCalls)
	require.Equal(t, []string{
		"> [x] codex    ready  (default)",
		"       model  default",
		"  [x] claude   ready",
		"       model  default",
	}, setupRows(t, m))

	m = press(t, press(t, m, ctrlO), ctrlR)
	require.Equal(t, "GPT-6-Sol · low", setupLaunchDefault(t, m, core.ProviderCodex))
	m = skipRepoDirs(t, press(t, m, enter))

	require.Equal(t, modeBrowse, m.mode)
	require.Equal(t, []string{"setup", "codex"}, frontend.saveCalls)
	require.Equal(
		t,
		map[core.Provider]core.LaunchChoice{core.ProviderCodex: {Model: "gpt-6-sol", ReasoningLevel: "low"}},
		frontend.savedLaunchDefaults,
	)
	require.Equal(t, []string{"codex · tab", "GPT-6-Sol · ctrl+o", "low · ctrl+r"}, choiceRows(t, openComposer(t, m)))
}

func TestProviderStyleDistinguishesClaudeFromCodex(t *testing.T) {
	claude := providerStyle(string(core.ProviderClaude))
	codex := providerStyle(string(core.ProviderCodex))

	require.Equal(t, colorClaude, claude.GetForeground())
	require.Equal(t, colorCodex, codex.GetForeground())
	require.NotEqual(t, claude.GetForeground(), codex.GetForeground())
}

func TestModel_StatusFromTheLaunchProviderDoesNotReloadTasks(t *testing.T) {
	frontend := multiProviderFrontend()
	frontend.listTasks = []*core.Task{
		{ID: "task-1", DisplayName: "first task", RepoName: "repo-a", Provider: core.ProviderCodex},
	}

	m := newLoadedModel(frontend)
	updates := make(chan core.TaskStatusUpdate, 1)

	_, cmd := m.Update(taskStatusUpdatedMsg{
		taskID: "task-1",
		update: core.TaskStatusUpdate{
			TaskID:   "task-1",
			Provider: core.ProviderCodex,
			Phase:    core.TaskStatusPhaseWorking,
		},
		updates: updates,
	})
	require.NotNil(t, cmd)

	close(updates)
	for _, msg := range runBatchCmd(t, cmd) {
		_, isLoaded := msg.(tasksLoadedMsg)
		require.False(t, isLoaded, "status from the launch provider must not trigger a task reload")
	}
}

// A task launched with Claude runs a Codex agent next to Claude. Both work,
// so the most recent status change leads and the published provider
// alternates; the task list is not reloaded for it.
func TestModel_StatusFromAlternatingProvidersDoesNotReloadTasks(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{
		{ID: "task-1", RepoName: "repo-a", DisplayName: "mixed task", Provider: core.ProviderClaude},
	}
	frontend.subscribeTaskStatus = map[string]chan core.TaskStatusUpdate{
		"task-1": make(chan core.TaskStatusUpdate, 1),
	}
	got := newLoadedModel(frontend)
	before := frontend.listTasksCalls
	updates := make(chan core.TaskStatusUpdate)
	close(updates)

	start := time.Date(2026, time.October, 10, 9, 0, 0, 0, time.UTC)
	for i := range 10 {
		provider := core.ProviderCodex
		if i%2 == 1 {
			provider = core.ProviderClaude
		}
		next, cmd := got.Update(taskStatusUpdatedMsg{taskID: "task-1", updates: updates, update: core.TaskStatusUpdate{
			TaskID:       "task-1",
			Provider:     provider,
			Phase:        core.TaskStatusPhaseWorking,
			RawEventName: core.HookEventPostToolUse,
			ObservedAt:   start.Add(time.Duration(i) * time.Second),
		}})
		got = asModel(t, next)
		require.NotNil(t, cmd)
		for _, msg := range runBatchCmd(t, cmd) {
			_, isLoaded := msg.(tasksLoadedMsg)
			require.False(t, isLoaded, "a status update must not trigger a task reload")
		}
	}
	require.Equal(t, before, frontend.listTasksCalls)
	require.Equal(t, core.ProviderClaude, got.rows[0].task.Provider)
}

func TestModel_ActivityRefreshTickReloadsSelectedTaskActivity(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{
		{ID: "task-1", DisplayName: "first task", RepoName: "repo-a", Provider: core.ProviderClaude},
	}

	m := newLoadedModel(frontend)

	next, cmd := m.Update(activityRefreshTickMsg{})
	require.NotNil(t, cmd)
	got := asModel(t, next)

	msgs := runBatchCmd(t, cmd)
	requireMsgType[taskActivityLoadedMsg](t, msgs)
	requireMsgType[taskUsageLoadedMsg](t, msgs)
	// The tick re-arms itself so activity keeps refreshing.
	requireMsgType[activityRefreshTickMsg](t, msgs)
	require.Contains(t, frontend.getTaskActivityCalls, "task-1:60")
	require.Contains(t, frontend.getTaskUsageCalls, "task-1")
	_ = got
}

func TestModel_ActivityRefreshTickRearmsWithoutASelectedTask(t *testing.T) {
	frontend := newFrontendHarness()
	m := newLoadedModel(frontend)

	_, cmd := m.Update(activityRefreshTickMsg{})
	require.NotNil(t, cmd)

	msg := runCmd(t, cmd)
	_, isTick := msg.(activityRefreshTickMsg)
	require.True(t, isTick)
	require.Empty(t, frontend.getTaskActivityCalls)
}
