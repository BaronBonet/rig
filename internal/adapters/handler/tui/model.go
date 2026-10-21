package tui

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
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
	worktrees   []core.TaskWorktree
	// order is where the row ranks, frozen when the row first appeared or the
	// list was last arranged afresh; live updates to the fields above never
	// change it, so rows hold still between those moments.
	order orderKey
}

type modelMode int

const (
	modeBrowse modelMode = iota
	modePromptInput
	modePRPicker
	modeCleanupConfirm
	modeProviderSetup
	modeSwitchProvider
	modeImportSession
	modeRenameTask
	modeAccounts
)

const defaultBuildVersion = "dev"

// pendingOp is the single in-flight task lifecycle mutation. Session opening
// is tracked separately because attaching to an existing session may overlap
// creation of another task.
type pendingOp int

const (
	opNone pendingOp = iota
	opCreating
	opDeleting
	opSwitching
	opImporting
	opShelving
	opRenaming
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
	// launchSettings are the configured providers' launch options and the
	// options each was last launched with; nil until loaded.
	launchSettings *core.LaunchSettings
	// accounts are the provider accounts this window's tasks may run under,
	// nil until loaded; accountUsage is each one's latest usage reading.
	accounts     []core.ProviderAccount
	accountsErr  error
	accountUsage map[string]accountUsageState
	selected     int
	width        int
	height       int
	shimmerTick  int
	// previewSeq numbers the composer's live views of a session about to be
	// replaced; readings for an earlier one are dropped.
	previewSeq int
	mode       modelMode
	pending    pendingOp
	opening    bool
	launchCwd  string
	// providerEnv is this rig window's provider configuration, sent with every
	// request that lists sessions or starts a task so the shared daemon uses
	// this window's accounts rather than its own.
	providerEnv   core.ProviderEnv
	buildVersion  string
	loading       bool
	detailsHidden bool
	setupOnly     bool
	// importableHere counts the sessions in the launch folder an empty
	// dashboard offers to import.
	importableHere int
	// allFolders lists every task; by default a window lists only the tasks
	// of the folder it was launched in, so one rig per client stays apart.
	allFolders bool
	// otherFolderTasks counts the tasks outside the launch folder, listed or not.
	otherFolderTasks int
	// showShelf lists the shelved tasks instead of the current ones.
	showShelf bool
	// shelvedTasks counts the shelved tasks in scope, listed or not.
	shelvedTasks int

	// adoptionReloads dampens the reload triggered by a status/record provider
	// mismatch: one reload per observed mismatch. When the mismatch survives a
	// reload, the stale side is the daemon's status row, not the task list —
	// reloading again cannot fix it and would loop forever.
	adoptionReloads map[string]core.Provider
	// settle is the wait, after a load that arranged the list, for the first
	// live statuses to arrange it by.
	settle orderSettle
	// statusSubscribed tracks tasks with a live status subscription so task
	// reloads do not open a duplicate subscription per reload.
	statusSubscribed map[string]bool
	// statusCancels owns each task's subscription lifetime independently so
	// task-list reconciliation can release daemon observation interest.
	statusCancels map[string]context.CancelFunc

	// Per-mode state, cleared by transition() when the mode family changes.
	draft          taskDraft
	setupForm      setupFormState
	providerSwitch switchState
	sessionImport  importState
	rename         renameState

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
	// inFolder runs the new task in the launch folder as it is instead of a
	// new worktree; it is the only choice outside a Git worktree.
	inFolder   bool
	outsideGit bool
	// model and effort are the launch options for the provider; "" means the
	// provider's own default.
	model  string
	effort string
	// account is the directory of the account chosen for the provider; ""
	// means the window's for a new task and the task's for a new session.
	account string
	// forTask, when set, makes the draft a fresh session of that existing
	// task rather than a new task.
	forTask *core.Task
	// handoff asks the task's previous session for a handoff note before the
	// new session starts.
	handoff bool
	// newWindow starts the session alongside the task's running one, in a new
	// window of its tmux session, as a child task.
	newWindow bool
	// preview is what the task's window shows while the session in it is to
	// be replaced, read live; previewErr is why it could not be read.
	preview    string
	previewErr error
}

