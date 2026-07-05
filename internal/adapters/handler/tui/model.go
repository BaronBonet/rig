package tui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/BaronBonet/rig/internal/core"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
)

type taskRow struct {
	task        *core.Task
	activity    []core.TaskActivityEvent
	status      *core.TaskStatusUpdate
	tokenUsage  *core.TaskTokenUsage
	pullRequest *core.PRStatus
}

type modelMode int

const (
	modeBrowse modelMode = iota
	modePromptInput
	modePRPicker
	modeCleanupConfirm
	modeProviderSetup
	modeSwitchProvider
)

const defaultBuildVersion = "dev"

// pendingOp is the single in-flight task operation. Create, delete, and
// switch are mutually exclusive: while one is pending the TUI refuses to
// start another.
type pendingOp int

const (
	opNone pendingOp = iota
	opCreating
	opDeleting
	opSwitching
)

const taskActivityPreviewLimit = 6

// nolint:recvcheck // bubbletea requires value receivers for tea.Model.
type model struct {
	frontend      core.TaskFrontend
	statusContext context.Context
	err           error
	cancelStatus  context.CancelFunc
	rows          []taskRow
	providerSetup *core.ProviderSetup
	selected      int
	width         int
	height        int
	shimmerTick   int
	mode          modelMode
	pending       pendingOp
	launchCwd     string
	buildVersion  string
	loading       bool
	detailsHidden bool
	setupOnly     bool

	// adoptionReloads dampens the reload triggered by a status/record provider
	// mismatch: one reload per observed mismatch. When the mismatch survives a
	// reload, the stale side is the daemon's status row, not the task list —
	// reloading again cannot fix it and would loop forever.
	adoptionReloads map[string]core.Provider
	// statusSubscribed tracks tasks with a live status subscription so task
	// reloads do not open a duplicate subscription per reload.
	statusSubscribed map[string]bool

	// Per-mode state, cleared by transition() when the mode family changes.
	draft          taskDraft
	setupForm      setupFormState
	providerSwitch switchState

	// In-flight creation progress; outlives the draft and renders in browse.
	create createFlowState
}

// taskDraft is the in-progress task the user is assembling before
// submission: the prompt text, the Tab-cycled provider choice, and the
// optional pull request source. It spans the prompt-input and PR-picker
// modes and is discarded when the TUI leaves that mode family.
type taskDraft struct {
	prompt     string
	input      textarea.Model
	provider   core.Provider // "" means use the configured default
	prs        []core.RepoPullRequest
	prSelected int
	repoRoot   string
	repoName   string
	err        error // validation error shown inside the draft views
}

// createFlowState is the progress of an in-flight or just-failed task
// creation, rendered in the browse list after the draft is submitted.
type createFlowState struct {
	active core.TaskCreateProgressStep
	done   []core.TaskCreateProgressStep
	fromPR bool
	err    error
}

// setupFormState is the provider setup screen's working state.
type setupFormState struct {
	rows            []providerSetupRow
	selected        int
	defaultProvider core.Provider
	detecting       bool
	saving          bool
	err             error
}

// switchState is the provider switch picker's working state.
type switchState struct {
	options  []core.Provider
	selected int
}

// providerSetupRow is one supported provider in the provider setup UI.
type providerSetupRow struct {
	provider core.Provider
	detail   string
	ready    bool
	enabled  bool
}

type tasksLoadedMsg struct {
	err   error
	tasks []*core.Task
}

type latestTaskStatusLoadedMsg struct {
	status *core.TaskStatusUpdate
	err    error
	taskID string
}

type pullRequestStatusLoadedMsg struct {
	status *core.PRStatus
	err    error
	taskID string
}

type taskActivityLoadedMsg struct {
	activity []core.TaskActivityEvent
	err      error
	taskID   string
}

type taskTokenUsageLoadedMsg struct {
	usage  *core.TaskTokenUsage
	err    error
	taskID string
}

type taskStatusSubscriptionReadyMsg struct {
	updates <-chan core.TaskStatusUpdate
	err     error
	taskID  string
}

type taskStatusUpdatedMsg struct {
	updates <-chan core.TaskStatusUpdate
	update  core.TaskStatusUpdate
	taskID  string
}

