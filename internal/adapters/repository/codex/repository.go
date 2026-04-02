package codex

import (
	"context"
	"strings"

	"agent/internal/core"
	"agent/internal/pkg/execx"
)

type Repository struct {
	runner execx.Runner
	binary string
}

func NewRepository(runner execx.Runner, binary string) *Repository {
	if binary == "" {
		binary = "codex"
	}

	return &Repository{
		runner: runner,
		binary: binary,
	}
}

func (r *Repository) IsAvailable(ctx context.Context) error {
	_, err := r.runner.Run(ctx, "", r.binary, "--help")
	return err
}

func (r *Repository) ProposeTaskName(ctx context.Context, prompt string) (string, error) {
	result, err := r.runner.Run(
		ctx,
		"",
		r.binary,
		"exec",
		"--skip-git-repo-check",
		"Reply with only a short task title: "+prompt,
	)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(result.Stdout), nil
}

func (r *Repository) BuildLaunchCommand(task *core.Task) ([]string, error) {
	return []string{r.binary, task.Prompt}, nil
}
