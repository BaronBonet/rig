package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/BaronBonet/rig/internal/adapters/handler/cli"
	"github.com/BaronBonet/rig/internal/core"
)

// cliHarness runs the task commands against a mock frontend, the way rig
// runs them from a terminal or a pipe.
type cliHarness struct {
	frontend      *core.MockTaskFrontend
	frontendCalls int
	stdin         io.Reader
	terminal      bool
	cwd           string
	stdout        bytes.Buffer
	stderr        bytes.Buffer
	// inputs are the CreateTaskInputs the frontend received.
	inputs []core.CreateTaskInput
}

func newCLIHarness(t *testing.T) *cliHarness {
	t.Helper()
	return &cliHarness{
		frontend: core.NewMockTaskFrontend(t),
		stdin:    strings.NewReader(""),
		terminal: true,
		cwd:      "/work/backend",
	}
}

// run runs the task command with args.
func (h *cliHarness) run(ctx context.Context, args ...string) error {
	cmd := cli.NewTaskCommand(h.dependencies())
	cmd.SetArgs(args)
	return cmd.ExecuteContext(ctx)
}

// runRepo runs the repo command with args.
func (h *cliHarness) runRepo(ctx context.Context, args ...string) error {
	cmd := cli.NewRepoCommand(h.dependencies())
	cmd.SetArgs(args)
	return cmd.ExecuteContext(ctx)
}

func (h *cliHarness) dependencies() cli.Dependencies {
	return cli.Dependencies{
		Frontend: func(context.Context) (core.TaskFrontend, error) {
			h.frontendCalls++
			return h.frontend, nil
		},
		Stdin:           h.stdin,
		Stdout:          &h.stdout,
		Stderr:          &h.stderr,
		Getwd:           func() (string, error) { return h.cwd, nil },
		StdinIsTerminal: func() bool { return h.terminal },
	}
}

// createStreams makes CreateTaskStream stream events, recording the input it
// was given.
func (h *cliHarness) createStreams(events ...core.TaskCreateEvent) {
	h.frontend.EXPECT().CreateTaskStream(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, input core.CreateTaskInput) (<-chan core.TaskCreateEvent, error) {
			h.inputs = append(h.inputs, input)
			stream := make(chan core.TaskCreateEvent, len(events))
			for _, event := range events {
				stream <- event
			}
			close(stream)
			return stream, nil
		},
	).Once()
}

func readyTask() *core.Task {
	return &core.Task{
		ID:             "1760000000000000000",
		Slug:           "call-the-new-endpoint",
		DisplayName:    "Call the new endpoint",
		RepoRoot:       "/work/frontend",
		RepoName:       "frontend",
		BranchName:     "feat/call-the-new-endpoint",
		WorktreePath:   "/work/frontend_call-the-new-endpoint",
		TmuxSession:    "frontend_call-the-new-endpoint",
		Provider:       core.ProviderCodex,
		CreationStatus: core.TaskCreationStatusReady,
	}
}

func progress(step core.TaskCreateProgressStep) core.TaskCreateEvent {
	return core.TaskCreateEvent{Progress: &core.TaskCreateProgressEvent{Step: step}}
}

func allProgress() []core.TaskCreateEvent {
	return []core.TaskCreateEvent{
		progress(core.TaskCreateProgressSuggestingName),
		progress(core.TaskCreateProgressCreatingWorktree),
		progress(core.TaskCreateProgressPreparingWorkspace),
		progress(core.TaskCreateProgressStartingSession),
	}
}

func TestTaskCreate_SendsTheCreateInput(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want core.CreateTaskInput
	}{
		{
			name: "defaults",
			args: []string{"create", "call the new endpoint"},
			want: core.CreateTaskInput{
				Cwd:               "/work/backend",
				Prompt:            "call the new endpoint",
				LinkToCallingTask: true,
			},
		},
		{
			name: "every flag",
			args: []string{
				"create", "--repo", "../frontend", "--provider", "claude", "--submit", "--no-parent",
				"  call the new endpoint\n",
			},
			want: core.CreateTaskInput{
				Cwd:          "/work/backend",
				Prompt:       "call the new endpoint",
				Provider:     core.ProviderClaude,
				TargetRepo:   "../frontend",
				SubmitPrompt: true,
			},
		},
		{
			name: "no parent",
			args: []string{"create", "--no-parent", "call the new endpoint"},
			want: core.CreateTaskInput{Cwd: "/work/backend", Prompt: "call the new endpoint"},
		},
		{
			name: "provider in mixed case",
			args: []string{"create", "--provider", " Claude ", "call the new endpoint"},
			want: core.CreateTaskInput{
				Cwd:               "/work/backend",
				Prompt:            "call the new endpoint",
				Provider:          core.ProviderClaude,
				LinkToCallingTask: true,
			},
		},
		{
			name: "repository by name",
			args: []string{"create", "--repo=frontend", "call the new endpoint"},
			want: core.CreateTaskInput{
				Cwd:               "/work/backend",
				Prompt:            "call the new endpoint",
				TargetRepo:        "frontend",
				LinkToCallingTask: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newCLIHarness(t)
			h.createStreams(core.TaskCreateEvent{Task: readyTask()})

			require.NoError(t, h.run(t.Context(), tt.args...))

			require.Equal(t, []core.CreateTaskInput{tt.want}, h.inputs)
		})
	}
}

