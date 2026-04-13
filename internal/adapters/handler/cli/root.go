package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	hookhttp "rig/internal/adapters/observability/codexhooks"
	observer "rig/internal/adapters/observability/observer"
	"rig/internal/core"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"
)

type TaskService interface {
	Doctor(ctx context.Context, cwd string) (core.DoctorResult, error)
	SuggestTaskName(ctx context.Context, prompt string, provider string) (core.TaskSuggestion, error)
	CreateTaskWithProgress(
		ctx context.Context,
		input core.NewTaskInput,
		options core.CreateTaskOptions,
		progress func(core.TaskProgress),
	) (*core.Task, error)
	ListTasks(ctx context.Context) ([]*core.Task, error)
	ListTaskViews(ctx context.Context) ([]*core.TaskView, error)
	ListTaskViewsByRepo(ctx context.Context, repoRoot string) ([]*core.TaskView, error)
	SubscribeTaskHookUpdates(ctx context.Context) (<-chan core.HookSessionSummary, func(), error)
	OpenTask(ctx context.Context, idOrSlug string) error
	DeleteTaskResources(ctx context.Context, idOrSlug string) (*core.Task, error)
	GetTaskHookEvents(ctx context.Context, taskID string, limit int) ([]core.HookEvent, error)
	GetPRStatus(ctx context.Context, repoRoot string, branchName string) (*core.PRStatus, error)
	InvalidatePRCache()
}

type ObserverProcessRunner interface {
	EnsureRunning(context.Context) error
}

type Dependencies struct {
	Service             TaskService
	HookIngestor        core.HookEventIngestor
	ObserverProcess     ObserverProcessRunner
	ObserverWatcher     *observer.TMuxWatcher
	HookListenAddr      string
	ObserverSocketPath  string
	ObserverFingerprint string
	Stdout              io.Writer
	Stderr              io.Writer
	Cwd                 string
	RepoRoot            string
	DefaultProvider     string
}

func NewRootCommand(deps Dependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rig",
		Short: "Manage isolated workspaces for AI-assisted coding tasks",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if deps.Service == nil {
				return fmt.Errorf("service not configured")
			}
			var startupErr error
			if deps.ObserverProcess != nil {
				startupErr = deps.ObserverProcess.EnsureRunning(cmd.Context())
			}

			program := tea.NewProgram(
				newTUIModel(
					deps.Service,
					deps.Cwd,
					deps.RepoRoot,
					deps.DefaultProvider,
					deps.ObserverSocketPath,
					startupErr,
				),
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
	cmd.AddCommand(newObserverCommand(deps))

	return cmd
}

func newIngestCommand(use string, deps Dependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:    use,
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
			if _, err := deps.HookIngestor.IngestHookEvent(cmd.Context(), input); err != nil &&
				!errors.Is(err, core.ErrUnmanagedHookEvent) {
				return err
			}

			return nil
		},
	}

	return cmd
}

func newForwardHookCommand(use string, deps Dependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:    use,
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := io.ReadAll(cmd.InOrStdin())
			if err != nil {
				return fmt.Errorf("read hook payload: %w", err)
			}

			forwarder := hookhttp.Forwarder{
				CollectorURL: observerCollectorURL(deps.HookListenAddr),
				Ingestor:     deps.HookIngestor,
			}

			return forwarder.Forward(cmd.Context(), args[0], body)
		},
	}

	return cmd
}

func observerCollectorURL(listenAddr string) string {
	listenAddr = strings.TrimSpace(listenAddr)
	if listenAddr == "" {
		return ""
	}

	if !strings.Contains(listenAddr, "://") {
		listenAddr = "http://" + listenAddr
	}
	if !strings.HasSuffix(listenAddr, "/hook") {
		listenAddr += "/hook"
	}

	return listenAddr
}

func newObserverCommand(deps Dependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "observer",
		Hidden: true,
		Args:   cobra.NoArgs,
	}

	cmd.AddCommand(&cobra.Command{
		Use:    "serve",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if deps.HookIngestor == nil {
				return fmt.Errorf("hook ingestor not configured")
			}

			return observer.Serve(cmd.Context(), observer.ServerConfig{
				SocketPath:     deps.ObserverSocketPath,
				HookListenAddr: deps.HookListenAddr,
				HookIngestor:   deps.HookIngestor,
				Watcher:        deps.ObserverWatcher,
				Hub:            observer.NewHub(),
				Fingerprint:    deps.ObserverFingerprint,
			})
		},
	})
	cmd.AddCommand(newIngestCommand("ingest <event-name>", deps))
	cmd.AddCommand(newForwardHookCommand("forward-hook <event-name>", deps))

	return cmd
}
