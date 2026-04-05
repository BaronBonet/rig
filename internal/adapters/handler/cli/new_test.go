package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"agent/internal/core"

	"github.com/stretchr/testify/require"
)

func TestNewCommand_InteractiveAcceptsSuggestedName(t *testing.T) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	in := bytes.NewBufferString("\n")
	service := &fakeNewCLIService{
		suggestedName: "billing retry flow",
		createdTask: &core.Task{
			DisplayName: "billing retry flow",
			Slug:        "billing-retry-flow",
			TmuxSession: "repo-billing-retry-flow",
		},
	}

	cmd := newNewCommand(Dependencies{
		Service: service,
		Stdout:  out,
		Stderr:  errOut,
		Cwd:     "/tmp/repo",
	})
	cmd.SetIn(in)
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"add billing retry flow"})

	err := cmd.Execute()
	require.NoError(t, err)
	require.Equal(t, "billing retry flow", service.createdInput.ConfirmedDisplayName)
	require.True(t, service.createWithProgressCalled)
	require.Contains(t, errOut.String(), "Naming task")
}

func TestNewCommand_InteractiveTreatsYesAsAcceptSuggestedName(t *testing.T) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	in := bytes.NewBufferString("yes\n")
	service := &fakeNewCLIService{
		suggestedName: "billing retry flow",
		createdTask: &core.Task{
			DisplayName: "billing retry flow",
			Slug:        "billing-retry-flow",
			TmuxSession: "repo-billing-retry-flow",
		},
	}

	cmd := newNewCommand(Dependencies{
		Service: service,
		Stdout:  out,
		Stderr:  errOut,
		Cwd:     "/tmp/repo",
	})
	cmd.SetIn(in)
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"add billing retry flow"})

	err := cmd.Execute()
	require.NoError(t, err)
	require.Equal(t, "billing retry flow", service.createdInput.ConfirmedDisplayName)
	require.True(t, service.createWithProgressCalled)
}

func TestNewCommand_PrintsProgressAndUsesProgressCreatePath(t *testing.T) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	service := &fakeNewCLIService{
		createdTask: &core.Task{
			DisplayName: "billing retry flow",
			Slug:        "billing-retry-flow",
			TmuxSession: "repo-billing-retry-flow",
		},
		progressEvents: []core.TaskProgress{
			{Step: core.TaskProgressNameSelected, Message: "Selected name: billing retry flow"},
			{Step: core.TaskProgressWorktreeCreating, Message: "Creating worktree..."},
			{Step: core.TaskProgressWorkspaceSeeding, Message: "Seeding workspace..."},
			{Step: core.TaskProgressWorkspaceSeeded, Message: "Copied .env"},
			{Step: core.TaskProgressWorkspaceSeeded, Message: "Copied local/"},
			{Step: core.TaskProgressTmuxStarting, Message: "Starting tmux session..."},
			{Step: core.TaskProgressAgentLaunching, Message: "Launching codex..."},
			{
				Step:    core.TaskProgressTaskCreated,
				Message: "Created task billing retry flow in session repo-billing-retry-flow",
			},
			{Step: core.TaskProgressSessionOpening, Message: "Opening tmux session..."},
		},
	}

	cmd := newNewCommand(Dependencies{
		Service: service,
		Stdout:  out,
		Stderr:  errOut,
		Cwd:     "/tmp/repo",
	})
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"--non-interactive", "add billing retry flow"})

	err := cmd.Execute()
	require.NoError(t, err)
	require.True(t, service.createWithProgressCalled)
	require.False(t, service.newTaskCalled)
	require.True(t, service.createOptions.OpenSession)
	require.Contains(t, errOut.String(), "Selected name: billing retry flow")
	require.Contains(t, errOut.String(), "Creating worktree...")
	require.Contains(t, errOut.String(), "Seeding workspace...")
	require.Contains(t, errOut.String(), "Copied .env")
	require.Contains(t, errOut.String(), "Copied local/")
	require.Contains(t, errOut.String(), "Opening tmux session...")
	require.Empty(t, out.String())
}

func TestNewCommand_JSONModePrintsTask(t *testing.T) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	service := &fakeNewCLIService{
		suggestedName: "billing retry flow",
		createdTask: &core.Task{
			DisplayName: "billing retry flow",
			Slug:        "billing-retry-flow",
			TmuxSession: "repo-billing-retry-flow",
		},
		progressEvents: []core.TaskProgress{
			{Step: core.TaskProgressNameSelected, Message: "Selected name: billing retry flow"},
			{Step: core.TaskProgressWorktreeCreating, Message: "Creating worktree..."},
			{
				Step:    core.TaskProgressTaskCreated,
				Message: "Created task billing retry flow in session repo-billing-retry-flow",
			},
		},
	}

	cmd := newNewCommand(Dependencies{
		Service: service,
		Stdout:  out,
		Stderr:  errOut,
		Cwd:     "/tmp/repo",
	})
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs([]string{"--non-interactive", "--json", "add billing retry flow"})

	err := cmd.Execute()
	require.NoError(t, err)

	var task core.Task
	require.NoError(t, json.Unmarshal(out.Bytes(), &task))
	require.Equal(t, "billing-retry-flow", task.Slug)
	require.True(t, service.createWithProgressCalled)
	require.False(t, service.createOptions.OpenSession)
	require.Contains(t, errOut.String(), "Creating worktree...")
}

type fakeNewCLIService struct {
	createErr                error
	createdTask              *core.Task
	createdInput             core.NewTaskInput
	suggestedName            string
	progressEvents           []core.TaskProgress
	newTaskCalled            bool
	createWithProgressCalled bool
	createOptions            core.CreateTaskOptions
}

func (f *fakeNewCLIService) Doctor(context.Context, string) (core.DoctorResult, error) {
	return core.DoctorResult{}, nil
}

func (f *fakeNewCLIService) SuggestTaskName(context.Context, string) (string, error) {
	return f.suggestedName, nil
}

func (f *fakeNewCLIService) NewTask(_ context.Context, input core.NewTaskInput) (*core.Task, error) {
	f.newTaskCalled = true
	f.createdInput = input
	return f.createdTask, f.createErr
}

func (f *fakeNewCLIService) CreateTaskWithProgress(
	_ context.Context,
	input core.NewTaskInput,
	options core.CreateTaskOptions,
	progress func(core.TaskProgress),
) (*core.Task, error) {
	f.createWithProgressCalled = true
	f.createdInput = input
	f.createOptions = options
	for _, event := range f.progressEvents {
		progress(event)
	}
	return f.createdTask, f.createErr
}

func (*fakeNewCLIService) ListTasks(context.Context) ([]*core.Task, error) { return nil, nil }

func (*fakeNewCLIService) GetTask(context.Context, string) (*core.Task, error) { return nil, nil }

func (*fakeNewCLIService) OpenTask(context.Context, string) error { return nil }

func (*fakeNewCLIService) DeleteTaskResources(context.Context, string) (*core.Task, error) {
	return nil, nil
}
