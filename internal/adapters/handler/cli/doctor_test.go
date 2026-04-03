package cli

import (
	"bytes"
	"context"
	"testing"

	"agent/internal/core"

	"github.com/stretchr/testify/require"
)

func TestDoctorCommand_PrintsFailures(t *testing.T) {
	out := &bytes.Buffer{}
	cmd := newDoctorCommand(Dependencies{
		Service: fakeCLIService{
			doctorResult: core.DoctorResult{
				Failures: []string{"codex: missing codex"},
			},
		},
		Stdout: out,
		Stderr: out,
		Cwd:    "/tmp/repo",
	})
	cmd.SetOut(out)
	cmd.SetErr(out)

	err := cmd.Execute()
	require.NoError(t, err)
	require.Contains(t, out.String(), "codex: missing codex")
}

type fakeCLIService struct {
	doctorResult core.DoctorResult
	doctorErr    error
}

func (f fakeCLIService) Doctor(context.Context, string) (core.DoctorResult, error) {
	return f.doctorResult, f.doctorErr
}

func (fakeCLIService) SuggestTaskName(context.Context, string) (string, error) {
	return "", nil
}

func (fakeCLIService) NewTask(context.Context, core.NewTaskInput) (*core.Task, error) {
	return nil, nil
}

func (fakeCLIService) ListTasks(context.Context) ([]*core.Task, error) { return nil, nil }

func (fakeCLIService) GetTask(context.Context, string) (*core.Task, error) { return nil, nil }

func (fakeCLIService) OpenTask(context.Context, string) error { return nil }

func (fakeCLIService) DeleteTaskResources(context.Context, string) (*core.Task, error) {
	return nil, nil
}
