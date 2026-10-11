package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/BaronBonet/rig/internal/core"
)

type taskListOptions struct {
	json bool
}

func newTaskListCommand(deps Dependencies) *cobra.Command {
	var opts taskListOptions
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List every task",
		Long: "List every task, one per line, with its repository, branch, creation status, runtime status " +
			"and parent. Tasks are grouped by repository, as the TUI lists them.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runTaskList(cmd.Context(), deps, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.json, "json", false, "print the tasks as JSON")

	return cmd
}

func runTaskList(ctx context.Context, deps Dependencies, opts taskListOptions) error {
	ctx, stop := untilInterrupted(ctx)
	defer stop()

	frontend, err := deps.frontend(ctx)
	if err != nil {
		return err
	}
	tasks, err := frontend.ListTasks(ctx)
	if err != nil {
		return err
	}
	tasks = groupedByRepository(tasks)
	views, err := loadTaskViews(ctx, frontend, tasks)
	if err != nil {
		return err
	}

	if opts.json {
		return writeJSON(deps.Stdout, views)
	}
	return writeTaskTable(deps.Stdout, views, tasks)
}

// groupedByRepository returns the tasks grouped by repository, the
// repositories in the order their first task comes, and each repository's
// tasks in the order they come, as the TUI lists them.
func groupedByRepository(tasks []*core.Task) []*core.Task {
	var order []string
	groups := make(map[string][]*core.Task)
	for _, task := range tasks {
		if task == nil {
			continue
		}
		key := strings.TrimSpace(task.RepoRoot)
		if key == "" {
			key = strings.TrimSpace(task.RepoName)
		}
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], task)
	}

	grouped := make([]*core.Task, 0, len(tasks))
	for _, key := range order {
		grouped = append(grouped, groups[key]...)
	}
	return grouped
}

// writeTaskTable prints one line per task under a header: its ID, slug,
// repository, branch, creation status, runtime status and parent's slug.
// Missing values are left blank.
func writeTaskTable(w io.Writer, views []taskView, tasks []*core.Task) error {
	if len(views) == 0 {
		_, err := fmt.Fprintln(w, "No tasks.")
		return err
	}

	var table bytes.Buffer
	columns := tabwriter.NewWriter(&table, 0, 0, 2, ' ', 0)
	fmt.Fprintln(columns, "ID\tSLUG\tREPO\tBRANCH\tCREATION\tSTATUS\tPARENT")
	for _, view := range views {
		fmt.Fprintf(columns, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			view.ID,
			view.Slug,
			view.RepoName,
			view.Branch,
			view.CreationStatus,
			view.Status,
			parentSlug(tasks, view.ParentID),
		)
	}
	if err := columns.Flush(); err != nil {
		return err
	}

	// A blank last column leaves the padding of the one before it behind.
	for line := range strings.Lines(table.String()) {
		if _, err := fmt.Fprintln(w, strings.TrimRight(line, " \n")); err != nil {
			return err
		}
	}
	return nil
}

// parentSlug names a task's parent by its slug, or by its ID when the parent
// is not among tasks. It is empty for a task without a parent.
func parentSlug(tasks []*core.Task, parentID string) string {
	if parentID == "" {
		return ""
	}
	if parent := taskByID(tasks, parentID); parent != nil {
		return parent.Slug
	}
	return parentID
}
