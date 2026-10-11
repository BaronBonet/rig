package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/BaronBonet/rig/internal/core"
)

type taskShowOptions struct {
	json bool
}

func newTaskShowCommand(deps Dependencies) *cobra.Command {
	var opts taskShowOptions
	cmd := &cobra.Command{
		Use:   "show [<id|slug>]",
		Short: "Show a task in detail, with its parent and children",
		Long: "Show the task with the ID or slug given, with its parent task and the tasks it spawned. Without " +
			"one, show the task whose worktree you are in.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTaskShow(cmd.Context(), deps, opts, args)
		},
	}
	cmd.Flags().BoolVar(&opts.json, "json", false, "print the task as JSON")

	return cmd
}

func runTaskShow(ctx context.Context, deps Dependencies, opts taskShowOptions, args []string) error {
	var ref, cwd string
	if len(args) > 0 {
		ref = strings.TrimSpace(args[0])
		if ref == "" {
			return errors.New("the task ID or slug is empty")
		}
	} else {
		var err error
		if cwd, err = deps.workingDirectory(); err != nil {
			return err
		}
	}

	ctx, stop := untilInterrupted(ctx)
	defer stop()

	frontend, err := deps.frontend(ctx)
	if err != nil {
		return err
	}
	task, tasks, err := shownTask(ctx, frontend, ref, cwd)
	if err != nil {
		return err
	}
	view, err := loadTaskView(ctx, frontend, task)
	if err != nil {
		return err
	}
	children, err := loadTaskViews(ctx, frontend, childrenOf(tasks, task.ID))
	if err != nil {
		return err
	}
	detail := taskDetailView{taskView: view, Children: children}

	if opts.json {
		return writeJSON(deps.Stdout, detail)
	}
	return writeTaskDetail(deps.Stdout, detail, parentLabel(tasks, task.ParentTaskID))
}

// shownTask returns the task to show, with every task: the task ref names
// by ID or slug, or without ref the task whose worktree cwd is in.
func shownTask(
	ctx context.Context,
	frontend core.TaskFrontend,
	ref string,
	cwd string,
) (*core.Task, []*core.Task, error) {
	if ref != "" {
		tasks, err := frontend.ListTasks(ctx)
		if err != nil {
			return nil, nil, err
		}
		task, err := findTask(tasks, ref)
		return task, tasks, err
	}

	task, err := frontend.TaskAtPath(ctx, cwd)
	if err != nil {
		return nil, nil, err
	}
	if task == nil {
		return nil, nil, fmt.Errorf(
			"%s is not in any task's worktree: pass a task ID or slug, which `rig task list` shows",
			cwd,
		)
	}
	tasks, err := frontend.ListTasks(ctx)
	return task, tasks, err
}

// findTask returns the task an ID or slug names: the task with that ID, else
// the one task with that slug. Slugs are unique only within a repository, so
// a slug of tasks in several is an error that lists their IDs.
func findTask(tasks []*core.Task, ref string) (*core.Task, error) {
	if task := taskByID(tasks, ref); task != nil {
		return task, nil
	}

	var matches []*core.Task
	for _, task := range tasks {
		if task != nil && task.Slug == ref {
			matches = append(matches, task)
		}
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("no task has the ID or slug %q: `rig task list` shows them", ref)
	case 1:
		return matches[0], nil
	default:
		candidates := make([]string, 0, len(matches))
		for _, task := range matches {
			candidates = append(candidates, fmt.Sprintf("%s (%s, %s)", task.ID, task.RepoName, task.RepoRoot))
		}
		return nil, fmt.Errorf("slug %q names %d tasks, so pass the ID of the one you mean: %s",
			ref, len(matches), strings.Join(candidates, ", "))
	}
}

// childrenOf returns the tasks whose parent is the task with parentID, in
// the order they come.
func childrenOf(tasks []*core.Task, parentID string) []*core.Task {
	var children []*core.Task
	for _, task := range tasks {
		if task != nil && task.ParentTaskID == parentID {
			children = append(children, task)
		}
	}
	return children
}

// writeTaskDetail prints the task one labelled line at a time, its parent's
// line when parent is set, then a line for each of its children.
func writeTaskDetail(w io.Writer, detail taskDetailView, parent string) error {
	view := detail.taskView
	lines := []labelledLine{
		{"Name", view.Name},
		{"ID", view.ID},
		{"Slug", view.Slug},
		{"Repository", fmt.Sprintf("%s (%s)", view.RepoName, view.RepoRoot)},
		{"Branch", view.Branch},
		{"Worktree", view.Worktree},
		{"Tmux session", view.TmuxSession},
		{"Provider", view.Provider},
		{"Creation", creationSummary(view)},
		{"Status", cmp.Or(view.Status, "none")},
	}
	if parent != "" {
		lines = append(lines, labelledLine{"Parent", parent})
	}
	if err := writeLabelledLines(w, lines); err != nil {
		return err
	}

	if len(detail.Children) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(w, "Children:"); err != nil {
		return err
	}
	for _, child := range detail.Children {
		if _, err := fmt.Fprintf(w, "  %s (%s): %s\n", child.Name, child.ID, taskStatusSummary(child)); err != nil {
			return err
		}
	}
	return nil
}

// creationSummary describes a task's creation status, with the step it is
// at while being created, and the step and error it failed with.
func creationSummary(view taskView) string {
	step := core.TaskCreateProgressStep(view.CreationStep)
	switch core.TaskCreationStatus(view.CreationStatus) {
	case core.TaskCreationStatusCreating:
		if step == "" {
			return view.CreationStatus
		}
		return fmt.Sprintf("%s (%s)", view.CreationStatus, creationStepPhrase(step))
	case core.TaskCreationStatusFailed:
		summary := view.CreationStatus
		if step != "" {
			summary += " while " + creationStepPhrase(step)
		}
		if view.creationError != "" {
			summary += ": " + view.creationError
		}
		return summary
	default:
		return view.CreationStatus
	}
}

// taskStatusSummary is a task's status in a word: its creation status until
// it is ready, then its runtime status, if it has one.
func taskStatusSummary(view taskView) string {
	if view.CreationStatus != string(core.TaskCreationStatusReady) || view.Status == "" {
		return view.CreationStatus
	}
	return view.Status
}
