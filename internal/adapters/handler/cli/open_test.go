package cli

import (
	"bytes"
	"context"
	"testing"

	"agent/internal/core"

	"github.com/stretchr/testify/require"
)

func TestOpenCommand_CallsService(t *testing.T) {
	out := &bytes.Buffer{}
	service := &fakeOpenCLIService{}
	cmd := newOpenCommand(Dependencies{Service: service, Stdout: out, Stderr: out})
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"billing-retry-flow"})

	err := cmd.Execute()
	require.NoError(t, err)
	require.Equal(t, "billing-retry-flow", service.openedSlug)
}

type fakeOpenCLIService struct {
	openedSlug string
}

func (*fakeOpenCLIService) Doctor(context.Context, string) (core.DoctorResult, error) {
	return core.DoctorResult{}, nil
}
func (*fakeOpenCLIService) SuggestTaskName(context.Context, string) (string, error) { return "", nil }
func (*fakeOpenCLIService) NewTask(context.Context, core.NewTaskInput) (*core.Task, error) {
	return nil, nil
}
func (*fakeOpenCLIService) ListTasks(context.Context) ([]*core.Task, error) { return nil, nil }
func (*fakeOpenCLIService) GetTask(context.Context, string) (*core.Task, error) { return nil, nil }
func (f *fakeOpenCLIService) OpenTask(_ context.Context, slug string) error {
	f.openedSlug = slug
	return nil
}
