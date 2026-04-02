package cli

import "github.com/spf13/cobra"

func newListCommand(_ Dependencies) *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List known tasks",
		RunE: func(_ *cobra.Command, _ []string) error {
			return nil
		},
	}
}
