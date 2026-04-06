package cli

import (
	"context"
	"fmt"
	"io"

	"agent/internal/core"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"
)

type TaskService interface {
	Doctor(ctx context.Context, cwd string) (core.DoctorResult, error)
	SuggestTaskName(ctx context.Context, prompt string, provider string) (string, error)
	CreateTaskWithProgress(
		ctx context.Context,
		input core.NewTaskInput,
		options core.CreateTaskOptions,
		progress func(core.TaskProgress),
	) (*core.Task, error)
	ListTasks(ctx context.Context) ([]*core.Task, error)
	OpenTask(ctx context.Context, idOrSlug string) error
	DeleteTaskResources(ctx context.Context, idOrSlug string) (*core.Task, error)
}

type Dependencies struct {
	Service         TaskService
	Stdout          io.Writer
	Stderr          io.Writer
	Cwd             string
	DefaultProvider string
}

func NewRootCommand(deps Dependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Manage task worktrees and tmux sessions for agent-driven work",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if deps.Service == nil {
				return fmt.Errorf("service not configured")
			}

			program := tea.NewProgram(
				newTUIModel(deps.Service, deps.Cwd, deps.DefaultProvider),
				tea.WithInput(cmd.InOrStdin()),
				tea.WithOutput(cmd.OutOrStdout()),
			)

			_, err := program.Run()
			return err
		},
	}

	if deps.Stdout != nil {
		cmd.SetOut(deps.Stdout)
	}

	if deps.Stderr != nil {
		cmd.SetErr(deps.Stderr)
	}

	cmd.AddCommand(newDoctorCommand(deps))

	return cmd
}
