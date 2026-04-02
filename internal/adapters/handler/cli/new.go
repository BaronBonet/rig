package cli

import "github.com/spf13/cobra"

func newNewCommand(_ Dependencies) *cobra.Command {
	return &cobra.Command{
		Use:   "new",
		Short: "Create a new task session",
		RunE: func(_ *cobra.Command, _ []string) error {
			return nil
		},
	}
}