type taskStatusSubscriptionClosedMsg struct {
	taskID string
}

type taskCreatedMsg struct {
	task *core.Task
	err  error
}

type taskOpenedMsg struct {
	err error
}

type taskCreateStreamStartFailedMsg struct {
	err error
}

type taskCreateEventMsg struct {
	events <-chan core.TaskCreateEvent
	event  core.TaskCreateEvent
}

type taskCreateStreamClosedMsg struct{}

type taskDeletedMsg struct {
	err    error
	taskID string
}

type repoPullRequestsLoadedMsg struct {
	err      error
	prs      []core.RepoPullRequest
	repoRoot string
	repoName string
}

type providerSetupLoadedMsg struct {
	setup *core.ProviderSetup
	err   error
}

type providerDetectionsMsg struct {
	err        error
	detections []core.ProviderDetection
}

type providerSetupSavedMsg struct {
	err   error
	setup core.ProviderSetup
}

type taskProviderSwitchedMsg struct {
	task *core.Task
	err  error
}

type shimmerTickMsg struct{}

type activityRefreshTickMsg struct{}

func newModel(frontend core.TaskFrontend, launchCwd string, buildVersion string) model {
	statusContext, cancelStatus := context.WithCancel(context.Background())

	buildVersion = strings.TrimSpace(buildVersion)
	if buildVersion == "" {
		buildVersion = defaultBuildVersion
	}

	return model{
		frontend:         frontend,
		statusContext:    statusContext,
		cancelStatus:     cancelStatus,
		launchCwd:        strings.TrimSpace(launchCwd),
		buildVersion:     buildVersion,
		draft:            taskDraft{input: newPromptInput()},
		loading:          true,
		mode:             modeBrowse,
		detailsHidden:    true,
		adoptionReloads:  make(map[string]core.Provider),
		statusSubscribed: make(map[string]bool),
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		getProviderSetupCmd(m.statusContext, m.frontend),
		loadTasksCmd(m.statusContext, m.frontend),
		activityRefreshTickCmd(),
	)
}

// configuredProviders returns the user's configured providers in supported
// order, or nil before provider setup has completed.
func (m model) configuredProviders() []core.Provider {
	if m.providerSetup == nil {
		return nil
	}

	ordered := make([]core.Provider, 0, len(m.providerSetup.Configured))
	for _, provider := range core.SupportedProviders() {
		if m.providerSetup.IsConfigured(provider) {
			ordered = append(ordered, provider)
		}
	}
	return ordered
}

// effectiveCreateProvider is the provider new tasks will use: the provider
// the user cycled to with Tab, falling back to the configured default.
func (m model) effectiveCreateProvider() core.Provider {
	if m.providerSetup == nil {
		return m.draft.provider
	}
	if m.draft.provider != "" && m.providerSetup.IsConfigured(m.draft.provider) {
		return m.draft.provider
	}
	return m.providerSetup.Default
}

// cycleCreateProvider advances the create-flow provider selection to the next
// configured provider. With a single configured provider it is a no-op.
func (m *model) cycleCreateProvider() {
	providers := m.configuredProviders()
	if len(providers) < 2 {
		return
	}

	current := m.effectiveCreateProvider()
	for index, provider := range providers {
		if provider == current {
			m.draft.provider = providers[(index+1)%len(providers)]
			return
		}
	}
	m.draft.provider = providers[0]
}

// quit cancels the background status context before exiting the program.
func (m model) quit() (tea.Model, tea.Cmd) {
	if m.cancelStatus != nil {
		m.cancelStatus()
	}
	return m, tea.Quit
}

// beginOp marks a task operation in flight and restarts the shimmer.
func (m *model) beginOp(op pendingOp) {
	m.pending = op
	m.shimmerTick = 0
}

