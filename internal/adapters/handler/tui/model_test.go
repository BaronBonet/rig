package tui

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/BaronBonet/rig/internal/core"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestModel_InitLoadsAllTasksAcrossRepos(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{
		{
			ID:          "task-1",
			RepoName:    "repo-a",
			DisplayName: "first task",
			Provider:    core.ProviderCodex,
		},
		{
			ID:          "task-2",
			RepoName:    "repo-b",
			DisplayName: "second task",
			Provider:    core.ProviderCodex,
		},
	}

	m := newModel(frontend.mock)
	cmd := m.Init()
	require.NotNil(t, cmd)

	msg := runCmd(t, cmd)
	next, _ := m.Update(msg)

	got, ok := next.(model)
	require.True(t, ok)
	require.Len(t, got.rows, 2)
	require.Equal(t, []string{"task-1", "task-2"}, []string{got.rows[0].task.ID, got.rows[1].task.ID})
	require.Equal(t, 1, frontend.listTasksCalls)
}

func TestModel_ViewRendersTaskMetadata(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{
		{
			ID:          "task-1",
			RepoName:    "repo-a",
			DisplayName: "first task",
			BranchName:  "feat/first-task",
			Provider:    core.ProviderCodex,
			CreatedAt:   time.Now().Add(-15 * time.Minute),
		},
	}

	m := newModel(frontend.mock)
	msg := runCmd(t, m.Init())
	next, _ := m.Update(msg)

	got, ok := next.(model)
	require.True(t, ok)

	view := stripANSI(got.View().Content)
	require.Contains(t, view, "RIG dev")
	require.Contains(t, view, "n new   r refresh   x clean   q quit")
	require.Contains(t, view, "first task")
	require.Contains(t, view, "repo-a")
	require.Contains(t, view, "feat/first-task")
	require.Contains(t, view, "codex")
	require.Contains(t, view, "WORKSPACE")
	require.Contains(t, view, "SESSION")
	require.Contains(t, view, "15m")
}

func TestModel_ViewRendersConfiguredVersionInHeader(t *testing.T) {
	frontend := newFrontendHarness()

	m := newModelWithLaunchCwdAndVersion(frontend.mock, "/tmp/repo", "1.2.3")
	msg := runCmd(t, m.Init())
	next, _ := m.Update(msg)

	got, ok := next.(model)
	require.True(t, ok)

	view := stripANSI(got.View().Content)
	require.Contains(t, view, "RIG 1.2.3")
}

func TestModel_ViewRendersFailedCreationTaskWithRetryHint(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{
		{
			ID:             "task-1",
			RepoName:       "repo-a",
			DisplayName:    "first task",
			BranchName:     "feat/first-task",
			Provider:       core.ProviderCodex,
			CreationStatus: core.TaskCreationStatusFailed,
			CreationStep:   core.TaskCreateProgressPreparingWorkspace,
			CreationError:  "setup workspace: docker daemon unavailable",
		},
	}

	m := newLoadedModel(frontend)

	view := stripANSI(m.View().Content)
	require.Contains(t, view, "R retry")
	require.Contains(t, view, "setup failed")
	require.Contains(t, view, "Failed while preparing workspace")
	require.Contains(t, view, "setup workspace: docker daemon unavailable")
}

func TestModel_ViewSplitsTaskOverviewByRepo(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{
		{
			ID:          "task-1",
			RepoRoot:    "/tmp/repo-a",
			RepoName:    "repo-a",
			DisplayName: "first task",
			Provider:    core.ProviderCodex,
		},
		{
			ID:          "task-2",
			RepoRoot:    "/tmp/repo-a",
			RepoName:    "repo-a",
			DisplayName: "second task",
			Provider:    core.ProviderCodex,
		},
		{
			ID:          "task-3",
			RepoRoot:    "/tmp/repo-b",
			RepoName:    "repo-b",
			DisplayName: "third task",
			Provider:    core.ProviderCodex,
		},
	}

	m := newModel(frontend.mock)
	msg := runCmd(t, m.Init())
	next, _ := m.Update(msg)

	got, ok := next.(model)
	require.True(t, ok)

	view := stripANSI(got.View().Content)
	require.Less(t, strings.Index(view, "repo-a"), strings.Index(view, "first task"))
	require.Less(t, strings.Index(view, "second task"), strings.Index(view, "repo-b"))
	require.Less(t, strings.Index(view, "repo-b"), strings.Index(view, "third task"))
	require.NotContains(t, view, "/tmp/repo-a")
	require.NotContains(t, view, "/tmp/repo-b")
}

func TestModel_ViewKeepsCreateStatusVisibleWhenRowsExceedHeight(t *testing.T) {
	frontend := newFrontendHarness()
	for i := range 20 {
		suffix := strconv.Itoa(i)
		frontend.listTasks = append(frontend.listTasks, &core.Task{
			ID:          "task-" + suffix,
			RepoName:    "repo",
			DisplayName: "task " + suffix,
			Provider:    core.ProviderCodex,
		})
	}

	m := newLoadedModel(frontend)
	m.width = 96
	m.height = 14
	m.createPending = true
	m.createActive = core.TaskCreateProgressStartingSession
	m.createDone = []core.TaskCreateProgressStep{
		core.TaskCreateProgressSuggestingName,
		core.TaskCreateProgressCreatingWorktree,
		core.TaskCreateProgressPreparingWorkspace,
	}

	view := stripANSI(m.View().Content)
	require.Contains(t, view, "Starting session")
	require.LessOrEqual(t, len(strings.Split(view, "\n")), m.height)
}

func TestModel_ViewClearsCreateStatusAfterCreatedTaskIsSelected(t *testing.T) {
	frontend := newFrontendHarness()
	for i := range 20 {
		suffix := strconv.Itoa(i)
		frontend.listTasks = append(frontend.listTasks, &core.Task{
			ID:          "task-" + suffix,
			RepoName:    "repo",
			DisplayName: "task " + suffix,
			Provider:    core.ProviderCodex,
		})
	}

	m := newLoadedModel(frontend)
	m.width = 96
	m.height = 16
	m.createPending = true
	m.createActive = core.TaskCreateProgressStartingSession
	m.createDone = []core.TaskCreateProgressStep{
		core.TaskCreateProgressSuggestingName,
		core.TaskCreateProgressCreatingWorktree,
		core.TaskCreateProgressPreparingWorkspace,
	}

	next, _ := m.Update(taskCreatedMsg{
		task: &core.Task{
			ID:          "task-new",
			RepoName:    "repo",
			DisplayName: "new selected task",
			Provider:    core.ProviderCodex,
		},
	})

	got, ok := next.(model)
	require.True(t, ok)

	view := stripANSI(got.View().Content)
	require.Contains(t, view, "new selected task")
	require.NotContains(t, view, "Suggesting name")
	require.NotContains(t, view, "Creating worktree")
	require.NotContains(t, view, "Preparing workspace")
	require.NotContains(t, view, "Starting session")
	require.LessOrEqual(t, len(strings.Split(view, "\n")), got.height)
}

func TestModel_ViewKeepsSelectedTaskDetailsVisibleWhenRowsExceedHeight(t *testing.T) {
	frontend := newFrontendHarness()
	for i := range 20 {
		suffix := strconv.Itoa(i)
		frontend.listTasks = append(frontend.listTasks, &core.Task{
			ID:           "task-" + suffix,
			RepoName:     "repo",
			DisplayName:  "task " + suffix,
			BranchName:   "feat/task-" + suffix,
			WorktreePath: "/tmp/task-" + suffix,
			Provider:     core.ProviderCodex,
		})
	}

	m := newLoadedModel(frontend)
	m.width = 96
	m.height = 18
	m.selected = 15

	view := stripANSI(m.View().Content)
	require.Contains(t, view, "WORKSPACE")
	require.Contains(t, view, "feat/task-15")
	require.LessOrEqual(t, len(strings.Split(view, "\n")), m.height)
}

func TestModel_ViewRendersTaskListScrollbarWhenRowsExceedHeight(t *testing.T) {
	frontend := newFrontendHarness()
	for i := range 20 {
		suffix := strconv.Itoa(i)
		frontend.listTasks = append(frontend.listTasks, &core.Task{
			ID:          "task-" + suffix,
			RepoName:    "repo",
			DisplayName: "task " + suffix,
			Provider:    core.ProviderCodex,
		})
	}

	m := newLoadedModel(frontend)
	m.width = 96
	m.height = 14

	view := m.View().Content
	require.Contains(t, stripANSI(view), "█")
	for _, line := range strings.Split(view, "\n") {
		require.LessOrEqual(t, lipgloss.Width(line), m.totalWidth(), stripANSI(line))
	}
}

