package cli

import (
	"bytes"
	"context"
	"testing"

	"agent/internal/core"

	"github.com/stretchr/testify/require"
)

func TestStatusCommand_PrintsTaskDetails(t *testing.T) {
	out := &bytes.Buffer{}
	service := fakeStatusCLIService{
		task: &core.Task{
			DisplayName:        "billing retry flow",
			Slug:               "billing-retry-flow",
			RepoName:           "repo",
			Status:             core.TaskStatusDegraded,
			WorktreePath:       "/tmp/repo-billing-retry-flow",
			TmuxSession:        "repo-billing-retry-flow",
			AgentWindowName:    "agent",
			EditorWindowName:   "editor",
			WorktreeExists:     true,
			BranchExists:       true,
			SessionExists:      true,
			AgentWindowExists:  true,
			EditorWindowExists: false,
		},
	}

	cmd := newStatusCommand(Dependencies{Service: service, Stdout: out, Stderr: out})
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{"billing-retry-flow"})

	err := cmd.Execute()
	require.NoError(t, err)
	require.Equal(
		t,
		"Name: billing retry flow\n"+
			"Slug: billing-retry-flow\n"+
			"Repo: repo\n"+
			"Status: degraded\n"+
			"Session: repo-billing-retry-flow\n"+
			"AgentWindow: agent\n"+
			"EditorWindow: editor\n"+
			"Worktree: /tmp/repo-billing-retry-flow\n"+
			"WorktreeExists: true\n"+
			"BranchExists: true\n"+
			"SessionExists: true\n"+
			"AgentWindowExists: true\n"+
			"EditorWindowExists: false\n",
		out.String(),
	)
}

type fakeStatusCLIService struct {
	task *core.Task
}

func (f fakeStatusCLIService) Doctor(context.Context, string) (core.DoctorResult, error) {
	return core.DoctorResult{}, nil
}
func (f fakeStatusCLIService) SuggestTaskName(context.Context, string) (string, error) {
	return "", nil
}
func (f fakeStatusCLIService) NewTask(context.Context, core.NewTaskInput) (*core.Task, error) {
	return nil, nil
}
func (f fakeStatusCLIService) ListTasks(context.Context) ([]*core.Task, error) { return nil, nil }
func (f fakeStatusCLIService) GetTask(context.Context, string) (*core.Task, error) {
	return f.task, nil
}
func (f fakeStatusCLIService) OpenTask(context.Context, string) error { return nil }
func (f fakeStatusCLIService) DeleteTaskResources(context.Context, string) (*core.Task, error) {
	return nil, nil
}