// symlinkedWorkingDirectory makes a directory in a worktree that the shell
// reaches through a symlink, as from ~/code linked elsewhere, and returns its
// path through the symlink and its physical path.
func symlinkedWorkingDirectory(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	physical := filepath.Join(root, "disk")
	require.NoError(t, os.MkdirAll(filepath.Join(physical, "backend_add-retries", "src"), 0o750))
	require.NoError(t, os.Symlink(physical, filepath.Join(root, "code")))
	// The temporary directory may itself be reached through a symlink, as
	// /var is on macOS.
	physical, err := filepath.EvalSymlinks(physical)
	require.NoError(t, err)

	return filepath.Join(root, "code", "backend_add-retries", "src"),
		filepath.Join(physical, "backend_add-retries", "src")
}

// Task worktree paths have their symlinks resolved, so the working directory
// must too for the calling task to be found.
func TestTaskCreate_SendsTheWorkingDirectoryWithItsSymlinksResolved(t *testing.T) {
	h := newCLIHarness(t)
	var physical string
	h.cwd, physical = symlinkedWorkingDirectory(t)
	h.createStreams(core.TaskCreateEvent{Task: readyTask()})

	require.NoError(t, h.run(t.Context(), "create", "call the new endpoint"))

	require.Len(t, h.inputs, 1)
	require.Equal(t, physical, h.inputs[0].Cwd)
}

func TestTaskCreate_PrintsProgressToStderrAndTheTaskToStdout(t *testing.T) {
	h := newCLIHarness(t)
	h.createStreams(append(allProgress(), core.TaskCreateEvent{Task: readyTask()})...)

	require.NoError(t, h.run(t.Context(), "create", "call the new endpoint"))

	require.Equal(t, "Suggesting a name…\n"+
		"Creating the worktree…\n"+
		"Preparing the workspace…\n"+
		"Starting the session…\n", h.stderr.String())
	require.Equal(t, "Name:         Call the new endpoint\n"+
		"ID:           1760000000000000000\n"+
		"Repository:   frontend (/work/frontend)\n"+
		"Branch:       feat/call-the-new-endpoint\n"+
		"Worktree:     /work/frontend_call-the-new-endpoint\n"+
		"Tmux session: frontend_call-the-new-endpoint\n", h.stdout.String())
}

func TestTaskCreate_PrintsTheParentOfALinkedTask(t *testing.T) {
	linked := readyTask()
	linked.ParentTaskID = "1750000000000000000"
	parent := &core.Task{ID: "1750000000000000000", Slug: "add-retries", DisplayName: "Add retries"}

	tests := []struct {
		name       string
		listed     []*core.Task
		listErr    error
		wantParent string
	}{
		{
			name:       "by name and ID",
			listed:     []*core.Task{parent, linked},
			wantParent: "Add retries (1750000000000000000)",
		},
		{
			name:       "by ID when the parent cannot be read",
			listErr:    errors.New("daemon busy"),
			wantParent: "1750000000000000000",
		},
		{
			name:       "by ID when the parent is gone",
			listed:     []*core.Task{linked},
			wantParent: "1750000000000000000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newCLIHarness(t)
			h.createStreams(core.TaskCreateEvent{Task: linked})
			h.frontend.EXPECT().ListTasks(mock.Anything).Return(tt.listed, tt.listErr)

			require.NoError(t, h.run(t.Context(), "create", "call the new endpoint"))

			require.Equal(t, "Name:         Call the new endpoint\n"+
				"ID:           1760000000000000000\n"+
				"Repository:   frontend (/work/frontend)\n"+
				"Branch:       feat/call-the-new-endpoint\n"+
				"Worktree:     /work/frontend_call-the-new-endpoint\n"+
				"Tmux session: frontend_call-the-new-endpoint\n"+
				"Parent:       "+tt.wantParent+"\n", h.stdout.String())
		})
	}
}

func TestTaskCreate_PrintsTheParentIDOfALinkedTaskAsJSON(t *testing.T) {
	h := newCLIHarness(t)
	linked := readyTask()
	linked.ParentTaskID = "1750000000000000000"
	h.createStreams(core.TaskCreateEvent{Task: linked})
	h.frontend.EXPECT().LatestTaskStatus(mock.Anything, "1760000000000000000").Return(nil, nil)

	require.NoError(t, h.run(t.Context(), "create", "--json", "call the new endpoint"))

	var printed map[string]any
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &printed))
	require.Equal(t, "1750000000000000000", printed["parent_id"])
}

