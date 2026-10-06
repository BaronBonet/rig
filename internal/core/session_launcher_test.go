package core

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSessionLauncher_TypesThePromptOnceTheProviderReportsItsSession(t *testing.T) {
	svc := newTestTaskService(t)
	svc.launcher.providerSessionWait = 2 * time.Second
	svc.launcher.sessionPollInterval = time.Millisecond
	svc.providerRepo.suggestedName = "billing retry flow"
	// The provider's session-start hook arrives some polls after the launch.
	svc.sessionClient.onStart = func(task *Task) {
		go func() {
			time.Sleep(20 * time.Millisecond)
			svc.taskRepo.mu.Lock()
			defer svc.taskRepo.mu.Unlock()
			svc.taskRepo.providerSessionsByTask[task.ID] = append(svc.taskRepo.providerSessionsByTask[task.ID],
				TaskProviderSession{TaskID: task.ID, Provider: ProviderCodex, ProviderSessionID: "sess-new"})
		}()
	}

	_, err := svc.service.CreateTaskWithProgress(t.Context(), CreateTaskInput{
		Cwd:    "/tmp/repo",
		Prompt: "add billing retry flow",
	}, nil)

	require.NoError(t, err)
	require.Equal(t, []string{"add billing retry flow"}, svc.sessionClient.prefilledLaunch.PrefillInput)
	require.Less(t, indexOf(svc.events, "start_task_session"), indexOf(svc.events, "prefill_task_session"))
}

func TestSessionLauncher_FinishesTheLaunchWhenTheProviderStaysSilent(t *testing.T) {
	svc := newTestTaskService(t)
	svc.launcher.providerSessionWait = 5 * time.Millisecond
	svc.launcher.sessionPollInterval = time.Millisecond
	svc.providerRepo.suggestedName = "billing retry flow"

	task, err := svc.service.CreateTaskWithProgress(t.Context(), CreateTaskInput{
		Cwd:    "/tmp/repo",
		Prompt: "add billing retry flow",
	}, nil)

	require.NoError(t, err)
	require.Equal(t, TaskCreationStatusReady, task.CreationStatus)
	require.Contains(t, svc.events, "start_task_session")
	require.NotContains(t, svc.events, "prefill_task_session",
		"with no session reported the prompt is left for the background wait")
}

func indexOf(values []string, target string) int {
	for index, value := range values {
		if value == target {
			return index
		}
	}
	return -1
}
