package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/BaronBonet/rig/internal/core"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type taskRow struct {
	task     *core.Task
	activity []core.TaskActivityEvent
	status   *core.TaskStatusUpdate
	usage    *core.TaskUsage
	// pullRequests are the task's pull requests, oldest first, and
	// unshippedWork the work in its worktree none of them contains, as last
	// loaded.
	pullRequests  []core.PullRequest
	unshippedWork core.UnshippedWork
	// creating is the creation of the row's task while it runs in the
	// background: the row shows its step and takes no action until it replies.
	creating *rowCreation
}

// rowCreation is a task creation under way. Its id tells it apart, as its
// task has no ID until it is named.
type rowCreation struct {
	id   int
	step core.TaskCreateProgressStep
}

type modelMode int

const (
	modeBrowse modelMode = iota
	modePromptInput
	modePRPicker
	// modeRepoPicker picks the repository the draft creates its task in.
	modeRepoPicker
	modeCleanupConfirm
	modeProviderSetup
	// modeRepoDirsSetup is the optional setup step after provider setup.
	modeRepoDirsSetup
)

const defaultBuildVersion = "dev"

const taskActivityPreviewLimit = 6

// taskActivityFetchLimit is how much of a task's recent activity the detail
// panel loads: enough that each of its agents' own shows when its sub-row is
// selected, while the task's other agents keep busy.
const taskActivityFetchLimit = 60

// nolint:recvcheck // bubbletea requires value receivers for tea.Model.
type model struct {
	frontend      core.TaskFrontend
	statusContext context.Context
	err           error
	cancelStatus  context.CancelFunc
	rows          []taskRow
	providerSetup *core.ProviderSetup
	selected      int
	// selectedAgent is the agent session whose sub-row is selected under the
	// selected task row, or empty when the task row itself is selected.
	selectedAgent string
	width         int
	height        int
	shimmerTick   int
	mode          modelMode
	opening       bool
	launchCwd     string
	buildVersion  string
	loading       bool
	detailsHidden bool
	setupOnly     bool
	// inPopup is set when rig runs in a tmux display-popup, which a jump
	// closes so it doesn't cover the session it jumped to.
	inPopup bool
	// notice is a short-lived confirmation shown in place of the key bar.
	notice string

	// spinnerFrame turns the spinner of working tasks and agents, while
	// spinnerRunning keeps a single tick driving it.
	spinnerFrame   int
	spinnerRunning bool

	// statusSubscribed tracks tasks with a live status subscription so task
	// reloads do not open a duplicate subscription per reload.
	statusSubscribed map[string]bool
	// statusCancels owns each task's subscription lifetime independently so
	// task-list reconciliation can release daemon observation interest.
	statusCancels map[string]context.CancelFunc

	// providerModels is each configured provider's model list, fetched once
	// provider setup loads, and launchDefaults each provider's Launch
	// default. Both outlive drafts: a draft starts from them.
	providerModels map[core.Provider]providerModelsState
	launchDefaults launchDefaultsState

	// setupDetections numbers each provider detection as the setup screen
	// starts it. It outlives the setup form, so a reply from a screen since
	// closed is told apart from the latest.
	setupDetections int

	// cleanup is what the cleanup confirmation acts on, fixed when it opens
	// so that a status update or reload since cannot change it.
	cleanup cleanupTarget
	// deleting and removingAgents are the tasks, and the agent sessions, a
	// confirmed cleanup is removing in the background. Their rows show it and
	// take no action until the removal replies.
	deleting       map[string]bool
	removingAgents map[string]bool

	// Per-mode state, cleared by transition() when the mode family changes.
	draft        taskDraft
	setupForm    setupFormState
	repoDirsForm repoDirsFormState
	// repoDirsLoad numbers the repo directories step's loads, so only the
	// latest one fills the step. It outlives the step's state.
	repoDirsLoad int

	// creationSeq numbers task creations, which run in the background, so
	// their replies find their rows.
	creationSeq int
}

// cleanupTarget is what x on the task list removes: a whole task, or, from an
// agent sub-row, only that agent.
type cleanupTarget struct {
	task  *core.Task
	agent *core.LiveAgentSession
}

// taskDraft is the in-progress task the user is assembling before
// submission: the prompt text, the Tab-cycled provider choice and its Launch
// choice, and the optional pull request source. It spans the prompt-input and
// PR-picker modes and is discarded when the TUI leaves that mode family.
type taskDraft struct {
	prompt     string
	input      textarea.Model
	provider   core.Provider // "" means use the configured default
	prs        []core.RepoPullRequest
	prSelected int
	// repoRoot and repoName are the repository the draft creates its task
	// in, once picked; until then it is the current repo scope.
	repoRoot string
	repoName string
	err      error // validation error shown inside the draft views

	// repos are the known repositories the repo picker offers, most recently
	// worked on first, once reposLoaded.
	repos        []core.KnownRepo
	reposLoaded  bool
	repoSelected int

	// choice is the draft's Launch choice. It starts from the provider's
	// Launch default; choiceTouched records that the user cycled it, so a
	// late Launch default no longer replaces it.
	choice        core.LaunchChoice
	choiceTouched bool
}