func TestModel_ViewOmitsTaskListScrollbarWhenRowsFitHeight(t *testing.T) {
	frontend := newFrontendHarness()
	for i := range 2 {
		suffix := strconv.Itoa(i)
		frontend.listTasks = append(frontend.listTasks, &core.Task{
			ID:          "task-" + suffix,
			RepoName:    "repo",
			DisplayName: "task " + suffix,
			Provider:    core.ProviderCodex,
		})
	}

	m := newLoadedModel(frontend)
	m.width = 96
	m.height = 32

	require.NotContains(t, stripANSI(m.View().Content), "█")
}

func TestModel_PageKeysMoveSelectionByVisibleTaskPage(t *testing.T) {
	frontend := newFrontendHarness()
	for i := range 20 {
		suffix := strconv.Itoa(i)
		frontend.listTasks = append(frontend.listTasks, &core.Task{
			ID:          "task-" + suffix,
			RepoName:    "repo",
			DisplayName: "task " + suffix,
			Provider:    core.ProviderCodex,
		})
	}

	m := newLoadedModel(frontend)
	m.width = 96
	m.height = 18
	m.selected = 0

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	pagedDown, ok := next.(model)
	require.True(t, ok)
	require.Greater(t, pagedDown.selected, 1)

	next, _ = pagedDown.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	pagedUp, ok := next.(model)
	require.True(t, ok)
	require.Equal(t, 0, pagedUp.selected)
}

func TestModel_PRStatusShownInOverviewRowsAndDetailPanel(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{
		{
			ID:          "task-1",
			RepoRoot:    "/tmp/repo",
			RepoName:    "repo",
			DisplayName: "auth rewrite",
			BranchName:  "feat/auth",
			Provider:    core.ProviderCodex,
		},
	}
	frontend.pullRequestStatus = map[string]*core.PRStatus{
		"/tmp/repo:feat/auth": {State: core.PRStateOpen, Number: 42},
	}

	m := newModel(frontend.mock)
	loadMsg := runCmd(t, m.Init())
	next, cmd := m.Update(loadMsg)
	require.NotNil(t, cmd)

	got, ok := next.(model)
	require.True(t, ok)

	msgs := runBatchCmd(t, cmd)
	prStatusMsg := requireMsgType[pullRequestStatusLoadedMsg](t, msgs)
	next, _ = got.Update(prStatusMsg)
	got, ok = next.(model)
	require.True(t, ok)

	require.NotNil(t, got.rows[0].pullRequest)
	require.Equal(t, core.PRStateOpen, got.rows[0].pullRequest.State)

	view := stripANSI(got.View().Content)
	require.Contains(t, view, "auth rewrite")
	require.Contains(t, view, "#42 open")
}

func TestModel_AfterLoadRequestsLatestStatusAndSubscriptionsForEachTask(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{
		{ID: "task-1", RepoName: "repo-a", DisplayName: "first task", Provider: core.ProviderCodex},
		{ID: "task-2", RepoName: "repo-b", DisplayName: "second task", Provider: core.ProviderCodex},
	}
	frontend.subscribeTaskStatus = map[string]chan core.TaskStatusUpdate{
		"task-1": make(chan core.TaskStatusUpdate, 1),
		"task-2": make(chan core.TaskStatusUpdate, 1),
	}

	m := newModel(frontend.mock)
	loadMsg := runCmd(t, m.Init())
	next, cmd := m.Update(loadMsg)
	require.NotNil(t, cmd)

	_, ok := next.(model)
	require.True(t, ok)

	msgs := runBatchCmd(t, cmd)
	require.Len(t, msgs, 8)
	require.Equal(t, []string{"task-1", "task-2"}, frontend.latestTaskStatusCalls)
	require.Equal(t, []string{"task-1:6", "task-2:6"}, frontend.getTaskActivityCalls)
	require.Equal(t, []string{"task-1", "task-2"}, frontend.getTaskTokenUsageCalls)
	require.Equal(t, []string{"task-1", "task-2"}, frontend.subscribeTaskStatusCalls)
}

func TestModel_AfterLoadRequestsTaskActivityForEachTask(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{
		{ID: "task-1", RepoName: "repo-a", DisplayName: "first task", Provider: core.ProviderCodex},
		{ID: "task-2", RepoName: "repo-b", DisplayName: "second task", Provider: core.ProviderCodex},
	}
	frontend.subscribeTaskStatus = map[string]chan core.TaskStatusUpdate{
		"task-1": make(chan core.TaskStatusUpdate),
		"task-2": make(chan core.TaskStatusUpdate),
	}

	m := newModel(frontend.mock)
	loadMsg := runCmd(t, m.Init())
	next, cmd := m.Update(loadMsg)
	require.NotNil(t, cmd)

	_, ok := next.(model)
	require.True(t, ok)

	_ = runBatchCmd(t, cmd)
	require.Equal(t, []string{"task-1:6", "task-2:6"}, frontend.getTaskActivityCalls)
}

func TestModel_LatestStatusSeedUpdatesRenderedPhase(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{
		{ID: "task-1", RepoName: "repo-a", DisplayName: "first task", Provider: core.ProviderCodex},
	}
	frontend.latestTaskStatus = map[string]*core.TaskStatusUpdate{
		"task-1": {
			TaskID: "task-1",
			Phase:  core.TaskStatusPhaseWorking,
		},
	}
	frontend.subscribeTaskStatus = map[string]chan core.TaskStatusUpdate{
		"task-1": make(chan core.TaskStatusUpdate, 1),
	}

	m := newModel(frontend.mock)
	loadMsg := runCmd(t, m.Init())
	next, cmd := m.Update(loadMsg)
	require.NotNil(t, cmd)

	got, ok := next.(model)
	require.True(t, ok)

	msgs := runBatchCmd(t, cmd)
	latestMsg := requireMsgType[latestTaskStatusLoadedMsg](t, msgs)

	next, _ = got.Update(latestMsg)
	got, ok = next.(model)
	require.True(t, ok)
	require.NotNil(t, got.rows[0].status)
	require.Equal(t, core.TaskStatusPhaseWorking, got.rows[0].status.Phase)
	require.Contains(t, stripANSI(got.View().Content), "working")
}

func TestModel_ViewRendersTaskActivity(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{
		{
			ID:          "task-1",
			RepoName:    "repo-a",
			DisplayName: "first task",
			Prompt:      "top-level task prompt",
			Provider:    core.ProviderCodex,
		},
	}
	frontend.getTaskActivity = map[string][]core.TaskActivityEvent{
		"task-1": {
			{
				TaskID:     "task-1",
				TurnID:     "turn-1",
				EventName:  "UserPromptSubmit",
				Role:       core.TaskActivityRoleUser,
				Text:       "restore the task preview",
				ObservedAt: time.Date(2026, time.April, 23, 10, 0, 0, 0, time.UTC),
			},
			{
				TaskID:     "task-1",
				TurnID:     "turn-1",
				EventName:  "PostToolUse",
				Role:       core.TaskActivityRoleAssistant,
				Text:       "rg -n task detail",
				ObservedAt: time.Date(2026, time.April, 23, 10, 0, 30, 0, time.UTC),
			},
			{
				TaskID:     "task-1",
				TurnID:     "turn-1",
				EventName:  "PostToolUse",
				Role:       core.TaskActivityRoleAssistant,
				Text:       "go test ./internal/adapters/handler/tui",
				ObservedAt: time.Date(2026, time.April, 23, 10, 0, 45, 0, time.UTC),
			},
			{
				TaskID:     "task-1",
				TurnID:     "turn-1",
				EventName:  "Stop",
				Role:       core.TaskActivityRoleAssistant,
				Text:       "Restored the task detail preview.",
				ObservedAt: time.Date(2026, time.April, 23, 10, 1, 0, 0, time.UTC),
			},
		},
	}
	frontend.subscribeTaskStatus = map[string]chan core.TaskStatusUpdate{
		"task-1": make(chan core.TaskStatusUpdate),
	}

	m := newModel(frontend.mock)
	loadMsg := runCmd(t, m.Init())
	next, cmd := m.Update(loadMsg)
	require.NotNil(t, cmd)

	got, ok := next.(model)
	require.True(t, ok)

	for _, msg := range runBatchCmd(t, cmd) {
		next, _ = got.Update(msg)
		got = next.(model)
	}

	view := stripANSI(got.View().Content)
	require.Contains(t, view, "INITIAL PROMPT")
	require.Contains(t, view, "ACTIVITY")
	require.Contains(t, view, "top-level task prompt")
	require.Contains(t, view, "restore the task preview")
	require.Contains(t, view, "rg -n task detail")
	require.Contains(t, view, "go test")
	require.Contains(t, view, "./internal/adapters/handler/tui")
	require.Contains(t, view, "Restored the task detail preview.")
	require.Less(t, strings.Index(view, "INITIAL PROMPT"), strings.Index(view, "ACTIVITY"))
	require.Less(
		t,
		strings.Index(view, "Restored the task detail preview."),
		strings.Index(view, "go test"),
	)
	require.Less(
		t,
		strings.Index(view, "go test"),
		strings.Index(view, "rg -n task detail"),
	)
}

