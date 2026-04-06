package execx

import (
	"context"
	"os/exec"
	"strings"
)

type Result struct {
	Stdout string
	Stderr string
}

type RunWithStdinOptions struct {
	Cwd   string
	Stdin string
	Name  string
	Args  []string
}

type Runner interface {
	Run(ctx context.Context, cwd string, name string, args ...string) (Result, error)
	RunWithStdin(ctx context.Context, opts RunWithStdinOptions) (Result, error)
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
