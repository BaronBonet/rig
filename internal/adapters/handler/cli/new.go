package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"agent/internal/core"

	"github.com/spf13/cobra"
)

func newNewCommand(deps Dependencies) *cobra.Command {
	var nonInteractive bool
	var jsonOutput bool
	var provider string

	cmd := &cobra.Command{
		Use:   "new <prompt>",
		Short: "Create a new task session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps.Service == nil {
				return fmt.Errorf("service not configured")
			}

			prompt := args[0]
			input := core.NewTaskInput{
				Cwd:      deps.Cwd,
				Prompt:   prompt,
				Provider: provider,
			}

			if !nonInteractive {
				if _, err := fmt.Fprintln(cmd.ErrOrStderr(), "Naming task..."); err != nil {
					return err
				}
				suggested, err := deps.Service.SuggestTaskName(context.Background(), prompt, provider)
				if err != nil {
					return err
				}

				msg := fmt.Sprintf(
					"Proposed name [%s] (press Enter to accept or type a replacement): ",
					suggested,
				)
				if _, err = fmt.Fprint(cmd.OutOrStdout(), msg); err != nil {
					return err
				}

				line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
				if err != nil && err.Error() != "EOF" {
					return err
				}

				line = strings.TrimSpace(line)
				if line == "" || strings.EqualFold(line, "y") || strings.EqualFold(line, "yes") {
					line = suggested
				}
				input.ConfirmedDisplayName = line
			}

			task, err := deps.Service.CreateTaskWithProgress(
				context.Background(),
				input,
				core.CreateTaskOptions{OpenSession: !jsonOutput},
				func(event core.TaskProgress) {
					if strings.TrimSpace(event.Message) == "" {
						return
					}
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(), event.Message)
				},
			)
			if err != nil {
				return err
			}

			if jsonOutput {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(task)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&nonInteractive, "non-interactive", false, "accept the suggested name without prompting")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "print the created task as JSON")
	cmd.Flags().StringVar(&provider, "provider", "", "provider to use (codex, claude)")

	return cmd
}
