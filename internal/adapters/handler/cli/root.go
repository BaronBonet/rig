package cli

import (
	"context"
	"io"

	"agent/internal/core"

	"github.com/spf13/cobra"
)

type TaskService interface {
	Doctor(ctx context.Context, cwd string) (core.DoctorResult, error)
	SuggestTaskName(ctx context.Context, prompt string) (string, error)
	NewTask(ctx context.Context, input core.NewTaskInput) (*core.Task, error)
	ListTasks(ctx context.Context) ([]*core.Task, error)
	GetTask(ctx context.Context, idOrSlug string) (*core.Task, error)
	OpenTask(ctx context.Context, idOrSlug string) error
}

type Dependencies struct {
	Service TaskService
	Stdout  io.Writer
	Stderr  io.Writer
	Cwd     string
}

func NewRootCommand(deps Dependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Manage task worktrees and tmux sessions for Codex-driven work",
	}

	if deps.Stdout != nil {
		cmd.SetOut(deps.Stdout)
	}

	if deps.Stderr != nil {
		cmd.SetErr(deps.Stderr)
	}

	cmd.AddCommand(newNewCommand(deps))
	cmd.AddCommand(newListCommand(deps))
	cmd.AddCommand(newOpenCommand(deps))
	cmd.AddCommand(newStatusCommand(deps))
	cmd.AddCommand(newDoctorCommand(deps))

	return cmd
}
