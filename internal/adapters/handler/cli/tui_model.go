package cli

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	observer "rig/internal/adapters/observability/observer"
	"rig/internal/core"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

var availableProviders = []string{"codex", "claude"}

type tuiMode string

const (
	tuiModeList           tuiMode = "list"
	tuiModeCleanupConfirm tuiMode = "cleanup_confirm"
	tuiModePromptInput    tuiMode = "prompt_input"
	tuiModeNameConfirm    tuiMode = "name_confirm"
)

// nolint:recvcheck // bubbletea requires value receivers for tea.Model; mutation helpers need pointer receivers.
type model struct {
	service            TaskService
	err                error
	createInput        core.NewTaskInput
	provider           string
	defaultCreationCwd string
	mode               tuiMode
	taskViews          []*core.TaskView
	tasks              []*core.Task
	observerSocketPath string
	hookUpdates        <-chan core.HookSessionSummary
	observerUpdates    <-chan core.ObserverTaskUpdate
	unsubscribeHooks   func()
	unsubscribeUpdates func()
	promptInput        textarea.Model
	nameInput          textinput.Model
	selected           int
	width              int
	loading            bool
	busy               bool
	createInFlight     bool
	creationFailed     bool
	creationProgress   core.TaskProgressStep
	creationSteps      []string
	recentEvents       []core.HookEvent
	recentEventsTaskID string
	shimmerTick        int
	progressCh         <-chan taskProgressMsg
	tasksRequestSeq    int
	creationTask       *core.Task
}

type tasksLoadedMsg struct {
	requestID int
	err       error
	views     []*core.TaskView
}

type prStatusLoadedMsg struct {
	taskID string
	status *core.PRStatus
}

type observerSubscriptionReadyMsg struct {
	err     error
	updates <-chan core.ObserverTaskUpdate
	cleanup func()
}

type hookSubscriptionReadyMsg struct {
	err     error
	updates <-chan core.HookSessionSummary
	cleanup func()
}

type hookTaskUpdatedMsg struct {
	update core.HookSessionSummary
}

type hookSubscriptionClosedMsg struct{}

type observerTaskUpdatedMsg struct {
	update core.ObserverTaskUpdate
}

type observerSubscriptionClosedMsg struct{}

type cleanupFinishedMsg struct {
	task *core.Task
	err  error
}

type openFinishedMsg struct {
	err error
}

type suggestNameFinishedMsg struct {
	err        error
	prompt     string
	suggestion core.TaskSuggestion
}

type createFinishedMsg struct {
	task *core.Task
	err  error
}

type taskProgressMsg struct {
	step    core.TaskProgressStep
	message string
	task    *core.Task
}

type recentEventsMsg struct {
	taskID string
	events []core.HookEvent
}

type shimmerTickMsg struct{}

type asyncErrMsg struct {
	err error
}

const shimmerTickInterval = 60 * time.Millisecond
const syntheticCreationTitle = "Creating task..."

