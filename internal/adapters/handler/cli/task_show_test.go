package cli_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// shownTask is a task as rig task show --json prints it.
type shownTask struct {
	ID       string      `json:"id"`
	Slug     string      `json:"slug"`
	Status   string      `json:"status"`
	ParentID string      `json:"parent_id"`
	Children []shownTask `json:"children"`
}

func TestTaskShow_PrintsTheTaskWithItsChildren(t *testing.T) {
	h := newCLIHarness(t)
	h.listsTasks(linkedTasks(), linkedTaskPhases())

	require.NoError(t, h.run(t.Context(), "show", addRetriesID))

	require.Equal(t, "Name:         Add retries\n"+
		"ID:           1760000000000000001\n"+
		"Slug:         add-retries\n"+
		"Repository:   backend (/work/backend)\n"+
		"Branch:       feat/add-retries\n"+
		"Worktree:     /work/backend_add-retries\n"+
		"Tmux session: backend_add-retries\n"+
		"Provider:     claude\n"+
		"Creation:     ready\n"+
		"Status:       working\n"+
		"Children:\n"+
		"  Call the new endpoint (1760000000000000002): waiting_for_input\n"+
		"  Write docs (1760000000000000004): creating\n", h.stdout.String())
	require.Empty(t, h.stderr.String())
}

func TestTaskShow_PrintsTheParentOfAChild(t *testing.T) {
	h := newCLIHarness(t)
	h.listsTasks(linkedTasks(), linkedTaskPhases())

	require.NoError(t, h.run(t.Context(), "show", "call-the-new-endpoint"))

	require.Equal(t, "Name:         Call the new endpoint\n"+
		"ID:           1760000000000000002\n"+
		"Slug:         call-the-new-endpoint\n"+
		"Repository:   frontend (/work/frontend)\n"+
		"Branch:       feat/call-the-new-endpoint\n"+
		"Worktree:     /work/frontend_call-the-new-endpoint\n"+
		"Tmux session: frontend_call-the-new-endpoint\n"+
		"Provider:     codex\n"+
		"Creation:     ready\n"+
		"Status:       waiting_for_input\n"+
		"Parent:       Add retries (1760000000000000001)\n", h.stdout.String())
}

// A parent deleted since the tasks were read is named by its ID.
func TestTaskShow_NamesAParentThatIsNotListedByItsID(t *testing.T) {
	h := newCLIHarness(t)
	h.listsTasks(linkedTasks()[1:], linkedTaskPhases())

	require.NoError(t, h.run(t.Context(), "show", "call-the-new-endpoint"))

	require.Contains(t, h.stdout.String(), "\nParent:       1760000000000000001\n")
}

func TestTaskShow_DescribesTheCreationOfATaskThatIsNotReady(t *testing.T) {
	tests := []struct {
		ref          string
		wantCreation string
	}{
		{ref: "fix-login", wantCreation: "failed while preparing the workspace: setup workspace: setup script failed"},
		{ref: "write-docs", wantCreation: "creating (starting the session)"},
	}

	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			h := newCLIHarness(t)
			h.listsTasks(linkedTasks(), linkedTaskPhases())

			require.NoError(t, h.run(t.Context(), "show", tt.ref))

			require.Contains(t, h.stdout.String(), "\nCreation:     "+tt.wantCreation+"\nStatus:       none\n")
		})
	}
}

func TestTaskShow_PrintsTheTaskAndItsChildrenAsJSON(t *testing.T) {
	h := newCLIHarness(t)
	h.listsTasks(linkedTasks(), linkedTaskPhases())

	require.NoError(t, h.run(t.Context(), "show", "--json", "add-retries"))

	var printed map[string]any
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &printed))
	children, ok := printed["children"].([]any)
	require.True(t, ok)
	require.Len(t, children, 2)
	delete(printed, "children")
	require.Equal(t, map[string]any{
		"id":              addRetriesID,
		"slug":            "add-retries",
		"name":            "Add retries",
		"repo_name":       "backend",
		"repo_root":       "/work/backend",
		"branch":          "feat/add-retries",
		"worktree":        "/work/backend_add-retries",
		"tmux_session":    "backend_add-retries",
		"provider":        "claude",
		"creation_status": "ready",
		"creation_step":   "",
		"status":          "working",
		"parent_id":       "",
	}, printed)

	var shown shownTask
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &shown))
	require.Equal(t, []shownTask{
		{
			ID:       callEndpointID,
			Slug:     "call-the-new-endpoint",
			Status:   "waiting_for_input",
			ParentID: addRetriesID,
		},
		{ID: writeDocsID, Slug: "write-docs", ParentID: addRetriesID},
	}, shown.Children)
}

func TestTaskShow_PrintsAnEmptyListOfChildrenAsJSON(t *testing.T) {
	h := newCLIHarness(t)
	h.listsTasks(linkedTasks(), linkedTaskPhases())

	require.NoError(t, h.run(t.Context(), "show", "--json", callEndpointID))

	var printed map[string]any
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &printed))
	require.Equal(t, addRetriesID, printed["parent_id"])
	require.Equal(t, []any{}, printed["children"])
}

