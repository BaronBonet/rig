package tui

import (
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/BaronBonet/rig/internal/core"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"
)

var (
	ctrlO = tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl}
	ctrlR = tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl}
	tab   = tea.KeyPressMsg{Code: tea.KeyTab}
	enter = tea.KeyPressMsg{Code: tea.KeyEnter}
)

func codexModels() []core.ProviderModel {
	return []core.ProviderModel{
		{ID: "gpt-6-sol", DisplayName: "GPT-6-Sol", ReasoningLevels: []string{"low", "medium", "high"}},
		{ID: "gpt-6-mini", DisplayName: "GPT-6-Mini", ReasoningLevels: []string{"minimal", "low"}},
		{ID: "gpt-6-fast", DisplayName: "GPT-6-Fast"},
	}
}

func claudeModels() []core.ProviderModel {
	return []core.ProviderModel{
		{ID: "opus", DisplayName: "Opus", ReasoningLevels: []string{"low", "medium", "high", "max"}},
		{ID: "haiku", DisplayName: "Haiku"},
	}
}

// launchChoiceFrontend has Codex, the default, and Claude configured, each
// listing its models, with the given Launch defaults.
func launchChoiceFrontend(defaults map[core.Provider]core.LaunchChoice) *frontendHarness {
	frontend := multiProviderFrontend()
	frontend.providerModels = map[core.Provider][]core.ProviderModel{
		core.ProviderCodex:  codexModels(),
		core.ProviderClaude: claudeModels(),
	}
	frontend.launchDefaults = defaults
	frontend.createTaskEvents = []core.TaskCreateEvent{{Task: &core.Task{ID: "task-9"}}}
	return frontend
}

// dataMsgs runs cmd, and each command of a batch, for the messages they
// yield. It skips the cursor blink and tea.Tick commands, which only wait.
func dataMsgs(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	if cmd == nil || isTimerCmd(cmd) {
		return nil
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{msg}
	}
	var msgs []tea.Msg
	for _, batchCmd := range batch {
		msgs = append(msgs, dataMsgs(t, batchCmd)...)
	}
	return msgs
}

func isTimerCmd(cmd tea.Cmd) bool {
	name := runtime.FuncForPC(reflect.ValueOf(cmd).Pointer()).Name()
	return strings.Contains(name, "bubbles/v2/cursor.") || strings.Contains(name, "bubbletea/v2.Tick.")
}

// msgsOf returns the messages of type T among msgs.
func msgsOf[T tea.Msg](msgs []tea.Msg) []tea.Msg {
	var matched []tea.Msg
	for _, msg := range msgs {
		if _, ok := msg.(T); ok {
			matched = append(matched, msg)
		}
	}
	return matched
}

// feed applies msgs to m in order.
func feed(t *testing.T, m model, msgs ...tea.Msg) model {
	t.Helper()
	for _, msg := range msgs {
		next, _ := m.Update(msg)
		m = asModel(t, next)
	}
	return m
}

// press presses key and applies the data messages its command yields.
func press(t *testing.T, m model, key tea.KeyPressMsg) model {
	t.Helper()
	next, cmd := m.Update(key)
	return feed(t, asModel(t, next), dataMsgs(t, cmd)...)
}

// loadedLaunchChoiceModel is a loaded model 120 columns wide that has applied
// its provider setup and the model lists and Launch defaults that fetched.
func loadedLaunchChoiceModel(t *testing.T, frontend *frontendHarness) model {
	t.Helper()
	m := newLoadedModel(frontend)
	m.width = 120
	next, cmd := m.Update(providerSetupLoadedMsg{setup: frontend.providerSetup})
	return feed(t, asModel(t, next), dataMsgs(t, cmd)...)
}

// openComposer opens the prompt composer from the task list, with a prompt.
func openComposer(t *testing.T, m model) model {
	t.Helper()
	got := press(t, m, tea.KeyPressMsg{Code: 'n', Text: "n"})
	require.Equal(t, modePromptInput, got.mode)
	got.draft.prompt = "add billing retry flow"
	return got
}

// choiceBlock is the composer's or PR picker's choice block, without
// styling: its provider, model and reasoning lines.
func choiceBlock(t *testing.T, m model) []string {
	t.Helper()
	lines := strings.Split(stripANSI(m.View().Content), "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "provider ") {
			require.GreaterOrEqual(t, len(lines), i+3)
			return lines[i : i+3]
		}
	}
	t.Fatal("the view has no choice block")
	return nil
}

// choiceRowText reads the choice block's line for label as "value · key", or as
// the value alone when the line has no key.
func choiceRowText(t *testing.T, m model, label string) string {
	t.Helper()
	for _, line := range choiceBlock(t, m) {
		if !strings.HasPrefix(line, label+" ") {
			continue
		}
		var cells []string
		for _, cell := range strings.Split(line, "·")[1:] {
			cells = append(cells, strings.TrimSpace(cell))
		}
		return strings.Join(cells, " · ")
	}
	t.Fatalf("the choice block has no %s line", label)
	return ""
}

// choiceRows reads each line of the choice block as choiceRowText does.
func choiceRows(t *testing.T, m model) []string {
	t.Helper()
	return []string{choiceRowText(t, m, "provider"), choiceRowText(t, m, "model"), choiceRowText(t, m, "reasoning")}
}

// submit submits the composer or PR picker, and returns the model once the
// creation it starts has ended, though before the Launch defaults fetch that
// ends it answers, and the creation input sent.
func submit(t *testing.T, m model, frontend *frontendHarness) (model, core.CreateTaskInput) {
	t.Helper()
	before := frontend.createTaskStreamCalls
	m = press(t, m, enter)
	require.Equal(t, before+1, frontend.createTaskStreamCalls)
	require.Equal(t, opNone, m.pending)
	// Each task the TUI creates saves its choice as the provider's default.
	require.True(t, frontend.createInput.RememberLaunchChoice)
	return m, frontend.createInput
}

