package cli

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
)

func newTUICommand(deps Dependencies) *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Open the cleanup TUI",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if deps.Service == nil {
				return fmt.Errorf("service not configured")
			}

			program := tea.NewProgram(
				newTUIModel(deps.Service),
				tea.WithAltScreen(),
				tea.WithInput(cmd.InOrStdin()),
				tea.WithOutput(cmd.OutOrStdout()),
			)

			_, err := program.Run()
			return err
		},
	}
}