func TestTaskShow_MatchesAnIDBeforeASlug(t *testing.T) {
	h := newCLIHarness(t)
	tasks := linkedTasks()
	// A slug may look like another task's ID.
	tasks[2].Slug = addRetriesID
	h.listsTasks(tasks, linkedTaskPhases())

	require.NoError(t, h.run(t.Context(), "show", "--json", addRetriesID))

	var shown shownTask
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &shown))
	require.Equal(t, addRetriesID, shown.ID)
}

func TestTaskShow_FailsForASlugOfTasksInSeveralRepositories(t *testing.T) {
	h := newCLIHarness(t)
	tasks := linkedTasks()
	tasks[3].Slug = "fix-login"
	h.listsTasks(tasks, linkedTaskPhases())

	err := h.run(t.Context(), "show", "fix-login")

	require.EqualError(t, err, `slug "fix-login" names 2 tasks, so pass the ID of the one you mean: `+
		`1760000000000000003 (backend, /work/backend), 1760000000000000004 (frontend, /work/frontend)`)
	require.Empty(t, h.stdout.String())
}

func TestTaskShow_FailsForAnUnknownTask(t *testing.T) {
	h := newCLIHarness(t)
	h.listsTasks(linkedTasks(), linkedTaskPhases())

	err := h.run(t.Context(), "show", "add-retires")

	require.EqualError(t, err, "no task has the ID or slug \"add-retires\": `rig task list` shows them")
	require.Empty(t, h.stdout.String())
}

func TestTaskShow_ShowsTheTaskWhoseWorktreeTheCallerIsIn(t *testing.T) {
	h := newCLIHarness(t)
	h.cwd = "/work/frontend_call-the-new-endpoint/src"
	tasks := linkedTasks()
	h.listsTasks(tasks, linkedTaskPhases())
	h.frontend.EXPECT().TaskAtPath(mock.Anything, "/work/frontend_call-the-new-endpoint/src").Return(tasks[1], nil)

	require.NoError(t, h.run(t.Context(), "show", "--json"))

	var shown shownTask
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &shown))
	require.Equal(t, callEndpointID, shown.ID)
	require.Equal(t, addRetriesID, shown.ParentID)
}

func TestTaskShow_FindsTheCallersTaskThroughASymlinkedWorkingDirectory(t *testing.T) {
	h := newCLIHarness(t)
	var physical string
	h.cwd, physical = symlinkedWorkingDirectory(t)
	tasks := linkedTasks()
	h.listsTasks(tasks, linkedTaskPhases())
	h.frontend.EXPECT().TaskAtPath(mock.Anything, physical).Return(tasks[0], nil)

	require.NoError(t, h.run(t.Context(), "show", "--json"))

	var shown shownTask
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &shown))
	require.Equal(t, addRetriesID, shown.ID)
}

func TestTaskShow_FailsOutsideAnyTasksWorktree(t *testing.T) {
	h := newCLIHarness(t)
	h.frontend.EXPECT().TaskAtPath(mock.Anything, "/work/backend").Return(nil, nil)

	err := h.run(t.Context(), "show")

	require.EqualError(t, err, "/work/backend is not in any task's worktree: pass a task ID or slug, "+
		"which `rig task list` shows")
	require.Empty(t, h.stdout.String())
}

func TestTaskShow_FailsWhenTheTaskCannotBeRead(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		arrange func(h *cliHarness)
		wantErr string
	}{
		{
			name: "finding the task the caller is in",
			args: []string{"show"},
			arrange: func(h *cliHarness) {
				h.frontend.EXPECT().TaskAtPath(mock.Anything, mock.Anything).Return(nil, errors.New("daemon busy"))
			},
			wantErr: "daemon busy",
		},
		{
			name: "listing the tasks",
			args: []string{"show", addRetriesID},
			arrange: func(h *cliHarness) {
				h.frontend.EXPECT().ListTasks(mock.Anything).Return(nil, errors.New("daemon busy"))
			},
			wantErr: "daemon busy",
		},
		{
			name: "reading a child's status",
			args: []string{"show", addRetriesID},
			arrange: func(h *cliHarness) {
				h.frontend.EXPECT().ListTasks(mock.Anything).Return(linkedTasks(), nil)
				h.frontend.EXPECT().LatestTaskStatus(mock.Anything, addRetriesID).Return(nil, nil)
				h.frontend.EXPECT().LatestTaskStatus(mock.Anything, callEndpointID).
					Return(nil, errors.New("daemon busy"))
			},
			wantErr: "read the status of task 1760000000000000002: daemon busy",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newCLIHarness(t)
			tt.arrange(h)

			err := h.run(t.Context(), tt.args...)

			require.EqualError(t, err, tt.wantErr)
			require.Empty(t, h.stdout.String())
		})
	}
}

func TestTaskShow_RejectsBadArgumentsBeforeContactingTheFrontend(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "blank task", args: []string{"show", "  "}, wantErr: "the task ID or slug is empty"},
		{name: "two tasks", args: []string{"show", "one", "two"}, wantErr: "accepts at most 1 arg(s), received 2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newCLIHarness(t)

			err := h.run(t.Context(), tt.args...)

			require.EqualError(t, err, tt.wantErr)
			require.Zero(t, h.frontendCalls)
		})
	}
}