// endCreation applies msg, which ends a creation, and returns the replies of
// the Launch defaults fetch that follows.
func endCreation(t *testing.T, m model, msg tea.Msg) (model, []tea.Msg) {
	t.Helper()
	next, cmd := m.Update(msg)
	return asModel(t, next), msgsOf[launchDefaultsLoadedMsg](dataMsgs(t, cmd))
}

func TestModel_SetupLoadFetchesLaunchDefaultsAndEachProvidersModels(t *testing.T) {
	frontend := launchChoiceFrontend(map[core.Provider]core.LaunchChoice{
		core.ProviderCodex: {Model: "gpt-6-sol"},
	})
	m := newLoadedModel(frontend)
	m.width = 120

	next, cmd := m.Update(providerSetupLoadedMsg{setup: frontend.providerSetup})
	msgs := dataMsgs(t, cmd)

	require.Len(t, msgsOf[launchDefaultsLoadedMsg](msgs), 1)
	require.Len(t, msgsOf[providerModelsLoadedMsg](msgs), 2)
	require.ElementsMatch(t, []core.Provider{core.ProviderCodex, core.ProviderClaude}, frontend.providerModelsCalls)
	// Each call has a deadline: the daemon does not see a client that gave up.
	_, hasDeadline := frontend.launchDefaultsContext.Deadline()
	require.True(t, hasDeadline)
	for _, ctx := range frontend.providerModelsContexts {
		_, hasDeadline := ctx.Deadline()
		require.True(t, hasDeadline)
	}

	m = openComposer(t, feed(t, asModel(t, next), msgs...))
	require.Equal(t, []string{"codex · tab", "GPT-6-Sol · ctrl+o", "default · ctrl+r"}, choiceRows(t, m))
	require.Equal(t, []string{"claude · tab", "default · ctrl+o", "default"}, choiceRows(t, press(t, m, tab)))
}

func TestModel_SetupSaveFetchesLaunchDefaultsAndModelsAgain(t *testing.T) {
	frontend := launchChoiceFrontend(nil)
	m := loadedLaunchChoiceModel(t, frontend)
	m.mode = modeProviderSetup
	frontend.providerModelsCalls = nil

	next, cmd := m.Update(providerSetupSavedMsg{setup: *frontend.providerSetup})
	msgs := dataMsgs(t, cmd)

	requireMsgType[repoDirsLoadedMsg](t, msgs)
	require.Len(t, msgsOf[providerModelsLoadedMsg](msgs), 2)
	require.Equal(t, 2, frontend.launchDefaultsCalls)
	// The lists that loaded stay on view while they refresh.
	m = skipRepoDirs(t, asModel(t, next))
	require.Equal(t, "default · ctrl+o", choiceRowText(t, openComposer(t, m), "model"))
}

func TestModel_FailedModelsAndDefaultsDoNotBlockCreatingATask(t *testing.T) {
	frontend := launchChoiceFrontend(nil)
	frontend.providerModelsErr = map[core.Provider]error{core.ProviderCodex: errors.New("codex timed out")}
	frontend.launchDefaultsErr = errors.New("config unreadable")
	m := openComposer(t, loadedLaunchChoiceModel(t, frontend))
	require.Equal(
		t,
		[]string{"codex · tab", "defaults unavailable · models unavailable", "default"},
		choiceRows(t, m),
	)

	_, input := submit(t, m, frontend)

	require.Equal(t, core.ProviderCodex, input.Provider)
	require.Empty(t, input.Model)
	require.Empty(t, input.ReasoningLevel)
}

func TestModel_OpeningTheComposerRetriesAFailedModelListOnce(t *testing.T) {
	frontend := launchChoiceFrontend(nil)
	frontend.providerModelsErr = map[core.Provider]error{core.ProviderCodex: errors.New("codex timed out")}
	m := loadedLaunchChoiceModel(t, frontend)

	frontend.providerModelsErr = nil
	frontend.providerModelsCalls = nil
	next, cmd := m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	m = asModel(t, next)
	// Only the failed list is fetched again, and shows as loading meanwhile.
	require.Equal(t, "default · loading models", choiceRowText(t, m, "model"))
	m = feed(t, m, dataMsgs(t, cmd)...)
	require.Equal(t, []core.Provider{core.ProviderCodex}, frontend.providerModelsCalls)
	require.Equal(t, "default · ctrl+o", choiceRowText(t, m, "model"))

	// A loaded list is not fetched again when the composer next opens.
	openComposer(t, press(t, m, tea.KeyPressMsg{Code: tea.KeyEscape}))
	require.Len(t, frontend.providerModelsCalls, 1)
}

// The daemon does not cache an empty list, so a logged-out CLI lists models
// once the user logs in.
func TestModel_OpeningTheComposerRetriesAnEmptyModelList(t *testing.T) {
	frontend := launchChoiceFrontend(nil)
	frontend.providerModels[core.ProviderCodex] = nil
	m := openComposer(t, loadedLaunchChoiceModel(t, frontend))
	require.Len(t, frontend.providerModelsCalls, 3)
	require.Equal(t, core.ProviderCodex, frontend.providerModelsCalls[2])
	require.Equal(t, "default", choiceRowText(t, m, "model"))

	frontend.providerModels[core.ProviderCodex] = codexModels()
	m = openComposer(t, press(t, m, tea.KeyPressMsg{Code: tea.KeyEscape}))
	require.Equal(t, "default · ctrl+o", choiceRowText(t, m, "model"))
}

