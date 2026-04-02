package cli

import "github.com/spf13/cobra"

func newStatusCommand(_ Dependencies) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show task status",
		RunE: func(_ *cobra.Command, _ []string) error {
			return nil
		},
	}
}
