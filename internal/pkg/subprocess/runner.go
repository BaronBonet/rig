package subprocess

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"
)

// pipeCloseDelay bounds how long a run waits for output pipes to close after
// its process exits or is killed. Without it, a grandchild holding the pipes,
// such as a binary spawned by an npm CLI wrapper, keeps a cancelled run alive
// until the grandchild exits on its own.
const pipeCloseDelay = 2 * time.Second

type Result struct {
	Stdout string
	Stderr string
}

type RunWithStdinOptions struct {
	Cwd   string
	Stdin string
	Name  string
	Args  []string
	// Env holds NAME=value pairs added to the environment the process
	// inherits, replacing any it has by the same name.
	Env []string
}

type StartLineSessionOptions struct {
	Cwd  string
	Name string
	Args []string
}

type Runner interface {
	Run(ctx context.Context, cwd string, name string, args ...string) (Result, error)
	RunWithStdin(ctx context.Context, opts RunWithStdinOptions) (Result, error)
	// StartLineSession starts a process for a line-by-line exchange over its
	// stdin and stdout. ctx bounds the whole session: once it is done, the
	// process and its children are asked to terminate and Receive reports
	// ctx's error. The caller must Close the session.
	StartLineSession(ctx context.Context, opts StartLineSessionOptions) (LineSession, error)
}

// LineSession is a running process spoken to one line at a time, such as a
// JSON-RPC server on stdio. Its stdin stays open until Close, for processes
// that stop answering once their input ends. It is meant for one goroutine.
type LineSession interface {
	// Send writes line and a newline to the process's stdin.
	Send(line string) error
	// Receive returns the next stdout line without its newline. Once stdout
	// ends it returns an error that errors.Is reports as io.EOF, carrying the
	// process's exit status and stderr when either says more.
	Receive() (string, error)
	// Close closes stdin and waits for the process to exit. A process that
	// does not exit is asked to terminate, with its children, and is killed
	// only if it still does not. Receive after Close returns io.EOF.
	Close() error
}

type CommandError struct {
	Err    error
	Cwd    string
	Name   string
	Stdout string
	Stderr string
	Args   []string
}

func (e CommandError) Error() string {
	var builder strings.Builder
	if strings.TrimSpace(e.Cwd) != "" {
		builder.WriteString(e.Cwd)
		builder.WriteString(": ")
	}

	builder.WriteString("command ")
	builder.WriteString(strings.TrimSpace(strings.Join(append([]string{e.Name}, e.Args...), " ")))
	builder.WriteString(" failed: ")
	builder.WriteString(e.Err.Error())
	builder.WriteString("\nstdout:\n")
	builder.WriteString(strings.TrimSpace(e.Stdout))
	builder.WriteString("\nstderr:\n")
	builder.WriteString(strings.TrimSpace(e.Stderr))

	return builder.String()
}

func (e CommandError) Unwrap() error {
	return e.Err
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, cwd string, name string, args ...string) (Result, error) {
	return ExecRunner{}.RunWithStdin(ctx, RunWithStdinOptions{
		Cwd:  cwd,
		Name: name,
		Args: args,
	})
}

func (ExecRunner) RunWithStdin(ctx context.Context, opts RunWithStdinOptions) (Result, error) {
	cmd := exec.CommandContext(ctx, opts.Name, opts.Args...)
	cmd.Dir = opts.Cwd
	cmd.WaitDelay = pipeCloseDelay
	if len(opts.Env) > 0 {
		// The last value of a name wins, so these replace inherited ones.
		cmd.Env = append(os.Environ(), opts.Env...)
	}
	if opts.Stdin != "" {
		cmd.Stdin = strings.NewReader(opts.Stdin)
	}

	var stdoutBuf strings.Builder
	var stderrBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	result := Result{
		Stdout: stdoutBuf.String(),
		Stderr: stderrBuf.String(),
	}

	if err != nil {
		return result, CommandError{
			Cwd:    opts.Cwd,
			Name:   opts.Name,
			Args:   append([]string(nil), opts.Args...),
			Stdout: result.Stdout,
			Stderr: result.Stderr,
			Err:    err,
		}
	}

	return result, nil
}