func TestModel_OpeningThePRPickerRetriesAFailedModelList(t *testing.T) {
	frontend := launchChoiceFrontend(nil)
	frontend.providerModelsErr = map[core.Provider]error{core.ProviderCodex: errors.New("codex timed out")}
	m := openComposer(t, loadedLaunchChoiceModel(t, frontend))
	require.Equal(t, "default · models unavailable", choiceRowText(t, m, "model"))

	frontend.providerModelsCalls = nil
	next, cmd := m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	m = asModel(t, next)
	require.Equal(t, modePRPicker, m.mode)
	msgs := dataMsgs(t, cmd)

	requireMsgType[repoPullRequestsLoadedMsg](t, msgs)
	requireMsgType[providerModelsLoadedMsg](t, msgs)
	require.Equal(t, []core.Provider{core.ProviderCodex}, frontend.providerModelsCalls)
}

func TestModel_OpeningTheComposerRetriesFailedLaunchDefaults(t *testing.T) {
	frontend := launchChoiceFrontend(map[core.Provider]core.LaunchChoice{
		core.ProviderCodex: {Model: "gpt-6-sol", ReasoningLevel: "high"},
	})
	frontend.launchDefaultsErr = errors.New("config unreadable")
	m := loadedLaunchChoiceModel(t, frontend)

	frontend.launchDefaultsErr = nil
	next, cmd := m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	m = asModel(t, next)
	require.Equal(t, []string{"codex · tab", "loading defaults · ctrl+o", "default"}, choiceRows(t, m))

	m = feed(t, m, dataMsgs(t, cmd)...)
	require.Equal(t, 2, frontend.launchDefaultsCalls)
	require.Equal(t, []string{"codex · tab", "GPT-6-Sol · ctrl+o", "high · ctrl+r"}, choiceRows(t, m))

	// Loaded defaults are not fetched again when the composer next opens.
	openComposer(t, press(t, m, tea.KeyPressMsg{Code: tea.KeyEscape}))
	require.Equal(t, 2, frontend.launchDefaultsCalls)
}

func TestModel_ComposerShowsWhileLaunchDefaultsAreMissing(t *testing.T) {
	frontend := launchChoiceFrontend(map[core.Provider]core.LaunchChoice{
		core.ProviderCodex: {Model: "gpt-6-sol"},
	})
	m := newLoadedModel(frontend)
	m.width = 120
	next, cmd := m.Update(providerSetupLoadedMsg{setup: frontend.providerSetup})
	msgs := dataMsgs(t, cmd)
	m = openComposer(t, feed(t, asModel(t, next), msgsOf[providerModelsLoadedMsg](msgs)...))
	defaults := requireMsgType[launchDefaultsLoadedMsg](t, msgs)

	require.Equal(t, "loading defaults · ctrl+o", choiceRowText(t, m, "model"))
	failed := feed(t, m, launchDefaultsLoadedMsg{fetch: defaults.fetch, err: errors.New("config unreadable")})
	require.Equal(t, "defaults unavailable · ctrl+o", choiceRowText(t, failed, "model"))
	require.Equal(t, "GPT-6-Sol · ctrl+o", choiceRowText(t, feed(t, m, defaults), "model"))

	// A model the user picked shows whether or not the defaults loaded.
	require.Equal(t, "GPT-6-Sol · ctrl+o", choiceRowText(t, press(t, failed, ctrlO), "model"))
}

func TestModel_AFailedRefreshKeepsALoadedModelList(t *testing.T) {
	m := openComposer(t, loadedLaunchChoiceModel(t, launchChoiceFrontend(nil)))
	m = feed(t, m, providerModelsLoadedMsg{provider: core.ProviderCodex, err: errors.New("codex timed out")})

	require.Equal(t, "GPT-6-Sol · ctrl+o", choiceRowText(t, press(t, m, ctrlO), "model"))
}

func TestModel_ModelListsLandOnTheirProviderInAnyOrder(t *testing.T) {
	frontend := launchChoiceFrontend(nil)
	m := newLoadedModel(frontend)
	m.width = 120
	next, cmd := m.Update(providerSetupLoadedMsg{setup: frontend.providerSetup})
	msgs := dataMsgs(t, cmd)
	lists := msgsOfType[providerModelsLoadedMsg](msgs)
	require.Len(t, lists, 2)
	if lists[0].provider == core.ProviderCodex {
		lists[0], lists[1] = lists[1], lists[0]
	}
	m = feed(t, asModel(t, next), msgsOf[launchDefaultsLoadedMsg](msgs)[0], lists[0], lists[1])

	m = press(t, openComposer(t, m), ctrlO)
	require.Equal(t, "GPT-6-Sol · ctrl+o", choiceRowText(t, m, "model"))
	m = press(t, press(t, m, tab), ctrlO)
	require.Equal(t, "Opus · ctrl+o", choiceRowText(t, m, "model"))
}

func TestModel_ComposerStartsFromTheProvidersLaunchDefault(t *testing.T) {
	frontend := launchChoiceFrontend(map[core.Provider]core.LaunchChoice{
		core.ProviderCodex:  {Model: "gpt-6-mini", ReasoningLevel: "minimal"},
		core.ProviderClaude: {Model: "opus", ReasoningLevel: "max"},
	})

	m := openComposer(t, loadedLaunchChoiceModel(t, frontend))
	codexRows := []string{"codex · tab", "GPT-6-Mini · ctrl+o", "minimal · ctrl+r"}
	require.Equal(t, codexRows, choiceRows(t, m))

	// Tab switches the draft to the other provider's Launch default, and back
	// to this one's rather than to what was picked before.
	m = press(t, m, tab)
	require.Equal(t, []string{"claude · tab", "Opus · ctrl+o", "max · ctrl+r"}, choiceRows(t, m))
	m = press(t, press(t, press(t, m, tab), ctrlO), tab)
	m = press(t, m, tab)
	require.Equal(t, codexRows, choiceRows(t, m))

	_, input := submit(t, m, frontend)
	require.Equal(t, "gpt-6-mini", input.Model)
	require.Equal(t, "minimal", input.ReasoningLevel)
}

