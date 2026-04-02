package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

func newOpenCommand(deps Dependencies) *cobra.Command {
	return &cobra.Command{
		Use:   "open <task>",
		Short: "Open an existing task session",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if deps.Service == nil {
				return fmt.Errorf("service not configured")
			}

			return deps.Service.OpenTask(context.Background(), args[0])
		},
	}
}
