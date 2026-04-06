package cli

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"
)

func newTUICommand(deps Dependencies) *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Open the task TUI",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if deps.Service == nil {
				return fmt.Errorf("service not configured")
			}

			program := tea.NewProgram(
				newTUIModel(deps.Service, deps.Cwd),
				tea.WithInput(cmd.InOrStdin()),
				tea.WithOutput(cmd.OutOrStdout()),
			)

			_, err := program.Run()
			return err
		},
	}
}
