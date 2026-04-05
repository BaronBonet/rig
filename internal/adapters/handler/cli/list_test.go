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
			DisplayName:        "billing retry flow",
			RepoName:           "repo",
			Provider:           "codex",
			Status:             core.TaskStatusDegraded,
			AgentWindowExists:  true,
			EditorWindowExists: false,
			TmuxSession:        "repo-billing-retry-flow",
			BranchName:         "feat/billing-retry-flow",
		}},
	}

	cmd := newListCommand(Dependencies{Service: service, Stdout: out, Stderr: out})
	cmd.SetOut(out)
	cmd.SetErr(out)

	err := cmd.Execute()
	require.NoError(t, err)
	require.Equal(
		t,
		"NAME\tREPO\tPROVIDER\tSTATUS\tAGENT\tEDITOR\tSESSION\tBRANCH\nbilling retry flow\trepo\tcodex\tdegraded\ttrue\tfalse\trepo-billing-retry-flow\tfeat/billing-retry-flow\n",
		out.String(),
	)
}

type fakeListCLIService struct {
	tasks []*core.Task
}

func (f fakeListCLIService) Doctor(context.Context, string) (core.DoctorResult, error) {
	return core.DoctorResult{}, nil
}
func (f fakeListCLIService) SuggestTaskName(context.Context, string, string) (string, error) {
	return "", nil
}
func (f fakeListCLIService) NewTask(context.Context, core.NewTaskInput) (*core.Task, error) {
	return nil, nil
}
func (f fakeListCLIService) ListTasks(context.Context) ([]*core.Task, error)     { return f.tasks, nil }
func (f fakeListCLIService) GetTask(context.Context, string) (*core.Task, error) { return nil, nil }
func (f fakeListCLIService) OpenTask(context.Context, string) error              { return nil }
func (f fakeListCLIService) DeleteTaskResources(context.Context, string) (*core.Task, error) {
	return nil, nil
}