func TestModel_ViewConstrainsTaskActivityColumnsWithLongWords(t *testing.T) {
	task := &core.Task{
		ID:          "task-1",
		RepoName:    "repo-a",
		DisplayName: "first task",
		Provider:    core.ProviderCodex,
	}
	longURL := "https://sendbird.com/docs/chat/sdk/v4/ios/channel/managing-channels/hide-or-archive-a-group-channel"
	m := model{
		width: 96,
		rows: []taskRow{{
			task: task,
			activity: []core.TaskActivityEvent{
				{
					TaskID:     task.ID,
					Role:       core.TaskActivityRoleUser,
					Text:       "reference " + longURL,
					ObservedAt: time.Date(2026, time.April, 23, 10, 0, 0, 0, time.UTC),
				},
				{
					TaskID:     task.ID,
					Role:       core.TaskActivityRoleAssistant,
					Text:       "assistant response stays readable",
					ObservedAt: time.Date(2026, time.April, 23, 10, 0, 30, 0, time.UTC),
				},
			},
		}},
	}

	for _, line := range strings.Split(m.selectedTaskDetailView(), "\n") {
		require.LessOrEqual(t, lipgloss.Width(line), m.totalWidth(), stripANSI(line))
	}
}

func TestRenderRow_CommandStatusKeepsElapsedColumnAligned(t *testing.T) {
	task := &core.Task{
		ID:          "task-1",
		DisplayName: "first task",
		Provider:    core.ProviderCodex,
		CreatedAt:   time.Now().Add(-15 * time.Minute),
	}

	m := model{selected: 1}
	idleLine, _ := m.renderRow(0, taskRow{task: task}, 72)
	commandLine, _ := m.renderRow(0, taskRow{
		task: task,
		status: &core.TaskStatusUpdate{
			TaskID:       task.ID,
			Phase:        core.TaskStatusPhaseWorking,
			RawEventName: "PreToolUse",
		},
	}, 72)

	idleView := stripANSI(idleLine)
	commandView := stripANSI(commandLine)

	require.Contains(t, commandView, "working · command")
	require.Equal(
		t,
		lipgloss.Width(strings.SplitN(idleView, "15m", 2)[0]),
		lipgloss.Width(strings.SplitN(commandView, "15m", 2)[0]),
	)
}

func TestModel_TaskRowUpdatesWhenSubscriptionUpdateArrives(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{
		{ID: "task-1", RepoName: "repo-a", DisplayName: "first task", Provider: core.ProviderCodex},
	}
	updates := make(chan core.TaskStatusUpdate, 1)
	frontend.subscribeTaskStatus = map[string]chan core.TaskStatusUpdate{
		"task-1": updates,
	}

	m := newModel(frontend.mock)
	loadMsg := runCmd(t, m.Init())
	next, cmd := m.Update(loadMsg)
	require.NotNil(t, cmd)

	got, ok := next.(model)
	require.True(t, ok)

	msgs := runBatchCmd(t, cmd)
	subscribeMsg := requireMsgType[taskStatusSubscriptionReadyMsg](t, msgs)

	next, waitCmd := got.Update(subscribeMsg)
	got, ok = next.(model)
	require.True(t, ok)
	require.NotNil(t, waitCmd)

	updates <- core.TaskStatusUpdate{
		TaskID: "task-1",
		Phase:  core.TaskStatusPhaseWaitingForInput,
	}

	updateMsg := runCmd(t, waitCmd)
	next, nextCmd := got.Update(updateMsg)
	got, ok = next.(model)
	require.True(t, ok)
	require.NotNil(t, nextCmd)
	require.NotNil(t, got.rows[0].status)
	require.Equal(t, core.TaskStatusPhaseWaitingForInput, got.rows[0].status.Phase)
	require.Contains(t, stripANSI(got.View().Content), "needs input")
}

func TestModel_TaskStatusUpdateReloadsTaskActivity(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{
		{ID: "task-1", RepoName: "repo-a", DisplayName: "first task", Provider: core.ProviderCodex},
	}
	updates := make(chan core.TaskStatusUpdate, 1)
	frontend.subscribeTaskStatus = map[string]chan core.TaskStatusUpdate{
		"task-1": updates,
	}
	frontend.getTaskActivity = map[string][]core.TaskActivityEvent{
		"task-1": {
			{
				TaskID:     "task-1",
				TurnID:     "turn-1",
				EventName:  "Stop",
				Role:       core.TaskActivityRoleAssistant,
				Text:       "fresh activity",
				ObservedAt: time.Date(2026, time.April, 23, 10, 1, 0, 0, time.UTC),
			},
		},
	}

	m := newModel(frontend.mock)
	loadMsg := runCmd(t, m.Init())
	next, cmd := m.Update(loadMsg)
	require.NotNil(t, cmd)

	got, ok := next.(model)
	require.True(t, ok)

	msgs := runBatchCmd(t, cmd)
	subscribeMsg := requireMsgType[taskStatusSubscriptionReadyMsg](t, msgs)

	next, waitCmd := got.Update(subscribeMsg)
	got, ok = next.(model)
	require.True(t, ok)
	require.NotNil(t, waitCmd)

	updates <- core.TaskStatusUpdate{
		TaskID: "task-1",
		Phase:  core.TaskStatusPhaseWorking,
	}

	updateMsg := runCmd(t, waitCmd)
	next, followCmd := got.Update(updateMsg)
	got, ok = next.(model)
	require.True(t, ok)
	require.NotNil(t, followCmd)

	batchMsg, ok := runCmd(t, followCmd).(tea.BatchMsg)
	require.True(t, ok)
	require.Len(t, batchMsg, 3)

	activityMsg, ok := batchMsg[0]().(taskActivityLoadedMsg)
	require.True(t, ok)
	require.Equal(t, []string{"task-1:6", "task-1:6"}, frontend.getTaskActivityCalls)
	next, _ = got.Update(activityMsg)
	got, ok = next.(model)
	require.True(t, ok)
	require.Len(t, got.rows[0].activity, 1)

	tokenMsg, ok := batchMsg[1]().(taskTokenUsageLoadedMsg)
	require.True(t, ok)
	require.Equal(t, []string{"task-1", "task-1"}, frontend.getTaskTokenUsageCalls)
	next, _ = got.Update(tokenMsg)
	got, ok = next.(model)
	require.True(t, ok)
	require.Equal(t, "fresh activity", got.rows[0].activity[0].Text)
}

func TestModel_TokenUsageLoadedRendersInSelectedTaskDetail(t *testing.T) {
	frontend := newFrontendHarness()
	m := newLoadedModel(frontend)
	m.rows = []taskRow{{
		task: &core.Task{
			ID:          "task-1",
			DisplayName: "token task",
			Provider:    core.ProviderCodex,
			Prompt:      "initial task prompt",
		},
	}}

	next, _ := m.Update(taskTokenUsageLoadedMsg{
		taskID: "task-1",
		usage: &core.TaskTokenUsage{
			SessionCount:             2,
			InputTokens:              130,
			OutputTokens:             60,
			CachedInputTokens:        30,
			CacheCreationInputTokens: 15,
			ReasoningOutputTokens:    10,
			TotalTokens:              190,
		},
	})

	got, ok := next.(model)
	require.True(t, ok)
	view := stripANSI(got.selectedTaskDetailView())
	require.Contains(t, view, "TOKENS")
	require.Contains(t, view, "total 190")
	require.Contains(t, view, "input 130")
	require.Contains(t, view, "output 60")
	require.Contains(t, view, "cached 30")
	require.Contains(t, view, "cache created 15")
	require.Contains(t, view, "reasoning 10")
	require.Contains(t, view, "190")
	require.Contains(t, view, "2 sessions")
	require.Contains(
		t,
		view,
		"total 190   input 130   output 60   cached 30   cache created 15   reasoning 10   2 sessions",
	)
	require.Less(t, strings.Index(view, "SESSION"), strings.Index(view, "TOKENS"))
	require.Less(t, strings.Index(view, "TOKENS"), strings.Index(view, "INITIAL PROMPT"))
}

func TestModel_TokenUsageLoadedErrorRendersError(t *testing.T) {
	frontend := newFrontendHarness()
	m := newLoadedModel(frontend)

	next, _ := m.Update(taskTokenUsageLoadedMsg{
		taskID: "task-1",
		err:    errors.New("token usage unavailable"),
	})

	got, ok := next.(model)
	require.True(t, ok)
	require.ErrorContains(t, got.err, "token usage unavailable")
	require.Contains(t, stripANSI(got.View().Content), "token usage unavailable")
}

func TestModel_TokenUsageLoadedSuccessClearsPreviousError(t *testing.T) {
	frontend := newFrontendHarness()
	m := newLoadedModel(frontend)
	m.err = errors.New("token usage unavailable")

	next, _ := m.Update(taskTokenUsageLoadedMsg{
		taskID: "task-1",
		usage:  &core.TaskTokenUsage{TotalTokens: 190},
	})

	got, ok := next.(model)
	require.True(t, ok)
	require.NoError(t, got.err)
}

