package cli

import (
	"bytes"
	"context"
	"testing"

	"agent/internal/core"

	"github.com/stretchr/testify/require"
)

func TestNewRootCommand_HelpIncludesTUI(t *testing.T) {
	out := &bytes.Buffer{}

	cmd := NewRootCommand(Dependencies{})
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"--help"})

	err := cmd.Execute()
	require.NoError(t, err)
	require.Contains(t, out.String(), "tui")
}

func TestNewTUICommand_ReturnsErrorWhenServiceNotConfigured(t *testing.T) {
	cmd := newTUICommand(Dependencies{})
	cmd.SetIn(bytes.NewBufferString("q"))
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err := cmd.Execute()
	require.EqualError(t, err, "service not configured")
}

func TestNewTUICommand_RunsAndQuitsWithMinimalService(t *testing.T) {
	out := &bytes.Buffer{}
	service := &fakeTUIService{}

	cmd := newTUICommand(Dependencies{Service: service, Stdout: out, Stderr: out})
	cmd.SetIn(bytes.NewBufferString("q"))
	cmd.SetOut(out)
	cmd.SetErr(out)

	err := cmd.Execute()
	require.NoError(t, err)
	require.Equal(t, 1, service.listCalls)
}

func (fakeCLIService) CreateTaskWithProgress(context.Context, core.NewTaskInput, core.CreateTaskOptions, func(core.TaskProgress)) (*core.Task, error) {
	return nil, nil
}

func (fakeListCLIService) CreateTaskWithProgress(context.Context, core.NewTaskInput, core.CreateTaskOptions, func(core.TaskProgress)) (*core.Task, error) {
	return nil, nil
}

func (*fakeOpenCLIService) CreateTaskWithProgress(context.Context, core.NewTaskInput, core.CreateTaskOptions, func(core.TaskProgress)) (*core.Task, error) {
	return nil, nil
}

func (fakeStatusCLIService) CreateTaskWithProgress(context.Context, core.NewTaskInput, core.CreateTaskOptions, func(core.TaskProgress)) (*core.Task, error) {
	return nil, nil
}
