package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	observer "agent/internal/adapters/observability/observer"
	"agent/internal/core"

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
	observerUpdates    <-chan core.ObserverTaskUpdate
	unsubscribeUpdates func()
	promptInput        textarea.Model
	nameInput          textinput.Model
	selected           int
	width              int
	loading            bool
	busy               bool
	tasksRequestSeq    int
	icons              IconSet
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
	err    error
	prompt string
	name   string
}

type createFinishedMsg struct {
	task *core.Task
	err  error
}

func newTUIModel(
	service TaskService,
	defaultCreationCwd string,
	defaultProvider string,
	observerSocketPath string,
	useNerdFont bool,
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
		icons:              activeIcons(useNerdFont),
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		refreshTasksCmd(m.service, m.tasksRequestSeq),
		subscribeObserverUpdatesCmd(m.observerSocketPath),
	)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.promptInput.SetWidth(msg.Width - 4)
		return m, nil
	case tea.KeyPressMsg:
		return m.updateKey(msg)
	case tasksLoadedMsg:
		if msg.requestID != 0 && msg.requestID < m.tasksRequestSeq {
			return m, nil
		}
		m.loading = false
		m.busy = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}

		m.taskViews = filterVisibleTaskViews(msg.views)
		m.tasks = taskViewsToTasks(m.taskViews)
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

		var prCmds []tea.Cmd
		for _, view := range m.taskViews {
			if view != nil && view.Task != nil && strings.TrimSpace(view.Task.BranchName) != "" && strings.TrimSpace(view.Task.RepoRoot) != "" {
				prCmds = append(prCmds, fetchPRStatusCmd(m.service, view.Task.ID, view.Task.RepoRoot, view.Task.BranchName))
			}
		}
		if len(prCmds) > 0 {
			return m, tea.Batch(prCmds...)
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
			m.tasksRequestSeq++
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

		m.mode = tuiModeList
		m.busy = true
		return m, openTaskCmd(m.service, selectedIDOrSlug(msg.task))
	case prStatusLoadedMsg:
		for _, view := range m.taskViews {
			if view != nil && view.Task != nil && view.Task.ID == msg.taskID {
				view.PR = msg.status
				break
			}
		}
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
		m.cleanupHookSubscription()
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

func (m model) updateListKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		m.cleanupHookSubscription()
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
			return m, nil
		}
		return m, nil
	case "k", "up":
		if m.selected > 0 {
			m.selected--
			return m, nil
		}
		return m, nil
	case "g", "home":
		if len(m.tasks) > 0 {
			m.selected = 0
			return m, nil
		}
		return m, nil
	case "G", "end":
		if len(m.tasks) > 0 {
			m.selected = len(m.tasks) - 1
			return m, nil
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
		m.createInput.Prompt = prompt
		m.createInput.Provider = m.provider
		m.promptInput.Blur()
		return m, suggestTaskNameCmd(m.service, prompt, m.provider)
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
		input := m.createInput
		input.ConfirmedDisplayName = name
		input.Provider = m.provider
		return m, createTaskCmd(m.service, input)
	}

	var cmd tea.Cmd
	m.nameInput, cmd = m.nameInput.Update(msg)
	return m, cmd
}

