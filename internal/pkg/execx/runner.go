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

type Call struct {
	Cwd  string
	Name string
	Args []string
}

type Runner interface {
	Run(ctx context.Context, cwd string, name string, args ...string) (Result, error)
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
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = cwd

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
			Cwd:    cwd,
			Name:   name,
			Args:   append([]string(nil), args...),
			Stdout: result.Stdout,
			Stderr: result.Stderr,
			Err:    err,
		}
	}

	return result, nil
}

type FakeRunner struct {
	Results []Result
	Errors  []error
	Calls   []Call
}

func NewFakeRunner(results []Result) *FakeRunner {
	return &FakeRunner{Results: results}
}

func (f *FakeRunner) Run(_ context.Context, cwd string, name string, args ...string) (Result, error) {
	f.Calls = append(f.Calls, Call{
		Cwd:  cwd,
		Name: name,
		Args: append([]string(nil), args...),
	})

	var result Result
	if len(f.Results) > 0 {
		result = f.Results[0]
		f.Results = f.Results[1:]
	}

	var err error
	if len(f.Errors) > 0 {
		err = f.Errors[0]
		f.Errors = f.Errors[1:]
	}

	return result, err
}
