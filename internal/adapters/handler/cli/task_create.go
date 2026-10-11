package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/BaronBonet/rig/internal/core"
)

// stdinPromptArg is the prompt argument that asks to read the prompt from
// stdin.
const stdinPromptArg = "-"

type taskCreateOptions struct {
	repo     string
	provider string
	submit   bool
	noParent bool
	json     bool
}

func newTaskCreateCommand(deps Dependencies) *cobra.Command {
	var opts taskCreateOptions
	cmd := &cobra.Command{
		Use:   "create [<prompt>]",
		Short: "Create a task and wait until it is ready",
		Long: "Create a task the way the TUI does, in its own worktree and tmux session, and print it once it " +
			"is ready. It never attaches your terminal to the task.\n\n" +
			"The prompt is the argument, or stdin when the argument is - or absent and stdin is not a " +
			"terminal. It is pasted into the new agent without being sent, unless --submit is given.\n\n" +
			"Run inside a task's worktree, the new task records that task as its parent, in whatever " +
			"repository the new task is, unless --no-parent is given.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTaskCreate(cmd.Context(), deps, opts, args)
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&opts.repo, "repo", "",
		"repository to create the task in, by path or by a name rig repo list shows "+
			"(default: the repository you are in)")
	flags.StringVar(&opts.provider, "provider", "", "provider to launch for the task (default: your default provider)")
	flags.BoolVar(&opts.submit, "submit", false, "submit the prompt so the agent starts working at once")
	flags.BoolVar(&opts.noParent, "no-parent", false, "do not record the task you run this in as the parent")
	flags.BoolVar(&opts.json, "json", false, "print the task as JSON")

	return cmd
}

func runTaskCreate(ctx context.Context, deps Dependencies, opts taskCreateOptions, args []string) error {
	provider, err := parseProvider(opts.provider)
	if err != nil {
		return err
	}
	prompt, err := readPrompt(deps, args)
	if err != nil {
		return err
	}
	cwd, err := deps.workingDirectory()
	if err != nil {
		return err
	}

	ctx, stop := untilInterrupted(ctx)
	defer stop()

	frontend, err := deps.frontend(ctx)
	if err != nil {
		return err
	}
	events, err := frontend.CreateTaskStream(ctx, core.CreateTaskInput{
		Cwd:               cwd,
		Prompt:            prompt,
		Provider:          provider,
		TargetRepo:        strings.TrimSpace(opts.repo),
		SubmitPrompt:      opts.submit,
		LinkToCallingTask: !opts.noParent,
	})
	if err != nil {
		return err
	}
	task, err := awaitCreatedTask(ctx, events, deps.Stderr)
	if err != nil {
		return err
	}

	if !opts.json {
		return writeTaskSummary(deps.Stdout, newTaskView(task, nil), createdTaskParent(ctx, frontend, task))
	}
	// The task exists by now, so a status that cannot be read is only a
	// warning: failing would invite creating the task again.
	view, err := loadTaskView(ctx, frontend, task)
	if err != nil {
		fmt.Fprintf(deps.Stderr, "Warning: %v\n", err)
	}
	return writeJSON(deps.Stdout, view)
}

// createdTaskParent names a created task's parent, or is empty when it has
// none. The task exists by now, so when the parent's name cannot be read its
// ID is enough.
func createdTaskParent(ctx context.Context, frontend core.TaskFrontend, task *core.Task) string {
	if task.ParentTaskID == "" {
		return ""
	}
	tasks, err := frontend.ListTasks(ctx)
	if err != nil {
		return task.ParentTaskID
	}
	return parentLabel(tasks, task.ParentTaskID)
}

// parseProvider returns the launch provider a --provider value names, or the
// empty provider, which is the user's default, when it names none.
func parseProvider(value string) (core.Provider, error) {
	provider := core.Provider(strings.ToLower(strings.TrimSpace(value)))
	if provider == "" || core.IsSupportedProvider(provider) {
		return provider, nil
	}

	supported := make([]string, 0, len(core.SupportedProviders()))
	for _, name := range core.SupportedProviders() {
		supported = append(supported, string(name))
	}
	return "", fmt.Errorf("unknown provider %q: choose one of %s", value, strings.Join(supported, ", "))
}

// readPrompt returns the prompt from the argument, or from stdin when the
// argument is - or absent and stdin is not a terminal.
func readPrompt(deps Dependencies, args []string) (string, error) {
	var prompt string
	switch {
	case len(args) > 0 && args[0] != stdinPromptArg:
		prompt = args[0]
	case len(args) == 0 && (deps.StdinIsTerminal == nil || deps.StdinIsTerminal()):
		return "", errors.New("a prompt is required: pass it as an argument, or on stdin")
	default:
		input, err := io.ReadAll(deps.Stdin)
		if err != nil {
			return "", fmt.Errorf("read the prompt from stdin: %w", err)
		}
		prompt = string(input)
	}

	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", errors.New("the prompt is empty")
	}
	return prompt, nil
}

// awaitCreatedTask reports each creation step to progress as it starts and
// returns the task once it is ready.
func awaitCreatedTask(
	ctx context.Context,
	events <-chan core.TaskCreateEvent,
	progress io.Writer,
) (*core.Task, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, stoppedWaiting(ctx)
		case event, ok := <-events:
			if !ok {
				if ctx.Err() != nil {
					return nil, stoppedWaiting(ctx)
				}
				return nil, errors.New("task creation ended without a result")
			}
			switch {
			case event.Err != nil:
				return nil, taskCreationFailed(event.Task, event.Err)
			case event.Task != nil:
				return event.Task, nil
			case event.Progress != nil:
				fmt.Fprintln(progress, creationStepProgress(event.Progress.Step))
			}
		}
	}
}

// stoppedWaiting is the error of a command interrupted while its task was
// being created. Like a TUI that quits meanwhile, it only stops waiting: the
// daemon goes on creating the task, so creating it again would duplicate it.
func stoppedWaiting(ctx context.Context) error {
	return fmt.Errorf(
		"stopped waiting (%w), but the rig daemon is still creating the task: "+
			"check `rig task list` or the rig TUI before creating it again",
		ctx.Err(),
	)
}

// taskCreationFailed is the error of a creation that failed. Once its task
// record exists, the error names the task and the step it failed at, which
// the TUI retries it from.
func taskCreationFailed(task *core.Task, cause error) error {
	if task == nil {
		return cause
	}
	name := fmt.Sprintf("task %q (%s)", task.DisplayName, task.ID)
	if task.CreationStep == "" {
		return fmt.Errorf("%s failed: %w; retry it from the rig TUI", name, cause)
	}
	return fmt.Errorf(
		"%s failed while %s: %w; retry it from the rig TUI",
		name,
		creationStepPhrase(task.CreationStep),
		cause,
	)
}

// creationStepProgress is the progress line of a creation step starting.
func creationStepProgress(step core.TaskCreateProgressStep) string {
	phrase := creationStepPhrase(step)
	return strings.ToUpper(phrase[:1]) + phrase[1:] + "…"
}

func creationStepPhrase(step core.TaskCreateProgressStep) string {
	switch step {
	case core.TaskCreateProgressSuggestingName:
		return "suggesting a name"
	case core.TaskCreateProgressCreatingWorktree:
		return "creating the worktree"
	case core.TaskCreateProgressPreparingWorkspace:
		return "preparing the workspace"
	case core.TaskCreateProgressStartingSession:
		return "starting the session"
	default:
		return "creating the task"
	}
}
