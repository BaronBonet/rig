package core

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestSessionLauncher_PrefillsOnTheInputUIBeforeAnySessionHook(t *testing.T) {
	svc := newTestTaskService(t)
	svc.launcher.providerSessionWait = time.Second
	svc.launcher.sessionPollInterval = time.Millisecond
	svc.sessionClient.inspectState = TaskSessionRuntimeState{Exists: true, ActiveCommands: []string{"codex"}}
	svc.providerRepo.launchRequest = TaskSessionLaunchSpec{
		Command:      []string{"codex", "-m", "gpt-5", "-c", "model_reasoning_effort=high"},
		ReadyMarker:  "›",
		ReadyPattern: regexp.MustCompile(`(?m)^OpenAI Codex\n› `),
		PrefillInput: []string{"fix the retry loop"},
	}
	svc.sessionClientMock.EXPECT().CaptureTaskSession(mock.Anything, mock.Anything).
		Return("shell launching codex\n", nil).Once()
	svc.sessionClientMock.EXPECT().CaptureTaskSession(mock.Anything, mock.Anything).
		Return("OpenAI Codex\n› Ask Codex to do anything\n", nil).Once()

	_, err := svc.launcher.startSession(t.Context(), readyTaskFixture())

	require.NoError(t, err)
	require.Equal(t, svc.providerRepo.launchRequest, svc.sessionClient.prefilledLaunch)
	require.Empty(t, svc.sessionClient.startedLaunch.PrefillInput, "only paste after the input UI appears")
	require.Empty(t, svc.taskRepo.providerSessionsByTask, "no session hook or manual prompt was needed")
	require.Less(t, indexOf(svc.events, "start_task_session"), indexOf(svc.events, "prefill_task_session"))
}

func TestSessionLauncher_DoesNotPrefillFromAnUnreadyOrStaleInputUI(t *testing.T) {
	for name, tc := range map[string]struct {
		content string
		err     error
		runtime TaskSessionRuntimeState
	}{
		"startup dialog": {content: "Do you trust this directory?\n› 1. Yes\n"},
		"capture failed": {err: errors.New("pane gone")},
		"provider exited": {
			content: "OpenAI Codex\n› Ask Codex to do anything\n",
			runtime: TaskSessionRuntimeState{Exists: true, ActiveCommands: []string{"zsh"}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			svc := newTestTaskService(t)
			svc.launcher.providerSessionWait = time.Millisecond
			svc.launcher.sessionPollInterval = time.Millisecond
			svc.sessionClient.inspectState = tc.runtime
			svc.providerRepo.launchRequest = TaskSessionLaunchSpec{
				Command:      []string{"codex"},
				ReadyMarker:  "›",
				ReadyPattern: regexp.MustCompile(`(?m)^OpenAI Codex\n› `),
				PrefillInput: []string{"fix the retry loop"},
			}
			svc.sessionClientMock.EXPECT().CaptureTaskSession(mock.Anything, mock.Anything).
				Return(tc.content, tc.err)

			_, err := svc.launcher.startSession(t.Context(), readyTaskFixture())

			require.NoError(t, err)
			require.NotContains(t, svc.events, "prefill_task_session")
		})
	}
}

func TestSessionLauncher_PrefillsOnceADelayedInputUIAppears(t *testing.T) {
	svc := newTestTaskService(t)
	svc.launcher.providerSessionWait = time.Millisecond
	svc.launcher.providerSessionLateWait = time.Second
	svc.launcher.sessionPollInterval = time.Millisecond
	svc.sessionClient.inspectState = TaskSessionRuntimeState{Exists: true, ActiveCommands: []string{"codex"}}
	svc.providerRepo.launchRequest = TaskSessionLaunchSpec{
		Command:      []string{"codex"},
		ReadyMarker:  "›",
		ReadyPattern: regexp.MustCompile(`(?m)^OpenAI Codex\n› `),
		PrefillInput: []string{"fix the retry loop"},
	}
	ready := make(chan struct{})
	prefilled := make(chan TaskSessionLaunchSpec, 1)
	session := NewMockTmuxSessionClient(t)
	svc.launcher.tmuxSession = session
	session.EXPECT().StartTaskSession(mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	session.EXPECT().CaptureTaskSession(mock.Anything, mock.Anything).RunAndReturn(
		func(context.Context, *Task) (string, error) {
			select {
			case <-ready:
				return "OpenAI Codex\n› Ask Codex to do anything\n", nil
			default:
				return "Do you trust this directory?\n› 1. Yes\n", nil
			}
		},
	)
	session.EXPECT().InspectTaskSession(mock.Anything, mock.Anything).
		Return(svc.sessionClient.inspectState, nil)
	session.EXPECT().PrefillTaskSession(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _ *Task, launch TaskSessionLaunchSpec) error {
			prefilled <- launch
			return nil
		}).Once()

	_, err := svc.launcher.startSession(t.Context(), readyTaskFixture())

	require.NoError(t, err)
	select {
	case <-prefilled:
		t.Fatal("prefilled before the input UI was ready")
	default:
	}
	close(ready)
	select {
	case launch := <-prefilled:
		require.Equal(t, svc.providerRepo.launchRequest, launch)
	case <-time.After(time.Second):
		t.Fatal("the background wait did not deliver the draft")
	}
}

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