func newTUIModel(
	service TaskService,
	defaultCreationCwd string,
	defaultProvider string,
	observerSocketPath string,
	initialErr error,
) model {
	promptInput := textarea.New()
	promptInput.Placeholder = "Describe the task to create..."
	promptInput.ShowLineNumbers = false
	promptInput.SetHeight(4)
	promptInput.Focus()

	nameInput := textinput.New()
	nameInput.Prompt = titleStyle.Render("❯") + " "
	nameInput.Placeholder = "Confirm or edit the suggested task name"

	return model{
		service:            service,
		err:                initialErr,
		loading:            true,
		mode:               tuiModeList,
		promptInput:        promptInput,
		nameInput:          nameInput,
		defaultCreationCwd: emptyFallback(defaultCreationCwd, "."),
		observerSocketPath: strings.TrimSpace(observerSocketPath),
		provider:           emptyFallback(defaultProvider, "codex"),
		tasksRequestSeq:    1,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		refreshTasksCmd(m.service, m.tasksRequestSeq),
		subscribeHookUpdatesCmd(m.service),
		subscribeObserverUpdatesCmd(m.observerSocketPath),
	)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.promptInput.SetWidth(msg.Width - 4)
		return m, nil
	case tea.PasteMsg:
		switch m.mode {
		case tuiModePromptInput:
			var cmd tea.Cmd
			m.promptInput, cmd = m.promptInput.Update(msg)
			return m, cmd
		case tuiModeNameConfirm:
			var cmd tea.Cmd
			m.nameInput, cmd = m.nameInput.Update(msg)
			return m, cmd
		}
		return m, nil
	case tea.KeyPressMsg:
		return m.updateKey(msg)
	case tasksLoadedMsg:
		if msg.requestID != 0 && msg.requestID < m.tasksRequestSeq {
			return m, nil
		}
		selectedKey := ""
		selectedSynthetic := m.isSyntheticCreationRowSelected()
		if task := m.selectedTask(); task != nil {
			selectedKey = strings.TrimSpace(selectedIDOrSlug(task))
		}
		m.loading = false
		if !m.createInFlight {
			m.busy = false
		}
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}

		m.taskViews = filterVisibleTaskViews(msg.views)
		m.tasks = taskViewsToTasks(m.taskViews)
		if len(m.visibleTaskViews()) == 0 {
			m.selected = 0
			if m.mode == tuiModeCleanupConfirm {
				m.mode = tuiModeList
			}
			return m, nil
		}

		switch {
		case selectedKey != "" && m.selectTaskByIDOrSlug(selectedKey):
		case selectedSynthetic && m.syntheticCreationTaskView() != nil:
			m.selected = m.syntheticCreationRowIndex()
		case m.selected >= len(m.visibleTaskViews()):
			m.selected = len(m.visibleTaskViews()) - 1
		}

		var cmds []tea.Cmd
		for _, view := range m.taskViews {
			if view != nil && view.Task != nil && strings.TrimSpace(view.Task.BranchName) != "" &&
				strings.TrimSpace(view.Task.RepoRoot) != "" {
				cmds = append(
					cmds,
					fetchPRStatusCmd(m.service, view.Task.ID, view.Task.RepoRoot, view.Task.BranchName),
				)
			}
		}
		if task := m.selectedTask(); task != nil && strings.TrimSpace(task.ID) != "" {
			cmds = append(cmds, fetchRecentEventsCmd(m.service, task.ID))
		}
		if len(cmds) > 0 {
			return m, tea.Batch(cmds...)
		}
		return m, nil
	case observerSubscriptionReadyMsg:
		if msg.err != nil {
			return m, nil
		}
		if m.unsubscribeUpdates != nil {
			m.unsubscribeUpdates()
		}
		m.unsubscribeUpdates = msg.cleanup
		m.observerUpdates = msg.updates
		return m, waitForObserverUpdateCmd(msg.updates)
	case hookSubscriptionReadyMsg:
		if msg.err != nil {
			return m, nil
		}
		if m.unsubscribeHooks != nil {
			m.unsubscribeHooks()
		}
		m.unsubscribeHooks = msg.cleanup
		m.hookUpdates = msg.updates
		return m, waitForHookUpdateCmd(msg.updates)
	case hookTaskUpdatedMsg:
		m.applyHookSessionUpdate(msg.update)
		var cmds []tea.Cmd
		cmds = append(cmds, waitForHookUpdateCmd(m.hookUpdates))
		if task := m.selectedTask(); task != nil && task.ID == msg.update.TaskID {
			cmds = append(cmds, fetchRecentEventsCmd(m.service, task.ID))
		}
		return m, tea.Batch(cmds...)
	case hookSubscriptionClosedMsg:
		if m.unsubscribeHooks != nil {
			m.unsubscribeHooks()
			m.unsubscribeHooks = nil
		}
		m.hookUpdates = nil
		return m, nil
	case observerTaskUpdatedMsg:
		m.applyObserverTaskUpdate(msg.update)
		return m, waitForObserverUpdateCmd(m.observerUpdates)
	case observerSubscriptionClosedMsg:
		if m.unsubscribeUpdates != nil {
			m.unsubscribeUpdates()
			m.unsubscribeUpdates = nil
		}
		m.observerUpdates = nil
		return m, nil
	case cleanupFinishedMsg:
		m.mode = tuiModeList
		m.err = msg.err
		if msg.task != nil {
			m.replaceTask(msg.task)
		}
		if msg.err != nil {
			m.busy = false
			return m, nil
		}
		m.busy = true
		return m, m.nextRefreshTasksCmd()
	case openFinishedMsg:
		m.err = msg.err
		if msg.err != nil {
			m.busy = false
			return m, nil
		}

		m.loading = true
		return m, m.nextRefreshTasksCmd()
	case suggestNameFinishedMsg:
		m.busy = false
		m.creationProgress = ""
		m.shimmerTick = 0
		m.err = msg.err
		if msg.err != nil {
			m.mode = tuiModePromptInput
			m.promptInput.Focus()
			return m, nil
		}

		m.createInput.Prompt = msg.prompt
		m.createInput.ConfirmedBranchType = msg.suggestion.BranchType
		m.nameInput.SetValue(msg.suggestion.Name)
		m.nameInput.CursorEnd()
		m.nameInput.Focus()
		m.promptInput.Blur()
		m.mode = tuiModeNameConfirm
		return m, nil
	case createFinishedMsg:
		m.busy = false
		m.progressCh = nil
		m.err = msg.err
		if msg.err != nil {
			if msg.task != nil {
				m.creationTask = cloneTaskSnapshot(msg.task)
				m.tasksRequestSeq++
				if isVisibleTask(msg.task) {
					m.upsertTask(msg.task)
					m.selectTaskByIDOrSlug(selectedIDOrSlug(msg.task))
				} else {
					m.selected = m.syntheticCreationRowIndex()
				}
				m.createInFlight = false
				m.creationFailed = true
				m.creationProgress = ""
				m.shimmerTick = 0
				m.mode = tuiModeList
				return m, nil
			}
			if m.createInFlight {
				m.markCreationFailed()
				return m, nil
			}
			m.mode = tuiModeList
			return m, nil
		}

		m.mode = tuiModeList
		m.createInFlight = false
		m.creationFailed = false
		m.creationProgress = ""
		m.creationSteps = nil
		m.shimmerTick = 0
		if msg.task != nil {
			m.creationTask = cloneTaskSnapshot(msg.task)
			m.tasksRequestSeq++
			m.upsertTask(msg.task)
			m.selectTaskByIDOrSlug(selectedIDOrSlug(msg.task))
		}
		return m, nil
	case recentEventsMsg:
		if task := m.selectedTask(); task != nil && task.ID == msg.taskID {
			m.recentEvents = msg.events
			m.recentEventsTaskID = msg.taskID
		}
		return m, nil
	case prStatusLoadedMsg:
		for _, view := range m.taskViews {
			if view != nil && view.Task != nil && view.Task.ID == msg.taskID {
				view.PR = msg.status
				break
			}
		}
		return m, nil
	case taskProgressMsg:
		if msg.task != nil {
			m.creationTask = cloneTaskSnapshot(msg.task)
		}
		m.creationProgress = msg.step
		if label := progressStepLabel(msg.step); label != "" {
			// Avoid duplicating the initial step that was seeded when
			// entering the creation phase.
			if len(m.creationSteps) == 0 || m.creationSteps[len(m.creationSteps)-1] != label {
				m.creationSteps = append(m.creationSteps, label)
			}
		}
		m.shimmerTick = 0
		var cmds []tea.Cmd
		if m.progressCh != nil {
			cmds = append(cmds, waitForProgressCmd(m.progressCh))
		}
		cmds = append(cmds, tea.Tick(shimmerTickInterval, func(time.Time) tea.Msg { return shimmerTickMsg{} }))
		return m, tea.Batch(cmds...)
	case shimmerTickMsg:
		if m.creationProgress == "" {
			return m, nil
		}
		m.shimmerTick++
		return m, tea.Tick(shimmerTickInterval, func(time.Time) tea.Msg { return shimmerTickMsg{} })
	case asyncErrMsg:
		m.err = msg.err
		m.loading = false
		m.busy = false
		m.creationProgress = ""
		m.shimmerTick = 0
		m.progressCh = nil
		if m.createInFlight {
			m.markCreationFailed()
			return m, nil
		}
		m.createInFlight = false
		m.creationFailed = false
		return m, nil
	default:
		return m, nil
	}
}

func (m model) View() tea.View {
	var body string
	switch m.mode {
	case tuiModeCleanupConfirm:
		body = m.confirmationView()
	case tuiModePromptInput:
		body = m.promptInputView()
	case tuiModeNameConfirm:
		body = m.nameConfirmView()
	default:
		body = m.listView()
	}
	v := tea.NewView(body)
	v.AltScreen = true
	return v
}

func (m model) updateKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.Code == 'c' && msg.Mod == tea.ModCtrl {
		m.cleanupSubscriptions()
		return m, tea.Quit
	}

	if m.busy && !m.createInFlight {
		return m, nil
	}

	switch m.mode {
	case tuiModeCleanupConfirm:
		return m.updateCleanupConfirmKey(msg)
	case tuiModePromptInput:
		return m.updatePromptInputKey(msg)
	case tuiModeNameConfirm:
		return m.updateNameConfirmKey(msg)
	default:
		return m.updateListKey(msg)
	}
}