// endOp clears the in-flight operation and stops the shimmer.
func (m *model) endOp() {
	m.pending = opNone
	m.shimmerTick = 0
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tea.PasteMsg:
		return m.updatePromptPaste(msg)
	case tea.KeyPressMsg:
		if isForceQuitKey(msg) {
			return m.quit()
		}
		if m.pending == opCreating && isQuitKey(msg) {
			return m.quit()
		}

		if m.mode == modePromptInput {
			return m.updatePromptInput(msg)
		}
		if m.mode == modePRPicker {
			return m.updatePRPicker(msg)
		}
		if m.mode == modeCleanupConfirm {
			return m.updateCleanupConfirm(msg)
		}
		if m.mode == modeProviderSetup {
			return m.updateProviderSetup(msg)
		}
		if m.mode == modeSwitchProvider {
			return m.updateSwitchProvider(msg)
		}

		if isQuitKey(msg) {
			return m.quit()
		}

		// A displayed error stays until the user acknowledges it with a key
		// press; background refreshes must not clear it.
		m.err = nil

		switch msg.String() {
		case "esc":
			return m.handleBack()
		case "a", "n":
			if m.pending != opNone {
				return m, nil
			}
			if m.providerSetup == nil {
				return m.enterProviderSetupMode()
			}
			return m.enterPromptInputMode("")
		case "p":
			return m.enterSwitchProviderMode()
		case "S":
			return m.enterProviderSetupMode()
		case "r":
			m.loading = true
			return m, loadTasksCmd(m.statusContext, m.frontend)
		case "R":
			return m.retrySelectedTaskCreation()
		case "g", "home":
			m.selected = 0
		case "G", "end":
			m.selected = len(m.rows) - 1
		case "pgdown":
			m.moveSelection(m.taskListPageSize())
		case "pgup":
			m.moveSelection(-m.taskListPageSize())
		case "j", "down":
			m.moveSelection(1)
		case "k", "up":
			m.moveSelection(-1)
		case " ", "space":
			m.detailsHidden = !m.detailsHidden
		case "x":
			if len(m.rows) == 0 {
				return m, nil
			}
			m.transition(modeCleanupConfirm)
			return m, nil
		case "enter":
			if len(m.rows) == 0 {
				return m, nil
			}
			row := m.selectedRow()
			if row == nil || row.task == nil {
				return m, nil
			}
			return m, openTaskSessionCmd(m.statusContext, m.frontend, row.task)
		}
		m.clampSelection()
		return m, nil
	case tasksLoadedMsg:
		m.loading = false
		// Only surface a failure; a successful background reload must not
		// clear an unacknowledged error shown to the user.
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}

		selectedTaskID := ""
		if row := m.selectedRow(); row != nil {
			selectedTaskID = taskID(row.task)
		}
		m.rows = rowsFromTasks(msg.tasks)
		m.clampSelection()
		m.selectTask(selectedTaskID)
		return m, tea.Batch(m.afterTasksLoadedCmds()...)
	case latestTaskStatusLoadedMsg:
		if msg.err != nil {
			return m, nil
		}
		m.setTaskStatus(msg.taskID, msg.status)
		return m, nil
	case pullRequestStatusLoadedMsg:
		if msg.err != nil {
			return m, nil
		}
		m.setTaskPullRequestStatus(msg.taskID, msg.status)
		return m, nil
	case taskActivityLoadedMsg:
		if msg.err != nil {
			return m, nil
		}
		m.setTaskActivity(msg.taskID, msg.activity)
		return m, nil
	case taskTokenUsageLoadedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.setTaskTokenUsage(msg.taskID, msg.usage)
		return m, nil
	case taskStatusSubscriptionReadyMsg:
		if msg.err != nil {
			delete(m.statusSubscribed, msg.taskID)
			return m, nil
		}
		return m, waitForTaskStatusCmd(msg.taskID, msg.updates)
	case taskStatusUpdatedMsg:
		update := msg.update
		m.setTaskStatus(msg.taskID, &update)
		cmds := []tea.Cmd{
			taskActivityCmd(m.statusContext, m.frontend, msg.taskID, taskActivityPreviewLimit),
			taskTokenUsageCmd(m.statusContext, m.frontend, msg.taskID),
			waitForTaskStatusCmd(msg.taskID, msg.updates),
		}
		// A live status from a different provider than the task record means
		// the daemon adopted a manually launched provider; reload tasks so the
		// displayed active provider reflects reality. One reload per observed
		// mismatch: if it persists after a reload, the daemon's status row is
		// the stale side and reloading again cannot fix it.
		if row := m.taskRowByID(msg.taskID); row != nil && row.task != nil && update.Provider != "" {
			switch {
			case row.task.Provider == update.Provider:
				delete(m.adoptionReloads, msg.taskID)
			case m.adoptionReloads[msg.taskID] != update.Provider:
				m.adoptionReloads[msg.taskID] = update.Provider
				cmds = append(cmds, loadTasksCmd(m.statusContext, m.frontend))
			}
		}
		return m, tea.Batch(cmds...)
	case taskStatusSubscriptionClosedMsg:
		delete(m.statusSubscribed, msg.taskID)
		return m, nil
	case providerSetupLoadedMsg:
		if msg.err != nil {
			// Invalid provider setup gates the TUI the same way missing setup
			// does; the setup screen shows what went wrong.
			m.setupForm.err = msg.err
			next, cmd := m.enterProviderSetupMode()
			return next, cmd
		}
		m.providerSetup = msg.setup
		if msg.setup == nil || m.setupOnly {
			next, cmd := m.enterProviderSetupMode()
			return next, cmd
		}
		return m, nil
	case providerDetectionsMsg:
		m.setupForm.detecting = false
		if msg.err != nil {
			m.setupForm.err = msg.err
			return m, nil
		}
		m.applyProviderDetections(msg.detections)
		return m, nil
	case providerSetupSavedMsg:
		m.setupForm.saving = false
		if msg.err != nil {
			m.setupForm.err = msg.err
			return m, nil
		}
		saved := msg.setup
		m.providerSetup = &saved
		m.setupForm.err = nil
		m.draft.provider = ""
		if m.setupOnly {
			return m.quit()
		}
		m.transition(modeBrowse)
		m.loading = true
		return m, loadTasksCmd(m.statusContext, m.frontend)
	case taskProviderSwitchedMsg:
		m.endOp()
		m.transition(modeBrowse)
		if msg.err != nil {
			// A failed or refused switch keeps the previous active provider.
			m.err = msg.err
			return m, nil
		}
		m.err = nil
		if index := m.upsertTaskRow(msg.task); index >= 0 {
			m.selected = index
		}
		m.clampSelection()
		return m, tea.Batch(m.taskStatusTrackingCmds(taskID(msg.task))...)
	case repoPullRequestsLoadedMsg:
		m.draft.err = msg.err
		if msg.err != nil {
			m.draft.prs = nil
			return m, nil
		}
		m.draft.repoRoot = msg.repoRoot
		m.draft.repoName = msg.repoName
		m.draft.prs = append([]core.RepoPullRequest(nil), msg.prs...)
		m.clampPRSelection()
		return m, nil
	case taskCreatedMsg:
		m.endOp()
		if msg.err != nil {
			m.create.err = msg.err
			return m, nil
		}

		m.transition(modeBrowse)
		m.create = createFlowState{}
		index := m.upsertTaskRow(msg.task)
		if index >= 0 {
			m.selected = index
		}
		m.clampSelection()
		cmds := []tea.Cmd{loadTasksCmd(m.statusContext, m.frontend)}
		cmds = append(cmds, m.taskStatusTrackingCmds(taskID(msg.task))...)
		return m, tea.Batch(cmds...)
	case taskCreateStreamStartFailedMsg:
		m.endOp()
		m.create.err = msg.err
		return m, nil
	case taskCreateEventMsg:
		switch {
		case msg.event.Progress != nil:
			m.advanceCreateProgress(msg.event.Progress.Step)
			return m, waitForTaskCreateEventCmd(msg.events)
		case msg.event.Err != nil:
			m.endOp()
			m.create.err = msg.event.Err
			if msg.event.Task != nil {
				if index := m.upsertTaskRow(msg.event.Task); index >= 0 {
					m.selected = index
				}
				m.clampSelection()
			}
			return m, nil
		case msg.event.Task != nil:
			return m.Update(taskCreatedMsg{task: msg.event.Task})
		default:
			return m, waitForTaskCreateEventCmd(msg.events)
		}
	case taskCreateStreamClosedMsg:
		if m.pending != opCreating {
			return m, nil
		}
		m.endOp()
		if m.create.err == nil {
			m.create.err = errors.New("create task stream closed unexpectedly")
		}
		return m, nil
	case taskOpenedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.err = nil
		return m, nil
	case taskDeletedMsg:
		m.endOp()
		m.transition(modeBrowse)
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}

		m.err = nil
		m.removeTaskRow(msg.taskID)
		delete(m.statusSubscribed, msg.taskID)
		delete(m.adoptionReloads, msg.taskID)
		m.clampSelection()
		return m, nil
	case activityRefreshTickMsg:
		cmds := []tea.Cmd{activityRefreshTickCmd()}
		if row := m.selectedRow(); row != nil && row.task != nil && taskID(row.task) != "" {
			id := taskID(row.task)
			cmds = append(cmds,
				taskActivityCmd(m.statusContext, m.frontend, id, taskActivityPreviewLimit),
				taskTokenUsageCmd(m.statusContext, m.frontend, id),
			)
		}
		return m, tea.Batch(cmds...)
	case shimmerTickMsg:
		if m.pending == opNone {
			return m, nil
		}
		m.shimmerTick++
		return m, shimmerTickCmd()
	default:
		if m.mode == modePromptInput {
			return m.updatePromptInput(msg)
		}
		return m, nil
	}
}