// createFlowState is the progress of an in-flight or just-failed task
// creation, rendered in the browse list after the draft is submitted.
type createFlowState struct {
	active core.TaskCreateProgressStep
	done   []core.TaskCreateProgressStep
	fromPR bool
	// newSession marks a fresh session of an existing task, which has only
	// the session steps.
	newSession bool
	// retry is a fresh session's submitted draft, kept so a session that
	// fails to start reopens it instead of losing the user's instruction.
	retry *taskDraft
	err   error
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

// importState is the session import picker: provider sessions started in the
// launch folder that no task owns yet, narrowed by an optional search.
type importState struct {
	err      error
	folder   string
	query    string
	sessions []core.ProviderSessionSummary
	// selected indexes the sessions that match the query.
	selected  int
	loading   bool
	searching bool
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
	// reorder marks a load that arranges the list afresh: when rig opens, on
	// a refresh, and when the view changes. Any other load leaves the rows
	// already listed where they are.
	reorder bool
}

// orderSettleMsg ends the wait for live statuses of the load numbered seq.
type orderSettleMsg struct {
	seq int
}

// orderSettle is the one more arrangement a load that arranged the list is
// owed. It arranges from what is known when it arrives, which is the record
// and whatever statuses were carried over; the rest of the statuses come in
// over the subscriptions in the moments after, so once they have, or have had
// a deadline to, the list is arranged again and then holds still.
type orderSettle struct {
	// seq numbers the loads that arranged the list, so the deadline of an
	// earlier one is ignored.
	seq int
	// pending are the listed tasks whose first status has not arrived.
	pending map[string]bool
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

type taskWorktreesLoadedMsg struct {
	err       error
	taskID    string
	worktrees []core.TaskWorktree
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

// sessionPreviewMsg is one reading of the window of the session a new one
// would replace; seq ties it to the view that asked for it.
type sessionPreviewMsg struct {
	seq     int
	content string
	err     error
}

// sessionPreviewTickMsg asks for the next reading of that window.
type sessionPreviewTickMsg struct {
	seq int
}

type taskCreateStreamStartFailedMsg struct {
	err error
}

type taskCreateEventMsg struct {
	events <-chan core.TaskCreateEvent
	event  core.TaskCreateEvent
}

type taskCreateStreamClosedMsg struct{}

// taskShelvedMsg reports a task moved onto the shelf, or off it; open asks
// for the task to be opened once it is back on the list.
type taskShelvedMsg struct {
	task *core.Task
	err  error
	open bool
}

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

type launchSettingsLoadedMsg struct {
	settings *core.LaunchSettings
	err      error
}

type taskProviderSwitchedMsg struct {
	task *core.Task
	err  error
}

type importableSessionsLoadedMsg struct {
	err      error
	sessions []core.ProviderSessionSummary
}

type sessionImportedMsg struct {
	task *core.Task
	err  error
}

type importableCountLoadedMsg struct {
	err   error
	count int
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
		statusCancels:    make(map[string]context.CancelFunc),
		accountUsage:     make(map[string]accountUsageState),
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		getProviderSetupCmd(m.statusContext, m.frontend),
		getLaunchSettingsCmd(m.statusContext, m.frontend, m.providerEnv),
		listProviderAccountsCmd(m.statusContext, m.frontend, m.launchCwd, m.providerEnv),
		m.loadTasks(true),
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
// configured provider, preselecting that provider's last launch options.
// With a single configured provider it is a no-op.
func (m *model) cycleCreateProvider() {
	providers := m.configuredProviders()
	if len(providers) < 2 {
		return
	}

	current := m.effectiveCreateProvider()
	m.draft.provider = providers[0]
	for index, provider := range providers {
		if provider == current {
			m.draft.provider = providers[(index+1)%len(providers)]
			break
		}
	}
	m.draft.account = ""
	m.applyLaunchDefaults(m.draft.provider)
}

// launchChoices lists the models and efforts provider accepts, each led by
// "" for the provider's own default: under the draft's account when provider
// is the draft's, under the window's otherwise.
func (m model) launchChoices(provider core.Provider) ([]string, []string) {
	models, efforts := []string{""}, []string{""}
	if provider == m.effectiveCreateProvider() {
		if account := m.draftAccount(); account != nil && len(account.Options.Models) > 0 {
			return append(models, account.Options.Models...), append(efforts, account.Options.Efforts...)
		}
	}
	if m.launchSettings == nil {
		return models, efforts
	}
	options, ok := m.launchSettings.Options[provider]
	if !ok {
		return models, efforts
	}
	return append(models, options.Models...), append(efforts, options.Efforts...)
}

// handoffAvailable reports whether the draft's task can start its new session
// from a handoff note. The note is written by the task's latest session, of
// the task's current provider, whichever provider the new session starts
// with, so a Claude Code session can hand off to Codex and back.
func (m model) handoffAvailable() bool {
	return m.draft.forTask != nil && m.providerSupportsHandoff(m.draft.forTask.Provider)
}

// providerSupportsHandoff reports whether provider can write a handoff note
// from a previous session.
func (m model) providerSupportsHandoff(provider core.Provider) bool {
	if m.launchSettings == nil {
		return false
	}
	return m.launchSettings.Options[provider].Handoff
}

// cycleCreateModel advances the draft's model through the provider's choices.
func (m *model) cycleCreateModel() {
	models, _ := m.launchChoices(m.effectiveCreateProvider())
	m.draft.model = nextChoice(models, m.draft.model)
}

// cycleCreateEffort advances the draft's effort through the provider's choices.
func (m *model) cycleCreateEffort() {
	_, efforts := m.launchChoices(m.effectiveCreateProvider())
	m.draft.effort = nextChoice(efforts, m.draft.effort)
}

// nextChoice is the choice after current, wrapping around; an unknown current
// leads to the first choice.
func nextChoice(choices []string, current string) string {
	if len(choices) == 0 {
		return ""
	}
	for index, choice := range choices {
		if choice == current {
			return choices[(index+1)%len(choices)]
		}
	}
	return choices[0]
}

// applyLaunchDefaults preselects the options provider was last launched with,
// leaving out any this window's account does not offer: the last launch may
// have come from a rig window under another account.
func (m *model) applyLaunchDefaults(provider core.Provider) {
	m.draft.model, m.draft.effort = "", ""
	if m.launchSettings == nil {
		return
	}
	defaults := m.launchSettings.Defaults[provider]
	models, efforts := m.launchChoices(provider)
	if slices.Contains(models, defaults.Model) {
		m.draft.model = defaults.Model
	}
	if slices.Contains(efforts, defaults.Effort) {
		m.draft.effort = defaults.Effort
	}
}

// rememberLaunchDefaults mirrors what the daemon records on launch, so the
// next draft preselects the same options without a reload.
func (m *model) rememberLaunchDefaults(provider core.Provider, options core.LaunchOptions) {
	if m.launchSettings == nil {
		return
	}
	if m.launchSettings.Defaults == nil {
		m.launchSettings.Defaults = make(map[core.Provider]core.LaunchOptions)
	}
	m.launchSettings.Defaults[provider] = options
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
		if m.mode == modeImportSession {
			return m.pasteImportQuery(msg)
		}
		if m.mode == modeRenameTask {
			return m.updateRename(msg)
		}
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
		if m.mode == modeImportSession {
			return m.updateImportSession(msg)
		}
		if m.mode == modeRenameTask {
			return m.updateRename(msg)
		}
		if m.mode == modeAccounts {
			return m.updateAccounts(msg)
		}

		if isQuitKey(msg) {
			return m.quit()
		}

		// A displayed error stays until the user acknowledges it with a key
		// press; background refreshes must not clear it.
		m.err = nil

		if m.showShelf && !shelfKey(msg.String()) {
			return m, nil
		}

		switch msg.String() {
		case "esc":
			if m.showShelf {
				return m.toggleShelf()
			}
			return m.handleBack()
		case "tab":
			return m.toggleShelf()
		case "d":
			return m.moveSelectedTask(false)
		case "a", "n":
			if m.pending != opNone {
				return m, nil
			}
			if m.providerSetup == nil {
				return m.enterProviderSetupMode()
			}
			return m.enterPromptInputMode("")
		case "N":
			return m.enterNewSessionMode()
		case "e":
			return m.enterRenameMode()
		case "p":
			return m.enterSwitchProviderMode()
		case "i":
			return m.enterImportSessionMode()
		case "u":
			return m.enterAccountsMode()
		case "S":
			return m.enterProviderSetupMode()
		case "r":
			m.loading = true
			return m, m.loadTasks(true)
		case "f":
			if m.launchCwd == "" {
				return m, nil
			}
			m.allFolders = !m.allFolders
			return m, m.loadTasks(true)
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
			if m.showShelf {
				return m.moveSelectedTask(true)
			}
			if len(m.rows) == 0 || m.opening || (m.pending != opNone && m.pending != opCreating) {
				return m, nil
			}
			row := m.selectedRow()
			if row == nil || row.task == nil {
				return m, nil
			}
			if row.task.CreationStatus == core.TaskCreationStatusFailed {
				// Finish the original creation, including its saved prompt,
				// instead of reconnecting into a fresh, promptless provider.
				return m.retrySelectedTaskCreation()
			}
			cmds := []tea.Cmd{openTaskSessionCmd(m.statusContext, m.frontend, row.task)}
			if m.pending == opNone {
				m.shimmerTick = 0
				cmds = append(cmds, shimmerTickCmd())
			}
			m.opening = true
			return m, tea.Batch(cmds...)
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

		selectedTaskID := m.selectedTaskID()
		current, shelved := splitShelved(msg.tasks)
		listed := current
		if m.showShelf {
			listed = shelved
		}
		visible, others := m.scopeTasks(listed)
		m.otherFolderTasks = others
		shelvedHere, _ := m.scopeTasks(shelved)
		m.shelvedTasks = len(shelvedHere)
		nextRows := rowsFromTasks(visible)
		carryOverRows(nextRows, m.rows)
		if msg.reorder {
			// Shown at once, by what is known now; settled once more below.
			rankRows(nextRows)
		}
		m.reconcileTaskStatusTracking(nextRows)
		m.rows = orderRows(nextRows)
		m.clampSelection()
		m.selectTask(selectedTaskID)
		cmds := m.afterTasksLoadedCmds()
		if msg.reorder {
			if settleCmd := m.awaitStatuses(); settleCmd != nil {
				cmds = append(cmds, settleCmd)
			}
		}
		// Every task, shelf and all folders included: the frontend keeps the
		// launch folder's.
		cmds = append(cmds, claimTaskSessionsCmd(m.statusContext, m.frontend, m.launchCwd, msg.tasks))
		if cmd := m.importHintCmd(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)
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
	case taskWorktreesLoadedMsg:
		// A worktree that cannot be inspected keeps the last known list; the
		// next refresh retries.
		if msg.err != nil {
			return m, nil
		}
		m.setTaskWorktrees(msg.taskID, msg.worktrees)
		return m, nil
	case taskStatusSubscriptionReadyMsg:
		if msg.err != nil {
			m.cancelTaskStatusTracking(msg.taskID)
			m.statusArrived(msg.taskID)
			return m, nil
		}
		return m, waitForTaskStatusCmd(msg.taskID, msg.updates)
	case taskStatusUpdatedMsg:
		update := msg.update
		m.setTaskStatus(msg.taskID, &update)
		m.statusArrived(msg.taskID)
		cmds := []tea.Cmd{
			taskActivityCmd(m.statusContext, m.frontend, msg.taskID, taskActivityPreviewLimit),
			taskTokenUsageCmd(m.statusContext, m.frontend, msg.taskID),
			taskWorktreesCmd(m.statusContext, m.frontend, msg.taskID),
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
				cmds = append(cmds, m.loadTasks(false))
			}
		}
		return m, tea.Batch(cmds...)
	case taskStatusSubscriptionClosedMsg:
		m.cancelTaskStatusTracking(msg.taskID)
		m.statusArrived(msg.taskID)
		if m.taskRowByID(msg.taskID) == nil {
			return m, nil
		}
		return m, tea.Batch(m.taskStatusTrackingCmds(msg.taskID)...)
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
		return m, tea.Batch(
			m.loadTasks(false),
			getLaunchSettingsCmd(m.statusContext, m.frontend, m.providerEnv),
			listProviderAccountsCmd(m.statusContext, m.frontend, m.launchCwd, m.providerEnv),
		)
	case providerAccountsLoadedMsg:
		return m.handleProviderAccountsLoaded(msg)
	case accountUsageMsg:
		return m.handleAccountUsage(msg)
	case launchSettingsLoadedMsg:
		// Without settings the composer simply offers no options to cycle.
		if msg.err == nil && msg.settings != nil {
			m.launchSettings = msg.settings
		}
		return m, nil
	case importableSessionsLoadedMsg:
		if m.mode != modeImportSession {
			return m, nil
		}
		m.sessionImport.loading = false
		m.sessionImport.err = msg.err
		m.sessionImport.sessions = msg.sessions
		m.sessionImport.selected = 0
		return m, nil
	case importableCountLoadedMsg:
		// The hint is optional: a failed count only hides it.
		m.importableHere = 0
		if msg.err == nil {
			m.importableHere = msg.count
		}
		return m, nil
	case sessionImportedMsg:
		m.endOp()
		m.transition(modeBrowse)
		m.err = msg.err
		if msg.task == nil {
			return m, nil
		}
		if index := m.upsertTaskRow(msg.task); index >= 0 {
			m.selected = index
		}
		m.clampSelection()
		return m, tea.Batch(m.taskStatusTrackingCmds(taskID(msg.task))...)
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
			return m.failCreate(msg.err)
		}

		m.transition(modeBrowse)
		m.create = createFlowState{}
		index := m.upsertTaskRow(msg.task)
		if index >= 0 {
			m.selected = index
		}
		m.clampSelection()
		cmds := []tea.Cmd{m.loadTasks(false)}
		cmds = append(cmds, m.taskStatusTrackingCmds(taskID(msg.task))...)
		return m, tea.Batch(cmds...)
	case taskCreateStreamStartFailedMsg:
		m.endOp()
		return m.failCreate(msg.err)
	case taskCreateEventMsg:
		switch {
		case msg.event.Progress != nil:
			m.advanceCreateProgress(msg.event.Progress.Step)
			return m, waitForTaskCreateEventCmd(msg.events)
		case msg.event.Err != nil:
			m.endOp()
			if msg.event.Task != nil {
				if index := m.upsertTaskRow(msg.event.Task); index >= 0 {
					m.selected = index
				}
				m.clampSelection()
			}
			return m.failCreate(msg.event.Err)
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
			return m.failCreate(errors.New("create task stream closed unexpectedly"))
		}
		return m, nil
	case sessionPreviewMsg:
		if msg.seq != m.previewSeq || !m.previewingSession() {
			return m, nil
		}
		m.draft.preview, m.draft.previewErr = msg.content, msg.err
		return m, sessionPreviewTickCmd(msg.seq)
	case sessionPreviewTickMsg:
		if msg.seq != m.previewSeq || !m.previewingSession() {
			return m, nil
		}
		return m, captureSessionPreviewCmd(m.statusContext, m.frontend, m.draft.forTask, msg.seq)
	case taskOpenedMsg:
		m.opening = false
		if m.pending == opNone {
			m.shimmerTick = 0
		}
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.err = nil
		return m, nil
	case taskShelvedMsg:
		m.endOp()
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		// Opening a task from the shelf leaves it for the current list, a
		// different view, which is arranged afresh like any other.
		if msg.open {
			m.showShelf = false
			m.opening = true
		}
		cmds := []tea.Cmd{m.loadTasks(msg.open)}
		if msg.open {
			cmds = append(cmds, openTaskSessionCmd(m.statusContext, m.frontend, msg.task), shimmerTickCmd())
		}
		return m, tea.Batch(cmds...)
	case taskRenamedMsg:
		m.endOp()
		if msg.err != nil {
			// The typed name stays, so it can be fixed and sent again.
			m.err = msg.err
			return m, nil
		}
		m.transition(modeBrowse)
		return m, m.loadTasks(false)
	case taskDeletedMsg:
		m.endOp()
		m.transition(modeBrowse)
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}

		m.err = nil
		m.removeTaskRow(msg.taskID)
		m.cancelTaskStatusTracking(msg.taskID)
		delete(m.adoptionReloads, msg.taskID)
		m.clampSelection()
		return m, m.importHintCmd()
	case activityRefreshTickMsg:
		cmds := []tea.Cmd{activityRefreshTickCmd()}
		if row := m.selectedRow(); row != nil && row.task != nil && taskID(row.task) != "" {
			id := taskID(row.task)
			cmds = append(cmds,
				taskActivityCmd(m.statusContext, m.frontend, id, taskActivityPreviewLimit),
				taskTokenUsageCmd(m.statusContext, m.frontend, id),
				taskWorktreesCmd(m.statusContext, m.frontend, id),
			)
		}
		return m, tea.Batch(cmds...)
	case orderSettleMsg:
		if msg.seq == m.settle.seq && len(m.settle.pending) > 0 {
			m.settleOrder()
		}
		return m, nil
	case shimmerTickMsg:
		if m.pending == opNone && !m.opening {
			return m, nil
		}
		m.shimmerTick++
		return m, shimmerTickCmd()
	default:
		if m.mode == modePromptInput {
			return m.updatePromptInput(msg)
		}
		if m.mode == modeRenameTask {
			return m.updateRename(msg)
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
	case modeImportSession:
		body = m.importSessionView()
	case modeRenameTask:
		body = m.renameTaskView()
	case modeAccounts:
		body = m.accountsView()
	default:
		body = m.listView()
	}

	view := tea.NewView(body)
	view.AltScreen = true
	return view
}

// splitShelved separates the tasks on the current list from those on the
// shelf.
func splitShelved(tasks []*core.Task) ([]*core.Task, []*core.Task) {
	var current, shelved []*core.Task
	for _, task := range tasks {
		if task.IsShelved() {
			shelved = append(shelved, task)
		} else {
			current = append(current, task)
		}
	}
	return current, shelved
}

// shelfKey reports whether a key does something on the shelf: browsing,
// putting a task back, opening or cleaning it up. Starting, importing or
// switching work happens on the current list.
func shelfKey(key string) bool {
	switch key {
	case "a", "n", "N", "p", "i", "R":
		return false
	default:
		return true
	}
}

// toggleShelf switches the list between the current tasks and the shelf.
func (m model) toggleShelf() (tea.Model, tea.Cmd) {
	m.showShelf = !m.showShelf
	m.selected = 0
	return m, m.loadTasks(true)
}

// moveSelectedTask shelves the selected task, or, on the shelf, puts it back
// on the current list; open then opens it, resuming its latest session.
func (m model) moveSelectedTask(open bool) (tea.Model, tea.Cmd) {
	row := m.selectedRow()
	if row == nil || row.task == nil || m.opening || m.pending != opNone {
		return m, nil
	}
	m.beginOp(opShelving)
	return m, tea.Batch(
		shelveTaskCmd(m.statusContext, m.frontend, row.task, !m.showShelf, open),
		shimmerTickCmd(),
	)
}

// rowsFromTasks makes a row for each task, in the order given, ranked by what
// its record says. A row new to the list keeps that rank until the list is
// arranged afresh.
func rowsFromTasks(tasks []*core.Task) []taskRow {
	rows := make([]taskRow, 0, len(tasks))
	for _, task := range tasks {
		if task == nil {
			continue
		}
		row := taskRow{task: task}
		row.freezeOrder()
		rows = append(rows, row)
	}
	return rows
}

// carryOverRows gives the reloaded rows what the current ones hold that a
// reload does not return: the live status, the loaded Activity events and the
// frozen rank. A row already listed so stays where it is.
func carryOverRows(nextRows []taskRow, currentRows []taskRow) {
	current := make(map[string]taskRow, len(currentRows))
	for _, row := range currentRows {
		if id := taskID(row.task); id != "" {
			current[id] = row
		}
	}

	for index := range nextRows {
		previous, ok := current[taskID(nextRows[index].task)]
		if !ok {
			continue
		}
		nextRows[index].status = previous.status
		nextRows[index].activity = previous.activity
		nextRows[index].order = previous.order
	}
}

// rankRows ranks every row afresh from its live state, for a load that
// arranges the list.
func rankRows(rows []taskRow) {
	for index := range rows {
		rows[index].freezeOrder()
	}
}

func (m *model) afterTasksLoadedCmds() []tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(m.rows)*4)
	for _, row := range m.rows {
		taskID := taskID(row.task)
		cmds = append(cmds, m.taskStatusTrackingCmds(taskID)...)
		if taskID != "" {
			cmds = append(cmds, taskActivityCmd(m.statusContext, m.frontend, taskID, taskActivityPreviewLimit))
			cmds = append(cmds, taskTokenUsageCmd(m.statusContext, m.frontend, taskID))
			cmds = append(cmds, taskWorktreesCmd(m.statusContext, m.frontend, taskID))
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
	// A new session has its continuation prompt; the user's own instruction
	// is optional.
	if prompt == "" && m.draft.forTask == nil {
		return m, nil
	}

	// Capture the draft's inputs before the transition discards it.
	cwd := m.currentCreateCwd()
	provider := m.effectiveCreateProvider()
	options := core.LaunchOptions{Model: m.draft.model, Effort: m.draft.effort}
	var workspace core.WorkspaceKind
	if m.draft.inFolder {
		workspace = core.WorkspaceKindFolder
	}
	forTask := m.draft.forTask
	handoff := m.draft.handoff
	newWindow := m.draft.newWindow
	taskEnv, sessionEnv := m.draftTaskEnv(), m.draftSessionEnv()
	submitted := m.draft
	m.transition(modeBrowse)
	m.beginOp(opCreating)
	m.create = createFlowState{newSession: forTask != nil}
	if forTask != nil {
		m.create.retry = &submitted
	}
	m.rememberLaunchDefaults(provider, options)

	if forTask != nil {
		return m, tea.Batch(
			newTaskSessionStreamCmd(m.statusContext, m.frontend, core.NewTaskSessionInput{
				TaskID:      forTask.ID,
				Prompt:      prompt,
				Provider:    provider,
				Model:       options.Model,
				Effort:      options.Effort,
				Handoff:     handoff,
				NewWindow:   newWindow,
				ProviderEnv: sessionEnv,
			}),
			shimmerTickCmd(),
		)
	}

	return m, tea.Batch(
		createTaskStreamCmd(m.statusContext, m.frontend, core.CreateTaskInput{
			Cwd:         cwd,
			Prompt:      prompt,
			Provider:    provider,
			Workspace:   workspace,
			ProviderEnv: taskEnv,
			Model:       options.Model,
			Effort:      options.Effort,
		}),
		shimmerTickCmd(),
	)
}

// failCreate records a failed creation. A fresh session that fails to start
// reopens its composer with the user's instruction and options and the error
// above them, so it can be sent again, in a new window, say.
func (m model) failCreate(err error) (model, tea.Cmd) {
	retry := m.create.retry
	m.create.err = err
	if retry == nil || !m.transition(modePromptInput) {
		return m, nil
	}
	m.draft = *retry
	m.draft.err = err
	m.create = createFlowState{}
	return m, tea.Batch(m.draft.input.Focus(), m.startSessionPreview())
}

// previewingSession reports whether the composer is about to replace a
// task's session, and so shows that session live.
func (m model) previewingSession() bool {
	return m.mode == modePromptInput && m.draft.forTask != nil && !m.draft.newWindow
}

// startSessionPreview starts a new live view of the session the composer is
// about to replace, ending any earlier one, or only ends it when the
// composer leaves the session alone.
func (m *model) startSessionPreview() tea.Cmd {
	m.previewSeq++
	m.draft.preview, m.draft.previewErr = "", nil
	if !m.previewingSession() {
		return nil
	}
	return captureSessionPreviewCmd(m.statusContext, m.frontend, m.draft.forTask, m.previewSeq)
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
			return m, m.readAccountUsage(m.accountsFor(m.effectiveCreateProvider()), false)
		}
		if typed.String() == "shift+tab" {
			return m, m.cycleCreateAccount()
		}
		if typed.String() == "ctrl+t" {
			m.cycleCreateModel()
			return m, nil
		}
		if typed.String() == "ctrl+r" {
			m.cycleCreateEffort()
			return m, nil
		}
		if typed.String() == "ctrl+g" {
			if m.handoffAvailable() {
				m.draft.handoff = !m.draft.handoff
			}
			return m, nil
		}
		if typed.String() == "ctrl+w" {
			if m.draft.forTask != nil {
				m.draft.newWindow = !m.draft.newWindow
				return m, m.startSessionPreview()
			}
			return m, nil
		}
		if typed.String() == "ctrl+o" {
			// Outside Git the folder is the only workspace there is, and a new
			// session keeps its task's workspace.
			if !m.draft.outsideGit && m.draft.forTask == nil {
				m.draft.inFolder = !m.draft.inFolder
			}
			return m, nil
		}
		if typed.String() == "ctrl+p" {
			if m.draft.forTask != nil {
				// A new session belongs to its task; there is no source to pick.
				return m, nil
			}
			if m.draft.outsideGit {
				m.draft.err = errors.New("pull requests need a git repository")
				return m, nil
			}
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
			// A modified enter is the textarea's newline.
			if typed.Mod == 0 {
				return m.submitPrompt()
			}
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
		env := m.draftTaskEnv()
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
				ProviderEnv: env,
			}),
			shimmerTickCmd(),
		)
	case "tab":
		m.cycleCreateProvider()
		return m, nil
	case "shift+tab":
		return m, m.cycleCreateAccount()
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
	outsideGit := !insideGitWorktree(m.currentCreateCwd())
	m.draft = taskDraft{prompt: initialValue, input: input, inFolder: outsideGit, outsideGit: outsideGit}
	m.applyLaunchDefaults(m.effectiveCreateProvider())
	m.create.err = nil
	m.create.fromPR = false
	m.create.newSession = false
	return m, tea.Batch(m.draft.input.Focus(), m.readAccountUsage(m.accountsFor(m.effectiveCreateProvider()), false))
}