func (m model) updateListKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		if m.blockedByActiveCreation("Task creation is still in progress") {
			return m, nil
		}
		m.cleanupSubscriptions()
		return m, tea.Quit
	case "enter":
		if m.isSyntheticCreationRowSelected() {
			m.err = m.syntheticCreationRowActionError()
			return m, nil
		}
		task := m.selectedTask()
		if task == nil {
			return m, nil
		}

		m.err = nil
		m.busy = true
		return m, openTaskCmd(m.service, selectedIDOrSlug(task))
	case "j", "down":
		if m.selected < m.visibleRowCount()-1 {
			m.selected++
			return m, m.fetchRecentEventsForSelected()
		}
		return m, nil
	case "k", "up":
		if m.selected > 0 {
			m.selected--
			return m, m.fetchRecentEventsForSelected()
		}
		return m, nil
	case "g", "home":
		if m.visibleRowCount() > 0 {
			m.selected = 0
			return m, m.fetchRecentEventsForSelected()
		}
		return m, nil
	case "G", "end":
		if m.visibleRowCount() > 0 {
			m.selected = m.visibleRowCount() - 1
			return m, m.fetchRecentEventsForSelected()
		}
		return m, nil
	case "x":
		if m.isSyntheticCreationRowSelected() {
			m.err = m.syntheticCreationRowActionError()
			return m, nil
		}
		if m.visibleRowCount() == 0 {
			return m, nil
		}

		m.mode = tuiModeCleanupConfirm
		return m, nil
	case "n":
		if m.blockedByActiveCreation("Task creation already in progress") {
			return m, nil
		}
		creationCwd := m.creationCwd()
		m.err = nil
		m.creationFailed = false
		m.mode = tuiModePromptInput
		m.createInput = core.NewTaskInput{Cwd: creationCwd}
		m.promptInput.Reset()
		m.promptInput.Focus()
		m.nameInput.Blur()
		return m, nil
	case "r":
		m.service.InvalidatePRCache()
		m.err = nil
		m.busy = true
		m.loading = true
		return m, m.nextRefreshTasksCmd()
	default:
		return m, nil
	}
}

func (m model) updateCleanupConfirmKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "n", "esc":
		m.mode = tuiModeList
		return m, nil
	case "y":
		if m.isSyntheticCreationRowSelected() {
			m.mode = tuiModeList
			m.err = m.syntheticCreationRowActionError()
			return m, nil
		}
		task := m.selectedTask()
		if task == nil {
			m.mode = tuiModeList
			return m, nil
		}

		m.mode = tuiModeList
		m.busy = true
		m.err = nil
		return m, cleanupTaskCmd(m.service, selectedIDOrSlug(task))
	default:
		return m, nil
	}
}

func (m model) updatePromptInputKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.Code {
	case tea.KeyEscape:
		m.mode = tuiModeList
		m.promptInput.Blur()
		return m, nil
	case tea.KeyTab:
		m.provider = nextProvider(m.provider)
		return m, nil
	case tea.KeyEnter:
		prompt := strings.TrimSpace(m.promptInput.Value())
		if prompt == "" {
			return m, nil
		}

		m.err = nil
		m.busy = true
		m.createInFlight = true
		m.creationFailed = false
		m.mode = tuiModeList
		m.creationTask = nil
		m.creationProgress = core.TaskProgressNaming
		m.creationSteps = []string{progressStepLabel(core.TaskProgressNaming)}
		m.shimmerTick = 0
		m.createInput = core.NewTaskInput{
			Cwd:      m.creationCwd(),
			Prompt:   prompt,
			Provider: m.provider,
		}
		m.selected = m.syntheticCreationRowIndex()
		m.promptInput.Blur()
		progressCh, createCmd := createTaskCmd(m.service, m.createInput)
		m.progressCh = progressCh
		return m, tea.Batch(
			createCmd,
			waitForProgressCmd(progressCh),
			tea.Tick(shimmerTickInterval, func(time.Time) tea.Msg { return shimmerTickMsg{} }),
		)
	}

	var cmd tea.Cmd
	m.promptInput, cmd = m.promptInput.Update(msg)
	return m, cmd
}

func (m model) updateNameConfirmKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.Code {
	case tea.KeyEscape:
		m.mode = tuiModeList
		m.nameInput.Blur()
		return m, nil
	case tea.KeyEnter:
		name := strings.TrimSpace(m.nameInput.Value())
		if name == "" {
			return m, nil
		}

		m.err = nil
		m.busy = true
		m.nameInput.Blur()
		m.creationProgress = core.TaskProgressWorktreeCreating
		m.creationFailed = false
		m.creationSteps = []string{progressStepLabel(core.TaskProgressWorktreeCreating)}
		m.shimmerTick = 0
		input := m.createInput
		input.ConfirmedDisplayName = name
		input.Provider = m.provider
		progressCh, createCmd := createTaskCmd(m.service, input)
		m.progressCh = progressCh
		return m, tea.Batch(
			createCmd,
			waitForProgressCmd(progressCh),
			tea.Tick(shimmerTickInterval, func(time.Time) tea.Msg { return shimmerTickMsg{} }),
		)
	}

	var cmd tea.Cmd
	m.nameInput, cmd = m.nameInput.Update(msg)
	return m, cmd
}

// Two-line row column widths.
const (
	colWidthAgent   = 7  // "claude" padded to 7 chars so PR aligns
	colWidthStatus  = 14 // "◐ needs input " — widest status label, left-aligned
	colWidthElapsed = 7  // "2h 15m" right-aligned
)

// truncateStr truncates s to max runes and appends "…" if it was longer.
func truncateStr(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-1]) + "…"
}

// padRight pads s with spaces to exactly width runes. If s is longer it is returned as-is.
func padRight(s string, width int) string {
	runes := []rune(s)
	if len(runes) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(runes))
}

// padRightVisible pads s based on visible width (ignoring ANSI escape codes).
func padRightVisible(s string, width int) string {
	visible := lipgloss.Width(s)
	if visible >= width {
		return s
	}
	return s + strings.Repeat(" ", width-visible)
}

