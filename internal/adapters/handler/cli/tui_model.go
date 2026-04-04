package cli

import (
	"context"
	"fmt"
	"strings"

	"agent/internal/core"

	textinput "github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type tuiMode string

const (
	tuiModeList           tuiMode = "list"
	tuiModeCleanupConfirm tuiMode = "cleanup_confirm"
	tuiModePromptInput    tuiMode = "prompt_input"
	tuiModeNameConfirm    tuiMode = "name_confirm"
)

type model struct {
	service            TaskService
	tasks              []*core.Task
	selected           int
	loading            bool
	busy               bool
	mode               tuiMode
	promptInput        textinput.Model
	nameInput          textinput.Model
	defaultCreationCwd string
	createInput        core.NewTaskInput
	err                error
}

type tasksLoadedMsg struct {
	tasks []*core.Task
	err   error
}

type cleanupFinishedMsg struct {
	task *core.Task
	err  error
}

type openFinishedMsg struct {
	err error
}

type suggestNameFinishedMsg struct {
	prompt string
	name   string
	err    error
}

type createFinishedMsg struct {
	task *core.Task
	err  error
}

func newTUIModel(service TaskService, defaultCreationCwd string) model {
	promptInput := textinput.New()
	promptInput.Prompt = titleStyle.Render("❯") + " "
	promptInput.Placeholder = "Describe the task to create"
	promptInput.Focus()

	nameInput := textinput.New()
	nameInput.Prompt = titleStyle.Render("❯") + " "
	nameInput.Placeholder = "Confirm or edit the suggested task name"

	return model{
		service:            service,
		loading:            true,
		mode:               tuiModeList,
		promptInput:        promptInput,
		nameInput:          nameInput,
		defaultCreationCwd: emptyFallback(defaultCreationCwd, "."),
	}
}

func (m model) Init() tea.Cmd {
	return refreshTasksCmd(m.service)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.updateKey(msg)
	case tasksLoadedMsg:
		m.loading = false
		m.busy = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}

		m.tasks = filterVisibleTasks(msg.tasks)
		if len(m.tasks) == 0 {
			m.selected = 0
			if m.mode == tuiModeCleanupConfirm {
				m.mode = tuiModeList
			}
			return m, nil
		}

		if m.selected >= len(m.tasks) {
			m.selected = len(m.tasks) - 1
		}

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
		return m, refreshTasksCmd(m.service)
	case openFinishedMsg:
		m.busy = false
		m.err = msg.err
		if msg.err != nil {
			return m, nil
		}

		return m, tea.Quit
	case suggestNameFinishedMsg:
		m.busy = false
		m.err = msg.err
		if msg.err != nil {
			m.mode = tuiModePromptInput
			m.promptInput.Focus()
			return m, nil
		}

		m.createInput.Prompt = msg.prompt
		m.nameInput.SetValue(msg.name)
		m.nameInput.CursorEnd()
		m.nameInput.Focus()
		m.promptInput.Blur()
		m.mode = tuiModeNameConfirm
		return m, nil
	case createFinishedMsg:
		m.busy = false
		m.err = msg.err
		if msg.task != nil {
			m.upsertTask(msg.task)
		}
		if msg.err != nil {
			if msg.task != nil {
				m.mode = tuiModeList
			} else {
				m.mode = tuiModeNameConfirm
				m.nameInput.Focus()
			}
			return m, nil
		}

		return m, tea.Quit
	default:
		return m, nil
	}
}

func (m model) View() string {
	switch m.mode {
	case tuiModeCleanupConfirm:
		return m.confirmationView()
	case tuiModePromptInput:
		return m.promptInputView()
	case tuiModeNameConfirm:
		return m.nameConfirmView()
	default:
		return m.listView()
	}
}

