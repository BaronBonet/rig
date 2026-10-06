package core

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// NewTaskSessionStream starts a fresh provider session in an existing Task and
// streams the same progress shape as task creation. A long conversation is
// cheaper to continue in a new session that starts from a handoff note than to
// compact and keep paying for its context, and the Task stays one row; a
// session started alongside becomes a child Task under it.
func (s *service) NewTaskSessionStream(
	ctx context.Context,
	input NewTaskSessionInput,
) (<-chan TaskCreateEvent, error) {
	return s.creation.taskCreateEventStream(
		ctx,
		func(ctx context.Context, reporter TaskCreateProgressReporter) (*Task, error) {
			return s.NewTaskSessionWithProgress(ctx, input, reporter)
		},
	)
}

// NewTaskSessionWithProgress is NewTaskSessionStream's synchronous form. It is
// not part of the TaskService port.
func (s *service) NewTaskSessionWithProgress(
	ctx context.Context,
	input NewTaskSessionInput,
	reporter TaskCreateProgressReporter,
) (*Task, error) {
	var task *Task
	err := s.operations.Run(ctx, input.TaskID, taskOperationNewSession, false, func(ctx context.Context) error {
		var runErr error
		task, runErr = s.newTaskSession(ctx, input, reporter)
		return runErr
	})
	return task, err
}

func (s *service) newTaskSession(
	ctx context.Context,
	input NewTaskSessionInput,
	reporter TaskCreateProgressReporter,
) (*Task, error) {
	task, err := taskByID(ctx, s.tasks, input.TaskID)
	if err != nil {
		return nil, err
	}
	if task.CreationStatus != TaskCreationStatusReady {
		return nil, fmt.Errorf("task is not ready: finish or retry its creation first")
	}

	provider, providerClient, err := s.launcher.resolveProvider(ctx, input.Provider)
	if err != nil {
		return nil, err
	}

	if !input.NewWindow {
		// Never kill or type over an interactive session: replacing it needs
		// the pane idle, whichever provider is running there.
		if err := s.refuseWhileProviderRuns(ctx, task, provider); err != nil {
			return nil, err
		}
	}

	custom := strings.TrimSpace(input.Prompt)
	handoffPath := ""
	if input.Handoff {
		handoffPath, err = s.writeSessionHandoff(ctx, task, custom, reporter)
		if err != nil {
			return nil, err
		}
	}
	prompt := ContinuationPrompt(task, custom, handoffPath)
	options := LaunchOptions{Model: input.Model, Effort: input.Effort}

	if input.NewWindow {
		return s.startSessionAlongside(ctx, task, provider, providerClient, custom, prompt, options, reporter)
	}

	if provider != task.Provider {
		if err := providerClient.EnsureTaskSessionEnvironment(ctx, task.ProviderEnv); err != nil {
			return nil, fmt.Errorf("ensure task session environment: %w", err)
		}
		// Like a provider switch: the workspace is bootstrapped for the new
		// provider, never reseeded.
		if err := s.launcher.bootstrapWorkspace(ctx, providerClient, task); err != nil {
			return nil, err
		}
	}

	// The Task's own prompt stays the original ask; the continuation prompt
	// is only typed into the new session.
	launchTask := *task
	launchTask.Provider = provider
	launchTask.SetLaunch(options)
	launchTask.Prompt = prompt

	reportTaskCreateProgress(reporter, TaskCreateProgressStartingSession)
	if _, err := s.launcher.startSession(ctx, &launchTask); err != nil {
		return nil, err
	}

	task.Provider = provider
	task.SetLaunch(launchTask.Launch())
	task.UpdatedAt = time.Now().UTC()
	if err := s.tasks.UpdateTask(ctx, task); err != nil {
		return nil, fmt.Errorf("record task launch options: %w", err)
	}
	s.launcher.rememberLaunchDefaults(ctx, provider, task.Launch())

	return task, nil
}

// refuseWhileProviderRuns returns ErrProviderSessionActive when the Task's
// pane is running its provider or the one about to start.
func (s *service) refuseWhileProviderRuns(ctx context.Context, task *Task, next Provider) error {
	runtime, err := s.tmuxSession.InspectTaskSession(ctx, task)
	if err != nil {
		return fmt.Errorf("inspect task session: %w", err)
	}
	if !runtime.Exists {
		return nil
	}
	for _, candidate := range []Provider{task.Provider, next} {
		client, clientErr := supportedProviderClient(s.providers, candidate)
		if clientErr == nil && taskSessionRunningProvider(runtime, client.TaskSessionCommandName()) {
			return fmt.Errorf(
				"%w: %s is still open in the task's window, idle or not; "+
					"exit it there, or start the session in a new window instead",
				ErrProviderSessionActive,
				candidate,
			)
		}
	}
	return nil
}

