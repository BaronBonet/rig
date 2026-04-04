package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

func newListCommand(deps Dependencies) *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List known tasks",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if deps.Service == nil {
				return fmt.Errorf("service not configured")
			}

			tasks, err := deps.Service.ListTasks(context.Background())
			if err != nil {
				return err
			}

			header := "NAME\tREPO\tPROVIDER\tSTATUS\t" +
				"AGENT\tEDITOR\tSESSION\tBRANCH"
			if _, err = fmt.Fprintln(cmd.OutOrStdout(), header); err != nil {
				return err
			}

			for _, task := range tasks {
				if _, err = fmt.Fprintf(
					cmd.OutOrStdout(),
					"%s\t%s\t%s\t%s\t%t\t%t\t%s\t%s\n",
					task.DisplayName,
					task.RepoName,
					task.Provider,
					task.Status,
					task.AgentWindowExists,
					task.EditorWindowExists,
					task.TmuxSession,
					task.BranchName,
				); err != nil {
					return err
				}
			}

			return nil
		},
	}
}