// setupFormState is the provider setup screen's working state.
type setupFormState struct {
	rows            []providerSetupRow
	selected        int
	defaultProvider core.Provider
	detecting       bool
	saving          bool
	err             error

	// edits is each Launch default the user edited on the screen. Enter
	// saves those of enabled providers once the provider setup has saved.
	// A provider without one shows its Launch default as fetched, so a late
	// fetch fills only the rows the user has not edited.
	edits map[core.Provider]core.LaunchChoice
}

// repoDirsFormState is the repo directories setup step's working state: the
// directories being edited, one per line.
type repoDirsFormState struct {
	input   textarea.Model
	err     error
	loading bool
	// loadFailed records that the user's repo directories could not be
	// loaded, so the step started empty rather than with them.
	loadFailed bool
	saving     bool
}

// providerSetupRow is one supported provider in the provider setup UI.
type providerSetupRow struct {
	provider core.Provider
	detail   string
	ready    bool
	enabled  bool
}

// showsLaunchDefault reports whether the row shows its provider's Launch
// default and lets the user edit it, which it does while ready and enabled.
func (r providerSetupRow) showsLaunchDefault() bool {
	return r.ready && r.enabled
}

// launchDefaultEdit is a Launch default the user set on the provider setup
// screen.
type launchDefaultEdit struct {
	provider core.Provider
	choice   core.LaunchChoice
}

type tasksLoadedMsg struct {
	err   error
	tasks []*core.Task
	// background marks a periodic reload, which fails quietly and fetches
	// details only for tasks new to the list.
	background bool
}

type taskPullRequestsLoadedMsg struct {
	err          error
	taskID       string
	pullRequests core.TaskPullRequests
	// stored marks the pull requests the daemon saved last time, which show
	// until it has looked them up again.
	stored bool
}

type taskActivityLoadedMsg struct {
	activity []core.TaskActivityEvent
	err      error
	taskID   string
}

