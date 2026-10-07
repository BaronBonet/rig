package core

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const renameTranscript = "/tmp/sessions/sess-1.jsonl"

func renameHookEvent(eventName string) HookEventInput {
	return HookEventInput{
		OccurredAt:     time.Now().UTC(),
		EventName:      eventName,
		Provider:       ProviderCodex,
		TaskID:         "task-1",
		SessionID:      "sess-1",
		TranscriptPath: renameTranscript,
	}
}

func TestTaskServiceRenameTask_RenamesAndDropsTheOriginalAsk(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.listTasks = []*Task{codexTaskFixture()}

	require.NoError(t, svc.service.RenameTask(t.Context(), "task-1", "  flaky test investigation "))

	renamed := svc.taskRepo.updatedTask
	require.NotNil(t, renamed)
	require.Equal(t, "flaky test investigation", renamed.DisplayName)
	require.Empty(t, renamed.Prompt)
	// The workspace keeps its name, so the tmux session and branch stay put.
	require.Equal(t, "repo_task", renamed.TmuxSession)
}

func TestTaskServiceRenameTask_KeepsTheAskWhenTheNameIsUnchanged(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.listTasks = []*Task{codexTaskFixture()}

	require.NoError(t, svc.service.RenameTask(t.Context(), "task-1", "billing retry flow"))

	require.Nil(t, svc.taskRepo.updatedTask)
}

func TestTaskServiceRenameTask_Refuses(t *testing.T) {
	t.Run("an empty name", func(t *testing.T) {
		svc := newTestTaskService(t)
		svc.taskRepo.listTasks = []*Task{codexTaskFixture()}

		require.ErrorContains(t, svc.service.RenameTask(t.Context(), "task-1", "  "), "needs a name")
		require.Nil(t, svc.taskRepo.updatedTask)
	})

	t.Run("a task still being created", func(t *testing.T) {
		svc := newTestTaskService(t)
		task := codexTaskFixture()
		task.CreationStatus = TaskCreationStatusCreating
		svc.taskRepo.listTasks = []*Task{task}

		require.ErrorContains(t, svc.service.RenameTask(t.Context(), "task-1", "other"), "still being created")
		require.Nil(t, svc.taskRepo.updatedTask)
	})
}

func TestTaskServiceHandleHookEvent_AdoptsANewSessionTitle(t *testing.T) {
	for _, eventName := range []string{HookEventSessionStart, HookEventUserPromptSubmit, HookEventStop} {
		t.Run(eventName, func(t *testing.T) {
			svc := newTestTaskService(t)
			svc.taskRepo.listTasks = []*Task{codexTaskFixture()}
			svc.providerRepo.sessionTitles = map[string]string{renameTranscript: "flaky-test-investigation"}

			require.NoError(t, svc.service.HandleHookEvent(t.Context(), renameHookEvent(eventName)))

			renamed := svc.taskRepo.updatedTask
			require.NotNil(t, renamed)
			require.Equal(t, "flaky-test-investigation", renamed.DisplayName)
			require.Equal(t, "flaky-test-investigation", renamed.SessionTitle)
			require.Empty(t, renamed.Prompt)
		})
	}
}

func TestTaskServiceHandleHookEvent_KeepsARenameMadeInRig(t *testing.T) {
	svc := newTestTaskService(t)
	task := codexTaskFixture()
	task.DisplayName = "named in rig"
	task.SessionTitle = "flaky-test-investigation"
	svc.taskRepo.listTasks = []*Task{task}
	svc.providerRepo.sessionTitles = map[string]string{renameTranscript: "flaky-test-investigation"}

	require.NoError(t, svc.service.HandleHookEvent(t.Context(), renameHookEvent(HookEventStop)))

	require.Nil(t, svc.taskRepo.updatedTask)
}

func TestTaskServiceHandleHookEvent_SessionNamedLikeItsTaskKeepsTheAsk(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.listTasks = []*Task{codexTaskFixture()}
	svc.providerRepo.sessionTitles = map[string]string{renameTranscript: "billing retry flow"}

	require.NoError(t, svc.service.HandleHookEvent(t.Context(), renameHookEvent(HookEventStop)))

	recorded := svc.taskRepo.updatedTask
	require.NotNil(t, recorded)
	require.Equal(t, "billing retry flow", recorded.SessionTitle)
	require.Equal(t, "fix billing retry flow", recorded.Prompt)
}

func TestTaskServiceHandleHookEvent_SkipsTitlesOnToolEventsAndUntitledSessions(t *testing.T) {
	svc := newTestTaskService(t)
	svc.taskRepo.listTasks = []*Task{codexTaskFixture()}

	require.NoError(t, svc.service.HandleHookEvent(t.Context(), renameHookEvent(HookEventPreToolUse)))
	require.Empty(t, svc.providerRepo.sessionTitleReads)

	require.NoError(t, svc.service.HandleHookEvent(t.Context(), renameHookEvent(HookEventStop)))
	require.Equal(t, []string{renameTranscript}, svc.providerRepo.sessionTitleReads)
	require.Nil(t, svc.taskRepo.updatedTask)
}
