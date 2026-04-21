package tui

import (
	"context"
	"time"

	"rig/internal/core"

	tea "charm.land/bubbletea/v2"
)

const shimmerTickInterval = 90 * time.Millisecond

func loadTasksCmd(ctx context.Context, frontend core.TaskFrontend) tea.Cmd {
	return func() tea.Msg {
		tasks, err := frontend.ListTasks(ctx)
		return tasksLoadedMsg{
			tasks: tasks,
			err:   err,
		}
	}
}

func openTaskSessionCmd(ctx context.Context, frontend core.TaskFrontend, task *core.Task) tea.Cmd {
	return func() tea.Msg {
		err := frontend.OpenTaskSession(ctx, task)
		return taskOpenedMsg{err: err}
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

func deleteTaskCmd(ctx context.Context, frontend core.TaskFrontend, taskID string) tea.Cmd {
	return func() tea.Msg {
		err := frontend.DeleteTask(ctx, taskID)
		return taskDeletedMsg{
			taskID: taskID,
			err:    err,
		}
	}
}

func shimmerTickCmd() tea.Cmd {
	return tea.Tick(shimmerTickInterval, func(time.Time) tea.Msg {
		return shimmerTickMsg{}
	})
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
