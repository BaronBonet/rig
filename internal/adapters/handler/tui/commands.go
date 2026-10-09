package tui

import (
	"context"
	"errors"
	"time"

	"github.com/BaronBonet/rig/internal/core"

	tea "charm.land/bubbletea/v2"
)

const shimmerTickInterval = 90 * time.Millisecond

// activityRefreshInterval paces the background refresh of the selected
// task's activity and token usage. Status updates stream live, but activity
// accumulates without status changes (and transcript-recovered activity has
// no hook events at all), so the detail view polls to stay fresh.
var activityRefreshInterval = 5 * time.Second

func activityRefreshTickCmd() tea.Cmd {
	return tea.Tick(activityRefreshInterval, func(time.Time) tea.Msg {
		return activityRefreshTickMsg{}
	})
}

func loadTasksCmd(ctx context.Context, frontend core.TaskFrontend) tea.Cmd {
	return func() tea.Msg {
		tasks, err := frontend.ListTasks(ctx)
		return tasksLoadedMsg{
			tasks: tasks,
			err:   err,
		}
	}
}

func listRepoPullRequestsCmd(
	ctx context.Context,
	frontend core.TaskFrontend,
	repoRoot string,
	repoName string,
) tea.Cmd {
	return func() tea.Msg {
		prs, err := frontend.ListRepoPullRequests(ctx, repoRoot)
		return repoPullRequestsLoadedMsg{
			repoRoot: repoRoot,
			repoName: repoName,
			prs:      prs,
			err:      err,
		}
	}
}

// openTaskSessionCmd attaches to a task's Session, landing on pane when it is
// set. A missing Session is reconnected first; its agents then run in new
// panes, so the attach after a reconnect targets the Session. A reconnect
// that left some agents behind still recreated the Session, so the attach
// goes ahead and the reconnect's error is shown either way.
func openTaskSessionCmd(
	ctx context.Context,
	frontend core.TaskFrontend,
	task *core.Task,
	pane *core.TmuxPaneRef,
) tea.Cmd {
	return func() tea.Msg {
		err := frontend.AttachTaskSession(ctx, task, pane)
		if errors.Is(err, core.ErrTaskSessionNotFound) && task != nil {
			reconnectErr := frontend.ReconnectTaskSession(ctx, task.ID)
			err = frontend.AttachTaskSession(ctx, task, nil)
			if reconnectErr != nil {
				err = reconnectErr
			}
		}
		return taskOpenedMsg{err: err}
	}
}

func createTaskStreamCmd(ctx context.Context, frontend core.TaskFrontend, input core.CreateTaskInput) tea.Cmd {
	return func() tea.Msg {
		events, err := frontend.CreateTaskStream(ctx, input)
		if err != nil {
			return taskCreateStreamStartFailedMsg{err: err}
		}
		return waitForTaskCreateEventCmd(events)()
	}
}

func retryTaskCreationStreamCmd(ctx context.Context, frontend core.TaskFrontend, taskID string) tea.Cmd {
	return func() tea.Msg {
		events, err := frontend.RetryTaskCreationStream(ctx, taskID)
		if err != nil {
			return taskCreateStreamStartFailedMsg{err: err}
		}
		return waitForTaskCreateEventCmd(events)()
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

func getProviderSetupCmd(ctx context.Context, frontend core.TaskFrontend) tea.Cmd {
	return func() tea.Msg {
		setup, err := frontend.GetProviderSetup(ctx)
		return providerSetupLoadedMsg{
			setup: setup,
			err:   err,
		}
	}
}

func detectProvidersCmd(ctx context.Context, frontend core.TaskFrontend) tea.Cmd {
	return func() tea.Msg {
		detections, err := frontend.DetectProviders(ctx)
		return providerDetectionsMsg{
			detections: detections,
			err:        err,
		}
	}
}

func saveProviderSetupCmd(ctx context.Context, frontend core.TaskFrontend, setup core.ProviderSetup) tea.Cmd {
	return func() tea.Msg {
		err := frontend.SaveProviderSetup(ctx, setup)
		return providerSetupSavedMsg{
			setup: setup,
			err:   err,
		}
	}
}

func shimmerTickCmd() tea.Cmd {
	return tea.Tick(shimmerTickInterval, func(time.Time) tea.Msg {
		return shimmerTickMsg{}
	})
}

func taskActivityCmd(ctx context.Context, frontend core.TaskFrontend, taskID string, limit int) tea.Cmd {
	return func() tea.Msg {
		activity, err := frontend.GetTaskActivity(ctx, taskID, limit)
		return taskActivityLoadedMsg{
			taskID:   taskID,
			activity: activity,
			err:      err,
		}
	}
}

func taskTokenUsageCmd(ctx context.Context, frontend core.TaskFrontend, taskID string) tea.Cmd {
	return func() tea.Msg {
		usage, err := frontend.GetTaskTokenUsage(ctx, taskID)
		return taskTokenUsageLoadedMsg{
			taskID: taskID,
			usage:  usage,
			err:    err,
		}
	}
}

func pullRequestStatusCmd(
	ctx context.Context,
	frontend core.TaskFrontend,
	taskID string,
	repoRoot string,
	branchName string,
) tea.Cmd {
	return func() tea.Msg {
		status, err := frontend.PullRequestStatus(ctx, repoRoot, branchName)
		return pullRequestStatusLoadedMsg{
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

func waitForTaskCreateEventCmd(events <-chan core.TaskCreateEvent) tea.Cmd {
	return func() tea.Msg {
		if events == nil {
			return taskCreateStreamClosedMsg{}
		}

		event, ok := <-events
		if !ok {
			return taskCreateStreamClosedMsg{}
		}

		return taskCreateEventMsg{
			events: events,
			event:  event,
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
