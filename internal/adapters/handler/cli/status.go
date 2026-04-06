package cli

import (
	"context"
	"fmt"

	"agent/internal/core"

	"github.com/spf13/cobra"
)

type statusTaskGetter interface {
	GetTask(context.Context, string) (*core.Task, error)
}

func newStatusCommand(service statusTaskGetter) *cobra.Command {
	return &cobra.Command{
		Use:   "status <task>",
		Short: "Show task status",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if service == nil {
				return fmt.Errorf("service not configured")
			}

			task, err := service.GetTask(context.Background(), args[0])
			if err != nil {
				return err
			}

			format := "Name: %s\nSlug: %s\nRepo: %s\n" +
				"Status: %s\nSession: %s\n" +
				"AgentWindow: %s\nEditorWindow: %s\n" +
				"Worktree: %s\nWorktreeExists: %t\n" +
				"BranchExists: %t\nSessionExists: %t\n" +
				"AgentWindowExists: %t\n" +
				"EditorWindowExists: %t\n"
			_, err = fmt.Fprintf(
				cmd.OutOrStdout(),
				format,
				task.DisplayName,
				task.Slug,
				task.RepoName,
				task.Status,
				task.TmuxSession,
				task.AgentWindowName,
				task.EditorWindowName,
				task.WorktreePath,
				task.WorktreeExists,
				task.BranchExists,
				task.SessionExists,
				task.AgentWindowExists,
				task.EditorWindowExists,
			)
			return err
		},
	}
}