func TestModel_TabToAProviderWithoutALaunchDefaultClearsTheChoice(t *testing.T) {
	frontend := launchChoiceFrontend(map[core.Provider]core.LaunchChoice{
		core.ProviderCodex: {Model: "gpt-6-sol", ReasoningLevel: "high"},
	})

	m := press(t, openComposer(t, loadedLaunchChoiceModel(t, frontend)), tab)
	require.Equal(t, []string{"claude · tab", "default · ctrl+o", "default"}, choiceRows(t, m))

	_, input := submit(t, m, frontend)
	require.Equal(t, core.ProviderClaude, input.Provider)
	require.Empty(t, input.Model)
	require.Empty(t, input.ReasoningLevel)
}

func TestModel_LateLaunchDefaultsApplyOnlyToAnUntouchedDraft(t *testing.T) {
	frontend := launchChoiceFrontend(map[core.Provider]core.LaunchChoice{
		core.ProviderCodex: {Model: "gpt-6-mini", ReasoningLevel: "low"},
	})
	// The composer opens with the model lists loaded, before the Launch
	// defaults arrive.
	m := newLoadedModel(frontend)
	m.width = 120
	next, cmd := m.Update(providerSetupLoadedMsg{setup: frontend.providerSetup})
	msgs := dataMsgs(t, cmd)
	composing := openComposer(t, feed(t, asModel(t, next), msgsOf[providerModelsLoadedMsg](msgs)...))
	late := requireMsgType[launchDefaultsLoadedMsg](t, msgs)

	tests := []struct {
		name string
		keys []tea.KeyPressMsg
		want []string
	}{
		{"untouched", nil, []string{"GPT-6-Mini · ctrl+o", "low · ctrl+r"}},
		// Without a model ctrl+r does nothing, so it leaves the draft untouched.
		{"after ctrl+r", []tea.KeyPressMsg{ctrlR}, []string{"GPT-6-Mini · ctrl+o", "low · ctrl+r"}},
		{"after ctrl+o", []tea.KeyPressMsg{ctrlO}, []string{"GPT-6-Sol · ctrl+o", "default · ctrl+r"}},
		{
			"after ctrl+o back to default",
			[]tea.KeyPressMsg{ctrlO, ctrlO, ctrlO, ctrlO},
			[]string{"default · ctrl+o", "default"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := composing
			for _, key := range tt.keys {
				m = press(t, m, key)
			}
			rows := choiceRows(t, feed(t, m, late))
			require.Equal(t, tt.want, rows[1:])
		})
	}
}

func TestModel_ADefaultLevelTheModelLacksStartsAsDefault(t *testing.T) {
	frontend := launchChoiceFrontend(map[core.Provider]core.LaunchChoice{
		core.ProviderCodex:  {Model: "gpt-6-fast", ReasoningLevel: "high"},
		core.ProviderClaude: {Model: "opus", ReasoningLevel: "minimal"},
	})
	m := openComposer(t, loadedLaunchChoiceModel(t, frontend))

	require.Equal(t, []string{"codex · tab", "GPT-6-Fast · ctrl+o", "default"}, choiceRows(t, m))
	// Cycling on to a model with levels does not bring the hidden one back.
	require.Equal(t, "default · ctrl+r", choiceRowText(t, press(t, press(t, m, ctrlO), ctrlO), "reasoning"))

	m = press(t, m, tab)
	require.Equal(t, []string{"claude · tab", "Opus · ctrl+o", "default · ctrl+r"}, choiceRows(t, m))
	_, input := submit(t, m, frontend)
	require.Equal(t, "opus", input.Model)
	require.Empty(t, input.ReasoningLevel)
}

// Earlier saves can hold a reasoning level without a model, which the TUI no
// longer lets a draft choose.
func TestModel_ALevelOnlyLaunchDefaultStartsAsDefault(t *testing.T) {
	frontend := launchChoiceFrontend(map[core.Provider]core.LaunchChoice{
		core.ProviderCodex:  {ReasoningLevel: "low"},
		core.ProviderClaude: {ReasoningLevel: "high"},
	})
	m := openComposer(t, loadedLaunchChoiceModel(t, frontend))
	require.Equal(t, []string{"codex · tab", "default · ctrl+o", "default"}, choiceRows(t, m))

	m = press(t, m, tab)
	require.Equal(t, []string{"claude · tab", "default · ctrl+o", "default"}, choiceRows(t, m))
	// Picking a model does not bring the saved level back.
	require.Equal(t, "default · ctrl+r", choiceRowText(t, press(t, m, ctrlO), "reasoning"))

	_, input := submit(t, m, frontend)
	require.Empty(t, input.Model)
	require.Empty(t, input.ReasoningLevel)
}

func TestModel_AModelListArrivingDropsALevelTheModelLacks(t *testing.T) {
	frontend := launchChoiceFrontend(map[core.Provider]core.LaunchChoice{
		core.ProviderCodex: {Model: "gpt-6-mini", ReasoningLevel: "high"},
	})
	// The composer opens with the Launch defaults loaded, before the lists.
	m := newLoadedModel(frontend)
	m.width = 120
	next, cmd := m.Update(providerSetupLoadedMsg{setup: frontend.providerSetup})
	msgs := dataMsgs(t, cmd)
	m = openComposer(t, feed(t, asModel(t, next), msgsOf[launchDefaultsLoadedMsg](msgs)...))
	require.Equal(t, []string{"codex · tab", "gpt-6-mini · loading models", "high"}, choiceRows(t, m))

	m = feed(t, m, msgsOf[providerModelsLoadedMsg](msgs)...)
	require.Equal(t, []string{"codex · tab", "GPT-6-Mini · ctrl+o", "default · ctrl+r"}, choiceRows(t, m))
	_, input := submit(t, m, frontend)
	require.Equal(t, "gpt-6-mini", input.Model)
	require.Empty(t, input.ReasoningLevel)
}