// renderHeader renders a left-right header padded to totalWidth.
func renderHeader(left, right string, totalWidth int) string {
	gap := totalWidth - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 2 {
		gap = 2
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m model) listView() string {
	var b strings.Builder

	totalWidth := m.width
	if totalWidth < 40 {
		totalWidth = 72
	}

	// Header: "RIG" on left, keybindings on right
	b.WriteString(renderHeader(
		headerLabelStyle.Render("RIG"),
		mutedStyle.Render("n new   r refresh   x clean   q quit"),
		totalWidth,
	) + "\n")
	b.WriteString(dividerStyle.Render(strings.Repeat("─", totalWidth)) + "\n")

	if m.err != nil {
		b.WriteString(errorStyle.Render("Error: "+m.err.Error()) + "\n\n")
	}

	if m.loading {
		b.WriteString(dimStyle.Render("Loading tasks..."))
		return b.String()
	}

	if m.busy {
		b.WriteString(dimStyle.Render("Working...") + "\n\n")
	}

	rows := m.visibleTaskViews()
	if len(rows) == 0 {
		b.WriteString(dimStyle.Render("No tasks found.") + "\n")
		b.WriteString(dimStyle.Render("Press n to create one."))
		return b.String()
	}

	// Task rows — two lines per task
	for i, view := range rows {
		if view == nil || view.Task == nil {
			continue
		}
		task := view.Task
		stateText, stStyle := taskStateText(view)
		elapsed := taskElapsed(view)

		// Line 1: task name (flex) + status (fixed) + time (fixed)
		statusCell := padRightVisible(stateText, colWidthStatus)
		timeCell := padLeftVisible(elapsed, colWidthElapsed)
		rightWidth := colWidthStatus + colWidthElapsed
		nameWidth := totalWidth - rightWidth - 4 // 4 = padding from style
		if nameWidth < 10 {
			nameWidth = 10
		}

		// Line 2: agent (fixed 7) + PR text
		agentName := emptyFallback(task.Provider, "-")
		agentCell := padRight(agentName, colWidthAgent)
		prText := m.prTextForTask(view)

		nameStr := truncateStr(task.DisplayName, nameWidth)
		namePad := padRight(nameStr, nameWidth)

		if i == m.selected {
			nameRendered := lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render(namePad)
			timeRendered := primaryStyle.Render(timeCell)
			line1 := nameRendered + stStyle.Render(statusCell) + timeRendered
			line2 := providerStyle(task.Provider).Render(agentCell) + prText
			b.WriteString(selectedRowStyle.Render(line1) + "\n")
			b.WriteString(selectedRowStyle.Render(line2) + "\n")
		} else {
			nameRendered := dimStyle.Render(namePad)
			timeRendered := dimStyle.Render(timeCell)
			line1 := nameRendered + stStyle.Render(statusCell) + timeRendered
			line2 := providerStyle(task.Provider).Render(agentCell) + prText
			b.WriteString(normalRowStyle.Render(line1) + "\n")
			b.WriteString(normalRowStyle.Render(line2) + "\n")
		}

		// Vertical spacing between tasks
		if i < len(rows)-1 {
			b.WriteString("\n")
		}
	}

	detail := m.selectedTaskDetailView()
	if detail != "" {
		b.WriteString(dividerStyle.Render(strings.Repeat("─", totalWidth)) + "\n")
		b.WriteString(detail)
	}

	return strings.TrimRight(b.String(), "\n")
}

// padLeftVisible right-aligns s within width, padding on the left.
func padLeftVisible(s string, width int) string {
	visible := lipgloss.Width(s)
	if visible >= width {
		return s
	}
	return strings.Repeat(" ", width-visible) + s
}

// prTextForTask returns formatted PR text for the second line of a task row.
func (m model) prTextForTask(view *core.TaskView) string {
	if view == nil || view.PR == nil || view.PR.State == core.PRStateNone {
		return mutedStyle.Render(iconPRNone)
	}
	icon, style := prStateIconStyle(view.PR.State)
	return style.Render(fmt.Sprintf(
		"%s PR #%d %s", icon, view.PR.Number, view.PR.State,
	))
}

func (m model) selectedTaskDetailView() string {
	task := m.selectedTask()
	if task == nil {
		return ""
	}

	if m.isSyntheticCreationRowSelected() {
		return m.syntheticCreationDetailView()
	}

	view := m.selectedTaskView()
	var b strings.Builder

	// Workspace column
	var wsCol strings.Builder
	// Column width: use ~45% of terminal width, min 42
	tw := m.width
	if tw < 40 {
		tw = 72
	}
	detailColWidth := tw * 45 / 100
	if detailColWidth < 42 {
		detailColWidth = 42
	}
	branchMaxLen := detailColWidth - 9 // "branch" + spaces + some margin

	wsCol.WriteString(headerLabelStyle.Render("WORKSPACE") + "\n")
	if strings.TrimSpace(task.BranchName) != "" {
		wsCol.WriteString(mutedStyle.Render("branch") + " " +
			primaryStyle.Render(truncateStr(task.BranchName, branchMaxLen)) + "\n")
	}
	if strings.TrimSpace(task.RepoName) != "" {
		wsCol.WriteString(mutedStyle.Render("repo") + "   " +
			primaryStyle.Render(task.RepoName) + "\n")
	}
	if view != nil && view.PR != nil && view.PR.State != core.PRStateNone {
		prIcon, prStyle := prStateIconStyle(view.PR.State)
		wsCol.WriteString(mutedStyle.Render("pr") + "     " +
			prStyle.Render(fmt.Sprintf("%s #%d %s", prIcon, view.PR.Number, view.PR.State)) + "\n")
	}

	// Session column
	var sessCol strings.Builder
	sessCol.WriteString(headerLabelStyle.Render("SESSION") + "\n")
	elapsed := taskElapsed(view)
	if elapsed != "" {
		timeLine := mutedStyle.Render("time") + "   " +
			primaryStyle.Bold(true).Render(elapsed) + mutedStyle.Render(" total")
		turnElapsed := taskTurnElapsed(view)
		if turnElapsed != "" {
			timeLine += dividerStyle.Render(" · ") +
				dimStyle.Render(turnElapsed) + mutedStyle.Render(" current turn")
		}
		sessCol.WriteString(timeLine + "\n")
	}
	if view.TokenUsage != nil {
		u := view.TokenUsage
		sessCol.WriteString(dimStyle.Render("tokens") + "\n")
		inputDetail := compactCount(u.InputTokens)
		if u.CachedInputTokens > 0 || u.CacheCreationInputTokens > 0 {
			inputDetail += mutedStyle.Render(" (") +
				compactCount(u.CachedInputTokens) + mutedStyle.Render(" cached")
			if u.CacheCreationInputTokens > 0 {
				inputDetail += mutedStyle.Render(" · ") +
					compactCount(u.CacheCreationInputTokens) + mutedStyle.Render(" new cache")
			}
			inputDetail += mutedStyle.Render(")")
		}
		sessCol.WriteString("  " + mutedStyle.Render("in") + "    " + inputDetail + "\n")
		outputDetail := compactCount(u.OutputTokens)
		if u.ReasoningOutputTokens > 0 {
			outputDetail += mutedStyle.Render(" (") +
				compactCount(u.ReasoningOutputTokens) + mutedStyle.Render(" reasoning") +
				mutedStyle.Render(")")
		}
		sessCol.WriteString("  " + mutedStyle.Render("out") + "   " + outputDetail + "\n")
	}

	// Combine two columns side by side
	wsLines := strings.Split(strings.TrimRight(wsCol.String(), "\n"), "\n")
	sessLines := strings.Split(strings.TrimRight(sessCol.String(), "\n"), "\n")
	maxLines := len(wsLines)
	if len(sessLines) > maxLines {
		maxLines = len(sessLines)
	}

	colWidth := detailColWidth
	for i := 0; i < maxLines; i++ {
		left := ""
		if i < len(wsLines) {
			left = wsLines[i]
		}
		right := ""
		if i < len(sessLines) {
			right = sessLines[i]
		}
		// Use lipgloss.Width to measure visible width (ignoring ANSI escapes)
		visibleWidth := lipgloss.Width(left)
		if visibleWidth < colWidth {
			left += strings.Repeat(" ", colWidth-visibleWidth)
		}
		b.WriteString("   " + left + right + "\n")
	}

	// Activity section
	const maxDisplayedActions = 5

	// Derive LLM actions from recent hook events when available.
	var llmActions []string
	if m.recentEventsTaskID == task.ID && len(m.recentEvents) > 0 {
		sessionID := ""
		turnID := ""
		if view != nil && view.HookSession != nil {
			sessionID = view.HookSession.SessionID
			turnID = view.HookSession.CurrentTurnID
		}
		llmActions = currentTurnLLMActions(m.recentEvents, sessionID, turnID, maxDisplayedActions)
	}

	// Fallback: use the HookSession summary when we have no event-sourced actions.
	var fallbackReplyText string
	if len(llmActions) == 0 && view != nil && view.HookSession != nil {
		fallbackReplyText = view.HookSession.LastAssistantMessage
		if fallbackReplyText == "" {
			fallbackReplyText = view.HookSession.LastCommandText
		}
	}

	hasActivity := false
	if view != nil && view.HookSession != nil {
		hasActivity = view.HookSession.LastPromptText != "" ||
			len(llmActions) > 0 || fallbackReplyText != ""
	}
	if hasActivity {
		totalWidth := m.width
		if totalWidth < 40 {
			totalWidth = 72
		}
		b.WriteString(dividerStyle.Render(strings.Repeat("─", totalWidth)) + "\n")
		b.WriteString("   " + headerLabelStyle.Render("ACTIVITY") + "\n")

		wrapWidth := totalWidth - 5 // 3 spaces margin + icon + space
		const maxActivityLines = 3

		// User prompt
		if view.HookSession.LastPromptText != "" {
			promptLines := wrapAndTruncate(
				view.HookSession.LastPromptText, wrapWidth, maxActivityLines,
			)
			iconSt := lipgloss.NewStyle().Foreground(colorUserPrompt)
			textSt := dimStyle
			if len(llmActions) == 0 && fallbackReplyText == "" {
				textSt = primaryStyle
			}
			for j, line := range promptLines {
				if j == 0 {
					b.WriteString("   " + iconSt.Render(iconUserPrompt) + " " +
						textSt.Render(line) + "\n")
				} else {
					b.WriteString("   " + "  " + textSt.Render(line) + "\n")
				}
			}
		}

		// LLM actions from recent events
		if len(llmActions) > 0 {
			if view.HookSession.LastPromptText != "" {
				b.WriteString("\n")
			}
			iconSt := lipgloss.NewStyle().Foreground(colorLLMReply)
			for i, actionText := range llmActions {
				actionLines := wrapAndTruncate(actionText, wrapWidth, maxActivityLines)
				textSt := dimStyle
				if i == len(llmActions)-1 {
					textSt = primaryStyle
				}
				for j, line := range actionLines {
					if j == 0 {
						b.WriteString("   " + iconSt.Render(iconLLMReply) + " " +
							textSt.Render(line) + "\n")
					} else {
						b.WriteString("   " + "  " + textSt.Render(line) + "\n")
					}
				}
			}
		} else if fallbackReplyText != "" {
			// Fallback: single LLM response from HookSession summary
			if view.HookSession.LastPromptText != "" {
				b.WriteString("\n")
			}
			replyLines := wrapAndTruncate(fallbackReplyText, wrapWidth, maxActivityLines)
			iconSt := lipgloss.NewStyle().Foreground(colorLLMReply)
			for j, line := range replyLines {
				if j == 0 {
					b.WriteString("   " + iconSt.Render(iconLLMReply) + " " +
						primaryStyle.Render(line) + "\n")
				} else {
					b.WriteString("   " + "  " + primaryStyle.Render(line) + "\n")
				}
			}
		}
	}

	return strings.TrimRight(b.String(), "\n")
}

// currentTurnLLMActions extracts up to maxActions LLM action texts from the
// current task session's current turn. Events are expected in latest-first
// order as returned by the query.
func currentTurnLLMActions(events []core.HookEvent, sessionID string, turnID string, maxActions int) []string {
	var actions []string
	for _, ev := range events {
		if sessionID != "" && ev.SessionID != "" && ev.SessionID != sessionID {
			continue
		}
		if turnID != "" && ev.TurnID != "" && ev.TurnID != turnID {
			if ev.EventName == "UserPromptSubmit" || ev.EventName == "SessionStart" {
				break
			}
			continue
		}
		if ev.EventName == "UserPromptSubmit" || ev.EventName == "SessionStart" {
			break // stop at the boundary of the current turn or session
		}
		var text string
		switch ev.EventName {
		case "Stop":
			text = ev.LastAssistantMessage
		case "PostToolUse":
			if ev.CommandText != "" {
				text = ev.CommandText
			}
		}
		if text != "" {
			actions = append(actions, text)
		}
	}
	// actions are in reverse order (latest first), reverse them
	for i, j := 0, len(actions)-1; i < j; i, j = i+1, j-1 {
		actions[i], actions[j] = actions[j], actions[i]
	}
	if len(actions) > maxActions {
		actions = actions[len(actions)-maxActions:]
	}
	return actions
}

func wrapAndTruncate(text string, width int, maxLines int) []string {
	if text == "" {
		return nil
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	var lines []string
	current := words[0]
	for _, word := range words[1:] {
		if len(current)+1+len(word) > width {
			lines = append(lines, current)
			current = word
		} else {
			current += " " + word
		}
	}
	lines = append(lines, current)
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		last := lines[maxLines-1]
		if len(last) > 3 {
			lines[maxLines-1] = last[:len(last)-3] + "..."
		}
	}
	return lines
}

func taskStateText(view *core.TaskView) (string, lipgloss.Style) {
	label := taskStateLabel(view)
	if label == "" {
		return "", dimStyle
	}

	icon, style := taskStateStyle(view)
	return strings.TrimSpace(icon + " " + label), style
}

func taskStateLabel(view *core.TaskView) string {
	if view == nil || view.Task == nil {
		return ""
	}

	if view.Observer != nil && view.Observer.DisplayStatus != "" {
		label := strings.ReplaceAll(string(view.Observer.DisplayStatus), "_", " ")
		if view.Observer.DisplayStatus == core.DisplayStatusWorking &&
			view.Observer.DisplayActivity == core.DisplayActivityCommand {
			label += " · " + string(core.DisplayActivityCommand)
		}
		return label
	}

	task := view.Task
	if task.RuntimeState != core.RuntimeStateNone {
		return strings.ReplaceAll(string(task.RuntimeState), "_", " ")
	}

	return string(task.Status)
}

func taskStateStyle(view *core.TaskView) (string, lipgloss.Style) {
	if view != nil && view.Observer != nil && view.Observer.DisplayStatus != "" {
		return displayStateStyle(string(view.Observer.DisplayStatus), string(view.Observer.DisplayActivity))
	}

	task := view.Task
	if task.RuntimeState != core.RuntimeStateNone {
		return runtimeStateStyle(string(task.RuntimeState))
	}

	return statusStyle(string(task.Status))
}

func formatElapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

func taskElapsed(view *core.TaskView) string {
	if view == nil {
		return ""
	}
	var started time.Time
	if view.Task != nil && view.Task.Provider == "codex" && !view.Task.CreatedAt.IsZero() {
		started = view.Task.CreatedAt
	}
	if view.HookSession != nil && !view.HookSession.StartedAt.IsZero() {
		if started.IsZero() {
			started = view.HookSession.StartedAt
		}
	}
	if started.IsZero() && view.Task != nil {
		started = view.Task.CreatedAt
	}
	if started.IsZero() {
		return ""
	}
	return formatElapsed(time.Since(started))
}

func taskTurnElapsed(view *core.TaskView) string {
	if view == nil || view.HookSession == nil {
		return ""
	}
	if view.HookSession.LastPromptSubmittedAt.IsZero() {
		return ""
	}
	return formatElapsed(time.Since(view.HookSession.LastPromptSubmittedAt))
}

func compactCount(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fm", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return strconv.Itoa(n)
	}
}