type taskUsageLoadedMsg struct {
	usage  *core.TaskUsage
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

type taskOpenedMsg struct {
	err error
}

type taskCreateStreamStartFailedMsg struct {
	err      error
	creation int
}

type taskCreateEventMsg struct {
	events   <-chan core.TaskCreateEvent
	event    core.TaskCreateEvent
	creation int
}

type taskCreateStreamClosedMsg struct {
	creation int
}

type taskDeletedMsg struct {
	err    error
	taskID string
}

type agentSessionRemovedMsg struct {
	err     error
	agentID string
}

type reposLoadedMsg struct {
	repos []core.KnownRepo
	err   error
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
	detection  int
}

// providerSetupSavedMsg is the result of saving the provider setup, then the
// Launch defaults edited with it. err is the setup's failure, which leaves
// the Launch defaults unsaved; defaultsErr is theirs, and savedDefaults the
// ones that saved.
type providerSetupSavedMsg struct {
	err           error
	setup         core.ProviderSetup
	defaultsErr   error
	savedDefaults []launchDefaultEdit
}

type providerModelsLoadedMsg struct {
	err      error
	provider core.Provider
	models   []core.ProviderModel
}

type launchDefaultsLoadedMsg struct {
	err      error
	defaults map[core.Provider]core.LaunchChoice
	fetch    int
}

type repoDirsLoadedMsg struct {
	err  error
	dirs []string
	// load is the number of the load this replies to.
	load int
}

type repoDirsSavedMsg struct {
	err error
}

type shimmerTickMsg struct{}

type spinnerTickMsg struct{}

type clearNoticeMsg struct{}

type activityRefreshTickMsg struct{}

type pullRequestRefreshTickMsg struct{}

type taskListRefreshTickMsg struct{}

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
		statusSubscribed: make(map[string]bool),
		statusCancels:    make(map[string]context.CancelFunc),
		providerModels:   make(map[core.Provider]providerModelsState),
		deleting:         make(map[string]bool),
		removingAgents:   make(map[string]bool),
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		getProviderSetupCmd(m.statusContext, m.frontend),
		loadTasksCmd(m.statusContext, m.frontend),
		activityRefreshTickCmd(),
		pullRequestRefreshTickCmd(),
		taskListRefreshTickCmd(),
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

// startSpinner starts the spinner's tick once a task or agent works, unless
// it already turns. The tick stops itself when nothing works.
func (m *model) startSpinner() tea.Cmd {
	if m.spinnerRunning || !m.anyWorking() {
		return nil
	}
	m.spinnerRunning = true
	return spinnerTickCmd()
}

// anyWorking reports whether a task, or an agent shown as one of its
// sub-rows, is working, or a task is being created.
func (m model) anyWorking() bool {
	for _, row := range m.rows {
		if row.creating != nil || row.status != nil && row.status.Phase == core.TaskStatusPhaseWorking {
			return true
		}
		for _, session := range agentSubRows(row) {
			if session.Status.Phase == core.TaskStatusPhaseWorking {
				return true
			}
		}
	}
	return false
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tea.PasteMsg:
		if m.mode == modeRepoDirsSetup {
			return m.updateRepoDirsSetup(msg)
		}
		return m.updatePromptPaste(msg)
	case tea.KeyPressMsg:
		if isForceQuitKey(msg) {
			return m.quit()
		}

		if m.mode == modePromptInput {
			return m.updatePromptInput(msg)
		}
		if m.mode == modePRPicker {
			return m.updatePRPicker(msg)
		}
		if m.mode == modeRepoPicker {
			return m.updateRepoPicker(msg)
		}
		if m.mode == modeCleanupConfirm {
			return m.updateCleanupConfirm(msg)
		}
		if m.mode == modeProviderSetup {
			return m.updateProviderSetup(msg)
		}
		if m.mode == modeRepoDirsSetup {
			return m.updateRepoDirsSetup(msg)
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
			if m.providerSetup == nil {
				return m.enterProviderSetupMode()
			}
			return m.enterPromptInputMode("")
		case "S":
			return m.enterProviderSetupMode()
		case "r":
			m.loading = true
			return m, loadTasksCmd(m.statusContext, m.frontend)
		case "R":
			if m.selectedBusy() || m.opening {
				return m, nil
			}
			return m.retrySelectedTaskCreation()
		case "g", "home":
			m.selected = 0
			m.selectedAgent = ""
		case "G", "end":
			m.selectLastLine()
		case "pgdown":
			m.moveSelection(m.taskListPageSize())
		case "pgup":
			m.moveSelection(-m.taskListPageSize())
		case "j", "down":
			m.moveCursor(1)
		case "k", "up":
			m.moveCursor(-1)
		case " ", "space":
			m.detailsHidden = !m.detailsHidden
		case "m":
			if row := m.selectedRow(); row != nil && row.task != nil {
				return m, openRepoSessionCmd(m.statusContext, m.frontend, row.task.RepoRoot)
			}
			return m, nil
		case "c":
			// OSC 52 copies through the terminal, so it works the same on
			// macOS, Linux, tmux and SSH without a platform clipboard tool.
			if row := m.selectedRow(); row != nil && row.task != nil && row.task.Prompt != "" {
				m.notice = "copied to clipboard"
				return m, tea.Batch(tea.SetClipboard(row.task.Prompt), clearNoticeCmd())
			}
			return m, nil
		case "x":
			row := m.selectedRow()
			if row == nil || row.task == nil || m.selectedBusy() {
				return m, nil
			}
			target := cleanupTarget{task: row.task}
			if session := m.selectedAgentSession(); session != nil {
				agent := *session
				target.agent = &agent
			}
			if m.transition(modeCleanupConfirm) {
				m.cleanup = target
			}
			return m, nil
		case "enter":
			if len(m.rows) == 0 || m.opening || m.selectedBusy() {
				return m, nil
			}
			row := m.selectedRow()
			if row == nil || row.task == nil {
				return m, nil
			}
			// Enter lands on the selected sub-row's agent or, on a task row, on
			// the agent that most needs the user, when one runs.
			var pane *core.TmuxPaneRef
			if session := m.selectedAgentSession(); session != nil {
				sessionPane := session.Pane
				pane = &sessionPane
			} else if lead := row.status.LeadAgentSession(); lead != nil {
				leadPane := lead.Pane
				pane = &leadPane
			}
			m.opening = true
			m.shimmerTick = 0
			return m, tea.Batch(openTaskSessionCmd(m.statusContext, m.frontend, row.task, pane), shimmerTickCmd())
		}
		m.clampSelection()
		return m, nil
	case tasksLoadedMsg:
		// A failed periodic reload stays quiet; the next one retries.
		if msg.err != nil && msg.background {
			return m, nil
		}
		m.loading = false
		// Only surface a failure; a successful background reload must not
		// clear an unacknowledged error shown to the user.
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}

		var known map[string]bool
		if msg.background {
			known = make(map[string]bool, len(m.rows))
			for _, row := range m.rows {
				known[taskID(row.task)] = true
			}
		}
		selectedKey := m.selectedRowKey()
		selectedAgent := m.selectedAgent
		// Until a row is selected, start on the task rig was opened from: a
		// tmux popup runs in its session's directory, a task's worktree.
		if selectedKey == "" {
			if task := core.FindTaskAtPath(msg.tasks, m.launchCwd); task != nil {
				selectedKey = task.ID
			}
		}
		// shortcut: a periodic reload sent just before a local delete lands can
		// show the deleted task again until the next reload, 2s later; add a
		// load generation if that flicker shows up.
		nextRows := withCreationRows(rowsFromTasks(msg.tasks), m.rows)
		preserveLoadedRowState(nextRows, m.rows)
		m.reconcileTaskStatusTracking(nextRows)
		m.rows = nextRows
		m.clampSelection()
		if m.selectRow(selectedKey) {
			// The reload keeps each task's status, so the cursor stays on the
			// same agent sub-row while that agent still runs.
			m.selectedAgent = selectedAgent
			m.clampAgentSelection()
		}
		return m, tea.Batch(m.afterTasksLoadedCmds(known)...)
	case taskPullRequestsLoadedMsg:
		// A refresh that fails keeps the pull requests and unshipped work
		// loaded before.
		if msg.err == nil {
			m.setTaskPullRequests(msg.taskID, msg.pullRequests)
		}
		if msg.stored {
			return m, taskPullRequestsCmd(m.statusContext, m.frontend, msg.taskID)
		}
		return m, nil
	case taskActivityLoadedMsg:
		if msg.err != nil {
			return m, nil
		}
		m.setTaskActivity(msg.taskID, msg.activity)
		return m, nil
	case taskUsageLoadedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.setTaskUsage(msg.taskID, msg.usage)
		return m, nil
	case taskStatusSubscriptionReadyMsg:
		if msg.err != nil {
			m.cancelTaskStatusTracking(msg.taskID)
			return m, nil
		}
		return m, waitForTaskStatusCmd(msg.taskID, msg.updates)
	case taskStatusUpdatedMsg:
		update := msg.update
		m.setTaskStatus(msg.taskID, &update)
		// A sub-row whose agent ended, or a task down to one agent, leaves the
		// cursor on the task row.
		m.clampAgentSelection()
		spinnerCmd := m.startSpinner()
		return m, tea.Batch(
			taskActivityCmd(m.statusContext, m.frontend, msg.taskID, taskActivityFetchLimit),
			taskUsageCmd(m.statusContext, m.frontend, msg.taskID),
			waitForTaskStatusCmd(msg.taskID, msg.updates),
			spinnerCmd,
		)
	case taskStatusSubscriptionClosedMsg:
		m.cancelTaskStatusTracking(msg.taskID)
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
		return m, tea.Batch(m.launchChoiceLoadCmds()...)
	case providerDetectionsMsg:
		return m, m.setProviderDetections(msg)
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
		// What saved is no longer an edit, so a retry saves only the rest.
		for _, edit := range msg.savedDefaults {
			m.setLocalLaunchDefault(edit.provider, edit.choice)
			delete(m.setupForm.edits, edit.provider)
		}
		if msg.defaultsErr != nil {
			// Setup stays to show it, and enter saves again. The saves
			// dropped the replies of fetches under way, so the defaults are
			// fetched again.
			m.setupForm.err = fmt.Errorf("provider setup saved; %w", msg.defaultsErr)
			return m, m.fetchLaunchDefaults()
		}
		// The Launch defaults and the configured providers' model lists load
		// while the repo directories step shows.
		loads := m.launchChoiceLoadCmds()
		next, cmd := m.enterRepoDirsSetupMode()
		return next, tea.Batch(append(loads, cmd)...)
	case repoDirsLoadedMsg:
		return m.applyLoadedRepoDirs(msg)
	case repoDirsSavedMsg:
		return m.applySavedRepoDirs(msg)
	case providerModelsLoadedMsg:
		m.setProviderModels(msg.provider, msg.models, msg.err)
		return m, nil
	case launchDefaultsLoadedMsg:
		m.setLaunchDefaults(msg)
		return m, nil
	case reposLoadedMsg:
		// A reply for a picker since closed is stale.
		if m.mode != modeRepoPicker {
			return m, nil
		}
		m.draft.err = msg.err
		m.draft.repos = m.reposByLastWork(msg.repos)
		m.draft.reposLoaded = msg.err == nil
		m.draft.repoSelected = 0
		return m, nil
	case repoPullRequestsLoadedMsg:
		// A reply for a repo other than the draft's, picked since, is stale.
		if msg.repoRoot != m.draft.repoRoot {
			return m, nil
		}
		m.draft.err = msg.err
		if msg.err != nil {
			m.draft.prs = nil
			return m, nil
		}
		m.draft.prs = append([]core.RepoPullRequest(nil), msg.prs...)
		m.clampPRSelection()
		return m, nil
	case taskCreateStreamStartFailedMsg:
		return m.finishCreation(msg.creation, nil, msg.err)
	case taskCreateEventMsg:
		switch {
		case msg.event.Progress != nil:
			m.advanceCreation(msg.creation, *msg.event.Progress)
			return m, waitForTaskCreateEventCmd(msg.creation, msg.events)
		case msg.event.Err != nil, msg.event.Task != nil:
			return m.finishCreation(msg.creation, msg.event.Task, msg.event.Err)
		default:
			return m, waitForTaskCreateEventCmd(msg.creation, msg.events)
		}
	case taskCreateStreamClosedMsg:
		// A stream that ends with its terminal event is not waited on again,
		// so this one ended without it.
		return m.finishCreation(msg.creation, nil, errors.New("create task stream closed unexpectedly"))
	case taskOpenedMsg:
		m.opening = false
		m.shimmerTick = 0
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.err = nil
		if m.inPopup {
			return m.quit()
		}
		return m, nil
	case taskDeletedMsg:
		// The user may have moved on since confirming, so the reply leaves
		// the mode and the cursor's task as they are.
		delete(m.deleting, msg.taskID)
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}

		selectedKey, selectedAgent := m.selectedRowKey(), m.selectedAgent
		m.removeTaskRow(msg.taskID)
		m.cancelTaskStatusTracking(msg.taskID)
		m.clampSelection()
		if m.selectRow(selectedKey) {
			m.selectedAgent = selectedAgent
			m.clampAgentSelection()
		}
		return m, nil
	case agentSessionRemovedMsg:
		// Once removed, the agent stays marked: the task's status update,
		// which may come after this reply, drops its sub-row. Agent session
		// IDs are never reused.
		if msg.err != nil {
			delete(m.removingAgents, msg.agentID)
			m.err = msg.err
		}
		return m, nil
	case activityRefreshTickMsg:
		cmds := []tea.Cmd{activityRefreshTickCmd()}
		if row := m.selectedRow(); row != nil && row.task != nil && taskID(row.task) != "" {
			id := taskID(row.task)
			cmds = append(cmds,
				taskActivityCmd(m.statusContext, m.frontend, id, taskActivityFetchLimit),
				taskUsageCmd(m.statusContext, m.frontend, id),
			)
		}
		return m, tea.Batch(cmds...)
	case taskListRefreshTickMsg:
		return m, tea.Batch(taskListRefreshTickCmd(), refreshTasksCmd(m.statusContext, m.frontend))
	case pullRequestRefreshTickMsg:
		cmds := []tea.Cmd{pullRequestRefreshTickCmd()}
		for _, row := range m.rows {
			if id := taskID(row.task); id != "" {
				cmds = append(cmds, taskPullRequestsCmd(m.statusContext, m.frontend, id))
			}
		}
		return m, tea.Batch(cmds...)
	case clearNoticeMsg:
		m.notice = ""
		return m, nil
	case shimmerTickMsg:
		if !m.opening {
			return m, nil
		}
		m.shimmerTick++
		return m, shimmerTickCmd()
	case spinnerTickMsg:
		if !m.anyWorking() {
			m.spinnerRunning = false
			return m, nil
		}
		m.spinnerFrame++
		return m, spinnerTickCmd()
	default:
		if m.mode == modePromptInput {
			return m.updatePromptInput(msg)
		}
		if m.mode == modeRepoDirsSetup {
			return m.updateRepoDirsSetup(msg)
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
	case modeRepoPicker:
		body = m.repoPickerView()
	case modeCleanupConfirm:
		body = m.confirmationView()
	case modeProviderSetup:
		body = m.providerSetupView()
	case modeRepoDirsSetup:
		body = m.repoDirsSetupView()
	default:
		body = m.listView()
	}
	if margin := (m.width - m.totalWidth()) / 2; margin > 0 {
		indent := strings.Repeat(" ", margin)
		body = indent + strings.ReplaceAll(body, "\n", "\n"+indent)
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

// preserveLoadedRowState carries each task's status, pull requests, unshipped
// work, activity and usage over to its reloaded row, so they show until their
// next update arrives.
func preserveLoadedRowState(nextRows []taskRow, currentRows []taskRow) {
	current := make(map[string]taskRow, len(currentRows))
	for _, row := range currentRows {
		if id := taskID(row.task); id != "" {
			current[id] = row
		}
	}

	for index := range nextRows {
		previous := current[taskID(nextRows[index].task)]
		nextRows[index].status = previous.status
		nextRows[index].pullRequests = previous.pullRequests
		nextRows[index].unshippedWork = previous.unshippedWork
		nextRows[index].activity = previous.activity
		nextRows[index].usage = previous.usage
	}
}

// afterTasksLoadedCmds tracks each listed task's status and fetches its
// details, skipping the details of known tasks, which their own refreshes
// keep current.
func (m *model) afterTasksLoadedCmds(known map[string]bool) []tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(m.rows)*4)
	for _, row := range m.rows {
		taskID := taskID(row.task)
		cmds = append(cmds, m.taskStatusTrackingCmds(taskID)...)
		if taskID != "" && !known[taskID] {
			cmds = append(cmds, taskActivityCmd(m.statusContext, m.frontend, taskID, taskActivityFetchLimit))
			cmds = append(cmds, taskUsageCmd(m.statusContext, m.frontend, taskID))
			cmds = append(cmds, storedTaskPullRequestsCmd(m.statusContext, m.frontend, taskID))
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
	cwd, _, _ := m.currentRepoScope()
	provider := m.effectiveCreateProvider()
	choice := m.draftLaunchChoice()
	m.transition(modeBrowse)
	m.rememberLaunchDefault(provider, choice)
	id := m.addCreationRow(cwd, core.Task{Provider: provider, Prompt: prompt})
	spinnerCmd := m.startSpinner()

	return m, tea.Batch(
		createTaskStreamCmd(m.statusContext, m.frontend, id, core.CreateTaskInput{
			Cwd:            cwd,
			Prompt:         prompt,
			Provider:       provider,
			Model:          choice.Model,
			ReasoningLevel: choice.ReasoningLevel,
			// The draft's choice becomes the provider's Launch default.
			RememberLaunchChoice: true,
		}),
		spinnerCmd,
	)
}

// retrySelectedTaskCreation retries the selected task's failed creation in
// the background, on its row, as a new creation does.
func (m model) retrySelectedTaskCreation() (model, tea.Cmd) {
	row := m.selectedRow()
	if row == nil || row.task == nil || row.task.CreationStatus != core.TaskCreationStatusFailed {
		return m, nil
	}

	m.creationSeq++
	row.creating = &rowCreation{id: m.creationSeq, step: row.task.CreationStep}
	m.err = nil
	spinnerCmd := m.startSpinner()

	return m, tea.Batch(
		retryTaskCreationStreamCmd(m.statusContext, m.frontend, m.creationSeq, row.task.ID),
		spinnerCmd,
	)
}

func (m model) updatePromptInput(msg tea.Msg) (tea.Model, tea.Cmd) {
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
		// Tab cycles the new task's provider, and ctrl+o and ctrl+r its Launch
		// choice; the textarea binds none of them.
		switch typed.String() {
		case "tab":
			m.cycleDraftProvider()
			return m, nil
		case "ctrl+o":
			m.cycleDraftLaunchChoice(model.nextLaunchModel)
			return m, nil
		case "ctrl+r":
			m.cycleDraftLaunchChoice(model.nextLaunchReasoningLevel)
			return m, nil
		case "ctrl+n":
			if !m.transition(modeRepoPicker) {
				return m, nil
			}
			m.draft.repos = nil
			m.draft.reposLoaded = false
			m.draft.err = nil
			m.draft.input.Blur()
			return m, listReposCmd(m.statusContext, m.frontend)
		case "ctrl+p":
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
			cmds := append(
				[]tea.Cmd{listRepoPullRequestsCmd(m.statusContext, m.frontend, repoRoot, repoName)},
				m.retryLaunchChoiceCmds()...,
			)
			return m, tea.Batch(cmds...)
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

func (m model) updateRepoPicker(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	last := len(m.draft.repos) - 1
	switch msg.String() {
	case "esc", "q":
		return m.handleBack()
	case "g", "home":
		m.draft.repoSelected = 0
	case "G", "end":
		m.draft.repoSelected = max(last, 0)
	case "j", "down":
		m.draft.repoSelected = min(m.draft.repoSelected+1, max(last, 0))
	case "k", "up":
		m.draft.repoSelected = max(m.draft.repoSelected-1, 0)
	case "enter":
		if m.draft.repoSelected > last {
			return m, nil
		}
		repo := m.draft.repos[m.draft.repoSelected]
		m.draft.repoRoot, m.draft.repoName = repo.Root, repo.Name
		return m.handleBack()
	}
	return m, nil
}

// reposByLastWork orders repos by when their tasks were last worked on, most
// recent first. Repos without tasks keep their order, after the others.
func (m model) reposByLastWork(repos []core.KnownRepo) []core.KnownRepo {
	lastWork := make(map[string]time.Time)
	for _, row := range m.rows {
		if row.task == nil {
			continue
		}
		root := filepath.Clean(strings.TrimSpace(row.task.RepoRoot))
		worked := latestTime(row.task.CreatedAt, row.task.UpdatedAt)
		if row.status != nil {
			worked = latestTime(worked, row.status.ObservedAt)
		}
		lastWork[root] = latestTime(lastWork[root], worked)
	}
	sorted := slices.Clone(repos)
	slices.SortStableFunc(sorted, func(a, b core.KnownRepo) int {
		return lastWork[filepath.Clean(b.Root)].Compare(lastWork[filepath.Clean(a.Root)])
	})
	return sorted
}

func latestTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

func (m model) updatePromptPaste(msg tea.PasteMsg) (tea.Model, tea.Cmd) {
	if m.mode != modePromptInput {
		return m, nil
	}
	return m.updatePromptInput(msg)
}

func (m model) updatePRPicker(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
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
		choice := m.draftLaunchChoice()
		m.transition(modeBrowse)
		m.rememberLaunchDefault(provider, choice)
		id := m.addCreationRow(repoRoot, core.Task{Provider: provider})
		spinnerCmd := m.startSpinner()

		return m, tea.Batch(
			createTaskStreamCmd(m.statusContext, m.frontend, id, core.CreateTaskInput{
				Cwd:                  repoRoot,
				Provider:             provider,
				Model:                choice.Model,
				ReasoningLevel:       choice.ReasoningLevel,
				RememberLaunchChoice: true,
				Source: core.CreateTaskSource{
					PullRequest: &selected,
				},
			}),
			spinnerCmd,
		)
	case "tab":
		m.cycleDraftProvider()
		return m, nil
	case "ctrl+o":
		m.cycleDraftLaunchChoice(model.nextLaunchModel)
		return m, nil
	case "ctrl+r":
		m.cycleDraftLaunchChoice(model.nextLaunchReasoningLevel)
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
	m.startDraftLaunchChoice()
	cmds := append([]tea.Cmd{m.draft.input.Focus()}, m.retryLaunchChoiceCmds()...)
	return m, tea.Batch(cmds...)
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
	switch msg.String() {
	case "q", "n", "esc":
		return m.handleBack()
	case "y":
		// The removal runs in the background: the list is back at once, and
		// the row shows the removal until it replies.
		target := m.cleanup
		m.cleanup = cleanupTarget{}
		m.transition(modeBrowse)
		if target.task == nil {
			return m, nil
		}
		m.err = nil
		id := taskID(target.task)
		if target.agent != nil {
			m.removingAgents[target.agent.ID] = true
			return m, removeAgentSessionCmd(m.statusContext, m.frontend, id, target.agent.ID)
		}
		m.deleting[id] = true
		return m, deleteTaskCmd(m.statusContext, m.frontend, id)
	default:
		return m, nil
	}
}

// selectedBusy reports whether the selected task is being created or removed,
// or the agent of the selected sub-row is being removed, so the row takes no
// action.
func (m model) selectedBusy() bool {
	row := m.selectedRow()
	if row == nil {
		return false
	}
	if row.creating != nil || m.deleting[taskID(row.task)] {
		return true
	}
	session := m.selectedAgentSession()
	return session != nil && m.removingAgents[session.ID]
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

// moveSelection moves the selection by whole task rows, landing on a task
// row rather than an agent sub-row. Past the last task it lands on the list's
// last line, so it never moves the cursor against its direction.
func (m *model) moveSelection(delta int) {
	next := clampIndex(m.selected+delta, len(m.rows))
	if delta > 0 && next == m.selected {
		m.selectLastLine()
		return
	}
	m.selected = next
	m.selectedAgent = ""
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
	m.clampAgentSelection()
}

func (m *model) movePRSelection(delta int) {
	m.draft.prSelected = clampIndex(m.draft.prSelected+delta, len(m.draft.prs))
}

func (m *model) clampPRSelection() {
	m.draft.prSelected = clampIndex(m.draft.prSelected, len(m.draft.prs))
}

// selectRow moves the cursor to the row with the given rowKey.
func (m *model) selectRow(key string) bool {
	if key == "" {
		return false
	}

	for index, row := range m.rows {
		if rowKey(row) != key {
			continue
		}
		m.selected = index
		return true
	}

	return false
}

// rowKey tells rows apart across list changes: a creation's row by its
// creation, as its task may have no ID yet, and any other by its task's ID.
func rowKey(row taskRow) string {
	if row.creating != nil {
		return "creating:" + strconv.Itoa(row.creating.id)
	}
	return taskID(row.task)
}

func (m model) selectedRowKey() string {
	if row := m.selectedRow(); row != nil {
		return rowKey(*row)
	}
	return ""
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

func (m *model) setTaskPullRequests(taskID string, pullRequests core.TaskPullRequests) {
	if row := m.taskRowByID(taskID); row != nil {
		row.pullRequests = pullRequests.PullRequests
		row.unshippedWork = pullRequests.UnshippedWork
	}
}

func (m *model) setTaskActivity(taskID string, activity []core.TaskActivityEvent) {
	if row := m.taskRowByID(taskID); row != nil {
		row.activity = append([]core.TaskActivityEvent(nil), activity...)
	}
}

func (m *model) setTaskUsage(taskID string, usage *core.TaskUsage) {
	if row := m.taskRowByID(taskID); row != nil {
		row.usage = usage
	}
}

// addCreationRow numbers a new creation and adds the row it shows until its
// task is named, in the group of its repository as far as the list knows it,
// with the cursor on it.
func (m *model) addCreationRow(cwd string, task core.Task) int {
	m.creationSeq++
	task.RepoRoot, task.RepoName = m.creationRepo(cwd)
	row := taskRow{task: &task, creating: &rowCreation{id: m.creationSeq}}
	m.rows = groupRowsByRepo(append(m.rows, row))
	m.selectRow(rowKey(row))
	m.selectedAgent = ""
	return m.creationSeq
}

// creationRepo is the repository root and name a creation from cwd goes
// under: those of the listed task whose repository or worktree holds cwd, or
// else cwd itself. Its row moves to its task's repository once named.
func (m model) creationRepo(cwd string) (string, string) {
	for _, row := range m.rows {
		if task := row.task; task != nil && (pathWithin(cwd, task.RepoRoot) || pathWithin(cwd, task.WorktreePath)) {
			return task.RepoRoot, task.RepoName
		}
	}
	return cwd, filepath.Base(cwd)
}

func pathWithin(path string, dir string) bool {
	return dir != "" && (path == dir || strings.HasPrefix(path, dir+string(filepath.Separator)))
}

// creationRow returns the index of the row creation id shows, or -1.
func (m model) creationRow(id int) int {
	return slices.IndexFunc(m.rows, func(row taskRow) bool {
		return row.creating != nil && row.creating.id == id
	})
}

// advanceCreation shows a creation's step on its row, and its task once the
// step carries it.
func (m *model) advanceCreation(id int, progress core.TaskCreateProgressEvent) {
	index := m.creationRow(id)
	if index < 0 {
		return
	}
	m.rows[index].creating.step = progress.Step
	if progress.Task != nil {
		m.setCreationTask(index, progress.Task)
	}
}

// setCreationTask gives the creation row at index its task. A row a reload
// already gave the task takes the creation over, and the creation's own row
// goes, so the task shows once. It returns the index of the row with the task.
func (m *model) setCreationTask(index int, task *core.Task) int {
	selectedKey := m.selectedRowKey()
	creating := m.rows[index].creating
	listed := slices.IndexFunc(m.rows, func(row taskRow) bool {
		return row.creating != creating && taskID(row.task) != "" && taskID(row.task) == taskID(task)
	})
	if listed < 0 {
		m.rows[index].task = task
		m.rows = groupRowsByRepo(m.rows)
	} else {
		m.rows[listed].task, m.rows[listed].creating = task, creating
		if selectedKey == taskID(task) {
			selectedKey = rowKey(m.rows[listed])
		}
		m.rows = slices.Delete(m.rows, index, index+1)
	}
	m.selectRow(selectedKey)
	m.clampSelection()
	return slices.IndexFunc(m.rows, func(row taskRow) bool { return row.creating == creating })
}

// finishCreation ends a creation as it replies: its row keeps its task,
// ready or failed, or goes when it has none. The user may have moved on, so
// it leaves the mode as it is and the cursor on the same row.
func (m model) finishCreation(id int, task *core.Task, err error) (tea.Model, tea.Cmd) {
	index := m.creationRow(id)
	if index < 0 {
		return m, nil
	}
	if err != nil {
		m.err = err
	}
	// A reply without a task, as from a refused retry or a dropped stream,
	// leaves a named row its task.
	if taskID(task) == "" && taskID(m.rows[index].task) != "" {
		task = m.rows[index].task
	}

	selectedKey := m.selectedRowKey()
	creationKey := rowKey(m.rows[index])
	if taskID(task) == "" {
		m.rows = slices.Delete(m.rows, index, index+1)
	} else {
		index = m.setCreationTask(index, task)
		m.rows[index].creating = nil
		if selectedKey == creationKey {
			selectedKey = taskID(task)
		}
	}
	m.selectRow(selectedKey)
	m.clampSelection()

	cmds := []tea.Cmd{loadTasksCmd(m.statusContext, m.frontend)}
	// Creations share the refetch, so the last to end makes it, once every
	// Launch choice submitted is saved or refused.
	if !slices.ContainsFunc(m.rows, func(row taskRow) bool { return row.creating != nil }) {
		cmds = append(cmds, m.launchDefaultsAfterCreateCmd())
	}
	cmds = append(cmds, m.taskStatusTrackingCmds(taskID(task))...)
	return m, tea.Batch(cmds...)
}

// withCreationRows carries each creation under way over to reloaded rows:
// onto its task's row, or as its own row while the reload does not list its
// task yet.
func withCreationRows(nextRows []taskRow, currentRows []taskRow) []taskRow {
	for _, row := range currentRows {
		if row.creating == nil {
			continue
		}
		id := taskID(row.task)
		listed := slices.IndexFunc(nextRows, func(next taskRow) bool { return id != "" && taskID(next.task) == id })
		if listed >= 0 {
			nextRows[listed].creating = row.creating
			continue
		}
		nextRows = append(nextRows, row)
	}
	return groupRowsByRepo(nextRows)
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

// currentRepoScope is the repository a new task is created in: the one picked
// for the draft, else where rig was launched, else the selected task's.
func (m model) currentRepoScope() (string, string, bool) {
	if m.draft.repoRoot != "" {
		return m.draft.repoRoot, m.draft.repoName, true
	}
	launchCwd := strings.TrimSpace(m.launchCwd)
	if launchCwd != "" {
		// Launched in a task's worktree, the new task goes to that task's repo.
		_, repoName := m.creationRepo(launchCwd)
		return launchCwd, repoName, true
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

// maxContentWidth caps how wide rig draws on a wide terminal. View centers
// the narrower content.
const maxContentWidth = 120

// totalWidth is the terminal's width, up to maxContentWidth. On a wider
// terminal it is only as wide as the widest task row and the list header
// need, so a row's columns sit just after the longest name and end under the
// header's keys.
func (m model) totalWidth() int {
	switch {
	case m.width < 40:
		return 72
	case m.width <= maxContentWidth:
		return m.width
	}
	header := lipgloss.Width(m.renderHeaderLabel()) + len(nameGap) + lipgloss.Width(m.listKeybindText())
	rows := m.widestNameArea() + taskRowFixedWidth
	return min(maxContentWidth, max(header, rows))
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
		grouped = append(grouped, nestChildRows(groups[key])...)
	}
	return grouped
}

// nestChildRows moves each child task's row below its parent's row, after the
// parent's earlier children, and keeps the rows' order otherwise. A child
// whose parent is not among rows, such as one in another repo, keeps its place.
func nestChildRows(rows []taskRow) []taskRow {
	present := make(map[string]bool, len(rows))
	for _, row := range rows {
		if row.task != nil && row.task.ID != "" {
			present[row.task.ID] = true
		}
	}
	children := make(map[string][]taskRow)
	roots := make([]taskRow, 0, len(rows))
	for _, row := range rows {
		if row.task != nil && present[row.task.ParentTaskID] {
			children[row.task.ParentTaskID] = append(children[row.task.ParentTaskID], row)
			continue
		}
		roots = append(roots, row)
	}

	nested := make([]taskRow, 0, len(rows))
	var appendWithChildren func(row taskRow)
	appendWithChildren = func(row taskRow) {
		nested = append(nested, row)
		if row.task != nil && row.task.ID != "" {
			for _, child := range children[row.task.ID] {
				appendWithChildren(child)
			}
		}
	}
	for _, row := range roots {
		appendWithChildren(row)
	}
	// A parent link cycle leaves its tasks without a root; never drop rows.
	if len(nested) != len(rows) {
		return rows
	}
	return nested
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
