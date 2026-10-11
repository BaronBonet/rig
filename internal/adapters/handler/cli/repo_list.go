package cli

import (
	"context"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/BaronBonet/rig/internal/core"
)

// repoView is a known repository as the CLI prints it. Its JSON field names
// are a public contract that agents parse: fields may be added, but never
// renamed.
type repoView struct {
	Name      string `json:"name"`
	Root      string `json:"root"`
	TaskCount int    `json:"task_count"`
}

func newRepoView(repo core.KnownRepo) repoView {
	return repoView{Name: repo.Name, Root: repo.Root, TaskCount: repo.TaskCount}
}

type repoListOptions struct {
	json bool
}

func newRepoListCommand(deps Dependencies) *cobra.Command {
	var opts repoListOptions
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the repositories rig knows",
		Long: "List the repositories rig knows, one per line, with their name, number of tasks and root: the " +
			"repositories of existing tasks, and every git repository directly inside your repo directories, " +
			"which rig setup sets. rig task create --repo takes these names; a repository not listed needs " +
			"its path.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runRepoList(cmd.Context(), deps, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.json, "json", false, "print the repositories as JSON")

	return cmd
}

func runRepoList(ctx context.Context, deps Dependencies, opts repoListOptions) error {
	ctx, stop := untilInterrupted(ctx)
	defer stop()

	frontend, err := deps.frontend(ctx)
	if err != nil {
		return err
	}
	repos, err := frontend.ListRepos(ctx)
	if err != nil {
		return err
	}

	views := make([]repoView, 0, len(repos))
	for _, repo := range repos {
		views = append(views, newRepoView(repo))
	}
	if opts.json {
		return writeJSON(deps.Stdout, views)
	}
	return writeRepoTable(deps.Stdout, views)
}

// writeRepoTable prints one line per repository under a header: its name,
// number of tasks and root.
func writeRepoTable(w io.Writer, views []repoView) error {
	if len(views) == 0 {
		_, err := fmt.Fprintln(w, "No known repositories.")
		return err
	}

	columns := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(columns, "NAME\tTASKS\tROOT")
	for _, view := range views {
		fmt.Fprintf(columns, "%s\t%d\t%s\n", view.Name, view.TaskCount, view.Root)
	}
	return columns.Flush()
}
