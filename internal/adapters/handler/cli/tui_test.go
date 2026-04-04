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
