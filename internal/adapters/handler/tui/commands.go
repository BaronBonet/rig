package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/BaronBonet/rig/internal/core"

	tea "charm.land/bubbletea/v2"
)

const shimmerTickInterval = 90 * time.Millisecond

// activityRefreshInterval paces the background refresh of the selected
// task's activity and usage. Status updates stream live, but activity
// accumulates without status changes (and transcript-recovered activity has
// no hook events at all), so the detail view polls to stay fresh.
var activityRefreshInterval = 5 * time.Second

func activityRefreshTickCmd() tea.Cmd {
	return tea.Tick(activityRefreshInterval, func(time.Time) tea.Msg {
		return activityRefreshTickMsg{}
	})
}

// pullRequestRefreshInterval paces the refresh of every task's pull
// requests, so a pull request opened or merged on GitHub shows without
// reloading the task list. The daemon caches each branch's pull requests for
// a minute or more, so most refreshes cost it no gh call.
var pullRequestRefreshInterval = 30 * time.Second

func pullRequestRefreshTickCmd() tea.Cmd {
	return tea.Tick(pullRequestRefreshInterval, func(time.Time) tea.Msg {
		return pullRequestRefreshTickMsg{}
	})
}

// taskListRefreshInterval paces the background reload of the task list, so
// tasks another rig creates, renames or removes show here too. Status updates
// already stream live; the list itself does not.
var taskListRefreshInterval = 2 * time.Second

func taskListRefreshTickCmd() tea.Cmd {
	return tea.Tick(taskListRefreshInterval, func(time.Time) tea.Msg {
		return taskListRefreshTickMsg{}
	})
}