func (m model) View() tea.View {
	var body string
	switch m.mode {
	case modePromptInput:
		body = m.promptInputView()
	case modePRPicker:
		body = m.prPickerView()
	case modeCleanupConfirm:
		body = m.confirmationView()
	case modeProviderSetup:
		body = m.providerSetupView()
	case modeSwitchProvider:
		body = m.switchProviderView()
	default:
		body = m.listView()
	}

	view := tea.NewView(body)
	view.AltScreen = true
	return view
}

func rowsFromTasks(tasks []*core.Task) []taskRow {
	rows := make([]taskRow, 0, len(tasks))
	for _, task := range tasks {
		if task == nil {
			continue
		}
		rows = append(rows, taskRow{task: task})
	}
	return groupRowsByRepo(rows)
}

func (m *model) afterTasksLoadedCmds() []tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(m.rows)*4)
	for _, row := range m.rows {
		taskID := taskID(row.task)
		cmds = append(cmds, m.taskStatusTrackingCmds(taskID)...)
		if taskID != "" {
			cmds = append(cmds, taskActivityCmd(m.statusContext, m.frontend, taskID, taskActivityPreviewLimit))
			cmds = append(cmds, taskTokenUsageCmd(m.statusContext, m.frontend, taskID))
		}
		if cmd := m.taskPullRequestStatusCmd(row.task); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return cmds
}

