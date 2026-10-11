package cli_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/BaronBonet/rig/internal/core"
)

const (
	addRetriesID   = "1760000000000000001"
	callEndpointID = "1760000000000000002"
	fixLoginID     = "1760000000000000003"
	writeDocsID    = "1760000000000000004"
)

// linkedTasks are a backend task and the two frontend tasks it spawned,
// beside a backend task whose creation failed, in the order ListTasks
// returns them.
func linkedTasks() []*core.Task {
	return []*core.Task{
		{
			ID:             addRetriesID,
			Slug:           "add-retries",
			DisplayName:    "Add retries",
			RepoRoot:       "/work/backend",
			RepoName:       "backend",
			BranchName:     "feat/add-retries",
			WorktreePath:   "/work/backend_add-retries",
			TmuxSession:    "backend_add-retries",
			Provider:       core.ProviderClaude,
			CreationStatus: core.TaskCreationStatusReady,
		},
		{
			ID:             callEndpointID,
			Slug:           "call-the-new-endpoint",
			DisplayName:    "Call the new endpoint",
			RepoRoot:       "/work/frontend",
			RepoName:       "frontend",
			BranchName:     "feat/call-the-new-endpoint",
			WorktreePath:   "/work/frontend_call-the-new-endpoint",
			TmuxSession:    "frontend_call-the-new-endpoint",
			Provider:       core.ProviderCodex,
			CreationStatus: core.TaskCreationStatusReady,
			ParentTaskID:   addRetriesID,
		},
		{
			ID:             fixLoginID,
			Slug:           "fix-login",
			DisplayName:    "Fix login",
			RepoRoot:       "/work/backend",
			RepoName:       "backend",
			BranchName:     "fix/fix-login",
			WorktreePath:   "/work/backend_fix-login",
			TmuxSession:    "backend_fix-login",
			Provider:       core.ProviderCodex,
			CreationStatus: core.TaskCreationStatusFailed,
			CreationStep:   core.TaskCreateProgressPreparingWorkspace,
			CreationError:  "setup workspace: setup script failed",
		},
		{
			ID:             writeDocsID,
			Slug:           "write-docs",
			DisplayName:    "Write docs",
			RepoRoot:       "/work/frontend",
			RepoName:       "frontend",
			BranchName:     "docs/write-docs",
			WorktreePath:   "/work/frontend_write-docs",
			TmuxSession:    "frontend_write-docs",
			Provider:       core.ProviderClaude,
			CreationStatus: core.TaskCreationStatusCreating,
			CreationStep:   core.TaskCreateProgressStartingSession,
			ParentTaskID:   addRetriesID,
		},
	}
}

// listsTasks makes ListTasks return tasks, and LatestTaskStatus each task's
// phase in phases, or no status for a task without one.
func (h *cliHarness) listsTasks(tasks []*core.Task, phases map[string]core.TaskStatusPhase) {
	h.frontend.EXPECT().ListTasks(mock.Anything).Return(tasks, nil).Maybe()
	h.frontend.EXPECT().LatestTaskStatus(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, taskID string) (*core.TaskStatusUpdate, error) {
			phase, ok := phases[taskID]
			if !ok {
				return nil, nil
			}
			return &core.TaskStatusUpdate{TaskID: taskID, Phase: phase}, nil
		},
	).Maybe()
}

func linkedTaskPhases() map[string]core.TaskStatusPhase {
	return map[string]core.TaskStatusPhase{
		addRetriesID:   core.TaskStatusPhaseWorking,
		callEndpointID: core.TaskStatusPhaseWaitingForInput,
	}
}

func TestTaskList_PrintsATableGroupedByRepository(t *testing.T) {
	h := newCLIHarness(t)
	h.listsTasks(linkedTasks(), linkedTaskPhases())

	require.NoError(t, h.run(t.Context(), "list"))

	require.Equal(t, ""+
		"ID                   SLUG                   REPO      BRANCH                      CREATION  STATUS             PARENT\n"+
		"1760000000000000001  add-retries            backend   feat/add-retries            ready     working\n"+
		"1760000000000000003  fix-login              backend   fix/fix-login               failed\n"+
		"1760000000000000002  call-the-new-endpoint  frontend  feat/call-the-new-endpoint  ready     waiting_for_input  add-retries\n"+
		"1760000000000000004  write-docs             frontend  docs/write-docs             creating                     add-retries\n",
		h.stdout.String())
	require.Empty(t, h.stderr.String())
}

