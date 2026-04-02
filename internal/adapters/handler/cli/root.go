package cli

import "github.com/spf13/cobra"

type Dependencies struct{}

func NewRootCommand(deps Dependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Manage task worktrees and tmux sessions for Codex-driven work",
	}

	cmd.AddCommand(newNewCommand(deps))
	cmd.AddCommand(newListCommand(deps))
	cmd.AddCommand(newOpenCommand(deps))
	cmd.AddCommand(newStatusCommand(deps))
	cmd.AddCommand(newDoctorCommand(deps))

	return cmd
}