func TestModel_DetailStatusUsesTaskStatusStyle(t *testing.T) {
	status := &core.TaskStatusUpdate{
		TaskID: "task-1",
		Phase:  core.TaskStatusPhaseWaitingForInput,
	}
	m := model{
		rows: []taskRow{{
			task:   &core.Task{ID: "task-1", DisplayName: "first task", Provider: core.ProviderCodex},
			status: status,
		}},
		width: 80,
	}

	statusText, statusStyle := taskStatusText(status)

	view := m.selectedTaskDetailView()

	require.Contains(t, view, mutedStyle.Render("state")+"  "+statusStyle.Render(statusText))
	require.NotContains(t, view, mutedStyle.Render("state")+"  "+primaryStyle.Render(statusText))
}

func TestModel_StatusEnrichmentFailuresDoNotCollapseListView(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{
		{ID: "task-1", RepoName: "repo-a", DisplayName: "first task", Provider: core.ProviderCodex},
		{ID: "task-2", RepoName: "repo-b", DisplayName: "second task", Provider: core.ProviderCodex},
	}
	frontend.latestTaskStatus = map[string]*core.TaskStatusUpdate{
		"task-2": {
			TaskID: "task-2",
			Phase:  core.TaskStatusPhaseWorking,
		},
	}
	frontend.latestTaskStatusErr = map[string]error{
		"task-1": errors.New("latest status unavailable"),
	}
	frontend.subscribeTaskStatus = map[string]chan core.TaskStatusUpdate{
		"task-1": make(chan core.TaskStatusUpdate, 1),
	}
	frontend.subscribeTaskStatusErr = map[string]error{
		"task-2": errors.New("subscription unavailable"),
	}

	m := newModel(frontend.mock)
	loadMsg := runCmd(t, m.Init())
	next, cmd := m.Update(loadMsg)
	require.NotNil(t, cmd)

	got, ok := next.(model)
	require.True(t, ok)

	for _, msg := range runBatchCmd(t, cmd) {
		next, _ = got.Update(msg)
		got, ok = next.(model)
		require.True(t, ok)
	}

	require.NoError(t, got.err)
	require.Len(t, got.rows, 2)
	require.Nil(t, got.rows[0].status)
	require.NotNil(t, got.rows[1].status)
	require.Equal(t, core.TaskStatusPhaseWorking, got.rows[1].status.Phase)

	view := stripANSI(got.View().Content)
	require.Contains(t, view, "first task")
	require.Contains(t, view, "second task")
	require.Contains(t, view, "working")
	require.NotContains(t, view, "latest status unavailable")
	require.NotContains(t, view, "subscription unavailable")
}

func TestModel_InitUsesLifecycleContextForInitialLoad(t *testing.T) {
	frontend := newFrontendHarness()
	m := newModel(frontend.mock)

	cmd := m.Init()
	require.NotNil(t, cmd)

	m.cancelStatus()
	runCmd(t, cmd)

	require.NotNil(t, frontend.listTasksContext)
	require.ErrorIs(t, frontend.listTasksContext.Err(), context.Canceled)
}

func TestModel_KeyAEntersPromptMode(t *testing.T) {
	frontend := newFrontendHarness()
	m := newLoadedModel(frontend)

	next, cmd := m.Update(tea.KeyPressMsg{Text: "a"})

	require.NotNil(t, cmd)

	got, ok := next.(model)
	require.True(t, ok)
	require.Equal(t, modePromptInput, got.mode)
	require.Empty(t, got.prompt)
	require.True(t, got.promptInput.Focused())
}

func TestModel_KeyNEntersPromptMode(t *testing.T) {
	frontend := newFrontendHarness()
	m := newLoadedModel(frontend)

	next, cmd := m.Update(tea.KeyPressMsg{Text: "n"})

	require.NotNil(t, cmd)

	got, ok := next.(model)
	require.True(t, ok)
	require.Equal(t, modePromptInput, got.mode)
	require.Empty(t, got.prompt)
	require.True(t, got.promptInput.Focused())
}

func TestModel_PromptInputTreatsQAsText(t *testing.T) {
	frontend := newFrontendHarness()
	m := newLoadedModel(frontend)
	m.mode = modePromptInput
	_ = m.promptInput.Focus()

	next, _ := m.Update(tea.KeyPressMsg{Text: "q"})

	got, ok := next.(model)
	require.True(t, ok)
	require.Equal(t, modePromptInput, got.mode)
	require.Equal(t, "q", got.prompt)
}

func TestModel_EnterOpensSelectedTaskAndKeepsRigRunningOnSuccess(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{
		{ID: "task-1", DisplayName: "first task", TmuxSession: "repo_task_1", Provider: core.ProviderCodex},
		{ID: "task-2", DisplayName: "second task", TmuxSession: "repo_task_2", Provider: core.ProviderCodex},
	}

	m := newLoadedModel(frontend)
	m.selected = 1

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)

	pending, ok := next.(model)
	require.True(t, ok)

	msg := runCmd(t, cmd)
	next, follow := pending.Update(msg)
	require.Nil(t, follow)

	got, ok := next.(model)
	require.True(t, ok)
	require.Equal(t, modeBrowse, got.mode)
	require.NoError(t, got.err)
	require.NotNil(t, frontend.attachedTask)
	require.Equal(t, "task-2", frontend.attachedTask.ID)
	require.Equal(t, 1, frontend.attachTaskSessionCalls)
}

func TestModel_OpenTaskFailureShowsErrorAndStaysInList(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{
		{ID: "task-1", DisplayName: "first task", TmuxSession: "repo_task_1", Provider: core.ProviderCodex},
	}
	frontend.attachTaskSessionErr = errors.New("open failed")

	m := newLoadedModel(frontend)

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)

	pending, ok := next.(model)
	require.True(t, ok)

	msg := runCmd(t, cmd)
	next, follow := pending.Update(msg)
	require.Nil(t, follow)

	got, ok := next.(model)
	require.True(t, ok)
	require.Equal(t, modeBrowse, got.mode)
	require.ErrorContains(t, got.err, "open failed")
	require.Equal(t, 1, frontend.attachTaskSessionCalls)
}

func TestModel_EnterReconnectsWhenSessionIsMissing(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{
		{ID: "task-1", DisplayName: "first task", TmuxSession: "repo_task_1", Provider: core.ProviderCodex},
	}
	attempts := 0
	frontend.attachTaskSessionFn = func(context.Context, *core.Task) error {
		attempts++
		if attempts == 1 {
			return core.ErrTaskSessionNotFound
		}
		return nil
	}
	frontend.reconnectTaskSessionFn = func(_ context.Context, taskID string) error {
		require.Equal(t, "task-1", taskID)
		return nil
	}

	m := newLoadedModel(frontend)

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)

	pending, ok := next.(model)
	require.True(t, ok)

	msg := runCmd(t, cmd)
	next, follow := pending.Update(msg)
	require.Nil(t, follow)

	got, ok := next.(model)
	require.True(t, ok)
	require.NoError(t, got.err)
	require.Equal(t, 2, frontend.attachTaskSessionCalls)
	require.Equal(t, 1, frontend.reconnectTaskSessionCalls)
}

