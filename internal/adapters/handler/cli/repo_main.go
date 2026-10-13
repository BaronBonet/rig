package cli

import (
	"github.com/spf13/cobra"
)

func newRepoMainCommand(deps Dependencies) *cobra.Command {
	return &cobra.Command{
		Use:   "main",
		Short: "Go to the main tmux session of the current repository",
		Long: "Switch to the tmux session of the repository the working directory is in, the one on its primary " +
			"checkout rather than a task's worktree: a session named after the repository in any letter case, " +
			"or else one whose first pane sits in the checkout. When there is none, rig checks out the default " +
			"branch there and creates the session, named after the repository in capitals.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := untilInterrupted(cmd.Context())
			defer stop()

			cwd, err := deps.workingDirectory()
			if err != nil {
				return err
			}
			frontend, err := deps.frontend(ctx)
			if err != nil {
				return err
			}
			return frontend.AttachRepoSession(ctx, cwd)
		},
	}
}