func (m model) promptInputView() string {
	totalWidth := m.width
	if totalWidth < 40 {
		totalWidth = 72
	}

	var b strings.Builder

	// Header
	b.WriteString(renderHeader(
		headerLabelStyle.Render("RIG"),
		mutedStyle.Render("new task"),
		totalWidth,
	) + "\n")
	b.WriteString(dividerStyle.Render(strings.Repeat("─", totalWidth)) + "\n")

	if m.err != nil {
		b.WriteString(errorStyle.Render("Error: "+m.err.Error()) + "\n\n")
	}

	// Instruction
	b.WriteString(dimStyle.Render("Enter task prompt. Tab to switch provider.") + "\n\n")

	// Provider toggle
	selected := providerStyle(m.provider).Render(m.provider)
	other := nextProvider(m.provider)
	unselected := mutedStyle.Render(other)
	b.WriteString(mutedStyle.Render("provider  ") + selected + mutedStyle.Render(" / ") + unselected + "\n\n")

	// Textarea
	b.WriteString(m.promptInput.View())

	// Shimmer during naming step
	if m.creationProgress == core.TaskProgressNaming {
		label := progressStepLabel(core.TaskProgressNaming)
		b.WriteString("\n\n" + warningStyle.Render("●") + " " + renderShimmer(label, m.shimmerTick))
	}

	b.WriteString("\n\n")
	b.WriteString(
		dimStyle.Render("enter") + mutedStyle.Render(" submit · ") +
			dimStyle.Render("esc") + mutedStyle.Render(" cancel"),
	)

	return b.String()
}