// Grid column widths.
const (
	colWidthName     = 40
	colWidthProvider = 10
	colWidthPR       = 4
	colWidthTime     = 10
	colWidthStatus   = 18
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

func (m model) listView() string {
	var b strings.Builder

	// Header
	header := titleStyle.Render(iconHeaderList + " Control Center")
	keys := dimStyle.Render("j/k move · enter open · n new · x clean · r refresh · q quit")
	b.WriteString(header + "  " + keys + "\n")
	totalWidth := 3 + colWidthName + 2 + colWidthProvider + 2 + colWidthPR + 2 + colWidthTime + 2 + colWidthStatus
	b.WriteString(dimStyle.Render(strings.Repeat("─", totalWidth)) + "\n")

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

	// Column header
	colHeader := fmt.Sprintf("   %s  %s  %s  %s  %s",
		padRight("TASK", colWidthName),
		padRight("PROVIDER", colWidthProvider),
		padRight("PR", colWidthPR),
		padRight("TIME", colWidthTime),
		padRight("STATUS", colWidthStatus),
	)
	b.WriteString(dimStyle.Render(colHeader) + "\n")

	// Task rows
	for i, task := range m.tasks {
		view := m.taskViewAt(i)
		providerText := providerIcon(task.Provider) + " " + emptyFallback(task.Provider, "-")
		stateText, stateStyle := taskStateText(view)
		elapsed := taskElapsed(view)
		prIcon := m.prIconForTask(view)

		timeText := ""
		if elapsed != "" {
			timeText = m.icons.Time + " " + elapsed
		}

		providerCell := padRight(providerText, colWidthProvider)
		prCell := padRight(prIcon, colWidthPR)
		timeCell := padRight(timeText, colWidthTime)
		stateCell := padRight(stateText, colWidthStatus)

		if i == m.selected {
			nameCell := padRight(truncateStr(iconSelected+" "+task.DisplayName, colWidthName), colWidthName)
			row := nameCell + "  " + primaryStyle.Render(providerCell) + "  " + prCell + "  " + timeCell + "  " + stateStyle.Render(stateCell)
			b.WriteString(selectedRowStyle.Render(row) + "\n")
		} else {
			nameCell := padRight(truncateStr("  "+task.DisplayName, colWidthName), colWidthName)
			row := nameCell + "  " + primaryStyle.Render(providerCell) + "  " + prCell + "  " + timeCell + "  " + stateStyle.Render(stateCell)
			b.WriteString(normalRowStyle.Render(row) + "\n")
		}
	}

	detail := m.selectedTaskDetailView()
	if detail != "" {
		b.WriteString("\n")
		b.WriteString(dimStyle.Render(strings.Repeat("─", totalWidth)) + "\n")
		b.WriteString(detail)
	}

	return strings.TrimRight(b.String(), "\n")
}

func (m model) selectedTaskDetailView() string {
	task := m.selectedTask()
	if task == nil {
		return ""
	}

	view := m.selectedTaskView()
	var b strings.Builder

	// Git column
	var gitCol strings.Builder
	gitCol.WriteString(titleStyle.Render("Git") + "\n")
	if strings.TrimSpace(task.BranchName) != "" {
		gitCol.WriteString(dimStyle.Render(m.icons.Branch) + " " + truncateStr(task.BranchName, 38) + "\n")
	}
	if strings.TrimSpace(task.RepoName) != "" {
		gitCol.WriteString(dimStyle.Render(m.icons.Repo) + " " + task.RepoName + "\n")
	}
	if view != nil && view.PR != nil && view.PR.State != core.PRStateNone {
		prIcon, prStyle := m.prStatusDisplay(view.PR)
		gitCol.WriteString(prStyle.Render(prIcon+fmt.Sprintf(" #%d %s", view.PR.Number, view.PR.State)) + "\n")
	}

	// Session column
	var sessCol strings.Builder
	sessCol.WriteString(titleStyle.Render("Session") + "\n")
	elapsed := taskElapsed(view)
	if elapsed != "" {
		sessCol.WriteString(dimStyle.Render(m.icons.Time) + " " + elapsed + "\n")
	}
	if view != nil && view.Observer != nil {
		if view.Observer.ProcessAlive {
			sessCol.WriteString(dimStyle.Render(m.icons.Process) + " " + healthyStyle.Render("connected") + "\n")
		} else {
			sessCol.WriteString(dimStyle.Render(m.icons.Process) + " " + dimStyle.Render("disconnected") + "\n")
		}
	}
	if view != nil && view.HookSession != nil {
		hook := view.HookSession
		llmLatest := isLLMOutputLatest(hook)
		promptText := truncateStr(strings.TrimSpace(hook.LastPromptText), 40)
		outputText := truncateStr(strings.TrimSpace(hook.LastAssistantMessage), 40)

		if promptText != "" {
			icon := dimStyle.Render(m.icons.Prompt)
			if llmLatest {
				sessCol.WriteString(icon + " " + dimStyle.Render(promptText) + "\n")
			} else {
				sessCol.WriteString(icon + " " + primaryStyle.Bold(true).Render(promptText) + "\n")
			}
		}
		if outputText != "" {
			icon := dimStyle.Render(m.icons.LLMOutput)
			if llmLatest {
				sessCol.WriteString(icon + " " + primaryStyle.Bold(true).Render(outputText) + "\n")
			} else {
				sessCol.WriteString(icon + " " + dimStyle.Render(outputText) + "\n")
			}
		}
	}

	// Combine two columns side by side
	gitLines := strings.Split(strings.TrimRight(gitCol.String(), "\n"), "\n")
	sessLines := strings.Split(strings.TrimRight(sessCol.String(), "\n"), "\n")
	maxLines := len(gitLines)
	if len(sessLines) > maxLines {
		maxLines = len(sessLines)
	}

	colWidth := 42
	for i := 0; i < maxLines; i++ {
		left := ""
		if i < len(gitLines) {
			left = gitLines[i]
		}
		right := ""
		if i < len(sessLines) {
			right = sessLines[i]
		}
		b.WriteString(padRight(left, colWidth) + right + "\n")
	}

	return strings.TrimRight(b.String(), "\n")
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
	if view.HookSession != nil && !view.HookSession.StartedAt.IsZero() {
		started = view.HookSession.StartedAt
	}
	if started.IsZero() && view.Task != nil {
		started = view.Task.CreatedAt
	}
	if started.IsZero() {
		return ""
	}
	return formatElapsed(time.Since(started))
}

func (m model) prIconForTask(view *core.TaskView) string {
	if view == nil || view.PR == nil {
		return ""
	}
	switch view.PR.State {
	case core.PRStateOpen:
		return healthyStyle.Render(m.icons.PROpen)
	case core.PRStateMerged:
		return titleStyle.Render(m.icons.PRMerged)
	default:
		return ""
	}
}

func isLLMOutputLatest(hook *core.HookSessionSummary) bool {
	if hook == nil {
		return false
	}
	return hook.LastEventName != "UserPromptSubmit"
}

func (m model) prStatusDisplay(pr *core.PRStatus) (string, lipgloss.Style) {
	if pr == nil {
		return "", dimStyle
	}
	switch pr.State {
	case core.PRStateOpen:
		return m.icons.PROpen, healthyStyle
	case core.PRStateMerged:
		return m.icons.PRMerged, titleStyle
	default:
		return "", dimStyle
	}
}

func (m model) promptInputView() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(iconHeaderCreate+" Create Task") + "\n\n")
	b.WriteString(dimStyle.Render("Enter the task prompt. Press Enter to submit, or Esc to cancel.") + "\n")
	b.WriteString(dimStyle.Render("tab to switch provider: ") + providerToggle(m.provider) + "\n\n")
	if m.err != nil {
		b.WriteString(errorStyle.Render("Error: "+m.err.Error()) + "\n\n")
	}
	b.WriteString(m.promptInput.View())
	return b.String()
}

