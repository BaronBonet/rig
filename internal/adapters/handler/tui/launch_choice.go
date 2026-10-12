package tui

import (
	"maps"
	"slices"
	"strings"

	"github.com/BaronBonet/rig/internal/core"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// providerModelsState is one provider's model list as the TUI fetched it. It
// is loading until loaded or err is set, and a list once loaded stays when a
// later refresh fails.
type providerModelsState struct {
	err    error
	models []core.ProviderModel
	loaded bool
}

// known reports whether the list loaded with models in it. An empty list is
// no answer: a logged-out CLI lists nothing until the user logs in.
func (s providerModelsState) known() bool {
	return s.loaded && len(s.models) > 0
}

// retryable reports whether the list's fetch answered without models: it
// failed, or came back empty. A view that shows the list fetches it again.
func (s providerModelsState) retryable() bool {
	return s.err != nil || (s.loaded && !s.known())
}

// launchDefaultsState is the Launch defaults as the TUI fetched them. Like a
// model list, they are loading until loaded or err is set, and once loaded
// they stay when a later refresh fails.
type launchDefaultsState struct {
	err     error
	choices map[core.Provider]core.LaunchChoice
	loaded  bool
	// fetches numbers each fetch as it starts, and applied is the number of
	// the fetch choices came from. staleAt is the number of the last fetch
	// started before a Launch choice was submitted or saved: no reply from it
	// or an earlier fetch has that choice in it.
	fetches int
	applied int
	staleAt int
	// refetchAfterCreate is set while a task creation that remembered a
	// Launch choice runs. The daemon saves the choice only once creation
	// passes its first checks, so its end fetches what the daemon holds.
	refetchAfterCreate bool
}

// launchChoiceLoadCmds fetches the Launch defaults and each configured
// provider's model list. What already loaded stays while the fetches
// refresh it.
func (m *model) launchChoiceLoadCmds() []tea.Cmd {
	cmds := []tea.Cmd{m.fetchLaunchDefaults()}
	for _, provider := range m.configuredProviders() {
		cmds = append(cmds, m.fetchProviderModels(provider))
	}
	return cmds
}

// retryLaunchChoiceCmds fetches again the Launch defaults when they failed,
// and each configured provider's model list that failed or came back empty,
// which opening a draft view does once.
func (m *model) retryLaunchChoiceCmds() []tea.Cmd {
	var cmds []tea.Cmd
	if !m.launchDefaults.loaded && m.launchDefaults.err != nil {
		cmds = append(cmds, m.fetchLaunchDefaults())
	}
	for _, provider := range m.configuredProviders() {
		if m.providerModels[provider].retryable() {
			cmds = append(cmds, m.fetchProviderModels(provider))
		}
	}
	return cmds
}

// fetchProviderModels starts a fetch of a provider's model list, which shows
// as loading until it answers unless a list with models already loaded.
func (m *model) fetchProviderModels(provider core.Provider) tea.Cmd {
	if m.providerModels == nil {
		m.providerModels = make(map[core.Provider]providerModelsState)
	}
	if !m.providerModels[provider].known() {
		m.providerModels[provider] = providerModelsState{}
	}
	return listProviderModelsCmd(m.statusContext, m.frontend, provider)
}

// setProviderModels records a model list fetch's result. A failure does not
// replace a list with models in it. A Launch choice made for the provider
// drops a reasoning level its model turns out not to offer, so it holds what
// it shows.
func (m *model) setProviderModels(provider core.Provider, models []core.ProviderModel, err error) {
	if m.providerModels == nil {
		m.providerModels = make(map[core.Provider]providerModelsState)
	}
	if err != nil {
		if !m.providerModels[provider].known() {
			m.providerModels[provider] = providerModelsState{err: err}
		}
		return
	}
	m.providerModels[provider] = providerModelsState{models: slices.Clone(models), loaded: true}
	switch familyOf(m.mode) {
	case familyDraft:
		if provider == m.effectiveCreateProvider() {
			m.draft.choice = m.draftLaunchChoice()
		}
	case familySetup:
		if choice, edited := m.setupForm.edits[provider]; edited {
			m.setupForm.edits[provider] = m.normalizedLaunchChoice(provider, choice)
		}
	}
}

// fetchLaunchDefaults starts a numbered fetch of the Launch defaults, which
// show as loading until it answers unless they already loaded.
func (m *model) fetchLaunchDefaults() tea.Cmd {
	m.launchDefaults.fetches++
	if !m.launchDefaults.loaded {
		m.launchDefaults.err = nil
	}
	return getLaunchDefaultsCmd(m.statusContext, m.frontend, m.launchDefaults.fetches)
}

// launchDefaultsAfterCreateCmd refetches the Launch defaults once a task
// creation that remembered a Launch choice ends, so they match what the
// daemon saved, which is nothing when creation failed its first checks.
func (m *model) launchDefaultsAfterCreateCmd() tea.Cmd {
	if !m.launchDefaults.refetchAfterCreate {
		return nil
	}
	m.launchDefaults.refetchAfterCreate = false
	return m.fetchLaunchDefaults()
}

// setLaunchDefaults records a Launch defaults fetch's result. It drops a
// reply older than the defaults it holds or than a remembered Launch choice,
// and a failure of any fetch but the latest. A draft whose choice the user
// has not cycled yet starts over from its provider's.
func (m *model) setLaunchDefaults(msg launchDefaultsLoadedMsg) {
	state := &m.launchDefaults
	if msg.fetch <= state.staleAt || msg.fetch <= state.applied {
		return
	}
	if msg.err != nil {
		if !state.loaded && msg.fetch == state.fetches {
			state.err = msg.err
		}
		return
	}
	state.choices = maps.Clone(msg.defaults)
	state.loaded, state.err, state.applied = true, nil, msg.fetch
	if familyOf(m.mode) == familyDraft && !m.draft.choiceTouched {
		m.startDraftLaunchChoice()
	}
}

// rememberLaunchDefault makes a submitted Launch choice its provider's Launch
// default at once, as the daemon does on creation, so the next draft starts
// from it. The creation's end fetches the defaults again.
func (m *model) rememberLaunchDefault(provider core.Provider, choice core.LaunchChoice) {
	if provider == "" {
		return
	}
	m.setLocalLaunchDefault(provider, choice)
	m.launchDefaults.refetchAfterCreate = true
}

// setLocalLaunchDefault makes choice provider's Launch default at once, ahead
// of a fetch that has it, and an empty choice clears it. Fetches already
// under way predate it, so their replies are dropped.
func (m *model) setLocalLaunchDefault(provider core.Provider, choice core.LaunchChoice) {
	state := &m.launchDefaults
	state.staleAt = state.fetches
	if state.choices == nil {
		state.choices = make(map[core.Provider]core.LaunchChoice)
	}
	if choice.IsZero() {
		delete(state.choices, provider)
		return
	}
	state.choices[provider] = choice
}

// startDraftLaunchChoice starts the draft's Launch choice over from its
// provider's Launch default.
func (m *model) startDraftLaunchChoice() {
	provider := m.effectiveCreateProvider()
	m.draft.choice = m.normalizedLaunchChoice(provider, m.launchDefaults.choices[provider])
	m.draft.choiceTouched = false
}

// cycleDraftProvider moves the draft to the next configured provider and to
// that provider's Launch default.
func (m *model) cycleDraftProvider() {
	before := m.effectiveCreateProvider()
	m.cycleCreateProvider()
	if m.effectiveCreateProvider() != before {
		m.startDraftLaunchChoice()
	}
}

// cycleDraftLaunchChoice moves the draft's Launch choice on by step, which
// touches it when the key does anything.
func (m *model) cycleDraftLaunchChoice(step launchChoiceStep) {
	if choice, ok := step(*m, m.effectiveCreateProvider(), m.draftLaunchChoice()); ok {
		m.draft.choice, m.draft.choiceTouched = choice, true
	}
}

// draftLaunchChoice is the Launch choice the draft shows and submits.
func (m model) draftLaunchChoice() core.LaunchChoice {
	return m.normalizedLaunchChoice(m.effectiveCreateProvider(), m.draft.choice)
}

// launchChoiceStep moves a Launch choice made for a provider on by one key
// press, and reports whether the key does anything for it. The task draft and
// the provider setup screen cycle their Launch choices with the same steps:
// nextLaunchModel for ctrl+o and nextLaunchReasoningLevel for ctrl+r.
type launchChoiceStep func(m model, provider core.Provider, choice core.LaunchChoice) (core.LaunchChoice, bool)

// nextLaunchModel moves choice from no model through each model provider
// lists and back, and from a model the list lacks to no model. It drops a
// reasoning level the new model does not offer, and any level on the way back
// to no model. It does nothing until a list loads.
func (m model) nextLaunchModel(provider core.Provider, choice core.LaunchChoice) (core.LaunchChoice, bool) {
	models, known := m.launchModels(provider)
	if !known {
		return choice, false
	}
	options := make([]string, 0, len(models)+1)
	options = append(options, "")
	for _, listed := range models {
		options = append(options, listed.ID)
	}
	choice = m.normalizedLaunchChoice(provider, choice)
	choice.Model = nextOption(options, choice.Model)
	return m.normalizedLaunchChoice(provider, choice), true
}

// nextLaunchReasoningLevel moves choice from no reasoning level through each
// level its listed model offers and back. It does nothing without a model,
// for a model the list lacks or that takes no level, and until a list loads.
func (m model) nextLaunchReasoningLevel(provider core.Provider, choice core.LaunchChoice) (core.LaunchChoice, bool) {
	levels, known := m.launchReasoningLevels(provider, choice.Model)
	if !known || len(levels) == 0 {
		return choice, false
	}
	choice = m.normalizedLaunchChoice(provider, choice)
	choice.ReasoningLevel = nextOption(append([]string{""}, levels...), choice.ReasoningLevel)
	return choice, true
}

// nextOption is the option after current, wrapping around, or the first
// option when current is none of them.
func nextOption(options []string, current string) string {
	index := slices.Index(options, current)
	if index < 0 {
		return options[0]
	}
	return options[(index+1)%len(options)]
}

// launchModels is a provider's model list, and whether it is known: loaded,
// and not empty.
func (m model) launchModels(provider core.Provider) ([]core.ProviderModel, bool) {
	state := m.providerModels[provider]
	return state.models, state.known()
}

// launchReasoningLevels is the reasoning levels a provider's model offers,
// and whether they are known, which they are only for a model its list has.
// A reasoning level needs a model, and Rig cannot tell which levels the CLI's
// default model or a model the list lacks accepts.
func (m model) launchReasoningLevels(provider core.Provider, id string) ([]string, bool) {
	models, known := m.launchModels(provider)
	if !known {
		return nil, false
	}
	selected, ok := findProviderModel(models, id)
	if !ok {
		return nil, false
	}
	return selected.ReasoningLevels, true
}

// normalizedLaunchChoice is a Launch choice made for provider as it shows and
// is submitted or saved. A reasoning level needs a model, so without one it
// reads as none, as it does when the listed model does not offer it. A model
// the list lacks keeps its level, as its levels are unknown.
func (m model) normalizedLaunchChoice(provider core.Provider, choice core.LaunchChoice) core.LaunchChoice {
	if choice.Model == "" {
		choice.ReasoningLevel = ""
		return choice
	}
	levels, known := m.launchReasoningLevels(provider, choice.Model)
	if known && !slices.Contains(levels, choice.ReasoningLevel) {
		choice.ReasoningLevel = ""
	}
	return choice
}

func findProviderModel(models []core.ProviderModel, id string) (core.ProviderModel, bool) {
	if id == "" {
		return core.ProviderModel{}, false
	}
	for _, listed := range models {
		if listed.ID == id {
			return listed, true
		}
	}
	return core.ProviderModel{}, false
}

// launchChoiceView renders the draft's provider and Launch choice as a block
// of labeled lines, each followed by the key that cycles it while that key
// does anything, or by the model list's state. Until the Launch defaults
// load, an untouched draft's model says so instead, as it does not start
// from the provider's Launch default.
func (m model) launchChoiceView(width int) string {
	provider := m.effectiveCreateProvider()
	choice := m.draftLaunchChoice()

	providerRow := choiceRow{label: "provider", value: providerStyle(string(provider)).Render(string(provider))}
	if len(m.configuredProviders()) > 1 {
		providerRow.key = keybindStyle.Render("tab")
	}

	modelRow := choiceRow{label: "model", value: m.launchModelText(provider, choice.Model)}
	if state := m.launchDefaultsStateText(); state != "" && !m.draft.choiceTouched {
		modelRow.value = mutedStyle.Render(state)
	}
	if _, cycles := m.nextLaunchModel(provider, choice); cycles {
		modelRow.key = keybindStyle.Render("ctrl+o")
	} else if state := m.modelListStateText(provider); state != "" {
		modelRow.key = mutedStyle.Render(state)
	}

	levelRow := choiceRow{label: "reasoning", value: launchChoiceValue(choice.ReasoningLevel)}
	if _, cycles := m.nextLaunchReasoningLevel(provider, choice); cycles {
		levelRow.key = keybindStyle.Render("ctrl+r")
	}

	return renderChoiceRows([]choiceRow{providerRow, modelRow, levelRow}, width)
}

// launchDefaultsStateText says the Launch defaults are loading or
// unavailable while they have not loaded, and is empty once they have.
func (m model) launchDefaultsStateText() string {
	switch {
	case m.launchDefaults.loaded:
		return ""
	case m.launchDefaults.err != nil:
		return "defaults unavailable"
	default:
		return "loading defaults"
	}
}

// modelListStateText says a provider's model list is loading or unavailable,
// and is empty once it answered with a list, empty or not.
func (m model) modelListStateText(provider core.Provider) string {
	state := m.providerModels[provider]
	switch {
	case state.known():
		return ""
	case state.err != nil:
		return "models unavailable"
	case !state.loaded:
		return "loading models"
	default:
		return ""
	}
}

// modelDetailText is how the detail panel's summary names the model and
// reasoning level of the agent it describes: on a sub-row that agent's, and
// on a task row its agent's while it has exactly one live agent session, as
// with its context. Until that agent's model is known, and while the task has
// no live agent session, a task row names the task's Launch choice instead,
// but only a choice that was made: the CLI's default says nothing. It is
// empty when there is nothing to name.
func (m model) modelDetailText(row taskRow, agentSession *core.LiveAgentSession) string {
	described, provider := agentSession, row.task.Provider
	if described == nil && row.status != nil {
		if len(row.status.AgentSessions) > 1 {
			// Each agent's sub-row names its own.
			return ""
		}
		if len(row.status.AgentSessions) == 1 {
			described = &row.status.AgentSessions[0]
		}
	}
	if described != nil {
		provider = described.Provider
		if running, ok := agentModel(row, described.ID); ok {
			models := m.providerModels[provider].models
			id, level := listedModelID(models, running.Model), running.ReasoningLevel
			// An agent's level is unknown until its first request, but one
			// running the task's Launch model was launched with its level.
			if launchModel, launchLevel := row.task.LaunchChoiceFor(provider); level == "" && launchModel != "" &&
				listedModelID(models, launchModel) == id {
				level = launchLevel
			}
			return m.modelLevelText(provider, id, level)
		}
		if agentSession != nil {
			return ""
		}
	}
	model, level := row.task.LaunchChoiceFor(provider)
	if model == "" && level == "" {
		return ""
	}
	return m.modelLevelText(provider, model, level)
}

// modelLevelText names a model as launchModelText does, but in its
// provider's color, then its reasoning level when it has one.
func (m model) modelLevelText(provider core.Provider, id string, level string) string {
	text := launchChoiceValue("")
	if name := m.modelName(provider, id); name != "" {
		text = providerStyle(string(provider)).Render(name)
	}
	if level = terminalSafeText(level); level != "" {
		text += mutedStyle.Render(" · ") + primaryStyle.Render(level)
	}
	return text
}

// listedModelID is the ID of the listed model that a model an agent runs on
// is, or that model's own ID when the list has none. Transcripts and hooks
// name a model by its full ID, which a listed model can stand for under an
// alias, as Claude's opus does, and Claude's hooks add a suffix, such as
// [1m], that the list leaves out.
func listedModelID(models []core.ProviderModel, id string) string {
	if _, ok := findProviderModel(models, id); ok {
		return id
	}
	base := withoutModelSuffix(id)
	for _, listed := range models {
		if strings.EqualFold(withoutModelSuffix(listed.ID), base) ||
			(listed.ResolvedID != "" && strings.EqualFold(withoutModelSuffix(listed.ResolvedID), base)) {
			return listed.ID
		}
	}
	return id
}

// withoutModelSuffix is a model ID without a suffix such as [1m].
func withoutModelSuffix(id string) string {
	base, _, _ := strings.Cut(id, "[")
	return base
}

// launchModelLabel and launchModelLabelGap lead each line on the provider
// setup screen that shows a Launch model.
const launchModelLabel, launchModelLabelGap = "model", "  "

// launchModelText is how a Launch model reads: by the display name its
// provider's model list gives it, else by its ID, and a dim "default" for no
// model.
func (m model) launchModelText(provider core.Provider, id string) string {
	return launchChoiceValue(m.modelName(provider, id))
}

// modelName is a model's display name in its provider's model list, else its
// ID.
func (m model) modelName(provider core.Provider, id string) string {
	if listed, ok := findProviderModel(m.providerModels[provider].models, id); ok {
		if name := terminalSafeText(listed.DisplayName); name != "" {
			return name
		}
	}
	return terminalSafeText(id)
}

// launchChoiceValue renders a Launch choice value, or a dim "default" for
// none.
func launchChoiceValue(value string) string {
	if value = terminalSafeText(value); value == "" {
		return dimStyle.Render("default")
	}
	return primaryStyle.Render(value)
}

// choiceRow is one line of the choice block: a label, its value, and the
// key or state after it, or none. Value and key come styled.
type choiceRow struct {
	label string
	value string
	key   string
}

// choiceSeparator separates a choice block's columns.
const choiceSeparator = "  · "

// renderChoiceRows lays rows out as columns: the labels, the values padded to
// the widest of them, then each key. Within width, values are truncated and
// padded no wider than the narrowest line with a key has room for, so labels
// and keys always show; a line without a key loses its separator too.
func renderChoiceRows(rows []choiceRow, width int) string {
	separator := mutedStyle.Render(choiceSeparator)
	labelWidth, valueWidth := 0, 0
	for _, row := range rows {
		labelWidth = max(labelWidth, lipgloss.Width(row.label))
		valueWidth = max(valueWidth, lipgloss.Width(row.value))
	}
	separatorWidth := lipgloss.Width(choiceSeparator)
	// room is how wide a row's value may be within width.
	room := func(row choiceRow) int {
		used := labelWidth + separatorWidth
		if row.key != "" {
			used += separatorWidth + lipgloss.Width(row.key)
		}
		return max(width-used, 1)
	}
	if width > 0 {
		for _, row := range rows {
			if row.key != "" {
				valueWidth = min(valueWidth, room(row))
			}
		}
	}

	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		line := mutedStyle.Render(padRightVisible(row.label, labelWidth)) + separator
		switch {
		case row.key != "":
			value := row.value
			if width > 0 {
				value = truncateStr(value, valueWidth)
			}
			line += padRightVisible(value, valueWidth) + separator + row.key
		case width > 0:
			line += truncateStr(row.value, room(row))
		default:
			line += row.value
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}