func TestModel_CreateTaskFromPromptAppendsTaskAndStartsStatusTracking(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{
		{ID: "task-1", DisplayName: "first task", RepoName: "repo-a", Provider: core.ProviderCodex},
		{ID: "task-2", DisplayName: "second task", RepoName: "repo-b", Provider: core.ProviderCodex},
	}
	createdTask := &core.Task{
		ID:          "task-3",
		DisplayName: "new task",
		RepoName:    "repo-c",
		Provider:    core.ProviderCodex,
	}
	frontend.createTaskEvents = []core.TaskCreateEvent{
		{Progress: &core.TaskCreateProgressEvent{Step: core.TaskCreateProgressSuggestingName}},
		{Task: createdTask},
	}
	frontend.latestTaskStatus = map[string]*core.TaskStatusUpdate{
		"task-3": {
			TaskID: "task-3",
			Phase:  core.TaskStatusPhaseWorking,
		},
	}
	frontend.subscribeTaskStatus = map[string]chan core.TaskStatusUpdate{
		"task-1": make(chan core.TaskStatusUpdate, 1),
		"task-2": make(chan core.TaskStatusUpdate, 1),
		"task-3": make(chan core.TaskStatusUpdate, 1),
	}

	m := newLoadedModel(frontend)
	m.mode = modePromptInput
	m.prompt = "fix the retry loop"

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)

	submitted, ok := next.(model)
	require.True(t, ok)
	require.Equal(t, modeBrowse, submitted.mode)
	require.Equal(t, "fix the retry loop", submitted.prompt)
	require.True(t, submitted.createPending)

	initialMsgs := runBatchCmd(t, cmd)
	createEvent := requireMsgType[taskCreateEventMsg](t, initialMsgs)
	requireMsgType[shimmerTickMsg](t, initialMsgs)
	require.NotNil(t, createEvent.event.Progress)
	require.Equal(t, core.TaskCreateProgressSuggestingName, createEvent.event.Progress.Step)

	next, follow := submitted.Update(createEvent)
	require.NotNil(t, follow)

	got, ok := next.(model)
	require.True(t, ok)
	require.True(t, got.createPending)
	require.Equal(t, modeBrowse, got.mode)
	require.Equal(t, core.TaskCreateProgressSuggestingName, got.createActive)
	require.Contains(t, stripANSI(got.View().Content), "Suggesting name")
	require.NotContains(t, stripANSI(got.View().Content), "Enter task prompt.")

	taskCreated := runCmd(t, follow)
	next, follow = got.Update(taskCreated)
	require.NotNil(t, follow)

	got, ok = next.(model)
	require.True(t, ok)
	require.Len(t, got.rows, 3)
	require.Equal(t, modeBrowse, got.mode)
	require.Empty(t, got.prompt)
	require.False(t, got.createPending)
	require.Equal(t, "task-3", got.rows[len(got.rows)-1].task.ID)
	require.Equal(t, "fix the retry loop", frontend.createInput.Prompt)
	require.Equal(t, core.ProviderCodex, frontend.createInput.Provider)
	require.Empty(t, frontend.createInput.Source.PullRequest)
	require.Equal(t, 1, frontend.createTaskStreamCalls)

	frontend.listTasks = append(frontend.listTasks, createdTask)
	msgs := runBatchCmd(t, follow)
	require.Len(t, msgs, 3)
	tasksLoaded := requireMsgType[tasksLoadedMsg](t, msgs)
	next, _ = got.Update(tasksLoaded)
	got, ok = next.(model)
	require.True(t, ok)
	require.Len(t, got.rows, 3)
	require.Equal(t, []string{"task-3"}, frontend.latestTaskStatusCalls)
	require.Equal(t, []string{"task-3"}, frontend.subscribeTaskStatusCalls)
	require.Equal(t, 1, frontend.listTasksCalls)

	latestMsg := requireMsgType[latestTaskStatusLoadedMsg](t, msgs)
	next, _ = got.Update(latestMsg)
	got, ok = next.(model)
	require.True(t, ok)
	require.NotNil(t, got.rows[2].status)
	require.Equal(t, core.TaskStatusPhaseWorking, got.rows[2].status.Phase)
}

func TestModel_CreateTaskFromPromptUsesLaunchCwdWhenAnotherRepoTaskIsSelected(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{
		{
			ID:          "task-1",
			DisplayName: "repo a task",
			RepoRoot:    "/tmp/repo-a",
			RepoName:    "repo-a",
			Provider:    core.ProviderCodex,
		},
	}
	frontend.createTaskEvents = []core.TaskCreateEvent{
		{Task: &core.Task{ID: "task-2", DisplayName: "new task", Provider: core.ProviderCodex}},
	}

	m := newLoadedModel(frontend)
	m.launchCwd = "/tmp/repo-b/subdir"
	m.selected = 0
	m.mode = modePromptInput
	m.prompt = "fix the retry loop"

	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)

	runBatchCmd(t, cmd)
	require.Equal(t, "/tmp/repo-b/subdir", frontend.createInput.Cwd)
}

func TestModel_CreateTaskReloadsAuthoritativeTaskSnapshotWhenCreateResponseIsPartial(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{
		{ID: "task-1", DisplayName: "first task", RepoName: "repo-a", Provider: core.ProviderCodex},
	}
	createdTask := &core.Task{
		ID:       "task-2",
		Prompt:   "testing if new rig things work",
		Provider: core.ProviderCodex,
	}
	frontend.createTaskEvents = []core.TaskCreateEvent{
		{Progress: &core.TaskCreateProgressEvent{Step: core.TaskCreateProgressSuggestingName}},
		{Task: createdTask},
	}
	frontend.latestTaskStatus = map[string]*core.TaskStatusUpdate{
		"task-2": {
			TaskID: "task-2",
			Phase:  core.TaskStatusPhaseStarting,
		},
	}
	frontend.subscribeTaskStatus = map[string]chan core.TaskStatusUpdate{
		"task-2": make(chan core.TaskStatusUpdate, 1),
	}

	m := newLoadedModel(frontend)
	m.mode = modePromptInput
	m.prompt = "testing if new rig things work"

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	submitted, ok := next.(model)
	require.True(t, ok)

	initialMsgs := runBatchCmd(t, cmd)
	progressMsg := requireMsgType[taskCreateEventMsg](t, initialMsgs)
	_, follow := submitted.Update(progressMsg)
	require.NotNil(t, follow)

	taskCreated := runCmd(t, follow)
	next, follow = submitted.Update(taskCreated)
	require.NotNil(t, follow)

	pendingReload, ok := next.(model)
	require.True(t, ok)
	require.Equal(t, "task-2", pendingReload.rows[len(pendingReload.rows)-1].task.ID)

	frontend.listTasks = []*core.Task{
		{ID: "task-1", DisplayName: "first task", RepoName: "repo-a", Provider: core.ProviderCodex},
		{
			ID:           "task-2",
			DisplayName:  "verify new rig behavior",
			Prompt:       "testing if new rig things work",
			RepoName:     "rig",
			BranchName:   "feat/verify-new-rig-behavior",
			WorktreePath: "/tmp/rig-verify-new-rig-behavior",
			Provider:     core.ProviderCodex,
		},
	}

	followMsgs := runBatchCmd(t, follow)
	tasksLoaded := requireMsgType[tasksLoadedMsg](t, followMsgs)
	next, _ = pendingReload.Update(tasksLoaded)
	reloaded, ok := next.(model)
	require.True(t, ok)

	selected := reloaded.selectedRow()
	require.NotNil(t, selected)
	require.NotNil(t, selected.task)
	require.Equal(t, "task-2", selected.task.ID)
	require.Equal(t, "verify new rig behavior", selected.task.DisplayName)
	require.Equal(t, "rig", selected.task.RepoName)
	require.Equal(t, "feat/verify-new-rig-behavior", selected.task.BranchName)
	require.Equal(t, "/tmp/rig-verify-new-rig-behavior", selected.task.WorktreePath)

	view := stripANSI(reloaded.View().Content)
	require.Contains(t, view, "verify new rig behavior")
	require.Contains(t, view, "feat/verify-new-rig-behavior")
	require.Contains(t, view, "testing if new rig things work")
}

func TestModel_EnterWithBlankPromptDoesNothing(t *testing.T) {
	frontend := newFrontendHarness()
	m := newLoadedModel(frontend)
	m.mode = modePromptInput
	m.prompt = "   "

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	require.Nil(t, cmd)

	got, ok := next.(model)
	require.True(t, ok)
	require.Equal(t, modePromptInput, got.mode)
	require.Equal(t, "   ", got.prompt)
	require.False(t, got.createPending)
	require.Zero(t, frontend.createTaskStreamCalls)
}

func TestModel_PasteIntoPromptInputAppendsPastedText(t *testing.T) {
	frontend := newFrontendHarness()
	m := newLoadedModel(frontend)
	m.mode = modePromptInput
	m.prompt = "existing "
	_ = m.promptInput.Focus()

	next, cmd := m.Update(tea.PasteMsg{Content: "copied text\nnext line"})

	require.NotNil(t, cmd)

	got, ok := next.(model)
	require.True(t, ok)
	require.Equal(t, modePromptInput, got.mode)
	require.Equal(t, "existing copied text\nnext line", got.prompt)
	require.NoError(t, got.createErr)
}

func TestModel_CreateTaskFailureKeepsPromptRecoverableAndPreservesListView(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{
		{ID: "task-1", DisplayName: "first task", RepoName: "repo-a", Provider: core.ProviderCodex},
		{ID: "task-2", DisplayName: "second task", RepoName: "repo-b", Provider: core.ProviderCodex},
	}
	frontend.createTaskEvents = []core.TaskCreateEvent{
		{Progress: &core.TaskCreateProgressEvent{Step: core.TaskCreateProgressCreatingWorktree}},
		{Err: errors.New("create failed")},
	}

	m := newLoadedModel(frontend)
	m.mode = modePromptInput
	m.prompt = "fix the retry loop"

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)

	pending, ok := next.(model)
	require.True(t, ok)
	require.Equal(t, modeBrowse, pending.mode)
	require.True(t, pending.createPending)

	initialMsgs := runBatchCmd(t, cmd)
	progressMsg := requireMsgType[taskCreateEventMsg](t, initialMsgs)
	requireMsgType[shimmerTickMsg](t, initialMsgs)
	require.NotNil(t, progressMsg.event.Progress)

	next, follow := pending.Update(progressMsg)
	require.NotNil(t, follow)

	withProgress, ok := next.(model)
	require.True(t, ok)
	require.Equal(t, core.TaskCreateProgressCreatingWorktree, withProgress.createActive)
	require.Contains(t, stripANSI(withProgress.View().Content), "Creating worktree")

	createFailed := runCmd(t, follow)
	next, follow = withProgress.Update(createFailed)
	require.Nil(t, follow)

	got, ok := next.(model)
	require.True(t, ok)
	require.Equal(t, modeBrowse, got.mode)
	require.Equal(t, "fix the retry loop", got.prompt)
	require.False(t, got.createPending)
	require.ErrorContains(t, got.createErr, "create failed")
	require.NoError(t, got.err)
	require.Len(t, got.rows, 2)
	require.Empty(t, frontend.latestTaskStatusCalls)
	require.Empty(t, frontend.subscribeTaskStatusCalls)
	require.Equal(t, core.TaskCreateProgressCreatingWorktree, got.createActive)

	view := stripANSI(got.View().Content)
	require.Contains(t, view, "RIG")
	require.Contains(t, view, "first task")
	require.Contains(t, view, "Creating worktree")
	require.Contains(t, view, "create failed")
	require.NotContains(t, view, "Loading tasks...")
	require.NotContains(t, view, "Enter task prompt.")
}

