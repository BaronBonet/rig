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
				Cwd:    deps.Cwd,
				Prompt: prompt,
			}

			if !nonInteractive {
				suggested, err := deps.Service.SuggestTaskName(context.Background(), prompt)
				if err != nil {
					return err
				}

				if _, err = fmt.Fprintf(cmd.OutOrStdout(), "Proposed name [%s]: ", suggested); err != nil {
					return err
				}

				line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
				if err != nil && err.Error() != "EOF" {
					return err
				}

				line = strings.TrimSpace(line)
				if line == "" {
					line = suggested
				}
				input.ConfirmedDisplayName = line
			}

			task, err := deps.Service.NewTask(context.Background(), input)
			if err != nil {
				return err
			}

			if jsonOutput {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(task)
			}

			_, err = fmt.Fprintf(
				cmd.OutOrStdout(),
				"created task %s in session %s\n",
				task.DisplayName,
				task.TmuxSession,
			)
			return err
		},
	}

	cmd.Flags().BoolVar(&nonInteractive, "non-interactive", false, "accept the suggested name without prompting")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "print the created task as JSON")

	return cmd
}
