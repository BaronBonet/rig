package core

import (
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func readyTaskFixture() *Task {
	return &Task{
		ID:             "task-1",
		Slug:           "billing-retry-flow",
		Prompt:         "add billing retry flow",
		DisplayName:    "billing retry flow",
		RepoRoot:       "/tmp/repo",
		RepoName:       "repo",
		BranchName:     "feat/billing-retry-flow",
		WorktreePath:   "/tmp/repo_billing-retry-flow",
		TmuxSession:    "repo_billing-retry-flow",
		Provider:       ProviderCodex,
		CreationStatus: TaskCreationStatusReady,
		WorkspaceKind:  WorkspaceKindWorktree,
	}
}

func idlePane() TaskSessionRuntimeState {
	return TaskSessionRuntimeState{Exists: true, ActiveCommands: []string{"zsh"}}
}

func TestTaskServiceNewTaskSession_StartsAFreshSessionFromAHandoffNote(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.listTasks = []*Task{readyTaskFixture()}
	svc.sessionClient.inspectState = idlePane()
	svc.taskRepo.providerSessionsByTask["task-1"] = []TaskProviderSession{
		{TaskID: "task-1", Provider: ProviderCodex, ProviderSessionID: "sess-old", LastObservedAt: time.Unix(100, 0)},
		{
			TaskID:            "task-1",
			Provider:          ProviderCodex,
			ProviderSessionID: "sess-latest",
			LastObservedAt:    time.Unix(200, 0),
		},
		{
			TaskID:            "task-1",
			Provider:          ProviderClaude,
			ProviderSessionID: "sess-claude",
			LastObservedAt:    time.Unix(300, 0),
		},
	}
	svc.providerRepo.handoffPath = "/data/rig/handoffs/task-1/note.md"

	var steps []TaskCreateProgressStep
	reporter := NewMockTaskCreateProgressReporter(t)
	reporter.EXPECT().ReportTaskCreateProgress(mock.Anything).Run(func(step TaskCreateProgressStep) {
		steps = append(steps, step)
	}).Return()

	task, err := svc.service.NewTaskSessionWithProgress(t.Context(), NewTaskSessionInput{
		TaskID:  "task-1",
		Prompt:  "Continue the task.",
		Model:   "gpt-5",
		Effort:  "high",
		Handoff: true,
	}, reporter)

	require.NoError(t, err)
	require.Equal(t, []TaskCreateProgressStep{
		TaskCreateProgressWritingHandoff,
		TaskCreateProgressStartingSession,
	}, steps)
	require.NotNil(t, svc.providerRepo.handoffSession)
	require.Equal(t, "sess-latest", svc.providerRepo.handoffSession.ProviderSessionID,
		"the active provider's latest session writes the note")
	require.Equal(t, "Continue the task.", svc.providerRepo.handoffFocus,
		"the previous session is told what the next one will do")
	require.Equal(t, []string{"codex"}, svc.sessionClient.startedLaunch.Command)
	require.Equal(t, []string{
		ContinuationPrompt(readyTaskFixture(), "Continue the task.", "/data/rig/handoffs/task-1/note.md"),
	}, svc.sessionClient.prefilledLaunch.PrefillInput)
	require.Equal(t, "gpt-5", task.Model)
	require.Equal(t, "high", task.Effort)
	require.Equal(t, "add billing retry flow", task.Prompt, "the task keeps its original ask")
	require.Equal(t, "gpt-5", svc.taskRepo.updatedTask.Model)
	require.Equal(
		t,
		LaunchOptions{Model: "gpt-5", Effort: "high"},
		svc.providerConfig.savedLaunchDefaults[ProviderCodex],
	)
}

func TestTaskServiceNewTaskSession_RefusesWhileTheProviderIsRunning(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.listTasks = []*Task{readyTaskFixture()}
	svc.sessionClient.inspectState = TaskSessionRuntimeState{Exists: true, ActiveCommands: []string{"codex"}}

	_, err := svc.service.NewTaskSessionWithProgress(t.Context(), NewTaskSessionInput{
		TaskID: "task-1",
		Prompt: "continue",
	}, nil)

	require.ErrorIs(t, err, ErrProviderSessionActive)
	require.Nil(t, svc.sessionClient.startedTask)
	require.Nil(t, svc.taskRepo.updatedTask)
}

func TestTaskServiceNewTaskSession_SkipsTheHandoffWithoutAPreviousSession(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.listTasks = []*Task{readyTaskFixture()}
	svc.sessionClient.inspectState = idlePane()

	_, err := svc.service.NewTaskSessionWithProgress(t.Context(), NewTaskSessionInput{
		TaskID:  "task-1",
		Prompt:  "continue",
		Handoff: true,
	}, nil)

	require.NoError(t, err)
	require.Nil(t, svc.providerRepo.handoffSession)
	require.Equal(t, []string{ContinuationPrompt(readyTaskFixture(), "continue", "")},
		svc.sessionClient.prefilledLaunch.PrefillInput)
	require.NotContains(t, svc.events, "write_session_handoff")
}

func TestContinuationPrompt_ComposesTheTaskTheHandoffAndTheInstruction(t *testing.T) {
	task := &Task{DisplayName: "billing retry flow", Prompt: "add billing retry flow"}

	require.Equal(t, "Continue the task \"billing retry flow\" in a fresh session.\n\n"+
		"Original ask:\nadd billing retry flow\n\n"+
		"Read the handoff note from the previous session before doing anything else: /notes/h.md\n\n"+
		"Now:\nadd the search page next",
		ContinuationPrompt(task, "add the search page next", "/notes/h.md"))
	require.Equal(t, "Continue the task \"billing retry flow\" in a fresh session.\n\n"+
		"Original ask:\nadd billing retry flow\n\n"+
		"First recover where the work stands from the workspace: "+
		"git status, the recent commits on the branch, and the changed files.",
		ContinuationPrompt(task, "  ", ""), "no handoff and no instruction")
	require.Equal(t, "Continue the task \"billing-retry\" in a fresh session.\n\n"+
		"First recover where the work stands from the workspace: "+
		"git status, the recent commits on the branch, and the changed files.",
		ContinuationPrompt(&Task{Slug: "billing-retry"}, "", ""), "an imported task has only a slug")
}

func TestTaskServiceNewTaskSession_InANewWindowCreatesAChildTaskAlongsideTheRunningOne(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.listTasks = []*Task{readyTaskFixture()}
	// The parent's provider keeps running: nothing to exit.
	svc.sessionClient.inspectState = TaskSessionRuntimeState{Exists: true, ActiveCommands: []string{"codex"}}
	svc.providerRepo.suggestedName = "search page"

	var steps []TaskCreateProgressStep
	reporter := NewMockTaskCreateProgressReporter(t)
	reporter.EXPECT().ReportTaskCreateProgress(mock.Anything).Run(func(step TaskCreateProgressStep) {
		steps = append(steps, step)
	}).Return()

	child, err := svc.service.NewTaskSessionWithProgress(t.Context(), NewTaskSessionInput{
		TaskID:    "task-1",
		Prompt:    "add the search page",
		Model:     "gpt-5",
		NewWindow: true,
	}, reporter)

	require.NoError(t, err)
	require.Equal(t, []TaskCreateProgressStep{
		TaskCreateProgressSuggestingName,
		TaskCreateProgressPreparingWorkspace,
		TaskCreateProgressStartingSession,
	}, steps)
	require.NotEqual(t, "task-1", child.ID)
	require.Equal(t, "task-1", child.ParentID)
	require.Equal(t, "search page", child.DisplayName)
	require.Equal(t, "search-page", child.Slug)
	require.Equal(t, "s2", child.TmuxWindow)
	require.Equal(t, "repo_billing-retry-flow", child.TmuxSession, "the parent's session")
	require.Equal(t, "/tmp/repo_billing-retry-flow", child.WorktreePath, "the parent's workspace")
	require.Equal(t, "feat/billing-retry-flow", child.BranchName)
	require.Equal(t, WorkspaceKindFolder, child.WorkspaceKind, "never seeded or removed")
	require.Equal(t, TaskCreationStatusReady, child.CreationStatus)
	require.Equal(t, "gpt-5", child.Model)
	require.Equal(t, ContinuationPrompt(readyTaskFixture(), "add the search page", ""), child.Prompt,
		"the composed prompt is the child's own ask, so a retry types it again")
	require.Equal(t, child.ID, svc.sessionClient.startedTask.ID)
	require.Equal(t, []string{child.Prompt}, svc.sessionClient.prefilledLaunch.PrefillInput)
	require.False(t, svc.workspace.setupCalled)
	require.True(t, svc.workspace.bootstrapCalled)
	require.Equal(t, child.ID, svc.taskRepo.createdTask.ID)
	require.Equal(t, child.ID, svc.taskRepo.updatedTask.ID, "the parent record is untouched")
}

func TestTaskServiceNewTaskSession_InANewWindowNamesSessionsWithoutAnInstruction(t *testing.T) {
	svc := newTestTaskService(t)
	parent := readyTaskFixture()
	sibling := newChildTaskRecord(parent, ProviderCodex, "session 2", "session-2", "s2")
	sibling.ID = "task-2"
	sibling.CreationStatus = TaskCreationStatusReady
	svc.taskRepo.listTasks = []*Task{parent, sibling}
	svc.sessionClient.inspectState = idlePane()

	child, err := svc.service.NewTaskSessionWithProgress(t.Context(), NewTaskSessionInput{
		TaskID:    "task-2",
		NewWindow: true,
	}, nil)

	require.NoError(t, err)
	require.Equal(t, "task-1", child.ParentID, "a session of a child joins the same group")
	require.Equal(t, "session 3", child.DisplayName)
	require.Equal(t, "s3", child.TmuxWindow)
	require.NotContains(t, svc.events, "suggest_task_name")
}

func TestNextSessionWindow_SkipsWindowsInUse(t *testing.T) {
	require.Equal(t, "s2", nextSessionWindow([]*Task{{}}))
	require.Equal(t, "s4", nextSessionWindow([]*Task{{}, {TmuxWindow: "s2"}, {TmuxWindow: "s3"}}))
	require.Equal(t, "s3", nextSessionWindow([]*Task{{}, {TmuxWindow: "s2"}, {TmuxWindow: "s4"}}))
}

func TestTaskServiceDeleteTask_RemovesAParentsChildRecords(t *testing.T) {
	svc := newTestTaskService(t)
	parent := readyTaskFixture()
	child := newChildTaskRecord(parent, ProviderCodex, "session 2", "session-2", "s2")
	child.ID = "task-2"
	svc.taskRepo.listTasks = []*Task{parent, child}

	err := svc.service.DeleteTask(t.Context(), "task-1")

	require.NoError(t, err)
	require.Equal(t, "task-1", svc.taskRepo.deletedTaskID)
	require.Contains(t, svc.taskRepo.deletedTaskIDs, "task-2", "its window dies with the session")
}

func TestTaskServiceNewTaskSession_ReportsAProviderThatCannotWriteHandoffs(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.listTasks = []*Task{readyTaskFixture()}
	svc.sessionClient.inspectState = idlePane()
	svc.taskRepo.providerSessionsByTask["task-1"] = []TaskProviderSession{
		{TaskID: "task-1", Provider: ProviderCodex, ProviderSessionID: "sess-1", LastObservedAt: time.Unix(100, 0)},
	}
	svc.providerRepo.handoffErr = ErrHandoffUnsupported

	_, err := svc.service.NewTaskSessionWithProgress(t.Context(), NewTaskSessionInput{
		TaskID:  "task-1",
		Prompt:  "continue",
		Handoff: true,
	}, nil)

	require.ErrorIs(t, err, ErrHandoffUnsupported)
	require.Nil(t, svc.sessionClient.startedTask)
}

func TestTaskServiceNewTaskSession_CanStartAnotherConfiguredProvider(t *testing.T) {
	svc := newTestTaskService(t)
	svc.providerConfig.setup = multiProviderSetup()
	svc.taskRepo.listTasks = []*Task{readyTaskFixture()}
	svc.sessionClient.inspectState = idlePane()

	task, err := svc.service.NewTaskSessionWithProgress(t.Context(), NewTaskSessionInput{
		TaskID:   "task-1",
		Prompt:   "continue",
		Provider: ProviderClaude,
		Model:    "opus",
	}, nil)

	require.NoError(t, err)
	require.Equal(t, ProviderClaude, task.Provider)
	require.Equal(t, []string{"claude"}, svc.sessionClient.startedLaunch.Command)
	require.NotNil(t, svc.claudeRepo.bootstrapRequest, "the workspace is bootstrapped for the new provider")
	require.False(t, svc.workspace.setupCalled, "never reseeded")
	require.Equal(t, LaunchOptions{Model: "opus"}, svc.providerConfig.savedLaunchDefaults[ProviderClaude])
}

func TestTaskServiceNewTaskSession_RequiresAReadyTask(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.listTasks = []*Task{failedTaskFixture(TaskCreateProgressStartingSession)}

	_, err := svc.service.NewTaskSessionWithProgress(t.Context(), NewTaskSessionInput{
		TaskID: "task-1",
		Prompt: "continue",
	}, nil)

	require.ErrorContains(t, err, "not ready")
	require.Nil(t, svc.sessionClient.startedTask)
}
