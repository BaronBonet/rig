package execx

import (
	"context"
	"os/exec"
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

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, cwd string, name string, args ...string) (Result, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = cwd

	output, err := cmd.CombinedOutput()
	result := Result{Stdout: string(output)}

	return result, err
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
