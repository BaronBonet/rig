// Package cli is the command-line handler: the non-interactive rig commands
// that people, scripts and agents run beside the TUI. Like the TUI it is a
// client of the core.TaskFrontend port and knows nothing of what serves it.
// It never attaches a terminal to a task's Session, only, with rig repo main,
// to a repository's main session.
//
// Every command prints its result to stdout, as JSON with --json, and any
// progress to stderr, and fails with an error that the caller reports and
// exits non-zero on.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/BaronBonet/rig/internal/core"
)

// Dependencies are what the CLI commands need from the composition root.
type Dependencies struct {
	// Frontend returns a ready task frontend, starting whatever serves it
	// first when it is not running.
	Frontend func(ctx context.Context) (core.TaskFrontend, error)
	// Stdin is read for a prompt passed on standard input.
	Stdin io.Reader
	// Stdout receives command results.
	Stdout io.Writer
	// Stderr receives progress and warnings.
	Stderr io.Writer
	// Getwd returns the caller's working directory.
	Getwd func() (string, error)
	// StdinIsTerminal reports whether Stdin is an interactive terminal. A
	// command reads a terminal only when told to with -, since nothing may be
	// typed there.
	StdinIsTerminal func() bool
}

// NewTaskCommand returns the task command group. Unset streams default to the
// process's own.
func NewTaskCommand(deps Dependencies) *cobra.Command {
	deps = deps.withDefaultStreams()
	return newCommandGroup(deps, "task", "Work with tasks without the TUI",
		newTaskCreateCommand(deps), newTaskListCommand(deps), newTaskShowCommand(deps))
}

// NewRepoCommand returns the repo command group. Unset streams default to the
// process's own.
func NewRepoCommand(deps Dependencies) *cobra.Command {
	deps = deps.withDefaultStreams()
	return newCommandGroup(deps, "repo", "Work with the repositories rig knows",
		newRepoListCommand(deps), newRepoMainCommand(deps))
}

// newCommandGroup returns a command group of commands that prints to deps'
// streams.
func newCommandGroup(deps Dependencies, use string, short string, commands ...*cobra.Command) *cobra.Command {
	group := &cobra.Command{
		Use:           use,
		Short:         short,
		SilenceUsage:  true,
		SilenceErrors: true,
		// A command group prints its help, and an unknown subcommand is an
		// error rather than help that exits successfully.
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	group.SetOut(deps.Stdout)
	group.SetErr(deps.Stderr)
	group.AddCommand(commands...)

	return group
}

func (d Dependencies) withDefaultStreams() Dependencies {
	if d.Stdin == nil {
		d.Stdin = os.Stdin
	}
	if d.Stdout == nil {
		d.Stdout = os.Stdout
	}
	if d.Stderr == nil {
		d.Stderr = os.Stderr
	}
	return d
}

// frontend returns the ready task frontend the commands talk to.
func (d Dependencies) frontend(ctx context.Context) (core.TaskFrontend, error) {
	if d.Frontend == nil {
		return nil, errors.New("task frontend not configured")
	}
	frontend, err := d.Frontend(ctx)
	if err != nil {
		return nil, err
	}
	if frontend == nil {
		return nil, errors.New("task frontend not configured")
	}
	return frontend, nil
}

// workingDirectory returns the caller's working directory with its symlinks
// resolved, as the paths of task worktrees are, so that a worktree reached
// through a symlink is still found. It is the path as given when that path
// cannot be resolved.
func (d Dependencies) workingDirectory() (string, error) {
	if d.Getwd == nil {
		return "", errors.New("working directory not configured")
	}
	cwd, err := d.Getwd()
	if err != nil {
		return "", fmt.Errorf("read the working directory: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		return resolved, nil
	}
	return cwd, nil
}

// untilInterrupted returns a copy of ctx that is also cancelled when the user
// interrupts the command, as Ctrl-C does, or it is told to terminate.
func untilInterrupted(ctx context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
}

// writeJSON prints value as indented JSON, leaving characters such as & and <
// as they are.
func writeJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