func (m model) nameConfirmView() string {
	totalWidth := m.width
	if totalWidth < 40 {
		totalWidth = 72
	}

	var b strings.Builder

	// Header
	b.WriteString(renderHeader(
		headerLabelStyle.Render("RIG"),
		mutedStyle.Render("new task"),
		totalWidth,
	) + "\n")
	b.WriteString(dividerStyle.Render(strings.Repeat("─", totalWidth)) + "\n")

	if m.err != nil {
		b.WriteString(errorStyle.Render("Error: "+m.err.Error()) + "\n\n")
	}

	// Checkmark recap: completed prompt and provider.
	b.WriteString(healthyStyle.Render(iconCheckmark) + " " + mutedStyle.Render(m.createInput.Prompt) + "\n")
	b.WriteString(
		healthyStyle.Render(iconCheckmark) + " " +
			mutedStyle.Render("provider: ") + providerStyle(m.provider).Render(m.provider) + "\n",
	)

	if m.busy && len(m.creationSteps) > 0 {
		// Name is confirmed — show it as a completed step.
		b.WriteString(healthyStyle.Render(iconCheckmark) + " " + mutedStyle.Render("name: "+m.nameInput.Value()) + "\n")
		b.WriteString("\n")

		// Render completed creation steps and active shimmer step.
		for i, label := range m.creationSteps {
			if i == len(m.creationSteps)-1 && m.creationProgress != core.TaskProgressTaskCreated {
				// Active (last) step gets shimmer.
				b.WriteString(warningStyle.Render("●") + " " + renderShimmer(label, m.shimmerTick) + "\n")
			} else {
				// Completed steps get checkmarks.
				b.WriteString(healthyStyle.Render(iconCheckmark) + " " + mutedStyle.Render(label) + "\n")
			}
		}
	} else {
		// Name input is active — show editable input.
		b.WriteString("\n")
		b.WriteString(warningStyle.Render("▸ Name: ") + m.nameInput.View() + "\n")
		b.WriteString("\n")
		b.WriteString(
			dimStyle.Render("enter") + mutedStyle.Render(" create · ") +
				dimStyle.Render("esc") + mutedStyle.Render(" cancel"),
		)
	}

	return b.String()
}

func (m model) confirmationView() string {
	task := m.selectedTask()
	if task == nil {
		return dimStyle.Render("No task selected.")
	}

	totalWidth := m.width
	if totalWidth < 40 {
		totalWidth = 72
	}

	var b strings.Builder

	// Header
	b.WriteString(renderHeader(
		headerLabelStyle.Render("RIG"),
		errorStyle.Render("cleanup"),
		totalWidth,
	) + "\n")
	b.WriteString(dividerStyle.Render(strings.Repeat("─", totalWidth)) + "\n")

	// Task name
	b.WriteString(primaryStyle.Render(task.DisplayName) + "\n\n")

	// Explanation
	b.WriteString(dimStyle.Render("The tmux session and worktree will be deleted.") + "\n")
	b.WriteString(dimStyle.Render("The branch will be kept.") + "\n\n")

	// Keybinds
	b.WriteString(
		dimStyle.Render("y") + mutedStyle.Render(" confirm · ") +
			dimStyle.Render("n") + mutedStyle.Render(" cancel"),
	)
	return b.String()
}

func (m model) selectedTask() *core.Task {
	view := m.selectedTaskView()
	if view == nil {
		return nil
	}
	return view.Task
}

func (m model) selectedTaskView() *core.TaskView {
	rows := m.visibleTaskViews()
	if len(rows) == 0 {
		return nil
	}

	if m.selected < 0 {
		return rows[0]
	}

	if m.selected >= len(rows) {
		return rows[len(rows)-1]
	}

	return rows[m.selected]
}

func (m model) taskViewAt(index int) *core.TaskView {
	rows := m.visibleTaskViews()
	if index < 0 || index >= len(rows) {
		return nil
	}

	return rows[index]
}

func (m *model) selectTaskByIDOrSlug(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}

	for i, view := range m.taskViews {
		if view == nil || view.Task == nil {
			continue
		}
		if strings.TrimSpace(selectedIDOrSlug(view.Task)) == key {
			m.selected = i
			return true
		}
	}

	if synthetic := m.syntheticCreationTaskView(); synthetic != nil && synthetic.Task != nil &&
		strings.TrimSpace(selectedIDOrSlug(synthetic.Task)) == key {
		m.selected = m.syntheticCreationRowIndex()
		return true
	}

	return false
}

func (m *model) replaceTask(updated *core.Task) {
	for i, task := range m.tasks {
		if selectedIDOrSlug(task) == selectedIDOrSlug(updated) {
			if !isVisibleTask(updated) {
				m.tasks = append(m.tasks[:i], m.tasks[i+1:]...)
				if i < len(m.taskViews) {
					m.taskViews = append(m.taskViews[:i], m.taskViews[i+1:]...)
				}
				if m.selected >= len(m.tasks) && len(m.tasks) > 0 {
					m.selected = len(m.tasks) - 1
				}
				return
			}

			m.tasks[i] = updated
			if i < len(m.taskViews) {
				m.taskViews[i] = &core.TaskView{Task: updated}
			}
			return
		}
	}
}

