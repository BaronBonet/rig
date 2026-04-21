package tui

import (
	"context"

	"rig/internal/core"

	tea "charm.land/bubbletea/v2"
)

func loadTasksCmd(ctx context.Context, frontend core.TaskFrontend) tea.Cmd {
	return func() tea.Msg {
		tasks, err := frontend.ListTasks(ctx)
		return tasksLoadedMsg{
			tasks: tasks,
			err:   err,
		}
	}
}

func createTaskCmd(ctx context.Context, frontend core.TaskFrontend, input core.CreateTaskInput) tea.Cmd {
	return func() tea.Msg {
		task, err := frontend.CreateTask(ctx, input)
		return taskCreatedMsg{
			task: task,
			err:  err,
		}
	}
}

func latestTaskStatusCmd(ctx context.Context, frontend core.TaskFrontend, taskID string) tea.Cmd {
	return func() tea.Msg {
		status, err := frontend.LatestTaskStatus(ctx, taskID)
		return latestTaskStatusLoadedMsg{
			taskID: taskID,
			status: status,
			err:    err,
		}
	}
}

func subscribeTaskStatusCmd(ctx context.Context, frontend core.TaskFrontend, taskID string) tea.Cmd {
	return func() tea.Msg {
		updates, err := frontend.SubscribeTaskStatus(ctx, taskID)
		return taskStatusSubscriptionReadyMsg{
			taskID:  taskID,
			updates: updates,
			err:     err,
		}
	}
}

func waitForTaskStatusCmd(taskID string, updates <-chan core.TaskStatusUpdate) tea.Cmd {
	return func() tea.Msg {
		if updates == nil {
			return taskStatusSubscriptionClosedMsg{taskID: taskID}
		}

		update, ok := <-updates
		if !ok {
			return taskStatusSubscriptionClosedMsg{taskID: taskID}
		}

		return taskStatusUpdatedMsg{
			taskID:  taskID,
			update:  update,
			updates: updates,
		}
	}
}
