package cli

import (
	"bytes"
	"context"
	"testing"

	"agent/internal/core"

	"github.com/stretchr/testify/require"
)

func TestListCommand_PrintsTaskTable(t *testing.T) {
	out := &bytes.Buffer{}
	service := fakeListCLIService{
		tasks: []*core.Task{{
			DisplayName: "billing retry flow",
			Provider:    "codex",
			Status:      core.TaskStatusRunning,
			TmuxSession: "repo-billing-retry-flow",
			BranchName:  "feat/billing-retry-flow",
		}},
	}

	cmd := newListCommand(Dependencies{Service: service, Stdout: out, Stderr: out})
	cmd.SetOut(out)
	cmd.SetErr(out)

	err := cmd.Execute()
	require.NoError(t, err)
	require.Contains(t, out.String(), "billing retry flow")
	require.Contains(t, out.String(), "feat/billing-retry-flow")
}

type fakeListCLIService struct {
	tasks []*core.Task
}

func (f fakeListCLIService) Doctor(context.Context, string) (core.DoctorResult, error) {
	return core.DoctorResult{}, nil
}
func (f fakeListCLIService) SuggestTaskName(context.Context, string) (string, error) { return "", nil }
func (f fakeListCLIService) NewTask(context.Context, core.NewTaskInput) (*core.Task, error) {
	return nil, nil
}
func (f fakeListCLIService) ListTasks(context.Context) ([]*core.Task, error)     { return f.tasks, nil }
func (f fakeListCLIService) GetTask(context.Context, string) (*core.Task, error) { return nil, nil }
func (f fakeListCLIService) OpenTask(context.Context, string) error              { return nil }
func (f fakeListCLIService) DeleteTaskResources(context.Context, string) (*core.Task, error) {
	return nil, nil
}
