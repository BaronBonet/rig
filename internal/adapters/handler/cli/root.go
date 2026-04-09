package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	hookhttp "agent/internal/adapters/observability/codexhooks"
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
	ListTaskViews(ctx context.Context) ([]*core.TaskView, error)
	GetTaskHookEvents(ctx context.Context, taskID string, limit int) ([]core.HookEvent, error)
	SubscribeTaskHookUpdates(ctx context.Context) (<-chan core.HookSessionSummary, func(), error)
	OpenTask(ctx context.Context, idOrSlug string) error
	DeleteTaskResources(ctx context.Context, idOrSlug string) (*core.Task, error)
}

type Dependencies struct {
	Service         TaskService
	HookIngestor    core.HookEventIngestor
	StartHookServer func() (func(), error)
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
			stopHookServer := func() {}
			if deps.StartHookServer != nil {
				cleanup, err := deps.StartHookServer()
				if err != nil {
					return err
				}
				if cleanup != nil {
					stopHookServer = cleanup
				}
			}
			defer stopHookServer()

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
	cmd.AddCommand(newHookIngestCommand(deps))

	return cmd
}

func newHookIngestCommand(deps Dependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "hook-ingest <event-name>",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps.HookIngestor == nil {
				return fmt.Errorf("hook ingestor not configured")
			}

			body, err := io.ReadAll(cmd.InOrStdin())
			if err != nil {
				return fmt.Errorf("read hook payload: %w", err)
			}

			input := hookhttp.DecodeHookEventInput(time.Now, args[0], body)
			if _, err := deps.HookIngestor.IngestHookEvent(cmd.Context(), input); err != nil && !errors.Is(err, core.ErrUnmanagedHookEvent) {
				return err
			}

			return nil
		},
	}

	return cmd
}