// A parent deleted since the tasks were read is named by its ID.
func TestTaskList_NamesAParentThatIsNotListedByItsID(t *testing.T) {
	h := newCLIHarness(t)
	h.listsTasks(linkedTasks()[1:2], linkedTaskPhases())

	require.NoError(t, h.run(t.Context(), "list"))

	require.Equal(t, ""+
		"ID                   SLUG                   REPO      BRANCH                      CREATION  STATUS             PARENT\n"+
		"1760000000000000002  call-the-new-endpoint  frontend  feat/call-the-new-endpoint  ready     waiting_for_input  1760000000000000001\n",
		h.stdout.String())
}

func TestTaskList_PrintsTheTasksAsJSON(t *testing.T) {
	h := newCLIHarness(t)
	h.listsTasks(linkedTasks(), linkedTaskPhases())

	require.NoError(t, h.run(t.Context(), "list", "--json"))

	var printed []map[string]any
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &printed))
	require.Len(t, printed, 4)
	ids := make([]any, 0, len(printed))
	for _, task := range printed {
		ids = append(ids, task["id"])
		require.NotContains(t, task, "children")
	}
	require.Equal(t, []any{addRetriesID, fixLoginID, callEndpointID, writeDocsID}, ids)
	require.Equal(t, map[string]any{
		"id":              callEndpointID,
		"slug":            "call-the-new-endpoint",
		"name":            "Call the new endpoint",
		"repo_name":       "frontend",
		"repo_root":       "/work/frontend",
		"branch":          "feat/call-the-new-endpoint",
		"worktree":        "/work/frontend_call-the-new-endpoint",
		"tmux_session":    "frontend_call-the-new-endpoint",
		"provider":        "codex",
		"creation_status": "ready",
		"creation_step":   "",
		"status":          "waiting_for_input",
		"parent_id":       addRetriesID,
	}, printed[2])
	require.Equal(t, "preparing_workspace", printed[1]["creation_step"])
	require.Empty(t, printed[1]["status"])
}

func TestTaskList_PrintsNoTasks(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "as a sentence", args: []string{"list"}, want: "No tasks.\n"},
		{name: "as an empty JSON array", args: []string{"list", "--json"}, want: "[]\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newCLIHarness(t)
			h.listsTasks(nil, nil)

			require.NoError(t, h.run(t.Context(), tt.args...))

			require.Equal(t, tt.want, h.stdout.String())
		})
	}
}

func TestTaskList_FailsWhenTheTasksCannotBeRead(t *testing.T) {
	tests := []struct {
		name      string
		listErr   error
		statusErr error
		wantErr   string
	}{
		{name: "listing the tasks", listErr: errors.New("daemon busy"), wantErr: "daemon busy"},
		{
			name:      "reading a task's status",
			statusErr: errors.New("daemon busy"),
			wantErr:   "read the status of task 1760000000000000001: daemon busy",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newCLIHarness(t)
			h.frontend.EXPECT().ListTasks(mock.Anything).Return(linkedTasks(), tt.listErr)
			h.frontend.EXPECT().LatestTaskStatus(mock.Anything, mock.Anything).Return(nil, tt.statusErr).Maybe()

			err := h.run(t.Context(), "list", "--json")

			require.EqualError(t, err, tt.wantErr)
			require.Empty(t, h.stdout.String())
		})
	}
}

func TestTaskList_RejectsArguments(t *testing.T) {
	h := newCLIHarness(t)

	err := h.run(t.Context(), "list", "add-retries")

	require.EqualError(t, err, `unknown command "add-retries" for "task list"`)
	require.Zero(t, h.frontendCalls)
}