func (m model) submitPrompt() (model, tea.Cmd) {
	m.ensurePromptInputInitialized()
	prompt := strings.TrimSpace(m.promptValue())
	if prompt == "" {
		return m, nil
	}

	// Capture the draft's inputs before the transition discards it.
	cwd := m.currentCreateCwd()
	provider := m.effectiveCreateProvider()
	m.transition(modeBrowse)
	m.beginOp(opCreating)
	m.create = createFlowState{}

	return m, tea.Batch(
		createTaskStreamCmd(m.statusContext, m.frontend, core.CreateTaskInput{
			Cwd:      cwd,
			Prompt:   prompt,
			Provider: provider,
		}),
		shimmerTickCmd(),
	)
}

func (m model) retrySelectedTaskCreation() (model, tea.Cmd) {
	if m.pending != opNone {
		return m, nil
	}
	row := m.selectedRow()
	if row == nil || row.task == nil || row.task.CreationStatus != core.TaskCreationStatusFailed {
		return m, nil
	}

	m.transition(modeBrowse)
	m.beginOp(opCreating)
	m.create = createFlowState{active: row.task.CreationStep}
	m.err = nil

	return m, tea.Batch(
		retryTaskCreationStreamCmd(m.statusContext, m.frontend, row.task.ID),
		shimmerTickCmd(),
	)
}

