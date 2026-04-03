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
	in := bytes.NewBufferString("\n")
	service := &fakeNewCLIService{
		suggestedName: "billing retry flow",
		newTask: &core.Task{
			DisplayName: "billing retry flow",
			Slug:        "billing-retry-flow",
			TmuxSession: "repo-billing-retry-flow",
		},
	}

	cmd := newNewCommand(Dependencies{
		Service: service,
		Stdout:  out,
		Stderr:  out,
		Cwd:     "/tmp/repo",
	})
	cmd.SetIn(in)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"add billing retry flow"})

	err := cmd.Execute()
	require.NoError(t, err)
	require.Equal(t, "billing retry flow", service.newTaskInput.ConfirmedDisplayName)
	require.Contains(t, out.String(), "repo-billing-retry-flow")
}

func TestNewCommand_InteractiveTreatsYesAsAcceptSuggestedName(t *testing.T) {
	out := &bytes.Buffer{}
	in := bytes.NewBufferString("yes\n")
	service := &fakeNewCLIService{
		suggestedName: "billing retry flow",
		newTask: &core.Task{
			DisplayName: "billing retry flow",
			Slug:        "billing-retry-flow",
			TmuxSession: "repo-billing-retry-flow",
		},
	}

	cmd := newNewCommand(Dependencies{
		Service: service,
		Stdout:  out,
		Stderr:  out,
		Cwd:     "/tmp/repo",
	})
	cmd.SetIn(in)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"add billing retry flow"})

	err := cmd.Execute()
	require.NoError(t, err)
	require.Equal(t, "billing retry flow", service.newTaskInput.ConfirmedDisplayName)
}

func TestNewCommand_JSONModePrintsTask(t *testing.T) {
	out := &bytes.Buffer{}
	service := &fakeNewCLIService{
		suggestedName: "billing retry flow",
		newTask: &core.Task{
			DisplayName: "billing retry flow",
			Slug:        "billing-retry-flow",
			TmuxSession: "repo-billing-retry-flow",
		},
	}

	cmd := newNewCommand(Dependencies{
		Service: service,
		Stdout:  out,
		Stderr:  out,
		Cwd:     "/tmp/repo",
	})
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"--non-interactive", "--json", "add billing retry flow"})

	err := cmd.Execute()
	require.NoError(t, err)

	var task core.Task
	require.NoError(t, json.Unmarshal(out.Bytes(), &task))
	require.Equal(t, "billing-retry-flow", task.Slug)
}

type fakeNewCLIService struct {
	suggestedName string
	newTask       *core.Task
	newTaskErr    error
	newTaskInput  core.NewTaskInput
}

func (f *fakeNewCLIService) Doctor(context.Context, string) (core.DoctorResult, error) {
	return core.DoctorResult{}, nil
}

func (f *fakeNewCLIService) SuggestTaskName(context.Context, string) (string, error) {
	return f.suggestedName, nil
}

func (f *fakeNewCLIService) NewTask(_ context.Context, input core.NewTaskInput) (*core.Task, error) {
	f.newTaskInput = input
	return f.newTask, f.newTaskErr
}

func (*fakeNewCLIService) ListTasks(context.Context) ([]*core.Task, error) { return nil, nil }

func (*fakeNewCLIService) GetTask(context.Context, string) (*core.Task, error) { return nil, nil }

func (*fakeNewCLIService) OpenTask(context.Context, string) error { return nil }

func (*fakeNewCLIService) DeleteTaskResources(context.Context, string) (*core.Task, error) {
	return nil, nil
}