func TestModel_RetryFailedTaskCreationStreamsProgress(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{
		{
			ID:             "task-1",
			DisplayName:    "first task",
			RepoName:       "repo-a",
			Provider:       core.ProviderCodex,
			CreationStatus: core.TaskCreationStatusFailed,
			CreationStep:   core.TaskCreateProgressPreparingWorkspace,
			CreationError:  "setup workspace: docker daemon unavailable",
		},
	}
	frontend.retryTaskEvents = []core.TaskCreateEvent{
		{Progress: &core.TaskCreateProgressEvent{Step: core.TaskCreateProgressPreparingWorkspace}},
		{Task: &core.Task{
			ID:             "task-1",
			DisplayName:    "first task",
			RepoName:       "repo-a",
			Provider:       core.ProviderCodex,
			CreationStatus: core.TaskCreationStatusReady,
		}},
	}

	m := newLoadedModel(frontend)

	next, cmd := m.Update(tea.KeyPressMsg{Text: "R"})
	require.NotNil(t, cmd)

	pending, ok := next.(model)
	require.True(t, ok)
	require.True(t, pending.createPending)

	initialMsgs := runBatchCmd(t, cmd)
	require.Equal(t, "task-1", frontend.retryTaskID)
	require.Equal(t, 1, frontend.retryTaskStreamCalls)
	progressMsg := requireMsgType[taskCreateEventMsg](t, initialMsgs)
	requireMsgType[shimmerTickMsg](t, initialMsgs)
	require.NotNil(t, progressMsg.event.Progress)
	require.Equal(t, core.TaskCreateProgressPreparingWorkspace, progressMsg.event.Progress.Step)

	next, follow := pending.Update(progressMsg)
	require.NotNil(t, follow)
	withProgress, ok := next.(model)
	require.True(t, ok)
	require.Equal(t, core.TaskCreateProgressPreparingWorkspace, withProgress.createActive)

	taskRetried := runCmd(t, follow)
	next, _ = withProgress.Update(taskRetried)
	got, ok := next.(model)
	require.True(t, ok)
	require.False(t, got.createPending)
	require.Equal(t, core.TaskCreationStatusReady, got.rows[0].task.CreationStatus)
}

func TestModel_CreateTaskFromPromptReturnsToBrowseImmediatelyAndKeepsOverviewUsable(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{
		{ID: "task-1", DisplayName: "first task", RepoName: "repo-a", Provider: core.ProviderCodex},
		{ID: "task-2", DisplayName: "second task", RepoName: "repo-b", Provider: core.ProviderCodex},
	}
	frontend.createTaskEvents = []core.TaskCreateEvent{
		{Progress: &core.TaskCreateProgressEvent{Step: core.TaskCreateProgressSuggestingName}},
	}

	m := newLoadedModel(frontend)
	m.mode = modePromptInput
	m.prompt = "fix the retry loop"

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)

	submitted, ok := next.(model)
	require.True(t, ok)
	require.Equal(t, modeBrowse, submitted.mode)
	require.True(t, submitted.createPending)
	require.Equal(t, 0, submitted.selected)

	next, navCmd := submitted.Update(tea.KeyPressMsg{Text: "j"})
	require.Nil(t, navCmd)

	navigated, ok := next.(model)
	require.True(t, ok)
	require.Equal(t, 1, navigated.selected)
	require.True(t, navigated.createPending)

	next, promptCmd := navigated.Update(tea.KeyPressMsg{Text: "n"})
	require.Nil(t, promptCmd)

	stillPending, ok := next.(model)
	require.True(t, ok)
	require.Equal(t, modeBrowse, stillPending.mode)
	require.True(t, stillPending.createPending)
}

func TestPromptInputView_RendersPromptTextBox(t *testing.T) {
	frontend := newFrontendHarness()
	m := newLoadedModel(frontend)
	m.mode = modePromptInput
	m.prompt = "fix the retry loop"

	view := stripANSI(m.View().Content)
	require.Contains(t, view, "╭")
	require.Contains(t, view, "fix the retry loop")
	require.Contains(t, view, "╰")
	require.Contains(t, view, "┃")
}

func TestModel_PendingCreateStillAllowsQuitKeys(t *testing.T) {
	frontend := newFrontendHarness()
	m := newLoadedModel(frontend)
	m.mode = modePromptInput
	m.prompt = "fix the retry loop"
	m.createPending = true

	for _, msg := range []tea.KeyPressMsg{
		{Text: "q"},
		{Code: 'c', Mod: tea.ModCtrl},
	} {
		next, cmd := m.Update(msg)
		require.NotNil(t, cmd)

		got, ok := next.(model)
		require.True(t, ok)
		require.True(t, got.createPending)

		quitMsg := runCmd(t, cmd)
		_, ok = quitMsg.(tea.QuitMsg)
		require.True(t, ok)
	}
}

func TestModel_ShimmerTickAdvancesAndReschedulesWhilePending(t *testing.T) {
	frontend := newFrontendHarness()
	m := newLoadedModel(frontend)
	m.createPending = true

	next, cmd := m.Update(shimmerTickMsg{})
	require.NotNil(t, cmd)

	got, ok := next.(model)
	require.True(t, ok)
	require.Equal(t, 1, got.shimmerTick)

	msg := runCmd(t, cmd)
	_, ok = msg.(shimmerTickMsg)
	require.True(t, ok)
}

func TestModel_EscCancelsPromptMode(t *testing.T) {
	frontend := newFrontendHarness()
	m := newLoadedModel(frontend)
	m.mode = modePromptInput
	m.prompt = "fix the retry loop"

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	require.Nil(t, cmd)

	got, ok := next.(model)
	require.True(t, ok)
	require.Equal(t, modeBrowse, got.mode)
	require.Empty(t, got.prompt)
	require.False(t, got.createPending)
	require.NoError(t, got.createErr)
	require.Zero(t, frontend.createTaskStreamCalls)
}

func TestModel_CtrlPFromPromptModeLoadsRepoPullRequests(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{
		{
			ID:          "task-1",
			DisplayName: "first task",
			RepoRoot:    "/tmp/repo",
			RepoName:    "repo",
			Provider:    core.ProviderCodex,
		},
	}
	frontend.listRepoPullRequests = []core.RepoPullRequest{
		{Number: 42, Title: "Auth rewrite", BranchName: "feat/auth", State: core.PRStateDraft},
	}

	m := newLoadedModel(frontend)
	m.mode = modePromptInput
	m.prompt = "typed already"

	next, cmd := m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	require.NotNil(t, cmd)

	pending, ok := next.(model)
	require.True(t, ok)
	require.Equal(t, modePRPicker, pending.mode)
	require.Equal(t, "typed already", pending.prompt)

	msg := runCmd(t, cmd)
	loaded, ok := msg.(repoPullRequestsLoadedMsg)
	require.True(t, ok)
	require.Equal(t, "/tmp/repo", loaded.repoRoot)
	require.Equal(t, "repo", loaded.repoName)

	next, _ = pending.Update(loaded)
	got, ok := next.(model)
	require.True(t, ok)
	require.Equal(t, "/tmp/repo", frontend.listRepoPullRequestsCwd)
	require.Equal(t, modePRPicker, got.mode)
	require.Len(t, got.prRows, 1)
}

func TestModel_CtrlPFromPromptModeUsesLaunchCwdWhenAnotherRepoTaskIsSelected(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{
		{
			ID:          "task-1",
			DisplayName: "repo a task",
			RepoRoot:    "/tmp/repo-a",
			RepoName:    "repo-a",
			Provider:    core.ProviderCodex,
		},
	}

	m := newLoadedModel(frontend)
	m.launchCwd = "/tmp/repo-b/subdir"
	m.selected = 0
	m.mode = modePromptInput

	next, cmd := m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	require.NotNil(t, cmd)

	pending, ok := next.(model)
	require.True(t, ok)

	msg := runCmd(t, cmd)
	loaded, ok := msg.(repoPullRequestsLoadedMsg)
	require.True(t, ok)
	require.Equal(t, "/tmp/repo-b/subdir", loaded.repoRoot)
	require.Equal(t, "subdir", loaded.repoName)

	_, _ = pending.Update(loaded)
	require.Equal(t, "/tmp/repo-b/subdir", frontend.listRepoPullRequestsCwd)
}