func (m model) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyCtrlC {
		return m, tea.Quit
	}

	if m.busy {
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

func (m model) updateListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		return m, tea.Quit
	case "enter":
		task := m.selectedTask()
		if task == nil {
			return m, nil
		}

		m.err = nil
		m.busy = true
		return m, openTaskCmd(m.service, selectedIDOrSlug(task))
	case "j", "down":
		if m.selected < len(m.tasks)-1 {
			m.selected++
		}
		return m, nil
	case "k", "up":
		if m.selected > 0 {
			m.selected--
		}
		return m, nil
	case "g", "home":
		if len(m.tasks) > 0 {
			m.selected = 0
		}
		return m, nil
	case "G", "end":
		if len(m.tasks) > 0 {
			m.selected = len(m.tasks) - 1
		}
		return m, nil
	case "x":
		if len(m.tasks) == 0 {
			return m, nil
		}

		m.mode = tuiModeCleanupConfirm
		return m, nil
	case "n":
		m.err = nil
		m.mode = tuiModePromptInput
		m.createInput = core.NewTaskInput{Cwd: m.creationCwd()}
		m.promptInput.SetValue("")
		m.promptInput.Focus()
		m.nameInput.Blur()
		return m, nil
	case "r":
		m.err = nil
		m.busy = true
		m.loading = true
		return m, refreshTasksCmd(m.service)
	default:
		return m, nil
	}
}

func (m model) updateCleanupConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "n", "esc":
		m.mode = tuiModeList
		return m, nil
	case "y":
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

func (m model) updatePromptInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.mode = tuiModeList
		m.promptInput.Blur()
		return m, nil
	case tea.KeyEnter:
		prompt := strings.TrimSpace(m.promptInput.Value())
		if prompt == "" {
			return m, nil
		}

		m.err = nil
		m.busy = true
		m.createInput.Prompt = prompt
		m.promptInput.Blur()
		return m, suggestTaskNameCmd(m.service, prompt)
	}

	var cmd tea.Cmd
	m.promptInput, cmd = m.promptInput.Update(msg)
	return m, cmd
}

func (m model) updateNameConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
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
		input := m.createInput
		input.ConfirmedDisplayName = name
		return m, createTaskCmd(m.service, input)
	}

	var cmd tea.Cmd
	m.nameInput, cmd = m.nameInput.Update(msg)
	return m, cmd
}

func (m model) listView() string {
	var b strings.Builder

	// Header
	header := titleStyle.Render(iconHeaderList + " Control Center")
	keys := dimStyle.Render("j/k move · enter open · n new · x clean · r refresh · q quit")
	b.WriteString(header + "  " + keys + "\n")
	b.WriteString(dimStyle.Render(strings.Repeat("─", 72)) + "\n")

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

	if len(m.tasks) == 0 {
		b.WriteString(dimStyle.Render("No tasks found.") + "\n")
		b.WriteString(dimStyle.Render("Press n to create one."))
		return b.String()
	}

	// Detail bar for selected task
	task := m.selectedTask()
	details := fmt.Sprintf(
		"%s %s  %s %s  %s %s  %s %s  %s %s  %s %s",
		iconRepo, emptyFallback(taskRepoName(task), "-"),
		iconAgent, healthStyle(task.AgentWindowExists),
		iconEditor, healthStyle(task.EditorWindowExists),
		iconBranch, dimStyle.Render(emptyFallback(task.BranchName, "-")),
		iconTmux, yesNoStyled(task.SessionExists),
		iconWorktree, yesNoStyled(task.WorktreeExists),
	)
	b.WriteString(detailBarStyle.Render(details) + "\n\n")

	// Task rows
	for i, task := range m.tasks {
		icon, style := statusStyle(string(task.Status))
		status := style.Render(icon + " " + string(task.Status))

		if i == m.selected {
			name := iconSelected + " " + task.DisplayName
			row := fmt.Sprintf("%-40s %s", name, status)
			b.WriteString(selectedRowStyle.Render(row) + "\n")
		} else {
			name := "  " + task.DisplayName
			row := fmt.Sprintf("%-40s %s", name, status)
			b.WriteString(normalRowStyle.Render(row) + "\n")
		}
	}

	return strings.TrimRight(b.String(), "\n")
}

func (m model) promptInputView() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(iconHeaderCreate+" Create Task") + "\n\n")
	b.WriteString(dimStyle.Render("Enter the task prompt. Press Enter to suggest a name, or Esc to cancel.") + "\n\n")
	if m.err != nil {
		b.WriteString(errorStyle.Render("Error: "+m.err.Error()) + "\n\n")
	}
	b.WriteString(m.promptInput.View())
	return b.String()
}

func (m model) nameConfirmView() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(iconHeaderCreate+" Confirm Task Name") + "\n\n")
	b.WriteString(dimStyle.Render("Edit the suggested name if needed. Press Enter to create and open the session, or Esc to cancel.") + "\n\n")
	if m.err != nil {
		b.WriteString(errorStyle.Render("Error: "+m.err.Error()) + "\n\n")
	}
	b.WriteString(dimStyle.Render("prompt: "+m.createInput.Prompt) + "\n\n")
	b.WriteString(m.nameInput.View())
	return b.String()
}