func (m *model) upsertTask(updated *core.Task) {
	for i, task := range m.tasks {
		if selectedIDOrSlug(task) == selectedIDOrSlug(updated) {
			if !isVisibleTask(updated) {
				m.tasks = append(m.tasks[:i], m.tasks[i+1:]...)
				if i < len(m.taskViews) {
					m.taskViews = append(m.taskViews[:i], m.taskViews[i+1:]...)
				}
				if m.selected >= len(m.tasks) && len(m.tasks) > 0 {
					m.selected = len(m.tasks) - 1
				}
				return
			}

			m.tasks[i] = updated
			if i < len(m.taskViews) {
				m.taskViews[i] = &core.TaskView{Task: updated}
			}
			return
		}
	}

	if isVisibleTask(updated) {
		m.tasks = append(m.tasks, updated)
		m.taskViews = append(m.taskViews, &core.TaskView{Task: updated})
		m.selected = len(m.tasks) - 1
	}
}

func (m *model) cleanupSubscriptions() {
	if m.unsubscribeHooks != nil {
		m.unsubscribeHooks()
		m.unsubscribeHooks = nil
	}
	m.hookUpdates = nil
	if m.unsubscribeUpdates != nil {
		m.unsubscribeUpdates()
		m.unsubscribeUpdates = nil
	}
	m.observerUpdates = nil
}

func (m *model) applyHookSessionUpdate(update core.HookSessionSummary) {
	if strings.TrimSpace(update.TaskID) == "" {
		return
	}

	for _, view := range m.taskViews {
		if view == nil || view.Task == nil || view.Task.ID != update.TaskID {
			continue
		}

		copySummary := update
		view.HookSession = &copySummary
		return
	}
}

func (m *model) applyObserverTaskUpdate(update core.ObserverTaskUpdate) {
	if strings.TrimSpace(update.TaskID) == "" {
		return
	}

	for _, view := range m.taskViews {
		if view == nil || view.Task == nil || view.Task.ID != update.TaskID {
			continue
		}

		copySummary := &core.ObserverSummary{
			TaskID:                update.TaskID,
			DisplayStatus:         update.DisplayStatus,
			DisplayActivity:       update.DisplayActivity,
			LastRuntimeObservedAt: update.LastActivityAt,
			ProcessAlive:          update.DisplayStatus != core.DisplayStatusDisconnected,
		}
		view.Observer = copySummary
		if update.HookSession != nil {
			hookCopy := *update.HookSession
			view.HookSession = &hookCopy
		}
		return
	}
}

func nextProvider(current string) string {
	for i, p := range availableProviders {
		if p == current {
			return availableProviders[(i+1)%len(availableProviders)]
		}
	}

	return availableProviders[0]
}

func providerStyle(provider string) lipgloss.Style {
	switch provider {
	case "claude":
		return claudeStyle
	default:
		return codexStyle
	}
}

func isVisibleTask(task *core.Task) bool {
	return task != nil && (task.SessionExists || task.WorktreeExists)
}

func (m model) fetchRecentEventsForSelected() tea.Cmd {
	task := m.selectedTask()
	if task == nil || strings.TrimSpace(task.ID) == "" {
		return nil
	}
	return fetchRecentEventsCmd(m.service, task.ID)
}

func (m model) visibleTaskViews() []*core.TaskView {
	rows := append([]*core.TaskView{}, m.taskViews...)
	if view := m.syntheticCreationTaskView(); view != nil {
		rows = append(rows, view)
	}
	return rows
}

func (m model) visibleRowCount() int {
	return len(m.visibleTaskViews())
}

func (m model) syntheticCreationTaskView() *core.TaskView {
	if !m.hasSyntheticCreationRow() {
		return nil
	}

	task := cloneTaskSnapshot(m.creationTask)
	if task == nil {
		task = &core.Task{}
	}
	if syntheticTaskIsPersisted(task, m.taskViews) {
		return nil
	}

	if strings.TrimSpace(task.DisplayName) == "" {
		task.DisplayName = syntheticCreationTitle
	}
	if strings.TrimSpace(task.Provider) == "" {
		task.Provider = emptyFallback(m.createInput.Provider, m.provider)
	}
	if strings.TrimSpace(task.Prompt) == "" {
		task.Prompt = m.createInput.Prompt
	}
	task.Status = core.TaskStatusCreating
	if m.creationFailed {
		task.Status = core.TaskStatusBroken
	}
	task.RuntimeState = core.RuntimeStateNone

	return &core.TaskView{Task: task}
}

func (m model) syntheticCreationRowIndex() int {
	return len(m.taskViews)
}

func (m model) isSyntheticCreationRowSelected() bool {
	return m.syntheticCreationTaskView() != nil && m.selected >= len(m.taskViews)
}

func (m model) syntheticCreationDetailView() string {
	totalWidth := m.width
	if totalWidth < 40 {
		totalWidth = 72
	}

	provider := emptyFallback(m.createInput.Provider, m.provider)

	var b strings.Builder
	b.WriteString("   " + headerLabelStyle.Render("CREATION") + "\n")
	if strings.TrimSpace(m.createInput.Prompt) != "" {
		b.WriteString("   " + mutedStyle.Render("prompt") + "   " +
			primaryStyle.Render(m.createInput.Prompt) + "\n")
	}
	b.WriteString("   " + mutedStyle.Render("provider") + " " +
		providerStyle(provider).Render(provider) + "\n")

	if len(m.creationSteps) > 0 {
		b.WriteString("\n")
		for i, label := range m.creationSteps {
			if i == len(m.creationSteps)-1 && m.creationProgress != "" &&
				m.creationProgress != core.TaskProgressTaskCreated {
				b.WriteString("   " + warningStyle.Render("●") + " " +
					renderShimmer(label, m.shimmerTick) + "\n")
				continue
			}
			b.WriteString("   " + healthyStyle.Render(iconCheckmark) + " " +
				mutedStyle.Render(label) + "\n")
		}
	}

	if m.err != nil {
		b.WriteString("\n")
		b.WriteString("   " + errorStyle.Render("Error: "+m.err.Error()))
	}

	if b.Len() == 0 {
		return ""
	}

	return strings.TrimRight(b.String(), "\n")
}

func (m *model) markCreationFailed() {
	m.createInFlight = false
	m.creationFailed = true
	m.creationProgress = ""
	m.shimmerTick = 0
	m.mode = tuiModeList
	m.selected = m.syntheticCreationRowIndex()
}

func (m model) hasSyntheticCreationRow() bool {
	return m.createInFlight || m.creationFailed
}

func (m *model) blockedByActiveCreation(errMsg string) bool {
	if !m.createInFlight {
		return false
	}
	if strings.TrimSpace(errMsg) != "" {
		m.err = errors.New(errMsg)
	}
	return true
}

func (m model) syntheticCreationRowActionError() error {
	if m.createInFlight {
		return fmt.Errorf("Task is still being created")
	}
	return fmt.Errorf("Task creation failed")
}

func (m *model) nextRefreshTasksCmd() tea.Cmd {
	m.tasksRequestSeq++
	return refreshTasksCmd(m.service, m.tasksRequestSeq)
}

func fetchPRStatusCmd(service TaskService, taskID, repoRoot, branch string) tea.Cmd {
	return safeCmd("fetchPRStatusCmd", func() tea.Msg {
		status, err := service.GetPRStatus(context.Background(), repoRoot, branch)
		if err != nil {
			return prStatusLoadedMsg{taskID: taskID, status: &core.PRStatus{State: core.PRStateNone}}
		}
		return prStatusLoadedMsg{taskID: taskID, status: status}
	})
}