// rowValues presses key count times and collects the value the choice block
// shows on label's line each time.
func rowValues(t *testing.T, m model, key tea.KeyPressMsg, label string, count int) []string {
	t.Helper()
	var values []string
	for range count {
		m = press(t, m, key)
		values = append(values, strings.Split(choiceRowText(t, m, label), " · ")[0])
	}
	return values
}

func TestModel_CtrlOCyclesFromDefaultThroughEachModel(t *testing.T) {
	m := openComposer(t, loadedLaunchChoiceModel(t, launchChoiceFrontend(nil)))

	require.Equal(
		t,
		[]string{"GPT-6-Sol", "GPT-6-Mini", "GPT-6-Fast", "default"},
		rowValues(t, m, ctrlO, "model", 4),
	)
}

func TestModel_CtrlOFromAnUnlistedModelGoesToDefault(t *testing.T) {
	frontend := launchChoiceFrontend(map[core.Provider]core.LaunchChoice{
		core.ProviderCodex: {Model: "gpt-4-retired"},
	})
	m := openComposer(t, loadedLaunchChoiceModel(t, frontend))

	// A saved model the CLI no longer lists is kept, by its ID, until cycled.
	require.Equal(t, "gpt-4-retired · ctrl+o", choiceRowText(t, m, "model"))
	_, input := submit(t, m, frontend)
	require.Equal(t, "gpt-4-retired", input.Model)

	require.Equal(t, "default · ctrl+o", choiceRowText(t, press(t, m, ctrlO), "model"))
}

// Rig cannot tell which levels a model the list lacks accepts, so it keeps
// the saved level as it is.
func TestModel_AnUnlistedModelKeepsItsSavedLevel(t *testing.T) {
	frontend := launchChoiceFrontend(map[core.Provider]core.LaunchChoice{
		core.ProviderCodex: {Model: "gpt-4-retired", ReasoningLevel: "xhigh"},
	})
	m := openComposer(t, loadedLaunchChoiceModel(t, frontend))
	require.Equal(t, []string{"codex · tab", "gpt-4-retired · ctrl+o", "xhigh"}, choiceRows(t, m))

	before := m.View().Content
	m = press(t, m, ctrlR)
	require.Equal(t, before, m.View().Content)
	// Tab away and back starts from the saved default again, level and all.
	m = press(t, press(t, m, tab), tab)
	require.Equal(t, before, m.View().Content)

	_, input := submit(t, m, frontend)
	require.Equal(t, "gpt-4-retired", input.Model)
	require.Equal(t, "xhigh", input.ReasoningLevel)
}

func TestModel_CtrlOAndCtrlRDoNothingUntilAModelListLoads(t *testing.T) {
	cases := map[string]providerModelsState{
		"loading":     {},
		"unavailable": {err: errors.New("codex timed out")},
		"empty":       {loaded: true},
	}
	for name, state := range cases {
		t.Run(name, func(t *testing.T) {
			frontend := launchChoiceFrontend(nil)
			m := openComposer(t, loadedLaunchChoiceModel(t, frontend))
			m.providerModels[core.ProviderCodex] = state
			before := m.View().Content

			got := press(t, press(t, m, ctrlO), ctrlR)
			require.Equal(t, before, got.View().Content)
			_, input := submit(t, got, frontend)
			require.Empty(t, input.Model)
			require.Empty(t, input.ReasoningLevel)
		})
	}
}

func TestModel_CtrlRCyclesTheSelectedModelsLevels(t *testing.T) {
	m := press(t, openComposer(t, loadedLaunchChoiceModel(t, launchChoiceFrontend(nil))), ctrlO)
	require.Equal(t, "GPT-6-Sol · ctrl+o", choiceRowText(t, m, "model"))

	require.Equal(t, []string{"low", "medium", "high", "default"}, rowValues(t, m, ctrlR, "reasoning", 4))
}

// A reasoning level needs a model: Rig cannot tell which model the CLI runs
// by default, nor which levels it accepts.
func TestModel_WithoutAModelThereIsNoReasoningLevel(t *testing.T) {
	frontend := launchChoiceFrontend(nil)
	m := openComposer(t, loadedLaunchChoiceModel(t, frontend))
	require.Equal(t, []string{"codex · tab", "default · ctrl+o", "default"}, choiceRows(t, m))

	before := m.View().Content
	m = press(t, m, ctrlR)
	require.Equal(t, before, m.View().Content)

	_, input := submit(t, m, frontend)
	require.Empty(t, input.Model)
	require.Empty(t, input.ReasoningLevel)
}

func TestModel_CtrlOBackToDefaultClearsTheLevel(t *testing.T) {
	frontend := launchChoiceFrontend(map[core.Provider]core.LaunchChoice{
		core.ProviderCodex: {Model: "gpt-6-mini", ReasoningLevel: "low"},
	})
	// With no model that takes no level, ctrl+o goes from a level straight to
	// no model.
	frontend.providerModels[core.ProviderCodex] = codexModels()[:2]
	m := openComposer(t, loadedLaunchChoiceModel(t, frontend))
	require.Equal(t, "low · ctrl+r", choiceRowText(t, m, "reasoning"))

	m = press(t, m, ctrlO)
	require.Equal(t, []string{"codex · tab", "default · ctrl+o", "default"}, choiceRows(t, m))
	_, input := submit(t, m, frontend)
	require.Empty(t, input.ReasoningLevel)

	// GPT-6-Sol offers low, but the level stays cleared.
	require.Equal(t, "default · ctrl+r", choiceRowText(t, press(t, m, ctrlO), "reasoning"))
}