func TestTaskCreate_PrintsTheTaskAsJSON(t *testing.T) {
	h := newCLIHarness(t)
	h.createStreams(append(allProgress(), core.TaskCreateEvent{Task: readyTask()})...)
	h.frontend.EXPECT().LatestTaskStatus(mock.Anything, "1760000000000000000").
		Return(&core.TaskStatusUpdate{TaskID: "1760000000000000000", Phase: core.TaskStatusPhaseStarting}, nil)

	require.NoError(t, h.run(t.Context(), "create", "--json", "call the new endpoint"))

	var printed map[string]any
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &printed))
	require.Equal(t, map[string]any{
		"id":              "1760000000000000000",
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
		"status":          "starting",
		"parent_id":       "",
	}, printed)
	require.Contains(t, h.stderr.String(), "Starting the session…")
}

func TestTaskCreate_PrintsAnEmptyStatusForATaskWithoutOne(t *testing.T) {
	h := newCLIHarness(t)
	h.createStreams(core.TaskCreateEvent{Task: readyTask()})
	h.frontend.EXPECT().LatestTaskStatus(mock.Anything, "1760000000000000000").Return(nil, nil)

	require.NoError(t, h.run(t.Context(), "create", "--json", "call the new endpoint"))

	var printed map[string]any
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &printed))
	require.Contains(t, printed, "status")
	require.Empty(t, printed["status"])
}

// The task exists once it is created, so failing to read its status must not
// fail the command and invite creating it again.
func TestTaskCreate_WarnsWhenTheStatusOfTheCreatedTaskCannotBeRead(t *testing.T) {
	h := newCLIHarness(t)
	h.createStreams(core.TaskCreateEvent{Task: readyTask()})
	h.frontend.EXPECT().LatestTaskStatus(mock.Anything, mock.Anything).Return(nil, errors.New("daemon busy"))

	require.NoError(t, h.run(t.Context(), "create", "--json", "call the new endpoint"))

	var printed map[string]any
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &printed))
	require.Equal(t, "1760000000000000000", printed["id"])
	require.Contains(t, printed, "status")
	require.Empty(t, printed["status"])
	require.Contains(t, h.stderr.String(), "daemon busy")
}

func TestTaskCreate_ReadsThePromptFromStdin(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		terminal bool
	}{
		{name: "when the prompt is -", args: []string{"create", "-"}, terminal: true},
		{name: "when the prompt is absent and stdin is not a terminal", args: []string{"create"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newCLIHarness(t)
			h.terminal = tt.terminal
			h.stdin = strings.NewReader("Call the endpoint:\n\n  POST /v1/retries\n")
			h.createStreams(core.TaskCreateEvent{Task: readyTask()})

			require.NoError(t, h.run(t.Context(), tt.args...))

			require.Len(t, h.inputs, 1)
			require.Equal(t, "Call the endpoint:\n\n  POST /v1/retries", h.inputs[0].Prompt)
		})
	}
}

func TestTaskCreate_RejectsBadInputBeforeContactingTheFrontend(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		stdin    string
		terminal bool
		wantErr  string
	}{
		{
			name:     "absent while stdin is a terminal",
			args:     []string{"create"},
			terminal: true,
			wantErr:  "a prompt is required: pass it as an argument, or on stdin",
		},
		{name: "blank argument", args: []string{"create", "  \n"}, wantErr: "the prompt is empty"},
		{name: "empty stdin", args: []string{"create"}, wantErr: "the prompt is empty"},
		{name: "blank stdin", args: []string{"create", "-"}, stdin: " \n\t", wantErr: "the prompt is empty"},
		{
			name:    "two prompts",
			args:    []string{"create", "one", "two"},
			wantErr: "accepts at most 1 arg(s), received 2",
		},
		{
			name:    "unknown provider",
			args:    []string{"create", "--provider", "gemini", "call the new endpoint"},
			wantErr: `unknown provider "gemini": choose one of codex, claude`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newCLIHarness(t)
			h.terminal = tt.terminal
			h.stdin = strings.NewReader(tt.stdin)

			err := h.run(t.Context(), tt.args...)

			require.EqualError(t, err, tt.wantErr)
			require.Zero(t, h.frontendCalls, "no frontend is needed without a prompt")
			require.Empty(t, h.stdout.String())
		})
	}
}

