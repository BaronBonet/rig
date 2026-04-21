package tui

import (
	"context"
	"strings"

	"rig/internal/core"

	tea "charm.land/bubbletea/v2"
)

type taskRow struct {
	task   *core.Task
	status *core.TaskStatusUpdate
}

type modelMode int

const (
	modeBrowse modelMode = iota
	modePromptInput
)

const defaultCreateProvider = core.AgentProviderCodex

// nolint:recvcheck // bubbletea requires value receivers for tea.Model.
type model struct {
	frontend      core.TaskFrontend
	statusContext context.Context
	cancelStatus  context.CancelFunc
	rows          []taskRow
	selected      int
	loading       bool
	err           error
	mode          modelMode
	prompt        string
	createPending bool
	createErr     error
}

type tasksLoadedMsg struct {
	tasks []*core.Task
	err   error
}

type latestTaskStatusLoadedMsg struct {
	taskID string
	status *core.TaskStatusUpdate
	err    error
}

type taskStatusSubscriptionReadyMsg struct {
	taskID  string
	updates <-chan core.TaskStatusUpdate
	err     error
}

type taskStatusUpdatedMsg struct {
	taskID  string
	update  core.TaskStatusUpdate
	updates <-chan core.TaskStatusUpdate
}

type taskStatusSubscriptionClosedMsg struct {
	taskID string
}

type taskCreatedMsg struct {
	task *core.Task
	err  error
}

func newModel(frontend core.TaskFrontend) model {
	statusContext, cancelStatus := context.WithCancel(context.Background())

	return model{
		frontend:      frontend,
		statusContext: statusContext,
		cancelStatus:  cancelStatus,
		loading:       true,
		mode:          modeBrowse,
	}
}

func (m model) Init() tea.Cmd {
	return loadTasksCmd(m.statusContext, m.frontend)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
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

		switch msg.String() {
		case "esc":
			if m.cancelStatus != nil {
				m.cancelStatus()
			}
			return m, tea.Quit
		case "a":
			m.mode = modePromptInput
			m.prompt = ""
			m.createErr = nil
			m.createPending = false
			return m, nil
		case "j", "down":
			m.moveSelection(1)
		case "k", "up":
			m.moveSelection(-1)
		}
		return m, nil
	case tasksLoadedMsg:
		m.loading = false
		m.err = msg.err
		if msg.err != nil {
			return m, nil
		}

		m.rows = rowsFromTasks(msg.tasks)
		m.clampSelection()
		return m, tea.Batch(m.afterTasksLoadedCmds()...)
	case latestTaskStatusLoadedMsg:
		if msg.err != nil {
			return m, nil
		}
		m.setTaskStatus(msg.taskID, msg.status)
		return m, nil
	case taskStatusSubscriptionReadyMsg:
		if msg.err != nil {
			return m, nil
		}
		return m, waitForTaskStatusCmd(msg.taskID, msg.updates)
	case taskStatusUpdatedMsg:
		update := msg.update
		m.setTaskStatus(msg.taskID, &update)
		return m, waitForTaskStatusCmd(msg.taskID, msg.updates)
	case taskStatusSubscriptionClosedMsg:
		return m, nil
	case taskCreatedMsg:
		m.createPending = false
		if msg.err != nil {
			m.createErr = msg.err
			return m, nil
		}

		m.mode = modeBrowse
		m.prompt = ""
		m.createErr = nil
		index := m.upsertTaskRow(msg.task)
		if index >= 0 {
			m.selected = index
		}
		m.clampSelection()
		return m, tea.Batch(m.taskStatusTrackingCmds(taskID(msg.task))...)
	default:
		return m, nil
	}
}

func (m model) View() tea.View {
	return tea.NewView(m.listView())
}

func rowsFromTasks(tasks []*core.Task) []taskRow {
	rows := make([]taskRow, 0, len(tasks))
	for _, task := range tasks {
		if task == nil {
			continue
		}
		rows = append(rows, taskRow{task: task})
	}
	return rows
}

func (m model) afterTasksLoadedCmds() []tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(m.rows)*2)
	for _, row := range m.rows {
		cmds = append(cmds, m.taskStatusTrackingCmds(taskID(row.task))...)
	}
	return cmds
}

func (m model) submitPrompt() (model, tea.Cmd) {
	prompt := strings.TrimSpace(m.prompt)
	if prompt == "" {
		return m, nil
	}

	m.createPending = true
	m.createErr = nil

	return m, createTaskCmd(m.statusContext, m.frontend, core.CreateTaskInput{
		Prompt:   prompt,
		Provider: defaultCreateProvider,
	})
}

func (m model) updatePromptInput(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.createPending {
		return m, nil
	}

	switch msg.Key().Code {
	case tea.KeyEscape:
		m.mode = modeBrowse
		m.prompt = ""
		m.createErr = nil
		return m, nil
	case tea.KeyEnter:
		return m.submitPrompt()
	case tea.KeyBackspace:
		m.createErr = nil
		m.prompt = trimLastRune(m.prompt)
		return m, nil
	}

	if msg.Text != "" {
		m.createErr = nil
		m.prompt += msg.Text
	}

	return m, nil
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

func (m *model) setTaskStatus(taskID string, status *core.TaskStatusUpdate) {
	for i := range m.rows {
		if m.rows[i].task == nil || m.rows[i].task.ID != taskID {
			continue
		}
		m.rows[i].status = status
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
	return len(m.rows) - 1
}

func taskID(task *core.Task) string {
	if task == nil {
		return ""
	}
	return strings.TrimSpace(task.ID)
}

func trimLastRune(value string) string {
	runes := []rune(value)
	if len(runes) == 0 {
		return ""
	}
	return string(runes[:len(runes)-1])
}

func isQuitKey(msg tea.KeyPressMsg) bool {
	switch msg.String() {
	case "q", "ctrl+c":
		return true
	default:
		return false
	}
}