func (m model) confirmationView() string {
	task := m.selectedTask()
	if task == nil {
		return dimStyle.Render("No task selected.")
	}

	var b strings.Builder
	b.WriteString(warningStyle.Render(iconHeaderCleanup+" Confirm Cleanup") + "\n\n")
	b.WriteString("Task: " + primaryStyle.Render(task.DisplayName) + "\n")
	b.WriteString(dimStyle.Render("The tmux session and worktree will be deleted.") + "\n")
	b.WriteString(dimStyle.Render("The branch will be kept.") + "\n\n")
	b.WriteString(healthyStyle.Render("y") + dimStyle.Render(" confirm · ") + errorStyle.Render("n") + dimStyle.Render(" cancel"))
	return b.String()
}

func (m model) selectedTask() *core.Task {
	if len(m.tasks) == 0 {
		return nil
	}

	if m.selected < 0 {
		return m.tasks[0]
	}

	if m.selected >= len(m.tasks) {
		return m.tasks[len(m.tasks)-1]
	}

	return m.tasks[m.selected]
}

func (m *model) replaceTask(updated *core.Task) {
	for i, task := range m.tasks {
		if selectedIDOrSlug(task) == selectedIDOrSlug(updated) {
			if !isVisibleTask(updated) {
				m.tasks = append(m.tasks[:i], m.tasks[i+1:]...)
				if m.selected >= len(m.tasks) && len(m.tasks) > 0 {
					m.selected = len(m.tasks) - 1
				}
				return
			}

			m.tasks[i] = updated
			return
		}
	}
}

func (m *model) upsertTask(updated *core.Task) {
	for i, task := range m.tasks {
		if selectedIDOrSlug(task) == selectedIDOrSlug(updated) {
			if !isVisibleTask(updated) {
				m.tasks = append(m.tasks[:i], m.tasks[i+1:]...)
				if m.selected >= len(m.tasks) && len(m.tasks) > 0 {
					m.selected = len(m.tasks) - 1
				}
				return
			}

			m.tasks[i] = updated
			return
		}
	}

	if isVisibleTask(updated) {
		m.tasks = append(m.tasks, updated)
		m.selected = len(m.tasks) - 1
	}
}

func filterVisibleTasks(tasks []*core.Task) []*core.Task {
	filtered := make([]*core.Task, 0, len(tasks))
	for _, task := range tasks {
		if isVisibleTask(task) {
			filtered = append(filtered, task)
		}
	}

	return filtered
}

func isVisibleTask(task *core.Task) bool {
	return task != nil && (task.SessionExists || task.WorktreeExists)
}

func refreshTasksCmd(service TaskService) tea.Cmd {
	return func() tea.Msg {
		tasks, err := service.ListTasks(context.Background())
		return tasksLoadedMsg{tasks: tasks, err: err}
	}
}

func cleanupTaskCmd(service TaskService, idOrSlug string) tea.Cmd {
	return func() tea.Msg {
		task, err := service.DeleteTaskResources(context.Background(), idOrSlug)
		return cleanupFinishedMsg{task: task, err: err}
	}
}

func openTaskCmd(service TaskService, idOrSlug string) tea.Cmd {
	return func() tea.Msg {
		return openFinishedMsg{err: service.OpenTask(context.Background(), idOrSlug)}
	}
}

func suggestTaskNameCmd(service TaskService, prompt string) tea.Cmd {
	return func() tea.Msg {
		name, err := service.SuggestTaskName(context.Background(), prompt)
		return suggestNameFinishedMsg{prompt: prompt, name: name, err: err}
	}
}

func createTaskCmd(service TaskService, input core.NewTaskInput) tea.Cmd {
	return func() tea.Msg {
		task, err := service.CreateTaskWithProgress(context.Background(), input, core.CreateTaskOptions{OpenSession: true}, func(core.TaskProgress) {})
		return createFinishedMsg{task: task, err: err}
	}
}

func selectedIDOrSlug(task *core.Task) string {
	if strings.TrimSpace(task.Slug) != "" {
		return task.Slug
	}

	return task.ID
}

func taskRepoName(task *core.Task) string {
	if task == nil {
		return "-"
	}

	return emptyFallback(task.RepoName, "-")
}

func (m model) creationCwd() string {
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

