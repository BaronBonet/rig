package cli

import (
	"context"
	"fmt"
	"strings"

	"agent/internal/core"

	tea "github.com/charmbracelet/bubbletea"
)

type model struct {
	service    TaskService
	tasks      []*core.Task
	selected   int
	loading    bool
	busy       bool
	confirming bool
	err        error
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

func newTUIModel(service TaskService) model {
	return model{service: service, loading: true}
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
			m.confirming = false
			return m, nil
		}

		if m.selected >= len(m.tasks) {
			m.selected = len(m.tasks) - 1
		}

		return m, nil
	case cleanupFinishedMsg:
		m.confirming = false
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
	default:
		return m, nil
	}
}

func (m model) View() string {
	if m.confirming {
		return m.confirmationView()
	}

	return m.listView()
}

func (m model) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.confirming {
		switch msg.String() {
		case "q", "n", "esc":
			m.confirming = false
			return m, nil
		case "y":
			task := m.selectedTask()
			if task == nil {
				m.confirming = false
				return m, nil
			}

			m.confirming = false
			m.busy = true
			m.err = nil
			return m, cleanupTaskCmd(m.service, selectedIDOrSlug(task))
		default:
			return m, nil
		}
	}

	if m.busy {
		return m, nil
	}

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

		m.confirming = true
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

func (m model) listView() string {
	var b strings.Builder

	b.WriteString("Task cleanup\n")
	b.WriteString("j/k: move  g/G: jump  enter: open  x: clean up  r: refresh  q: quit\n\n")

	if m.err != nil {
		b.WriteString("Error: ")
		b.WriteString(m.err.Error())
		b.WriteString("\n\n")
	}

	if m.loading {
		b.WriteString("Loading tasks...")
		return b.String()
	}

	if m.busy {
		b.WriteString("Working...\n\n")
	}

	if len(m.tasks) == 0 {
		b.WriteString("No tasks found.")
		return b.String()
	}

	for i, task := range m.tasks {
		marker := " "
		if i == m.selected {
			marker = ">"
		}

		fmt.Fprintf(
			&b,
			"%s %s | status: %s | tmux: %s | worktree: %s | branch: %s\n",
			marker,
			task.DisplayName,
			task.Status,
			yesNo(task.SessionExists),
			yesNo(task.WorktreeExists),
			task.BranchName,
		)
	}

	return strings.TrimRight(b.String(), "\n")
}

func (m model) confirmationView() string {
	task := m.selectedTask()
	if task == nil {
		return "No task selected."
	}

	var b strings.Builder
	b.WriteString("Confirm cleanup\n\n")
	fmt.Fprintf(&b, "Task: %s\n", task.DisplayName)
	b.WriteString("The tmux session and worktree will be deleted.\n")
	b.WriteString("The branch will be kept.\n\n")
	b.WriteString("Press y to confirm. Press n, esc, or q to cancel.")
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

func selectedIDOrSlug(task *core.Task) string {
	if strings.TrimSpace(task.Slug) != "" {
		return task.Slug
	}

	return task.ID
}

func yesNo(ok bool) string {
	if ok {
		return "yes"
	}

	return "no"
}
