package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

func newStatusCommand(deps Dependencies) *cobra.Command {
	return &cobra.Command{
		Use:   "status <task>",
		Short: "Show task status",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps.Service == nil {
				return fmt.Errorf("service not configured")
			}

			task, err := deps.Service.GetTask(context.Background(), args[0])
			if err != nil {
				return err
			}

			_, err = fmt.Fprintf(
				cmd.OutOrStdout(),
				"Name: %s\nSlug: %s\nStatus: %s\nSession: %s\nWorktree: %s\nWorktreeExists: %t\nBranchExists: %t\nSessionExists: %t\n",
				task.DisplayName,
				task.Slug,
				task.Status,
				task.TmuxSession,
				task.WorktreePath,
				task.WorktreeExists,
				task.BranchExists,
				task.SessionExists,
			)
			return err
		},
	}
}