// enterNewSessionMode opens the composer for a fresh provider session of the
// selected task: a new conversation in the same workspace and tmux session,
// so a long-running task keeps one row however many sessions it takes.
func (m model) enterNewSessionMode() (tea.Model, tea.Cmd) {
	if m.pending != opNone {
		return m, nil
	}
	if m.providerSetup == nil {
		return m.enterProviderSetupMode()
	}
	row := m.selectedRow()
	if row == nil || row.task == nil {
		return m, nil
	}
	task := row.task
	if task.CreationStatus != core.TaskCreationStatusReady {
		m.err = errors.New("the task is not ready: retry its creation first")
		return m, nil
	}
	if !m.transition(modePromptInput) {
		return m, nil
	}

	// The box takes the user's own instruction for the session; the
	// continuation prompt (the task, its original ask, where to pick up) is
	// composed around it and previewed above.
	input := newPromptInput()
	input.Placeholder = "What should this session do? Optional: the continuation prompt goes first."
	m.draft = taskDraft{input: input, forTask: task, provider: task.Provider}
	// The task's own last options win; a task without any takes the
	// provider's last used ones.
	m.draft.model, m.draft.effort = task.Launch().Model, task.Launch().Effort
	if m.draft.model == "" && m.draft.effort == "" {
		m.applyLaunchDefaults(m.effectiveCreateProvider())
	}
	m.draft.handoff = m.handoffAvailable()
	// The running session is left as it is; the new one opens next to it.
	m.draft.newWindow = true
	m.create = createFlowState{}
	m.err = nil
	return m, tea.Batch(m.draft.input.Focus(), m.readAccountUsage(m.accountsFor(m.effectiveCreateProvider()), false))
}