func fetchRecentEventsCmd(service TaskService, taskID string) tea.Cmd {
	return safeCmd("fetchRecentEventsCmd", func() tea.Msg {
		events, err := service.GetTaskHookEvents(context.Background(), taskID, 20)
		if err != nil {
			return recentEventsMsg{taskID: taskID}
		}
		return recentEventsMsg{taskID: taskID, events: events}
	})
}

func refreshTasksCmd(service TaskService, requestID int) tea.Cmd {
	return safeCmd("refreshTasksCmd", func() tea.Msg {
		views, err := service.ListTaskViews(context.Background())
		return tasksLoadedMsg{requestID: requestID, views: views, err: err}
	})
}

func filterVisibleTaskViews(views []*core.TaskView) []*core.TaskView {
	filtered := make([]*core.TaskView, 0, len(views))
	for _, view := range views {
		if view != nil && isVisibleTask(view.Task) {
			filtered = append(filtered, view)
		}
	}

	return filtered
}

func taskViewsToTasks(views []*core.TaskView) []*core.Task {
	tasks := make([]*core.Task, 0, len(views))
	for _, view := range views {
		if view != nil && view.Task != nil {
			tasks = append(tasks, view.Task)
		}
	}

	return tasks
}

func cleanupTaskCmd(service TaskService, idOrSlug string) tea.Cmd {
	return safeCmd("cleanupTaskCmd", func() tea.Msg {
		task, err := service.DeleteTaskResources(context.Background(), idOrSlug)
		return cleanupFinishedMsg{task: task, err: err}
	})
}

func openTaskCmd(service TaskService, idOrSlug string) tea.Cmd {
	return safeCmd("openTaskCmd", func() tea.Msg {
		return openFinishedMsg{err: service.OpenTask(context.Background(), idOrSlug)}
	})
}

func subscribeObserverUpdatesCmd(socketPath string) tea.Cmd {
	return safeCmd("subscribeObserverUpdatesCmd", func() tea.Msg {
		if strings.TrimSpace(socketPath) == "" {
			return observerSubscriptionReadyMsg{}
		}

		updates, cleanup, err := observer.Subscribe(context.Background(), socketPath)
		return observerSubscriptionReadyMsg{updates: updates, cleanup: cleanup, err: err}
	})
}

func subscribeHookUpdatesCmd(service TaskService) tea.Cmd {
	return func() tea.Msg {
		updates, cleanup, err := service.SubscribeTaskHookUpdates(context.Background())
		return hookSubscriptionReadyMsg{updates: updates, cleanup: cleanup, err: err}
	}
}

func waitForHookUpdateCmd(updates <-chan core.HookSessionSummary) tea.Cmd {
	if updates == nil {
		return nil
	}

	return func() tea.Msg {
		update, ok := <-updates
		if !ok {
			return hookSubscriptionClosedMsg{}
		}
		return hookTaskUpdatedMsg{update: update}
	}
}

func waitForObserverUpdateCmd(updates <-chan core.ObserverTaskUpdate) tea.Cmd {
	if updates == nil {
		return nil
	}

	return safeCmd("waitForObserverUpdateCmd", func() tea.Msg {
		update, ok := <-updates
		if !ok {
			return observerSubscriptionClosedMsg{}
		}
		return observerTaskUpdatedMsg{update: update}
	})
}

func suggestTaskNameCmd(service TaskService, prompt string, provider string) tea.Cmd {
	return safeCmd("suggestTaskNameCmd", func() tea.Msg {
		suggestion, err := service.SuggestTaskName(context.Background(), prompt, provider)
		return suggestNameFinishedMsg{prompt: prompt, suggestion: suggestion, err: err}
	})
}

func createTaskCmd(service TaskService, input core.NewTaskInput) (<-chan taskProgressMsg, tea.Cmd) {
	progressCh := make(chan taskProgressMsg, 8)

	cmd := func() (msg tea.Msg) {
		defer close(progressCh)
		defer func() {
			if r := recover(); r != nil {
				msg = asyncErrMsg{err: fmt.Errorf("createTaskCmd panicked: %v", r)}
			}
		}()

		task, err := service.CreateTaskWithProgress(
			context.Background(),
			input,
			core.CreateTaskOptions{OpenSession: false},
			func(p core.TaskProgress) {
				progressCh <- taskProgressMsg{
					step:    p.Step,
					message: p.Message,
					task:    cloneTaskSnapshot(p.Task),
				}
			},
		)
		return createFinishedMsg{task: task, err: err}
	}

	return progressCh, cmd
}

func waitForProgressCmd(ch <-chan taskProgressMsg) tea.Cmd {
	if ch == nil {
		return nil
	}
	return safeCmd("waitForProgressCmd", func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	})
}

func safeCmd(name string, fn func() tea.Msg) tea.Cmd {
	return func() (msg tea.Msg) {
		defer func() {
			if r := recover(); r != nil {
				msg = asyncErrMsg{err: fmt.Errorf("%s panicked: %v", name, r)}
			}
		}()
		return fn()
	}
}

func selectedIDOrSlug(task *core.Task) string {
	if strings.TrimSpace(task.Slug) != "" {
		return task.Slug
	}

	return task.ID
}

func (m model) creationCwd() string {
	if m.isSyntheticCreationRowSelected() {
		if cwd := strings.TrimSpace(m.createInput.Cwd); cwd != "" {
			return cwd
		}
		if m.creationTask != nil && strings.TrimSpace(m.creationTask.RepoRoot) != "" {
			return m.creationTask.RepoRoot
		}
	}

	task := m.selectedTask()
	if task != nil && strings.TrimSpace(task.RepoRoot) != "" {
		return task.RepoRoot
	}

	return m.defaultCreationCwd
}

func emptyFallback(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}

	return value
}

func progressStepLabel(step core.TaskProgressStep) string {
	switch step {
	case core.TaskProgressNaming:
		return "Suggesting name..."
	case core.TaskProgressWorktreeCreating:
		return "Creating worktree..."
	case core.TaskProgressWorkspaceSeeding:
		return "Seeding workspace..."
	case core.TaskProgressSetupScriptRunning:
		return "Running setup script..."
	case core.TaskProgressTmuxStarting:
		return "Starting session..."
	case core.TaskProgressAgentLaunching:
		return "Launching agent..."
	case core.TaskProgressTaskCreated:
		return "Task created"
	default:
		return ""
	}
}

func cloneTaskSnapshot(task *core.Task) *core.Task {
	if task == nil {
		return nil
	}

	cloned := *task
	return &cloned
}

func syntheticTaskIsPersisted(task *core.Task, views []*core.TaskView) bool {
	if task == nil {
		return false
	}

	key := strings.TrimSpace(selectedIDOrSlug(task))
	if key == "" {
		return false
	}

	for _, view := range views {
		if view == nil || view.Task == nil {
			continue
		}
		if strings.TrimSpace(selectedIDOrSlug(view.Task)) == key {
			return true
		}
	}

	return false
}