func (m model) updatePromptInput(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.pending != opNone {
		return m, nil
	}
	m.ensurePromptInputInitialized()
	if m.draft.input.Value() != m.draft.prompt {
		m.draft.input.SetValue(m.draft.prompt)
	}
	var focusCmd tea.Cmd
	if !m.draft.input.Focused() {
		focusCmd = m.draft.input.Focus()
	}

	switch typed := msg.(type) {
	case tea.KeyPressMsg:
		if typed.String() == "tab" {
			// Tab cycles through configured providers for the new task.
			m.cycleCreateProvider()
			return m, nil
		}
		if typed.String() == "ctrl+p" {
			repoRoot, repoName, ok := m.currentRepoScope()
			if !ok {
				m.draft.err = errors.New("repo scope unavailable")
				return m, nil
			}

			if !m.transition(modePRPicker) {
				return m, nil
			}
			m.draft.repoRoot = repoRoot
			m.draft.repoName = repoName
			m.draft.prs = nil
			m.draft.prSelected = 0
			m.draft.err = nil
			m.draft.input.Blur()
			return m, listRepoPullRequestsCmd(m.statusContext, m.frontend, repoRoot, repoName)
		}

		switch typed.Key().Code {
		case tea.KeyEscape:
			return m.handleBack()
		case tea.KeyEnter:
			return m.submitPrompt()
		}
	}

	previousValue := m.draft.input.Value()
	updatedInput, cmd := m.draft.input.Update(msg)
	m.draft.input = updatedInput
	m.draft.prompt = m.draft.input.Value()
	if m.draft.prompt != previousValue {
		m.draft.err = nil
	}

	if focusCmd != nil {
		return m, tea.Batch(focusCmd, cmd)
	}
	return m, cmd
}

func (m model) updatePromptPaste(msg tea.PasteMsg) (tea.Model, tea.Cmd) {
	if m.mode != modePromptInput || m.pending != opNone {
		return m, nil
	}
	return m.updatePromptInput(msg)
}

func (m model) updatePRPicker(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.pending != opNone {
		return m, nil
	}

	switch msg.String() {
	case "esc", "q":
		return m.handleBack()
	case "g", "home":
		m.draft.prSelected = 0
		return m, nil
	case "G", "end":
		m.draft.prSelected = len(m.draft.prs) - 1
		return m, nil
	case "j", "down":
		m.movePRSelection(1)
		return m, nil
	case "k", "up":
		m.movePRSelection(-1)
		return m, nil
	case "enter":
		pr := m.selectedPR()
		if pr == nil {
			return m, nil
		}
		if pr.HasExistingTask {
			m.draft.err = errors.New("PR already has workspace")
			return m, nil
		}

		// Capture the draft's inputs before the transition discards it.
		selected := *pr
		repoRoot := m.draft.repoRoot
		provider := m.effectiveCreateProvider()
		m.transition(modeBrowse)
		m.beginOp(opCreating)
		m.create = createFlowState{fromPR: true}

		return m, tea.Batch(
			createTaskStreamCmd(m.statusContext, m.frontend, core.CreateTaskInput{
				Cwd:      repoRoot,
				Provider: provider,
				Source: core.CreateTaskSource{
					PullRequest: &selected,
				},
			}),
			shimmerTickCmd(),
		)
	case "tab":
		m.cycleCreateProvider()
		return m, nil
	default:
		return m, nil
	}
}

func (m model) enterPromptInputMode(initialValue string) (tea.Model, tea.Cmd) {
	if !m.transition(modePromptInput) {
		return m, nil
	}
	input := newPromptInput()
	input.SetValue(initialValue)
	m.draft = taskDraft{prompt: initialValue, input: input}
	m.create.err = nil
	m.create.fromPR = false
	return m, m.draft.input.Focus()
}

func (m model) promptValue() string {
	m.ensurePromptInputInitialized()
	if value := m.draft.input.Value(); value != "" || m.draft.prompt == "" {
		return value
	}
	return m.draft.prompt
}

func (m *model) ensurePromptInputInitialized() {
	if m.draft.input.MaxHeight != 0 {
		return
	}
	m.draft.input = newPromptInput()
}

