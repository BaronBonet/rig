package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"agent/internal/core"

	"github.com/stretchr/testify/require"
)

func TestNewRootCommand_HelpOnlyIncludesDoctorSubcommand(t *testing.T) {
	out := &bytes.Buffer{}

	cmd := NewRootCommand(Dependencies{})
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"--help"})

	err := cmd.Execute()
	require.NoError(t, err)

	output := out.String()
	require.Contains(t, output, "doctor")
	require.NotContains(t, output, "new")
	require.NotContains(t, output, "ls")
	require.NotContains(t, output, "open")
	require.NotContains(t, output, "status")
	require.NotContains(t, output, "tui")
}

func TestNewRootCommand_RunsTUIWhenNoArgsProvided(t *testing.T) {
	out := &bytes.Buffer{}
	service := &fakeTUIService{}

	cmd := NewRootCommand(Dependencies{Service: service, Stdout: out, Stderr: out})
	cmd.SetIn(strings.NewReader("q"))
	cmd.SetOut(out)
	cmd.SetErr(out)

	err := cmd.Execute()
	require.NoError(t, err)
	require.Equal(t, 1, service.listCalls)
}

func TestNewRootCommand_DoctorDispatchBypassesRootTUI(t *testing.T) {
	out := &bytes.Buffer{}
	service := &rootDoctorDispatchService{}

	cmd := NewRootCommand(Dependencies{Service: service, Stdout: out, Stderr: out})
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"doctor"})

	err := cmd.Execute()
	require.NoError(t, err)
	require.True(t, service.doctorCalled)
	require.Contains(t, out.String(), "doctor: ok")
}

type rootDoctorDispatchService struct {
	doctorCalled bool
}

func (s *rootDoctorDispatchService) Doctor(_ context.Context, _ string) (core.DoctorResult, error) {
	s.doctorCalled = true
	return core.DoctorResult{Notes: []string{"doctor: ok"}}, nil
}

func (*rootDoctorDispatchService) SuggestTaskName(context.Context, string, string) (string, error) {
	return "", nil
}

func (*rootDoctorDispatchService) CreateTaskWithProgress(
	context.Context,
	core.NewTaskInput,
	core.CreateTaskOptions,
	func(core.TaskProgress),
) (*core.Task, error) {
	return nil, nil
}

func (*rootDoctorDispatchService) ListTasks(context.Context) ([]*core.Task, error) {
	panic("root TUI must not run for doctor dispatch")
}

func (*rootDoctorDispatchService) OpenTask(context.Context, string) error { return nil }

func (*rootDoctorDispatchService) DeleteTaskResources(context.Context, string) (*core.Task, error) {
	return nil, nil
}