func TestModel_CtrlRDoesNothingForAModelWithoutLevels(t *testing.T) {
	frontend := launchChoiceFrontend(map[core.Provider]core.LaunchChoice{
		core.ProviderCodex: {Model: "gpt-6-fast"},
	})
	m := openComposer(t, loadedLaunchChoiceModel(t, frontend))
	require.Equal(t, "default", choiceRowText(t, m, "reasoning"))
	before := m.View().Content

	got := press(t, m, ctrlR)
	require.Equal(t, before, got.View().Content)
	_, input := submit(t, got, frontend)
	require.Empty(t, input.ReasoningLevel)
}

func TestModel_ModelChangeDropsALevelTheNewModelLacks(t *testing.T) {
	frontend := launchChoiceFrontend(map[core.Provider]core.LaunchChoice{
		core.ProviderCodex: {Model: "gpt-6-sol", ReasoningLevel: "low"},
	})
	m := openComposer(t, loadedLaunchChoiceModel(t, frontend))

	// GPT-6-Mini takes low too, so the level stays.
	m = press(t, m, ctrlO)
	require.Equal(t, []string{"codex · tab", "GPT-6-Mini · ctrl+o", "low · ctrl+r"}, choiceRows(t, m))

	// GPT-6-Fast takes no level, and no model takes none.
	m = press(t, m, ctrlO)
	require.Equal(t, []string{"codex · tab", "GPT-6-Fast · ctrl+o", "default"}, choiceRows(t, m))
	m = press(t, m, ctrlO)
	require.Equal(t, []string{"codex · tab", "default · ctrl+o", "default"}, choiceRows(t, m))

	_, input := submit(t, m, frontend)
	require.Empty(t, input.Model)
	require.Empty(t, input.ReasoningLevel)
}

func TestModel_SubmitPromptSendsTheLaunchChoiceAndRemembersIt(t *testing.T) {
	frontend := launchChoiceFrontend(nil)
	m := openComposer(t, loadedLaunchChoiceModel(t, frontend))

	m = press(t, press(t, press(t, press(t, m, tab), ctrlO), ctrlR), ctrlR)
	claudeRows := []string{"claude · tab", "Opus · ctrl+o", "medium · ctrl+r"}
	require.Equal(t, claudeRows, choiceRows(t, m))

	m, input := submit(t, m, frontend)

	require.Equal(t, core.ProviderClaude, input.Provider)
	require.Equal(t, "opus", input.Model)
	require.Equal(t, "medium", input.ReasoningLevel)
	// The next Claude draft starts from it.
	require.Equal(t, claudeRows, choiceRows(t, press(t, openComposer(t, m), tab)))
}

func TestModel_SubmittingNoChoiceClearsTheLocalLaunchDefault(t *testing.T) {
	frontend := launchChoiceFrontend(map[core.Provider]core.LaunchChoice{
		core.ProviderCodex: {Model: "gpt-6-fast"},
	})
	m := press(t, openComposer(t, loadedLaunchChoiceModel(t, frontend)), ctrlO)
	require.Equal(t, "default · ctrl+o", choiceRowText(t, m, "model"))

	m, input := submit(t, m, frontend)

	require.Equal(t, core.ProviderCodex, input.Provider)
	require.Empty(t, input.Model)
	require.Equal(t, "default · ctrl+o", choiceRowText(t, openComposer(t, m), "model"))
}

