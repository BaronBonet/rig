package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func newDoctorCommand(deps Dependencies) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check environment health",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if deps.Service == nil {
				return fmt.Errorf("service not configured")
			}

			cwd := deps.Cwd
			if strings.TrimSpace(cwd) == "" {
				var err error
				cwd, err = os.Getwd()
				if err != nil {
					return err
				}
			}

			result, err := deps.Service.Doctor(context.Background(), cwd)
			if err != nil {
				return err
			}

			for _, note := range result.Notes {
				if _, err = fmt.Fprintln(cmd.OutOrStdout(), note); err != nil {
					return err
				}
			}

			if len(result.Failures) == 0 {
				_, err = fmt.Fprintln(cmd.OutOrStdout(), "doctor: ok")
				return err
			}

			for _, failure := range result.Failures {
				if _, err = fmt.Fprintln(cmd.OutOrStdout(), failure); err != nil {
					return err
				}
			}

			return nil
		},
	}
}