// continuationPreview is the continuation prompt as the daemon will compose
// it, before the user's own instruction, for the composer to show.
func (m model) continuationPreview() string {
	if m.draft.forTask == nil {
		return ""
	}
	handoffPath := ""
	if m.draft.handoff && m.handoffAvailable() {
		handoffPath = "(the note written when the session starts)"
	}
	return core.ContinuationPrompt(m.draft.forTask, "", handoffPath)
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

	if m.statusSubscribed[taskID] {
		return nil
	}
	subscriptionCtx, cancel := context.WithCancel(m.statusContext)
	m.statusSubscribed[taskID] = true
	m.statusCancels[taskID] = cancel
	return []tea.Cmd{subscribeTaskStatusCmd(subscriptionCtx, m.frontend, taskID)}
}

func (m *model) reconcileTaskStatusTracking(rows []taskRow) {
	tracked := make(map[string]bool, len(rows))
	for _, row := range rows {
		if taskID := taskID(row.task); taskID != "" {
			tracked[taskID] = true
		}
	}
	for taskID := range m.statusSubscribed {
		if !tracked[taskID] {
			m.cancelTaskStatusTracking(taskID)
		}
	}
}

func (m *model) cancelTaskStatusTracking(taskID string) {
	if cancel := m.statusCancels[taskID]; cancel != nil {
		cancel()
	}
	delete(m.statusCancels, taskID)
	delete(m.statusSubscribed, taskID)
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

// selectedTaskID is the ID of the selected row's task, or "" with none.
func (m model) selectedTaskID() string {
	if row := m.selectedRow(); row != nil {
		return taskID(row.task)
	}
	return ""
}

// sortRows arranges the rows by their frozen ranks and keeps the selection on
// the task it was on, not at the same place in the list.
func (m *model) sortRows() {
	selectedTaskID := m.selectedTaskID()
	m.rows = orderRows(m.rows)
	m.selectTask(selectedTaskID)
}

// loadTasks loads the tasks. A load that reorders arranges the list afresh,
// which only happens when rig opens, on a refresh and when the view changes.
func (m model) loadTasks(reorder bool) tea.Cmd {
	return loadTasksCmd(m.statusContext, m.frontend, reorder)
}

// awaitStatuses starts the wait, after a load that arranged the list, for the
// first live status of every listed task that has none, and returns the
// deadline that ends it, or nil with nothing to wait for. It replaces any wait
// still going: its deadline no longer matches and is ignored.
func (m *model) awaitStatuses() tea.Cmd {
	m.settle.seq++
	m.settle.pending = nil
	pending := make(map[string]bool)
	for _, row := range m.rows {
		if id := taskID(row.task); id != "" && row.status == nil {
			pending[id] = true
		}
	}
	if len(pending) == 0 {
		return nil
	}
	m.settle.pending = pending
	return orderSettleTickCmd(m.settle.seq)
}

// statusArrived notes that a task's first live status, or the end of its
// subscription, has arrived; the last one settles the order.
func (m *model) statusArrived(id string) {
	if !m.settle.pending[id] {
		return
	}
	delete(m.settle.pending, id)
	if len(m.settle.pending) == 0 {
		m.settleOrder()
	}
}

// settleOrder arranges the list once more, by the live state the rows have
// gathered since the load, keeping the selection on its task. It ends the
// wait, so nothing moves the rows again until the next refresh or view change.
func (m *model) settleOrder() {
	m.settle.pending = nil
	selectedTaskID := m.selectedTaskID()
	rankRows(m.rows)
	m.rows = orderRows(m.rows)
	m.selectTask(selectedTaskID)
}

// taskRowIndex is where the task with the given ID is in the list, or -1.
func (m *model) taskRowIndex(id string) int {
	for index, row := range m.rows {
		if taskID(row.task) == id {
			return index
		}
	}
	return -1
}

func (m *model) taskRowByID(taskID string) *taskRow {
	for i := range m.rows {
		if m.rows[i].task != nil && m.rows[i].task.ID == taskID {
			return &m.rows[i]
		}
	}
	return nil
}

// setTaskStatus records a task's live status for the row to show. It does not
// move the row: the list is only arranged afresh when it is opened, refreshed
// or switched to another view.
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

// setTaskActivity records a task's recent Activity events for the row to show;
// like a status it does not move the row.
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

func (m *model) setTaskWorktrees(taskID string, worktrees []core.TaskWorktree) {
	if row := m.taskRowByID(taskID); row != nil {
		row.worktrees = append([]core.TaskWorktree(nil), worktrees...)
	}
}

// upsertTaskRow lists task and returns the index it is at, or -1 when it has
// no ID. A task already listed keeps its row and its place. A new one is
// ranked once, by its record, so a task just created is the newest and a new
// Child task lifts its family; it holds still from then on.
func (m *model) upsertTaskRow(task *core.Task) int {
	id := taskID(task)
	if id == "" {
		return -1
	}

	if index := m.taskRowIndex(id); index >= 0 {
		m.rows[index].task = task
		return index
	}

	row := taskRow{task: task}
	row.freezeOrder()
	m.rows = append(m.rows, row)
	m.sortRows()
	return m.taskRowIndex(id)
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

// removeTaskRow drops a task's row; the rest keep their order and nesting. The
// selection stays on the same task unless it is the one removed, when it stays
// at the same place in the list.
func (m *model) removeTaskRow(targetTaskID string) {
	selectedTaskID := m.selectedTaskID()
	filtered := m.rows[:0]
	for _, row := range m.rows {
		if taskID(row.task) == targetTaskID {
			continue
		}
		filtered = append(filtered, row)
	}
	m.rows = filtered
	m.selectTask(selectedTaskID)
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