func (m model) updateCleanupConfirm(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.pending != opNone {
		return m, nil
	}

	switch msg.String() {
	case "q", "n", "esc":
		return m.handleBack()
	case "y":
		row := m.selectedRow()
		if row == nil || row.task == nil {
			m.transition(modeBrowse)
			return m, nil
		}
		m.beginOp(opDeleting)
		m.err = nil
		return m, tea.Batch(
			deleteTaskCmd(m.statusContext, m.frontend, taskID(row.task)),
			shimmerTickCmd(),
		)
	default:
		return m, nil
	}
}

func (m *model) taskStatusTrackingCmds(taskID string) []tea.Cmd {
	if strings.TrimSpace(taskID) == "" {
		return nil
	}

	cmds := []tea.Cmd{latestTaskStatusCmd(m.statusContext, m.frontend, taskID)}
	if !m.statusSubscribed[taskID] {
		m.statusSubscribed[taskID] = true
		cmds = append(cmds, subscribeTaskStatusCmd(m.statusContext, m.frontend, taskID))
	}
	return cmds
}

func (m model) taskPullRequestStatusCmd(task *core.Task) tea.Cmd {
	if task == nil {
		return nil
	}
	taskID := strings.TrimSpace(task.ID)
	repoRoot := strings.TrimSpace(task.RepoRoot)
	branchName := strings.TrimSpace(task.BranchName)
	if taskID == "" || repoRoot == "" || branchName == "" {
		return nil
	}
	return pullRequestStatusCmd(m.statusContext, m.frontend, taskID, repoRoot, branchName)
}

// clampIndex clamps index into [0, length-1], or 0 for an empty list.
func clampIndex(index int, length int) int {
	if length <= 0 || index < 0 {
		return 0
	}
	if index >= length {
		return length - 1
	}
	return index
}

func (m *model) moveSelection(delta int) {
	m.selected = clampIndex(m.selected+delta, len(m.rows))
}

func (m model) taskListPageSize() int {
	if len(m.rows) == 0 {
		return 1
	}

	rowBudget := m.taskListRowBudget(m.totalWidth(), m.totalHeight())
	if rowBudget <= 0 {
		return 1
	}

	viewport := m.visibleTaskList(m.totalWidth(), rowBudget)
	pageSize := viewport.endBlock - viewport.startBlock + 1
	if pageSize < 1 {
		return 1
	}
	return pageSize
}

func (m *model) clampSelection() {
	m.selected = clampIndex(m.selected, len(m.rows))
}

func (m *model) movePRSelection(delta int) {
	m.draft.prSelected = clampIndex(m.draft.prSelected+delta, len(m.draft.prs))
}

func (m *model) clampPRSelection() {
	m.draft.prSelected = clampIndex(m.draft.prSelected, len(m.draft.prs))
}

func (m *model) selectTask(targetTaskID string) bool {
	targetTaskID = strings.TrimSpace(targetTaskID)
	if targetTaskID == "" {
		return false
	}

	for index, row := range m.rows {
		if taskID(row.task) != targetTaskID {
			continue
		}
		m.selected = index
		return true
	}

	return false
}

func (m *model) taskRowByID(taskID string) *taskRow {
	for i := range m.rows {
		if m.rows[i].task != nil && m.rows[i].task.ID == taskID {
			return &m.rows[i]
		}
	}
	return nil
}

func (m *model) setTaskStatus(taskID string, status *core.TaskStatusUpdate) {
	if row := m.taskRowByID(taskID); row != nil {
		row.status = status
	}
}

func (m *model) setTaskPullRequestStatus(taskID string, status *core.PRStatus) {
	if row := m.taskRowByID(taskID); row != nil {
		row.pullRequest = status
	}
}

func (m *model) setTaskActivity(taskID string, activity []core.TaskActivityEvent) {
	if row := m.taskRowByID(taskID); row != nil {
		row.activity = append([]core.TaskActivityEvent(nil), activity...)
	}
}

func (m *model) setTaskTokenUsage(taskID string, usage *core.TaskTokenUsage) {
	row := m.taskRowByID(taskID)
	if row == nil {
		return
	}
	if usage == nil {
		row.tokenUsage = nil
		return
	}
	copied := *usage
	row.tokenUsage = &copied
}

