package cli

import "github.com/spf13/cobra"

func newOpenCommand(_ Dependencies) *cobra.Command {
	return &cobra.Command{
		Use:   "open",
		Short: "Open an existing task session",
		RunE: func(_ *cobra.Command, _ []string) error {
			return nil
		},
	}
}