// startSessionAlongside creates a child Task for a session that runs next to
// the Task's others: same workspace, branch and tmux session, its own window
// and record, so it has its own status, activity and token usage. A session
// of a child joins the same group.
func (s *service) startSessionAlongside(
	ctx context.Context,
	task *Task,
	provider Provider,
	providerClient ProviderClient,
	custom string,
	prompt string,
	options LaunchOptions,
	reporter TaskCreateProgressReporter,
) (*Task, error) {
	existing, err := s.tasks.ListTasks(ctx)
	if err != nil {
		return nil, err
	}
	root := task
	if task.IsChild() {
		if parent, parentErr := taskByID(ctx, s.tasks, task.ParentID); parentErr == nil {
			root = parent
		}
	}
	group := []*Task{root}
	for _, candidate := range existing {
		if candidate != nil && candidate.ParentID == root.ID {
			group = append(group, candidate)
		}
	}

	displayName := "session " + strconv.Itoa(len(group)+1)
	if custom != "" {
		reportTaskCreateProgress(reporter, TaskCreateProgressSuggestingName)
		// The name is a label; a failed suggestion is not worth failing the
		// session.
		if suggestion, suggestErr := suggestTaskName(ctx, providerClient, custom, root.ProviderEnv); suggestErr == nil {
			displayName = suggestion.Name
		}
	}

	child := newChildTaskRecord(
		root,
		provider,
		displayName,
		uniqueTaskSlug(root.RepoRoot, displayName, existing),
		nextSessionWindow(group),
	)
	// The composed prompt is the child's own ask: a retry types it again.
	child.Prompt = prompt
	child.ProviderEnv = root.ProviderEnv
	child.SetLaunch(options)
	if err := s.tasks.CreateTask(ctx, child); err != nil {
		return nil, err
	}
	s.launcher.rememberLaunchDefaults(ctx, provider, child.Launch())

	steps := s.creation.creationSteps(child, root.WorktreePath, nil)
	if err := s.creation.runSteps(ctx, child, reporter, steps, taskCreationStepPersistenceReadyOnly); err != nil {
		return child, err
	}
	return child, nil
}

// newChildTaskRecord builds a Task that is a session of root: it borrows
// root's workspace, branch and tmux session, runs in its own window there,
// and, as a folder Task, never has that workspace seeded or removed.
func newChildTaskRecord(root *Task, provider Provider, displayName string, taskSlug string, window string) *Task {
	now := time.Now().UTC()

	return &Task{
		ID:             fmt.Sprintf("%d", now.UnixNano()),
		Slug:           taskSlug,
		DisplayName:    displayName,
		RepoRoot:       root.RepoRoot,
		RepoName:       root.RepoName,
		BranchName:     root.BranchName,
		WorktreePath:   root.WorktreePath,
		TmuxSession:    root.TmuxSession,
		TmuxWindow:     window,
		Provider:       provider,
		CreationStatus: TaskCreationStatusCreating,
		WorkspaceKind:  WorkspaceKindFolder,
		ParentID:       root.ID,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}

// nextSessionWindow names the next free window of a group's tmux session:
// s2, s3, ... after the root's main window.
func nextSessionWindow(group []*Task) string {
	used := make(map[string]bool, len(group))
	for _, task := range group {
		if task != nil && task.TmuxWindow != "" {
			used[task.TmuxWindow] = true
		}
	}
	for n := 2; ; n++ {
		window := "s" + strconv.Itoa(n)
		if !used[window] {
			return window
		}
	}
}

// writeSessionHandoff asks the Task's latest session of its active provider
// for a handoff note and returns its path; "" when the Task has no previous
// session to hand off from.
func (s *service) writeSessionHandoff(
	ctx context.Context,
	task *Task,
	focus string,
	reporter TaskCreateProgressReporter,
) (string, error) {
	previous, err := s.latestProviderSession(ctx, task)
	if err != nil {
		return "", err
	}
	if previous == nil {
		return "", nil
	}
	providerClient, err := supportedProviderClient(s.providers, previous.Provider)
	if err != nil {
		return "", err
	}

	reportTaskCreateProgress(reporter, TaskCreateProgressWritingHandoff)
	path, err := providerClient.WriteSessionHandoff(ctx, task, *previous, focus)
	if err != nil {
		if errors.Is(err, ErrHandoffUnsupported) {
			return "", fmt.Errorf("%w: start the new session without a handoff", err)
		}
		return "", fmt.Errorf("write handoff from the previous session: %w", err)
	}
	return strings.TrimSpace(path), nil
}

// latestProviderSession is the Task's most recently observed session of its
// active provider, the one whose context a handoff should carry over.
func (s *service) latestProviderSession(ctx context.Context, task *Task) (*TaskProviderSession, error) {
	sessions, err := s.tasks.ListTaskProviderSessions(ctx, task.ID)
	if err != nil {
		return nil, fmt.Errorf("list task provider sessions: %w", err)
	}
	var latest *TaskProviderSession
	for index := range sessions {
		session := &sessions[index]
		if session.Provider != task.Provider || strings.TrimSpace(session.ProviderSessionID) == "" {
			continue
		}
		if latest == nil || session.LastObservedAt.After(latest.LastObservedAt) {
			latest = session
		}
	}
	return latest, nil
}

// ContinuationPrompt is what a new session of task is asked: the task and its
// original ask, where to pick up its state (the handoff note, or the workspace
// when there is none), and then the user's own instruction for this session.
func ContinuationPrompt(task *Task, custom string, handoffPath string) string {
	name := strings.TrimSpace(task.DisplayName)
	if name == "" {
		name = strings.TrimSpace(task.Slug)
	}
	var builder strings.Builder
	builder.WriteString("Continue the task \"" + name + "\" in a fresh session.")
	if ask := strings.TrimSpace(task.Prompt); ask != "" {
		builder.WriteString("\n\nOriginal ask:\n" + ask)
	}
	if handoffPath = strings.TrimSpace(handoffPath); handoffPath != "" {
		builder.WriteString(
			"\n\nRead the handoff note from the previous session before doing anything else: " + handoffPath,
		)
	} else {
		builder.WriteString("\n\nFirst recover where the work stands from the workspace: " +
			"git status, the recent commits on the branch, and the changed files.")
	}
	if custom = strings.TrimSpace(custom); custom != "" {
		builder.WriteString("\n\nNow:\n" + custom)
	}
	return builder.String()
}