func (m *model) upsertTaskRow(task *core.Task) int {
	id := taskID(task)
	if id == "" {
		return -1
	}

	for i := range m.rows {
		if taskID(m.rows[i].task) != id {
			continue
		}
		m.rows[i].task = task
		return i
	}

	m.rows = append(m.rows, taskRow{task: task})
	m.rows = groupRowsByRepo(m.rows)
	for i := range m.rows {
		if taskID(m.rows[i].task) == id {
			return i
		}
	}
	return -1
}

func (m *model) advanceCreateProgress(step core.TaskCreateProgressStep) {
	if step == "" {
		return
	}
	if m.create.active != "" && m.create.active != step && !containsCreateStep(m.create.done, m.create.active) {
		m.create.done = append(m.create.done, m.create.active)
	}
	m.create.active = step
}

func (m *model) removeTaskRow(targetTaskID string) {
	filtered := m.rows[:0]
	for _, row := range m.rows {
		if taskID(row.task) == targetTaskID {
			continue
		}
		filtered = append(filtered, row)
	}
	m.rows = filtered
}

func taskID(task *core.Task) string {
	if task == nil {
		return ""
	}
	return strings.TrimSpace(task.ID)
}

func (m model) currentCreateCwd() string {
	if launchCwd := strings.TrimSpace(m.launchCwd); launchCwd != "" {
		return launchCwd
	}

	if row := m.selectedRow(); row != nil && row.task != nil {
		return strings.TrimSpace(row.task.RepoRoot)
	}

	return ""
}

func (m model) currentRepoScope() (string, string, bool) {
	launchCwd := strings.TrimSpace(m.launchCwd)
	if launchCwd != "" {
		return launchCwd, filepath.Base(launchCwd), true
	}

	if row := m.selectedRow(); row != nil && row.task != nil {
		repoRoot := strings.TrimSpace(row.task.RepoRoot)
		if repoRoot != "" {
			repoName := strings.TrimSpace(row.task.RepoName)
			if repoName == "" {
				repoName = filepath.Base(repoRoot)
			}
			return repoRoot, repoName, true
		}
	}

	return "", "", false
}

func (m model) selectedPR() *core.RepoPullRequest {
	if len(m.draft.prs) == 0 || m.draft.prSelected < 0 || m.draft.prSelected >= len(m.draft.prs) {
		return nil
	}

	return &m.draft.prs[m.draft.prSelected]
}

func (m model) totalWidth() int {
	if m.width >= 40 {
		return m.width
	}
	return 72
}

func (m model) totalHeight() int {
	return m.height
}

func (m model) selectedRow() *taskRow {
	if len(m.rows) == 0 {
		return nil
	}
	if m.selected < 0 {
		return &m.rows[0]
	}
	if m.selected >= len(m.rows) {
		return &m.rows[len(m.rows)-1]
	}
	return &m.rows[m.selected]
}

func containsCreateStep(steps []core.TaskCreateProgressStep, target core.TaskCreateProgressStep) bool {
	for _, step := range steps {
		if step == target {
			return true
		}
	}
	return false
}

func groupRowsByRepo(rows []taskRow) []taskRow {
	if len(rows) < 2 {
		return rows
	}

	order := make([]string, 0, len(rows))
	groups := make(map[string][]taskRow, len(rows))
	for _, row := range rows {
		key := repoGroupKey(row.task)
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], row)
	}

	grouped := make([]taskRow, 0, len(rows))
	for _, key := range order {
		grouped = append(grouped, groups[key]...)
	}
	return grouped
}

func repoGroupKey(task *core.Task) string {
	if task == nil {
		return ""
	}
	if repoRoot := strings.TrimSpace(task.RepoRoot); repoRoot != "" {
		return repoRoot
	}
	if repoName := strings.TrimSpace(task.RepoName); repoName != "" {
		return repoName
	}
	return ""
}

func isQuitKey(msg tea.KeyPressMsg) bool {
	switch msg.String() {
	case "q", "ctrl+c":
		return true
	default:
		return false
	}
}

func isForceQuitKey(msg tea.KeyPressMsg) bool {
	return msg.String() == "ctrl+c"
}
