package cli

import "github.com/spf13/cobra"

func newDoctorCommand(_ Dependencies) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check environment health",
		RunE: func(_ *cobra.Command, _ []string) error {
			return nil
		},
	}
}