func TestTaskCreate_FailsNamingTheTaskAndTheStepItFailedAt(t *testing.T) {
	h := newCLIHarness(t)
	failed := readyTask()
	failed.CreationStatus = core.TaskCreationStatusFailed
	failed.CreationStep = core.TaskCreateProgressPreparingWorkspace
	h.createStreams(
		progress(core.TaskCreateProgressSuggestingName),
		progress(core.TaskCreateProgressCreatingWorktree),
		progress(core.TaskCreateProgressPreparingWorkspace),
		core.TaskCreateEvent{Err: errors.New("setup workspace: setup script failed"), Task: failed},
	)

	err := h.run(t.Context(), "create", "--json", "call the new endpoint")

	require.EqualError(t, err, `task "Call the new endpoint" (1760000000000000000) failed while preparing `+
		`the workspace: setup workspace: setup script failed; retry it from the rig TUI`)
	require.Empty(t, h.stdout.String())
}

func TestTaskCreate_FailsWithTheCauseWhenNoTaskWasCreated(t *testing.T) {
	h := newCLIHarness(t)
	h.createStreams(core.TaskCreateEvent{Err: errors.New(`no known repository is named "frontend"`)})

	err := h.run(t.Context(), "create", "--repo", "frontend", "call the new endpoint")

	require.EqualError(t, err, `no known repository is named "frontend"`)
	require.Empty(t, h.stdout.String())
}

func TestTaskCreate_FailsWhenTheFrontendIsUnavailable(t *testing.T) {
	h := newCLIHarness(t)
	cmd := cli.NewTaskCommand(cli.Dependencies{
		Frontend: func(context.Context) (core.TaskFrontend, error) {
			return nil, errors.New("task daemon did not start")
		},
		Stdout: &h.stdout,
		Stderr: &h.stderr,
		Getwd:  func() (string, error) { return "/work/backend", nil },
	})
	cmd.SetArgs([]string{"create", "call the new endpoint"})

	require.EqualError(t, cmd.ExecuteContext(t.Context()), "task daemon did not start")
}

// Like a TUI quitting while it creates a task, an interrupted command stops
// waiting for the task.
func TestTaskCreate_StopsWaitingWhenCancelled(t *testing.T) {
	h := newCLIHarness(t)
	ctx, cancel := context.WithCancel(t.Context())
	h.frontend.EXPECT().CreateTaskStream(mock.Anything, mock.Anything).RunAndReturn(
		func(context.Context, core.CreateTaskInput) (<-chan core.TaskCreateEvent, error) {
			stream := make(chan core.TaskCreateEvent, 1)
			stream <- progress(core.TaskCreateProgressSuggestingName)
			cancel()
			return stream, nil
		},
	)

	err := h.run(ctx, "create", "call the new endpoint")

	require.ErrorIs(t, err, context.Canceled)
	require.EqualError(t, err, "stopped waiting (context canceled), but the rig daemon is still creating the task: "+
		"check `rig task list` or the rig TUI before creating it again")
	require.Empty(t, h.stdout.String())
}

func TestTaskCreate_FailsWhenTheStreamEndsWithoutAResult(t *testing.T) {
	h := newCLIHarness(t)
	h.createStreams(progress(core.TaskCreateProgressSuggestingName))

	err := h.run(t.Context(), "create", "call the new endpoint")

	require.EqualError(t, err, "task creation ended without a result")
	require.Empty(t, h.stdout.String())
}

func TestTaskCreate_FailsWhenTheWorkingDirectoryCannotBeRead(t *testing.T) {
	var stdout bytes.Buffer
	frontendCalls := 0
	cmd := cli.NewTaskCommand(cli.Dependencies{
		Frontend: func(context.Context) (core.TaskFrontend, error) {
			frontendCalls++
			return core.NewMockTaskFrontend(t), nil
		},
		Stdout: &stdout,
		Stderr: &bytes.Buffer{},
		Getwd:  func() (string, error) { return "", errors.New("getwd: no such file or directory") },
	})
	cmd.SetArgs([]string{"create", "call the new endpoint"})

	err := cmd.ExecuteContext(t.Context())

	require.EqualError(t, err, "read the working directory: getwd: no such file or directory")
	require.Zero(t, frontendCalls)
	require.Empty(t, stdout.String())
}

func TestTaskCommand_PrintsItsHelpWithoutASubcommand(t *testing.T) {
	h := newCLIHarness(t)

	require.NoError(t, h.run(t.Context()))

	require.Contains(t, h.stdout.String(), "create")
	require.Zero(t, h.frontendCalls)
}

// An agent reads a successful exit as success, so a mistyped subcommand must
// fail rather than print help.
func TestTaskCommand_RejectsAnUnknownSubcommand(t *testing.T) {
	h := newCLIHarness(t)

	err := h.run(t.Context(), "crate", "call the new endpoint")

	require.EqualError(t, err, `unknown command "crate" for "task"`)
	require.Zero(t, h.frontendCalls)
}
