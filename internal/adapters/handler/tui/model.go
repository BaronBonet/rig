package tui

import (
	"context"
	"errors"
	"path/filepath"
	"rig/internal/core"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
)

type taskRow struct {
	task        *core.Task
	activity    []core.TaskActivityEvent
	status      *core.TaskStatusUpdate
	pullRequest *core.PRStatus
}

type modelMode int

const (
	modeBrowse modelMode = iota
	modePromptInput
	modePRPicker
	modeCleanupConfirm
)

const defaultCreateProvider = core.ProviderCodex

const taskActivityPreviewLimit = 6

// nolint:recvcheck // bubbletea requires value receivers for tea.Model.
type model struct {
	frontend      core.TaskFrontend
	statusContext context.Context
	err           error
	createErr     error
	cancelStatus  context.CancelFunc
	rows          []taskRow
	prRows        []core.RepoPullRequest
	prompt        string
	promptInput   textarea.Model
	createActive  core.TaskCreateProgressStep
	createDone    []core.TaskCreateProgressStep
	selected      int
	prSelected    int
	width         int
	shimmerTick   int
	mode          modelMode
	launchCwd     string
	prRepoRoot    string
	prRepoName    string
	loading       bool
	createPending bool
	createFromPR  bool
	deletePending bool
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

type shimmerTickMsg struct{}

func newModel(frontend core.TaskFrontend) model {
	return newModelWithLaunchCwd(frontend, "")
}

func newModelWithLaunchCwd(frontend core.TaskFrontend, launchCwd string) model {
	statusContext, cancelStatus := context.WithCancel(context.Background())

	return model{
		frontend:      frontend,
		statusContext: statusContext,
		cancelStatus:  cancelStatus,
		launchCwd:     strings.TrimSpace(launchCwd),
		promptInput:   newPromptInput(),
		loading:       true,
		mode:          modeBrowse,
	}
}

func (m model) Init() tea.Cmd {
	return loadTasksCmd(m.statusContext, m.frontend)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil
	case tea.PasteMsg:
		return m.updatePromptPaste(msg)
	case tea.KeyPressMsg:
		if isQuitKey(msg) {
			if m.cancelStatus != nil {
				m.cancelStatus()
			}
			return m, tea.Quit
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

		switch msg.String() {
		case "esc":
			if m.cancelStatus != nil {
				m.cancelStatus()
			}
			return m, tea.Quit
		case "a", "n":
			if m.createPending {
				return m, nil
			}
			return m.enterPromptInputMode("")
		case "r":
			m.loading = true
			m.err = nil
			return m, loadTasksCmd(m.statusContext, m.frontend)
		case "g", "home":
			m.selected = 0
		case "G", "end":
			m.selected = len(m.rows) - 1
		case "j", "down":
			m.moveSelection(1)
		case "k", "up":
			m.moveSelection(-1)
		case "x":
			if len(m.rows) == 0 {
				return m, nil
			}
			m.mode = modeCleanupConfirm
			return m, nil
		case "enter":
			if len(m.rows) == 0 {
				return m, nil
			}
			row := m.selectedRow()
			if row == nil || row.task == nil {
				return m, nil
			}
			m.err = nil
			return m, openTaskSessionCmd(m.statusContext, m.frontend, row.task)
		}
		m.clampSelection()
		return m, nil
	case tasksLoadedMsg:
		m.loading = false
		m.err = msg.err
		if msg.err != nil {
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
	case taskStatusSubscriptionReadyMsg:
		if msg.err != nil {
			return m, nil
		}
		return m, waitForTaskStatusCmd(msg.taskID, msg.updates)
	case taskStatusUpdatedMsg:
		update := msg.update
		m.setTaskStatus(msg.taskID, &update)
		return m, tea.Batch(
			taskActivityCmd(m.statusContext, m.frontend, msg.taskID, taskActivityPreviewLimit),
			waitForTaskStatusCmd(msg.taskID, msg.updates),
		)
	case taskStatusSubscriptionClosedMsg:
		return m, nil
	case repoPullRequestsLoadedMsg:
		m.createErr = msg.err
		if msg.err != nil {
			m.prRows = nil
			return m, nil
		}
		m.prRepoRoot = msg.repoRoot
		m.prRepoName = msg.repoName
		m.prRows = append([]core.RepoPullRequest(nil), msg.prs...)
		m.clampPRSelection()
		return m, nil
	case taskCreatedMsg:
		m.createPending = false
		m.shimmerTick = 0
		if msg.err != nil {
			m.createErr = msg.err
			return m, nil
		}

		m.mode = modeBrowse
		m.prompt = ""
		m.promptInput = newPromptInput()
		m.createErr = nil
		m.createFromPR = false
		index := m.upsertTaskRow(msg.task)
		if index >= 0 {
			m.selected = index
		}
		m.resetCreateProgress()
		m.clampSelection()
		cmds := []tea.Cmd{loadTasksCmd(m.statusContext, m.frontend)}
		cmds = append(cmds, m.taskStatusTrackingCmds(taskID(msg.task))...)
		return m, tea.Batch(cmds...)
	case taskCreateStreamStartFailedMsg:
		m.createPending = false
		m.shimmerTick = 0
		m.createErr = msg.err
		return m, nil
	case taskCreateEventMsg:
		switch {
		case msg.event.Progress != nil:
			m.advanceCreateProgress(msg.event.Progress.Step)
			return m, waitForTaskCreateEventCmd(msg.events)
		case msg.event.Task != nil:
			return m.Update(taskCreatedMsg{task: msg.event.Task})
		case msg.event.Err != nil:
			m.createPending = false
			m.shimmerTick = 0
			m.createErr = msg.event.Err
			return m, nil
		default:
			return m, waitForTaskCreateEventCmd(msg.events)
		}
	case taskCreateStreamClosedMsg:
		if !m.createPending {
			return m, nil
		}
		m.createPending = false
		m.shimmerTick = 0
		if m.createErr == nil {
			m.createErr = errors.New("create task stream closed unexpectedly")
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
		m.deletePending = false
		m.shimmerTick = 0
		m.mode = modeBrowse
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}

		m.err = nil
		m.removeTaskRow(msg.taskID)
		m.clampSelection()
		return m, nil
	case shimmerTickMsg:
		if !m.createPending && !m.deletePending {
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
	body := m.listView()
	switch m.mode {
	case modePromptInput:
		body = m.promptInputView()
	case modePRPicker:
		body = m.prPickerView()
	case modeCleanupConfirm:
		body = m.confirmationView()
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

func (m model) afterTasksLoadedCmds() []tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(m.rows)*4)
	for _, row := range m.rows {
		taskID := taskID(row.task)
		cmds = append(cmds, m.taskStatusTrackingCmds(taskID)...)
		if taskID != "" {
			cmds = append(cmds, taskActivityCmd(m.statusContext, m.frontend, taskID, taskActivityPreviewLimit))
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

	m.mode = modeBrowse
	m.createPending = true
	m.createFromPR = false
	m.createErr = nil
	m.shimmerTick = 0
	m.resetCreateProgress()
	m.promptInput.Blur()

	return m, tea.Batch(
		createTaskStreamCmd(m.statusContext, m.frontend, core.CreateTaskInput{
			Cwd:      m.currentCreateCwd(),
			Prompt:   prompt,
			Provider: defaultCreateProvider,
		}),
		shimmerTickCmd(),
	)
}

func (m model) updatePromptInput(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.createPending {
		return m, nil
	}
	m.ensurePromptInputInitialized()
	if m.promptInput.Value() != m.prompt {
		m.promptInput.SetValue(m.prompt)
	}
	var focusCmd tea.Cmd
	if !m.promptInput.Focused() {
		focusCmd = m.promptInput.Focus()
	}

	switch typed := msg.(type) {
	case tea.KeyPressMsg:
		if typed.String() == "ctrl+p" {
			repoRoot, repoName, ok := m.currentRepoScope()
			if !ok {
				m.createErr = errors.New("repo scope unavailable")
				return m, nil
			}

			m.mode = modePRPicker
			m.prRepoRoot = repoRoot
			m.prRepoName = repoName
			m.prRows = nil
			m.prSelected = 0
			m.createErr = nil
			m.promptInput.Blur()
			return m, listRepoPullRequestsCmd(m.statusContext, m.frontend, repoRoot, repoName)
		}

		switch typed.Key().Code {
		case tea.KeyEscape:
			m.mode = modeBrowse
			m.prompt = ""
			m.promptInput = newPromptInput()
			m.createErr = nil
			m.resetCreateProgress()
			return m, nil
		case tea.KeyEnter:
			return m.submitPrompt()
		}
	}

	previousValue := m.promptInput.Value()
	updatedInput, cmd := m.promptInput.Update(msg)
	m.promptInput = updatedInput
	m.prompt = m.promptInput.Value()
	if m.prompt != previousValue {
		m.createErr = nil
	}

	if focusCmd != nil {
		return m, tea.Batch(focusCmd, cmd)
	}
	return m, cmd
}

func (m model) updatePromptPaste(msg tea.PasteMsg) (tea.Model, tea.Cmd) {
	if m.mode != modePromptInput || m.createPending {
		return m, nil
	}
	return m.updatePromptInput(msg)
}

func (m model) updatePRPicker(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.createPending {
		return m, nil
	}

	switch msg.String() {
	case "esc":
		m.mode = modePromptInput
		m.createErr = nil
		return m, m.promptInput.Focus()
	case "g", "home":
		m.prSelected = 0
		return m, nil
	case "G", "end":
		m.prSelected = len(m.prRows) - 1
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
			m.createErr = errors.New("PR already has workspace")
			return m, nil
		}

		m.mode = modeBrowse
		m.createPending = true
		m.createFromPR = true
		m.createErr = nil
		m.shimmerTick = 0
		m.resetCreateProgress()

		selected := *pr
		return m, tea.Batch(
			createTaskStreamCmd(m.statusContext, m.frontend, core.CreateTaskInput{
				Cwd:      m.prRepoRoot,
				Provider: defaultCreateProvider,
				Source: core.CreateTaskSource{
					PullRequest: &selected,
				},
			}),
			shimmerTickCmd(),
		)
	default:
		return m, nil
	}
}

func (m model) enterPromptInputMode(initialValue string) (tea.Model, tea.Cmd) {
	m.mode = modePromptInput
	m.prompt = initialValue
	m.promptInput = newPromptInput()
	m.promptInput.SetValue(initialValue)
	m.createErr = nil
	m.createPending = false
	m.createFromPR = false
	return m, m.promptInput.Focus()
}

func (m model) promptValue() string {
	m.ensurePromptInputInitialized()
	if value := m.promptInput.Value(); value != "" || m.prompt == "" {
		return value
	}
	return m.prompt
}

func (m *model) ensurePromptInputInitialized() {
	if m.promptInput.MaxHeight != 0 {
		return
	}
	m.promptInput = newPromptInput()
}

func (m model) updateCleanupConfirm(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.deletePending {
		return m, nil
	}

	switch msg.String() {
	case "q", "n", "esc":
		m.mode = modeBrowse
		return m, nil
	case "y":
		row := m.selectedRow()
		if row == nil || row.task == nil {
			m.mode = modeBrowse
			return m, nil
		}
		m.deletePending = true
		m.err = nil
		m.shimmerTick = 0
		return m, tea.Batch(
			deleteTaskCmd(m.statusContext, m.frontend, taskID(row.task)),
			shimmerTickCmd(),
		)
	default:
		return m, nil
	}
}

func (m model) taskStatusTrackingCmds(taskID string) []tea.Cmd {
	if strings.TrimSpace(taskID) == "" {
		return nil
	}

	return []tea.Cmd{
		latestTaskStatusCmd(m.statusContext, m.frontend, taskID),
		subscribeTaskStatusCmd(m.statusContext, m.frontend, taskID),
	}
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

func (m *model) moveSelection(delta int) {
	if len(m.rows) == 0 {
		m.selected = 0
		return
	}

	m.selected += delta
	m.clampSelection()
}

func (m *model) clampSelection() {
	if len(m.rows) == 0 {
		m.selected = 0
		return
	}
	if m.selected < 0 {
		m.selected = 0
		return
	}
	if m.selected >= len(m.rows) {
		m.selected = len(m.rows) - 1
	}
}

func (m *model) movePRSelection(delta int) {
	if len(m.prRows) == 0 {
		m.prSelected = 0
		return
	}

	m.prSelected += delta
	m.clampPRSelection()
}

func (m *model) clampPRSelection() {
	if len(m.prRows) == 0 {
		m.prSelected = 0
		return
	}
	if m.prSelected < 0 {
		m.prSelected = 0
		return
	}
	if m.prSelected >= len(m.prRows) {
		m.prSelected = len(m.prRows) - 1
	}
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

func (m *model) setTaskStatus(taskID string, status *core.TaskStatusUpdate) {
	for i := range m.rows {
		if m.rows[i].task == nil || m.rows[i].task.ID != taskID {
			continue
		}
		m.rows[i].status = status
		return
	}
}

func (m *model) setTaskPullRequestStatus(taskID string, status *core.PRStatus) {
	for i := range m.rows {
		if m.rows[i].task == nil || m.rows[i].task.ID != taskID {
			continue
		}
		m.rows[i].pullRequest = status
		return
	}
}

func (m *model) setTaskActivity(taskID string, activity []core.TaskActivityEvent) {
	for i := range m.rows {
		if m.rows[i].task == nil || m.rows[i].task.ID != taskID {
			continue
		}
		m.rows[i].activity = append([]core.TaskActivityEvent(nil), activity...)
		return
	}
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

func (m *model) resetCreateProgress() {
	m.createActive = ""
	m.createDone = nil
}

func (m *model) advanceCreateProgress(step core.TaskCreateProgressStep) {
	if step == "" {
		return
	}
	if m.createActive != "" && m.createActive != step && !containsCreateStep(m.createDone, m.createActive) {
		m.createDone = append(m.createDone, m.createActive)
	}
	m.createActive = step
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
	if len(m.prRows) == 0 || m.prSelected < 0 || m.prSelected >= len(m.prRows) {
		return nil
	}

	return &m.prRows[m.prSelected]
}

func (m model) totalWidth() int {
	if m.width >= 40 {
		return m.width
	}
	return 72
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
