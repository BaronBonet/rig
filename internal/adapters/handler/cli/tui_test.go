package cli

import (
	"bytes"
	"context"
	"testing"

	"agent/internal/core"

	"github.com/stretchr/testify/require"
)

func TestNewRootCommand_ReturnsErrorWhenServiceNotConfigured(t *testing.T) {
	cmd := NewRootCommand(Dependencies{})
	cmd.SetIn(bytes.NewBufferString("q"))
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err := cmd.Execute()
	require.EqualError(t, err, "service not configured")
}

func (fakeCLIService) CreateTaskWithProgress(
	context.Context,
	core.NewTaskInput,
	core.CreateTaskOptions,
	func(core.TaskProgress),
) (*core.Task, error) {
	return nil, nil
}

func (fakeListCLIService) CreateTaskWithProgress(
	context.Context,
	core.NewTaskInput,
	core.CreateTaskOptions,
	func(core.TaskProgress),
) (*core.Task, error) {
	return nil, nil
}

func (*fakeOpenCLIService) CreateTaskWithProgress(
	context.Context,
	core.NewTaskInput,
	core.CreateTaskOptions,
	func(core.TaskProgress),
) (*core.Task, error) {
	return nil, nil
}

func (fakeStatusCLIService) CreateTaskWithProgress(
	context.Context,
	core.NewTaskInput,
	core.CreateTaskOptions,
	func(core.TaskProgress),
) (*core.Task, error) {
	return nil, nil
}
