package core_test

import (
	"testing"

	"agent/internal/core"

	"github.com/stretchr/testify/require"
)

func TestTaskStatusIsTerminal_BrokenIsTerminal(t *testing.T) {
	require.True(t, core.TaskStatusBroken.IsTerminal())
	require.False(t, core.TaskStatusRunning.IsTerminal())
	require.False(t, core.TaskStatusDegraded.IsTerminal())
}

func TestTaskSuggestion_DefaultBranchType(t *testing.T) {
	s := core.TaskSuggestion{Name: "billing retry flow"}
	require.Equal(t, "feat", s.BranchTypeOrDefault())
}

func TestTaskSuggestion_ValidBranchType(t *testing.T) {
	s := core.TaskSuggestion{Name: "billing retry flow", BranchType: "fix"}
	require.Equal(t, "fix", s.BranchTypeOrDefault())
}

func TestTaskSuggestion_InvalidBranchTypeFallsBackToFeat(t *testing.T) {
	s := core.TaskSuggestion{Name: "billing retry flow", BranchType: "banana"}
	require.Equal(t, "feat", s.BranchTypeOrDefault())
}

func TestCorePublicTypesRemainUsable(t *testing.T) {
	cfg := core.Config{Provider: "codex"}
	task := core.Task{
		DisplayName:  "billing retry flow",
		Status:       core.TaskStatusRunning,
		RuntimeState: core.RuntimeStateNeedsInput,
		Provider:     cfg.Provider,
	}
	progress := core.TaskProgress{
		Task:    &task,
		Step:    core.TaskProgressAgentLaunching,
		Message: "Launching codex...",
	}
	snapshot := core.RuntimeSnapshot{
		SessionName:       "repo_billing-retry-flow",
		WindowName:        "agent",
		ForegroundCommand: "codex",
	}
	input := core.NewTaskInput{
		Cwd:      "/tmp/repo",
		Prompt:   "fix billing retries",
		Provider: cfg.Provider,
	}
	options := core.CreateTaskOptions{OpenSession: true}
	doctor := core.DoctorResult{Notes: []string{"config ok"}}

	require.Equal(t, "billing retry flow", task.DisplayName)
	require.Equal(t, core.TaskStatusRunning, task.Status)
	require.Equal(t, core.RuntimeStateNeedsInput, task.RuntimeState)
	require.False(t, task.Status.IsTerminal())
	require.Same(t, &task, progress.Task)
	require.Equal(t, core.TaskProgressAgentLaunching, progress.Step)
	require.Equal(t, "agent", snapshot.WindowName)
	require.Equal(t, "codex", snapshot.ForegroundCommand)
	require.Equal(t, "fix billing retries", input.Prompt)
	require.True(t, options.OpenSession)
	require.Equal(t, []string{"config ok"}, doctor.Notes)
	require.ErrorIs(t, core.ErrTaskNotFound, core.ErrTaskNotFound)
}