func TestPRPickerView_ShowsDuplicateRowsAsDisabled(t *testing.T) {
	frontend := newFrontendHarness()
	m := newLoadedModel(frontend)
	m.mode = modePRPicker
	m.prRepoName = "repo"
	m.prRows = []core.RepoPullRequest{
		{Number: 42, Title: "Auth rewrite", BranchName: "feat/auth", State: core.PRStateDraft, HasExistingTask: true},
	}

	view := stripANSI(m.View().Content)
	require.Contains(t, view, "PRs: repo")
	require.Contains(t, view, "Auth rewrite")
	require.Contains(t, view, "branch checked out")
	require.NotContains(t, view, "already has workspace")
}

func TestModel_PRPickerEnterCreatesTaskFromSelectedPR(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.createTaskEvents = []core.TaskCreateEvent{
		{
			Task: &core.Task{
				ID:          "task-2",
				DisplayName: "PR #42 Auth rewrite",
				RepoName:    "repo",
				Provider:    core.ProviderCodex,
			},
		},
	}
	frontend.listTasks = []*core.Task{
		{ID: "task-1", DisplayName: "existing task", RepoName: "repo", Provider: core.ProviderCodex},
	}
	frontend.subscribeTaskStatus = map[string]chan core.TaskStatusUpdate{
		"task-2": make(chan core.TaskStatusUpdate),
	}
	frontend.latestTaskStatus = map[string]*core.TaskStatusUpdate{}

	m := newLoadedModel(frontend)
	m.mode = modePRPicker
	m.prompt = "typed already"
	m.prRepoRoot = "/tmp/repo"
	m.prRepoName = "repo"
	m.prRows = []core.RepoPullRequest{
		{Number: 42, Title: "Auth rewrite", BranchName: "feat/auth", State: core.PRStateDraft},
	}

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)

	pending, ok := next.(model)
	require.True(t, ok)
	require.Equal(t, modeBrowse, pending.mode)
	require.True(t, pending.createPending)
	view := stripANSI(pending.View().Content)
	require.Contains(t, view, "n new   r refresh   x clean   q quit")
	require.Contains(t, view, "Creating task from pull request")
	require.NotContains(t, view, "Suggesting name")
	require.Less(t, strings.Index(view, "existing task"), strings.Index(view, "Creating task from pull request"))

	msgs := runBatchCmd(t, cmd)
	createMsg := requireMsgType[taskCreateEventMsg](t, msgs)
	requireMsgType[shimmerTickMsg](t, msgs)
	require.NotNil(t, createMsg.event.Task)
	require.Equal(t, "/tmp/repo", frontend.createInput.Cwd)
	require.Equal(t, core.ProviderCodex, frontend.createInput.Provider)
	require.NotNil(t, frontend.createInput.Source.PullRequest)
	require.Equal(t, 42, frontend.createInput.Source.PullRequest.Number)
	require.Equal(t, "feat/auth", frontend.createInput.Source.PullRequest.BranchName)
}

func TestModel_PRPickerCreateFailureReturnsToBrowseWithProgressAndError(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{
		{ID: "task-1", DisplayName: "existing task", RepoName: "repo", Provider: core.ProviderCodex},
	}
	frontend.createTaskEvents = []core.TaskCreateEvent{
		{Progress: &core.TaskCreateProgressEvent{Step: core.TaskCreateProgressCreatingWorktree}},
		{Err: errors.New("create failed")},
	}

	m := newLoadedModel(frontend)
	m.mode = modePRPicker
	m.prRepoRoot = "/tmp/repo"
	m.prRepoName = "repo"
	m.prRows = []core.RepoPullRequest{
		{Number: 42, Title: "Auth rewrite", BranchName: "feat/auth", State: core.PRStateDraft},
	}

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)

	pending, ok := next.(model)
	require.True(t, ok)
	require.Equal(t, modeBrowse, pending.mode)
	require.True(t, pending.createPending)
	view := stripANSI(pending.View().Content)
	require.Contains(t, view, "Creating task from pull request")
	require.NotContains(t, view, "Suggesting name")

	initialMsgs := runBatchCmd(t, cmd)
	progressMsg := requireMsgType[taskCreateEventMsg](t, initialMsgs)
	requireMsgType[shimmerTickMsg](t, initialMsgs)
	require.NotNil(t, progressMsg.event.Progress)

	next, follow := pending.Update(progressMsg)
	require.NotNil(t, follow)

	withProgress, ok := next.(model)
	require.True(t, ok)
	require.Equal(t, modeBrowse, withProgress.mode)
	require.Equal(t, core.TaskCreateProgressCreatingWorktree, withProgress.createActive)
	view = stripANSI(withProgress.View().Content)
	require.Contains(t, view, "Creating task from pull request")
	require.NotContains(t, view, "Creating worktree")

	createFailed := runCmd(t, follow)
	next, follow = withProgress.Update(createFailed)
	require.Nil(t, follow)

	got, ok := next.(model)
	require.True(t, ok)
	require.Equal(t, modeBrowse, got.mode)
	require.False(t, got.createPending)
	require.ErrorContains(t, got.createErr, "create failed")

	view = stripANSI(got.View().Content)
	require.Contains(t, view, "n new   r refresh   x clean   q quit")
	require.Contains(t, view, "Creating task from pull request")
	require.NotContains(t, view, "Creating worktree")
	require.Contains(t, view, "create failed")
}

func TestModel_EscFromPRPickerReturnsToPromptMode(t *testing.T) {
	frontend := newFrontendHarness()
	m := newLoadedModel(frontend)
	m.mode = modePRPicker
	m.prompt = "typed already"

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	require.NotNil(t, cmd)

	got, ok := next.(model)
	require.True(t, ok)
	require.Equal(t, modePromptInput, got.mode)
	require.Equal(t, "typed already", got.prompt)
	require.True(t, got.promptInput.Focused())
}

func TestModel_KeyXEntersCleanupConfirmMode(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{{ID: "task-1", DisplayName: "first task"}}

	m := newLoadedModel(frontend)
	next, cmd := m.Update(tea.KeyPressMsg{Text: "x"})

	require.Nil(t, cmd)

	got, ok := next.(model)
	require.True(t, ok)
	require.Equal(t, modeCleanupConfirm, got.mode)
}

func TestModel_ConfirmCleanupDeletesTaskAndRemovesRow(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{
		{ID: "task-1", DisplayName: "first task", Provider: core.ProviderCodex},
		{ID: "task-2", DisplayName: "second task", Provider: core.ProviderCodex},
	}

	m := newLoadedModel(frontend)
	m.selected = 1
	m.mode = modeCleanupConfirm

	next, cmd := m.Update(tea.KeyPressMsg{Text: "y"})
	require.NotNil(t, cmd)

	pending, ok := next.(model)
	require.True(t, ok)
	require.True(t, pending.deletePending)
	require.Equal(t, modeCleanupConfirm, pending.mode)

	initialMsgs := runBatchCmd(t, cmd)
	taskDeleted := requireMsgType[taskDeletedMsg](t, initialMsgs)
	requireMsgType[shimmerTickMsg](t, initialMsgs)

	next, follow := pending.Update(taskDeleted)
	require.Nil(t, follow)

	got, ok := next.(model)
	require.True(t, ok)
	require.False(t, got.deletePending)
	require.Equal(t, modeBrowse, got.mode)
	require.Len(t, got.rows, 1)
	require.Equal(t, "task-1", got.rows[0].task.ID)
	require.Equal(t, 0, got.selected)
	require.Equal(t, []string{"task-2"}, frontend.deleteTaskIDs)
}

func TestModel_CleanupFailurePreservesRowsAndShowsError(t *testing.T) {
	frontend := newFrontendHarness()
	frontend.listTasks = []*core.Task{
		{ID: "task-1", DisplayName: "first task", Provider: core.ProviderCodex},
	}
	frontend.deleteTaskErr = errors.New("cleanup failed")

	m := newLoadedModel(frontend)
	m.mode = modeCleanupConfirm

	next, cmd := m.Update(tea.KeyPressMsg{Text: "y"})
	require.NotNil(t, cmd)

	pending, ok := next.(model)
	require.True(t, ok)
	require.True(t, pending.deletePending)

	initialMsgs := runBatchCmd(t, cmd)
	taskDeleted := requireMsgType[taskDeletedMsg](t, initialMsgs)
	requireMsgType[shimmerTickMsg](t, initialMsgs)

	next, _ = pending.Update(taskDeleted)

	got, ok := next.(model)
	require.True(t, ok)
	require.False(t, got.deletePending)
	require.Equal(t, modeBrowse, got.mode)
	require.Len(t, got.rows, 1)
	require.ErrorContains(t, got.err, "cleanup failed")
}