func (m model) nameConfirmView() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(iconHeaderCreate+" Confirm Task Name") + "\n\n")
	b.WriteString(
		dimStyle.Render(
			"Edit the suggested name if needed. Press Enter to create and open the session, or Esc to cancel.",
		) + "\n\n",
	)
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
	b.WriteString(
		healthyStyle.Render("y") + dimStyle.Render(" confirm · ") + errorStyle.Render("n") + dimStyle.Render(" cancel"),
	)
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

func (m model) selectedTaskView() *core.TaskView {
	if len(m.taskViews) == 0 {
		return nil
	}

	if m.selected < 0 {
		return m.taskViews[0]
	}

	if m.selected >= len(m.taskViews) {
		return m.taskViews[len(m.taskViews)-1]
	}

	return m.taskViews[m.selected]
}

func (m model) taskViewAt(index int) *core.TaskView {
	if index < 0 || index >= len(m.taskViews) {
		return nil
	}

	return m.taskViews[index]
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

func (m *model) cleanupHookSubscription() {
	if m.unsubscribeUpdates != nil {
		m.unsubscribeUpdates()
		m.unsubscribeUpdates = nil
	}
	m.observerUpdates = nil
}

func (m *model) applyObserverTaskUpdate(update core.ObserverTaskUpdate) {
	if strings.TrimSpace(update.TaskID) == "" {
		return
	}

	for i, view := range m.taskViews {
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
		if view.Observer == nil {
			view = &core.TaskView{Task: view.Task}
			m.taskViews[i] = view
		}
		view.Observer = copySummary
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

func providerIcon(provider string) string {
	switch provider {
	case "claude":
		return iconProviderClaude
	default:
		return iconProviderCodex
	}
}

func providerToggle(selected string) string {
	var parts []string
	for _, p := range availableProviders {
		label := providerIcon(p) + " " + p
		if p == selected {
			parts = append(parts, primaryStyle.Render(label))
		} else {
			parts = append(parts, dimStyle.Render(label))
		}
	}

	return strings.Join(parts, dimStyle.Render(" / "))
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

func (m *model) nextRefreshTasksCmd() tea.Cmd {
	m.tasksRequestSeq++
	return refreshTasksCmd(m.service, m.tasksRequestSeq)
}

func fetchPRStatusCmd(service TaskService, taskID, repoRoot, branch string) tea.Cmd {
	return func() tea.Msg {
		status, err := service.GetPRStatus(context.Background(), repoRoot, branch)
		if err != nil {
			return prStatusLoadedMsg{taskID: taskID, status: &core.PRStatus{State: core.PRStateNone}}
		}
		return prStatusLoadedMsg{taskID: taskID, status: status}
	}
}

func refreshTasksCmd(service TaskService, requestID int) tea.Cmd {
	return func() tea.Msg {
		views, err := service.ListTaskViews(context.Background())
		return tasksLoadedMsg{requestID: requestID, views: views, err: err}
	}
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

func subscribeObserverUpdatesCmd(socketPath string) tea.Cmd {
	return func() tea.Msg {
		if strings.TrimSpace(socketPath) == "" {
			return observerSubscriptionReadyMsg{}
		}

		updates, cleanup, err := observer.Subscribe(context.Background(), socketPath)
		return observerSubscriptionReadyMsg{updates: updates, cleanup: cleanup, err: err}
	}
}

func waitForObserverUpdateCmd(updates <-chan core.ObserverTaskUpdate) tea.Cmd {
	if updates == nil {
		return nil
	}

	return func() tea.Msg {
		update, ok := <-updates
		if !ok {
			return observerSubscriptionClosedMsg{}
		}
		return observerTaskUpdatedMsg{update: update}
	}
}

func suggestTaskNameCmd(service TaskService, prompt string, provider string) tea.Cmd {
	return func() tea.Msg {
		name, err := service.SuggestTaskName(context.Background(), prompt, provider)
		return suggestNameFinishedMsg{prompt: prompt, name: name, err: err}
	}
}

func createTaskCmd(service TaskService, input core.NewTaskInput) tea.Cmd {
	return func() tea.Msg {
		task, err := service.CreateTaskWithProgress(
			context.Background(),
			input,
			core.CreateTaskOptions{OpenSession: false},
			func(core.TaskProgress) {},
		)
		return createFinishedMsg{task: task, err: err}
	}
}

func selectedIDOrSlug(task *core.Task) string {
	if strings.TrimSpace(task.Slug) != "" {
		return task.Slug
	}

	return task.ID
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