func TestModel_PRPickerCyclesAndSendsTheLaunchChoice(t *testing.T) {
	frontend := launchChoiceFrontend(nil)
	frontend.listRepoPullRequests = []core.RepoPullRequest{{Number: 42, Title: "Auth rewrite", BranchName: "feat/auth"}}
	m := openComposer(t, loadedLaunchChoiceModel(t, frontend))
	m = press(t, m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	require.Equal(t, modePRPicker, m.mode)

	m = press(t, press(t, m, ctrlO), ctrlR)
	solRows := []string{"codex · tab", "GPT-6-Sol · ctrl+o", "low · ctrl+r"}
	require.Equal(t, solRows, choiceRows(t, m))

	m, input := submit(t, m, frontend)

	require.NotNil(t, input.Source.PullRequest)
	require.Equal(t, core.ProviderCodex, input.Provider)
	require.Equal(t, "gpt-6-sol", input.Model)
	require.Equal(t, "low", input.ReasoningLevel)
	require.Equal(t, solRows, choiceRows(t, openComposer(t, m)))
}

// A defaults fetch under way when the user submits answers with the defaults
// from before the submitted choice was saved.
func TestModel_AStaleLaunchDefaultsReplyAfterSubmitIsIgnored(t *testing.T) {
	frontend := launchChoiceFrontend(map[core.Provider]core.LaunchChoice{
		core.ProviderCodex: {Model: "gpt-6-mini", ReasoningLevel: "minimal"},
	})
	m := loadedLaunchChoiceModel(t, frontend)
	m.mode = modeProviderSetup
	// Setup is saved again, and the user submits before its fetch answers.
	next, cmd := m.Update(providerSetupSavedMsg{setup: *frontend.providerSetup})
	stale := requireMsgType[launchDefaultsLoadedMsg](t, dataMsgs(t, cmd))
	m = press(t, openComposer(t, skipRepoDirs(t, asModel(t, next))), ctrlO)
	require.Equal(t, "GPT-6-Fast · ctrl+o", choiceRowText(t, m, "model"))

	next, cmd = m.Update(enter)
	m = asModel(t, next)
	created := requireMsgType[taskCreateEventMsg](t, dataMsgs(t, cmd))
	// The daemon saves the choice as creation starts.
	frontend.launchDefaults = map[core.Provider]core.LaunchChoice{core.ProviderCodex: {Model: "gpt-6-fast"}}

	m = feed(t, m, stale)
	m, refetched := endCreation(t, m, created)
	require.Len(t, refetched, 1)
	require.Equal(t, "GPT-6-Fast · ctrl+o", choiceRowText(t, openComposer(t, m), "model"))
	require.Equal(t, "GPT-6-Fast · ctrl+o", choiceRowText(t, openComposer(t, feed(t, m, refetched...)), "model"))
}

// Creation that fails its first checks saves no Launch default, so the
// fetch after it puts the saved one back.
func TestModel_TheLaunchDefaultsFetchAfterCreationApplies(t *testing.T) {
	for name, failure := range map[string]func(*frontendHarness){
		"stream refused": func(f *frontendHarness) { f.createTaskStreamErr = errors.New("not a git repository") },
		"creation failed": func(f *frontendHarness) {
			f.createTaskEvents = []core.TaskCreateEvent{{Err: errors.New("not a git repository")}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			frontend := launchChoiceFrontend(map[core.Provider]core.LaunchChoice{
				core.ProviderCodex: {Model: "gpt-6-mini", ReasoningLevel: "minimal"},
			})
			failure(frontend)
			m := press(t, openComposer(t, loadedLaunchChoiceModel(t, frontend)), ctrlO)

			next, cmd := m.Update(enter)
			m = asModel(t, next)
			msgs := dataMsgs(t, cmd)
			require.Len(t, msgs, 1)

			m, refetched := endCreation(t, m, msgs[0])
			require.Len(t, refetched, 1)
			require.Equal(t, "GPT-6-Fast · ctrl+o", choiceRowText(t, openComposer(t, m), "model"))
			m = feed(t, m, refetched...)
			require.Equal(
				t,
				[]string{"codex · tab", "GPT-6-Mini · ctrl+o", "minimal · ctrl+r"},
				choiceRows(t, openComposer(t, m)),
			)
		})
	}
}

func TestModel_ChoiceBlockAlignsItsColumns(t *testing.T) {
	frontend := launchChoiceFrontend(map[core.Provider]core.LaunchChoice{
		core.ProviderCodex: {Model: "gpt-6-sol", ReasoningLevel: "high"},
	})
	m := openComposer(t, loadedLaunchChoiceModel(t, frontend))

	// Values pad to the widest of them, so the keys line up.
	require.Equal(t, []string{
		"provider   · codex      · tab",
		"model      · GPT-6-Sol  · ctrl+o",
		"reasoning  · high       · ctrl+r",
	}, choiceBlock(t, m))

	// A line whose key does nothing ends at its value, with no separator.
	require.Equal(t, []string{
		"provider   · claude   · tab",
		"model      · default  · ctrl+o",
		"reasoning  · default",
	}, choiceBlock(t, press(t, m, tab)))
}

func TestModel_ChoiceBlockShowsTheModelListsStateInTheKeysPlace(t *testing.T) {
	frontend := launchChoiceFrontend(map[core.Provider]core.LaunchChoice{
		core.ProviderCodex: {Model: "gpt-6-sol", ReasoningLevel: "high"},
	})
	m := openComposer(t, loadedLaunchChoiceModel(t, frontend))

	m.providerModels[core.ProviderCodex] = providerModelsState{}
	require.Equal(t, []string{
		"provider   · codex      · tab",
		"model      · gpt-6-sol  · loading models",
		"reasoning  · high",
	}, choiceBlock(t, m))

	m.providerModels[core.ProviderCodex] = providerModelsState{err: errors.New("codex timed out")}
	require.Equal(t, "model      · gpt-6-sol  · models unavailable", choiceBlock(t, m)[1])
}

func TestModel_ComposerHintsOnlyTheKeysThatWork(t *testing.T) {
	m := openComposer(t, loadedLaunchChoiceModel(t, launchChoiceFrontend(nil)))

	tests := []struct {
		name   string
		mutate func(*model)
		want   []string
	}{
		{
			name: "a single configured provider",
			mutate: func(m *model) {
				m.providerSetup = &core.ProviderSetup{
					Configured: []core.Provider{core.ProviderCodex},
					Default:    core.ProviderCodex,
				}
			},
			want: []string{"codex", "default · ctrl+o", "default"},
		},
		{
			name:   "a model with reasoning levels",
			mutate: func(m *model) { m.draft.choice.Model = "gpt-6-sol" },
			want:   []string{"codex · tab", "GPT-6-Sol · ctrl+o", "default · ctrl+r"},
		},
		{
			name:   "a model without reasoning levels",
			mutate: func(m *model) { m.draft.choice.Model = "gpt-6-fast" },
			want:   []string{"codex · tab", "GPT-6-Fast · ctrl+o", "default"},
		},
		{
			name:   "models loading",
			mutate: func(m *model) { m.providerModels[core.ProviderCodex] = providerModelsState{} },
			want:   []string{"codex · tab", "default · loading models", "default"},
		},
		{
			name: "models unavailable",
			mutate: func(m *model) {
				m.providerModels[core.ProviderCodex] = providerModelsState{err: errors.New("codex timed out")}
			},
			want: []string{"codex · tab", "default · models unavailable", "default"},
		},
		{
			name:   "no models listed",
			mutate: func(m *model) { m.providerModels[core.ProviderCodex] = providerModelsState{loaded: true} },
			want:   []string{"codex · tab", "default", "default"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := m
			got.providerModels = map[core.Provider]providerModelsState{
				core.ProviderCodex: m.providerModels[core.ProviderCodex],
			}
			tt.mutate(&got)
			require.Equal(t, tt.want, choiceRows(t, got))
		})
	}
}

func TestModel_PRPickerShowsTheLaunchChoiceAboveItsFooter(t *testing.T) {
	frontend := launchChoiceFrontend(map[core.Provider]core.LaunchChoice{
		core.ProviderCodex: {Model: "gpt-6-mini", ReasoningLevel: "minimal"},
	})
	m := openComposer(t, loadedLaunchChoiceModel(t, frontend))
	block := []string{
		"provider   · codex       · tab",
		"model      · GPT-6-Mini  · ctrl+o",
		"reasoning  · minimal     · ctrl+r",
	}

	// With pull requests, and with none, which reads as they do while loading.
	for name, prs := range map[string][]core.RepoPullRequest{
		"pull requests": {{Number: 42, Title: "Auth rewrite", BranchName: "feat/auth"}},
		"none":          nil,
	} {
		t.Run(name, func(t *testing.T) {
			frontend.listRepoPullRequests = prs
			picker := press(t, m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
			require.Equal(t, modePRPicker, picker.mode)

			lines := strings.Split(stripANSI(picker.View().Content), "\n")
			require.Equal(t, block, lines[len(lines)-5:len(lines)-2])
			require.Contains(t, lines[len(lines)-1], "esc back")
		})
	}
}

// launchChoiceTasksModel is agentsTaskModel with model lists and Launch
// defaults loaded, where task-1 launched GPT-6-Sol on high and task-2 a model
// the list lacks.
func launchChoiceTasksModel(t *testing.T, frontend *frontendHarness) model {
	t.Helper()
	agentsTaskModel(frontend)
	frontend.listTasks[0].Model, frontend.listTasks[0].ReasoningLevel = "gpt-6-sol", "high"
	frontend.listTasks[1].Model = "gpt-4-retired"
	m := loadedLaunchChoiceModel(t, frontend)
	m.width = 96
	return m
}

func TestModel_DetailPanelShowsTheTasksLaunchModel(t *testing.T) {
	m := launchChoiceTasksModel(t, launchChoiceFrontend(nil))

	require.Equal(t, "GPT-6-Sol · high", detailLines(m)[0])

	// A model the list lacks reads by its ID.
	m = pressKey(t, m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, "gpt-4-retired", detailLines(m)[0])

	// A task without a Launch choice ran the CLI's default, which says
	// nothing, so its panel has nothing to show.
	m.rows[1].task.Model = ""
	require.Empty(t, stripANSI(m.selectedTaskDetailView()))
}

// With two or more agents, the task row leaves the model to each agent's
// sub-row, which names the model that agent runs on, and nothing until that
// is known: the task's Launch choice need not be what it runs.
func TestModel_DetailPanelNamesEachSubRowsOwnModel(t *testing.T) {
	m := launchChoiceTasksModel(t, launchChoiceFrontend(nil))
	m = withAgentSessions(t, m, "task-1", claudeAgent(), codexAgent())
	m = withTaskUsage(t, m, "task-1", &core.TaskUsage{
		AgentModels: map[string]core.AgentModel{"agent-codex": {Model: "gpt-6-mini", ReasoningLevel: "minimal"}},
	})
	require.NotContains(t, stripANSI(m.selectedTaskDetailView()), "GPT-6")

	m = pressKey(t, m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, "agent-claude", m.selectedAgent)
	require.Equal(t, "window 0 (task) · pane 0", detailLines(m)[0])

	m = pressKey(t, m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	require.Equal(t, "agent-codex", m.selectedAgent)
	require.Equal(t, "GPT-6-Mini · minimal    window 2 (review) · pane 1", detailLines(m)[0])
}

func TestModel_LaunchChoiceFitsNarrowTerminals(t *testing.T) {
	frontend := launchChoiceFrontend(map[core.Provider]core.LaunchChoice{
		core.ProviderCodex: {Model: "gpt-6-sol", ReasoningLevel: "medium"},
	})
	loaded := launchChoiceTasksModel(t, frontend)
	composer := openComposer(t, loaded)
	picker := composer
	picker.mode = modePRPicker
	picker.draft.prs = []core.RepoPullRequest{{Number: 42, Title: "Auth rewrite", BranchName: "feat/auth"}}

	for _, width := range []int{120, 80, 60, 50} {
		for name, m := range map[string]model{"composer": composer, "picker": picker} {
			m.width = width
			view := m.View().Content
			for _, line := range strings.Split(view, "\n") {
				require.LessOrEqual(t, lipgloss.Width(line), width, "%s at %d: %q", name, width, stripANSI(line))
			}
			require.Equal(t, []string{"codex · tab", "GPT-6-Sol · ctrl+o", "medium · ctrl+r"}, choiceRows(t, m))
		}

		browse := loaded
		browse.width = width
		for _, line := range strings.Split(browse.selectedTaskDetailView(), "\n") {
			require.LessOrEqual(t, lipgloss.Width(line), width, "panel at %d: %q", width, stripANSI(line))
		}
	}
}

func TestModel_ALongModelNameKeepsItsKeyHint(t *testing.T) {
	frontend := launchChoiceFrontend(map[core.Provider]core.LaunchChoice{
		core.ProviderCodex: {Model: "gpt-6-sol-preview", ReasoningLevel: "high"},
	})
	frontend.providerModels[core.ProviderCodex] = append(codexModels(), core.ProviderModel{
		ID:              "gpt-6-sol-preview",
		DisplayName:     "GPT-6-Sol Extended Context Research Preview (October 2026)",
		ReasoningLevels: []string{"high"},
	})
	m := openComposer(t, loadedLaunchChoiceModel(t, frontend))

	for _, width := range []int{60, 50} {
		m.width = width
		block := choiceBlock(t, m)
		for _, line := range block {
			require.LessOrEqual(t, lipgloss.Width(line), width, "at %d: %q", width, line)
		}
		require.True(t, strings.HasPrefix(block[1], "model      · GPT-6-Sol Extended"), block[1])
		require.True(t, strings.HasSuffix(block[1], "…  · ctrl+o"), block[1])
		// The other keys stay in line with it.
		keyColumn := func(line string) int { return lipgloss.Width(line[:strings.LastIndex(line, "·")]) }
		require.Equal(t, keyColumn(block[1]), keyColumn(block[0]), block[0])
		require.Equal(t, keyColumn(block[1]), keyColumn(block[2]), block[2])
		require.True(t, strings.HasSuffix(block[0], "· tab"), block[0])
		require.True(t, strings.HasSuffix(block[2], "· ctrl+r"), block[2])
	}
}