// refreshTasksCmd reloads the task list in the background.
func refreshTasksCmd(ctx context.Context, frontend core.TaskFrontend) tea.Cmd {
	return func() tea.Msg {
		tasks, err := frontend.ListTasks(ctx)
		return tasksLoadedMsg{tasks: tasks, err: err, background: true}
	}
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

func listReposCmd(ctx context.Context, frontend core.TaskFrontend) tea.Cmd {
	return func() tea.Msg {
		repos, err := frontend.ListRepos(ctx)
		return reposLoadedMsg{repos: repos, err: err}
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

// openRepoSessionCmd attaches to the main session of the repository at
// repoRoot, creating it when it has none.
func openRepoSessionCmd(ctx context.Context, frontend core.TaskFrontend, repoRoot string) tea.Cmd {
	return func() tea.Msg {
		return taskOpenedMsg{err: frontend.AttachRepoSession(ctx, repoRoot)}
	}
}

func createTaskStreamCmd(
	ctx context.Context,
	frontend core.TaskFrontend,
	creation int,
	input core.CreateTaskInput,
) tea.Cmd {
	return func() tea.Msg {
		events, err := frontend.CreateTaskStream(ctx, input)
		if err != nil {
			return taskCreateStreamStartFailedMsg{creation: creation, err: err}
		}
		return waitForTaskCreateEventCmd(creation, events)()
	}
}

func retryTaskCreationStreamCmd(ctx context.Context, frontend core.TaskFrontend, creation int, taskID string) tea.Cmd {
	return func() tea.Msg {
		events, err := frontend.RetryTaskCreationStream(ctx, taskID)
		if err != nil {
			return taskCreateStreamStartFailedMsg{creation: creation, err: err}
		}
		return waitForTaskCreateEventCmd(creation, events)()
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

func removeAgentSessionCmd(
	ctx context.Context,
	frontend core.TaskFrontend,
	taskID string,
	agentSessionID string,
) tea.Cmd {
	return func() tea.Msg {
		return agentSessionRemovedMsg{
			err:     frontend.RemoveAgentSession(ctx, taskID, agentSessionID),
			agentID: agentSessionID,
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

// detectProvidersCmd detects the providers, its reply carrying the
// detection's number so a stale one can be told apart.
func detectProvidersCmd(ctx context.Context, frontend core.TaskFrontend, detection int) tea.Cmd {
	return func() tea.Msg {
		detections, err := frontend.DetectProviders(ctx)
		return providerDetectionsMsg{
			detections: detections,
			err:        err,
			detection:  detection,
		}
	}
}

// saveProviderSetupCmd saves the provider setup and then, only once it saved,
// each Launch default edit in turn: before the first provider setup there is
// no user config to keep them in, and saving one would do nothing. A Launch
// default that fails to save does not stop the rest.
func saveProviderSetupCmd(
	ctx context.Context,
	frontend core.TaskFrontend,
	setup core.ProviderSetup,
	edits []launchDefaultEdit,
) tea.Cmd {
	return func() tea.Msg {
		msg := providerSetupSavedMsg{setup: setup}
		if msg.err = frontend.SaveProviderSetup(ctx, setup); msg.err != nil {
			return msg
		}
		var failures []string
		for _, edit := range edits {
			if err := saveLaunchDefault(ctx, frontend, edit); err != nil {
				failures = append(failures, fmt.Sprintf("save %s launch default: %v", edit.provider, err))
				continue
			}
			msg.savedDefaults = append(msg.savedDefaults, edit)
		}
		if len(failures) > 0 {
			msg.defaultsErr = errors.New(strings.Join(failures, "; "))
		}
		return msg
	}
}

func saveLaunchDefault(ctx context.Context, frontend core.TaskFrontend, edit launchDefaultEdit) error {
	ctx, cancel := context.WithTimeout(ctx, launchDefaultsTimeout)
	defer cancel()
	return frontend.SaveLaunchDefault(ctx, edit.provider, edit.choice)
}

// providerModelsTimeout bounds one model list fetch. The daemon answers from
// its cache at once when warm and bounds a cold fetch from the provider CLI
// itself; the deadline keeps a stuck daemon from holding the fetch forever,
// as the daemon does not see a client that gave up.
const providerModelsTimeout = 60 * time.Second

// launchDefaultsTimeout bounds the Launch defaults fetch, a user config read,
// and each Launch default save, a user config write.
const launchDefaultsTimeout = 10 * time.Second

func listProviderModelsCmd(ctx context.Context, frontend core.TaskFrontend, provider core.Provider) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, providerModelsTimeout)
		defer cancel()
		models, err := frontend.ListProviderModels(ctx, provider)
		return providerModelsLoadedMsg{
			provider: provider,
			models:   models,
			err:      err,
		}
	}
}

// getLaunchDefaultsCmd fetches the Launch defaults, its reply carrying the
// fetch's number so a stale one can be told apart.
func getLaunchDefaultsCmd(ctx context.Context, frontend core.TaskFrontend, fetch int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, launchDefaultsTimeout)
		defer cancel()
		defaults, err := frontend.GetLaunchDefaults(ctx)
		return launchDefaultsLoadedMsg{
			defaults: defaults,
			err:      err,
			fetch:    fetch,
		}
	}
}

// loadRepoDirsCmd loads the directories the repo directories step starts
// with: the user's repo directories, or else the one suggested for the
// directory setup runs from, if any.
func loadRepoDirsCmd(ctx context.Context, frontend core.TaskFrontend, cwd string, load int) tea.Cmd {
	return func() tea.Msg {
		dirs, err := frontend.GetRepoDirs(ctx)
		if err != nil || len(dirs) > 0 {
			return repoDirsLoadedMsg{dirs: dirs, err: err, load: load}
		}
		// The suggestion only prefills the step, so without one the step
		// starts empty.
		suggestion, err := frontend.SuggestRepoDir(ctx, cwd)
		if err != nil || suggestion == "" {
			return repoDirsLoadedMsg{load: load}
		}
		return repoDirsLoadedMsg{dirs: []string{suggestion}, load: load}
	}
}

func saveRepoDirsCmd(ctx context.Context, frontend core.TaskFrontend, dirs []string) tea.Cmd {
	return func() tea.Msg {
		return repoDirsSavedMsg{err: frontend.SaveRepoDirs(ctx, dirs)}
	}
}

func shimmerTickCmd() tea.Cmd {
	return tea.Tick(shimmerTickInterval, func(time.Time) tea.Msg {
		return shimmerTickMsg{}
	})
}

// spinnerTickInterval paces the spinner of working tasks and agents.
var spinnerTickInterval = 100 * time.Millisecond

func spinnerTickCmd() tea.Cmd {
	return tea.Tick(spinnerTickInterval, func(time.Time) tea.Msg {
		return spinnerTickMsg{}
	})
}

// noticeDuration is how long a confirmation such as "copied to clipboard"
// replaces the key bar before it clears.
var noticeDuration = 1500 * time.Millisecond

func clearNoticeCmd() tea.Cmd {
	return tea.Tick(noticeDuration, func(time.Time) tea.Msg {
		return clearNoticeMsg{}
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

func taskUsageCmd(ctx context.Context, frontend core.TaskFrontend, taskID string) tea.Cmd {
	return func() tea.Msg {
		usage, err := frontend.GetTaskUsage(ctx, taskID)
		return taskUsageLoadedMsg{
			taskID: taskID,
			usage:  usage,
			err:    err,
		}
	}
}

func taskPullRequestsCmd(ctx context.Context, frontend core.TaskFrontend, taskID string) tea.Cmd {
	return func() tea.Msg {
		pullRequests, err := frontend.TaskPullRequests(ctx, taskID)
		return taskPullRequestsLoadedMsg{
			taskID:       taskID,
			pullRequests: pullRequests,
			err:          err,
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

func waitForTaskCreateEventCmd(creation int, events <-chan core.TaskCreateEvent) tea.Cmd {
	return func() tea.Msg {
		if events == nil {
			return taskCreateStreamClosedMsg{creation: creation}
		}

		event, ok := <-events
		if !ok {
			return taskCreateStreamClosedMsg{creation: creation}
		}

		return taskCreateEventMsg{
			events:   events,
			event:    event,
			creation: creation,
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