func newLoadedModel(frontend *frontendHarness) model {
	return model{
		frontend:      frontend.mock,
		launchCwd:     "/tmp/repo",
		promptInput:   newPromptInput(),
		statusContext: context.Background(),
		rows:          rowsFromTasks(frontend.listTasks),
		mode:          modeBrowse,
	}
}

func runCmd(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	require.NotNil(t, cmd)
	return cmd()
}

func runBatchCmd(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	msg := runCmd(t, cmd)
	batch, ok := msg.(tea.BatchMsg)
	require.True(t, ok)

	msgs := make([]tea.Msg, 0, len(batch))
	for _, batchCmd := range batch {
		msgs = append(msgs, runCmd(t, batchCmd))
	}
	return msgs
}

func requireMsgType[T tea.Msg](t *testing.T, msgs []tea.Msg) T {
	t.Helper()

	for _, msg := range msgs {
		typed, ok := msg.(T)
		if ok {
			return typed
		}
	}

	var zero T
	t.Fatalf("message of type %T not found", zero)
	return zero
}

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string {
	return ansiPattern.ReplaceAllString(s, "")
}

type frontendHarness struct {
	mock *core.MockTaskFrontend

	listTasks                 []*core.Task
	listTasksContext          context.Context
	listTasksErr              error
	listTasksCalls            int
	listRepoPullRequests      []core.RepoPullRequest
	listRepoPullRequestsErr   error
	listRepoPullRequestsCwd   string
	pullRequestStatus         map[string]*core.PRStatus
	pullRequestStatusErr      map[string]error
	pullRequestStatusCalls    []string
	attachedTask              *core.Task
	attachTaskSessionErr      error
	attachTaskSessionCalls    int
	attachTaskSessionFn       func(context.Context, *core.Task) error
	reconnectTaskSessionErr   error
	reconnectTaskSessionFn    func(context.Context, string) error
	reconnectTaskSessionCalls int
	createInput               core.CreateTaskInput
	createTaskEvents          []core.TaskCreateEvent
	createTaskStreamErr       error
	createTaskStreamCalls     int
	retryTaskID               string
	retryTaskEvents           []core.TaskCreateEvent
	retryTaskStreamErr        error
	retryTaskStreamCalls      int
	deleteTaskErr             error
	deleteTaskIDs             []string
	latestTaskStatus          map[string]*core.TaskStatusUpdate
	latestTaskStatusErr       map[string]error
	latestTaskStatusCalls     []string
	getTaskTokenUsage         map[string]*core.TaskTokenUsage
	getTaskTokenUsageErr      map[string]error
	getTaskTokenUsageCalls    []string
	getTaskActivity           map[string][]core.TaskActivityEvent
	getTaskActivityErr        map[string]error
	getTaskActivityCalls      []string
	subscribeTaskStatus       map[string]chan core.TaskStatusUpdate
	subscribeTaskStatusErr    map[string]error
	subscribeTaskStatusCalls  []string
}

func newFrontendHarness() *frontendHarness {
	frontend := &frontendHarness{mock: &core.MockTaskFrontend{}}
	frontend.mock.EXPECT().AttachTaskSession(mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, task *core.Task) error {
			frontend.attachTaskSessionCalls++
			frontend.attachedTask = task
			if frontend.attachTaskSessionFn != nil {
				return frontend.attachTaskSessionFn(ctx, task)
			}
			return frontend.attachTaskSessionErr
		},
	).Maybe()
	frontend.mock.EXPECT().ReconnectTaskSession(mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, taskID string) error {
			frontend.reconnectTaskSessionCalls++
			if frontend.reconnectTaskSessionFn != nil {
				return frontend.reconnectTaskSessionFn(ctx, taskID)
			}
			return frontend.reconnectTaskSessionErr
		},
	).Maybe()
	frontend.mock.EXPECT().CreateTaskStream(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, input core.CreateTaskInput) (<-chan core.TaskCreateEvent, error) {
			frontend.createTaskStreamCalls++
			frontend.createInput = input
			if frontend.createTaskStreamErr != nil {
				return nil, frontend.createTaskStreamErr
			}
			events := make(chan core.TaskCreateEvent, len(frontend.createTaskEvents))
			for _, event := range frontend.createTaskEvents {
				events <- event
			}
			close(events)
			return events, nil
		},
	).Maybe()
	frontend.mock.EXPECT().RetryTaskCreationStream(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, taskID string) (<-chan core.TaskCreateEvent, error) {
			frontend.retryTaskStreamCalls++
			frontend.retryTaskID = taskID
			if frontend.retryTaskStreamErr != nil {
				return nil, frontend.retryTaskStreamErr
			}
			events := make(chan core.TaskCreateEvent, len(frontend.retryTaskEvents))
			for _, event := range frontend.retryTaskEvents {
				events <- event
			}
			close(events)
			return events, nil
		},
	).Maybe()
	frontend.mock.EXPECT().DeleteTask(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, taskID string) error {
			frontend.deleteTaskIDs = append(frontend.deleteTaskIDs, taskID)
			return frontend.deleteTaskErr
		},
	).Maybe()
	frontend.mock.EXPECT().ListTasks(mock.Anything).RunAndReturn(
		func(ctx context.Context) ([]*core.Task, error) {
			frontend.listTasksCalls++
			frontend.listTasksContext = ctx
			return frontend.listTasks, frontend.listTasksErr
		},
	).Maybe()
	frontend.mock.EXPECT().ListRepoPullRequests(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, cwd string) ([]core.RepoPullRequest, error) {
			frontend.listRepoPullRequestsCwd = cwd
			if frontend.listRepoPullRequestsErr != nil {
				return nil, frontend.listRepoPullRequestsErr
			}
			return append([]core.RepoPullRequest(nil), frontend.listRepoPullRequests...), nil
		},
	).Maybe()
	frontend.mock.EXPECT().PullRequestStatus(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, repoRoot string, branchName string) (*core.PRStatus, error) {
			key := repoRoot + ":" + branchName
			frontend.pullRequestStatusCalls = append(frontend.pullRequestStatusCalls, key)
			if frontend.pullRequestStatusErr != nil && frontend.pullRequestStatusErr[key] != nil {
				return nil, frontend.pullRequestStatusErr[key]
			}
			if frontend.pullRequestStatus == nil {
				return &core.PRStatus{State: core.PRStateNone}, nil
			}
			return frontend.pullRequestStatus[key], nil
		},
	).Maybe()
	frontend.mock.EXPECT().LatestTaskStatus(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, taskID string) (*core.TaskStatusUpdate, error) {
			frontend.latestTaskStatusCalls = append(frontend.latestTaskStatusCalls, taskID)
			if frontend.latestTaskStatusErr != nil && frontend.latestTaskStatusErr[taskID] != nil {
				return nil, frontend.latestTaskStatusErr[taskID]
			}
			if frontend.latestTaskStatus == nil {
				return nil, nil
			}
			return frontend.latestTaskStatus[taskID], nil
		},
	).Maybe()
	frontend.mock.EXPECT().GetTaskActivity(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, taskID string, limit int) ([]core.TaskActivityEvent, error) {
			frontend.getTaskActivityCalls = append(frontend.getTaskActivityCalls, taskID+":"+strconv.Itoa(limit))
			if frontend.getTaskActivityErr != nil && frontend.getTaskActivityErr[taskID] != nil {
				return nil, frontend.getTaskActivityErr[taskID]
			}
			if frontend.getTaskActivity == nil {
				return nil, nil
			}
			return append([]core.TaskActivityEvent(nil), frontend.getTaskActivity[taskID]...), nil
		},
	).Maybe()
	frontend.mock.EXPECT().GetTaskTokenUsage(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, taskID string) (*core.TaskTokenUsage, error) {
			frontend.getTaskTokenUsageCalls = append(frontend.getTaskTokenUsageCalls, taskID)
			if frontend.getTaskTokenUsageErr != nil && frontend.getTaskTokenUsageErr[taskID] != nil {
				return nil, frontend.getTaskTokenUsageErr[taskID]
			}
			if frontend.getTaskTokenUsage == nil {
				return nil, nil
			}
			return frontend.getTaskTokenUsage[taskID], nil
		},
	).Maybe()
	frontend.mock.EXPECT().SubscribeTaskStatus(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, taskID string) (<-chan core.TaskStatusUpdate, error) {
			frontend.subscribeTaskStatusCalls = append(frontend.subscribeTaskStatusCalls, taskID)
			if frontend.subscribeTaskStatusErr != nil && frontend.subscribeTaskStatusErr[taskID] != nil {
				return nil, frontend.subscribeTaskStatusErr[taskID]
			}
			if frontend.subscribeTaskStatus == nil {
				ch := make(chan core.TaskStatusUpdate)
				close(ch)
				return ch, nil
			}
			return frontend.subscribeTaskStatus[taskID], nil
		},
	).Maybe()
	return frontend
}
