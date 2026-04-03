package tmux

import (
	"context"
	"errors"
	"os"
	"strings"

	"agent/internal/core"
	"agent/internal/pkg/execx"
)

type Repository struct {
	runner execx.Runner
}

func NewRepository(runner execx.Runner) *Repository {
	return &Repository{runner: runner}
}

func (r *Repository) IsAvailable(ctx context.Context) error {
	_, err := r.runner.Run(ctx, "", "tmux", "-V")
	return err
}

func (r *Repository) SessionExists(ctx context.Context, session string) (bool, error) {
	result, err := r.runner.Run(ctx, "", "tmux", "has-session", "-t", exactSessionTarget(session))
	if err != nil {
		if isMissingSession(result, err) {
			return false, nil
		}

		return false, err
	}

	return true, nil
}

func (r *Repository) CreateSession(ctx context.Context, in core.CreateSessionInput) error {
	_, err := r.runner.Run(
		ctx,
		"",
		"tmux",
		"new-session",
		"-d",
		"-s",
		in.SessionName,
		"-c",
		in.WorkingDir,
	)
	return err
}

func (r *Repository) KillSession(ctx context.Context, session string) error {
	_, err := r.runner.Run(ctx, "", "tmux", "kill-session", "-t", exactSessionTarget(session))
	return err
}

func (r *Repository) AttachOrSwitch(ctx context.Context, session string) error {
	command := "attach-session"
	if insideTmux() {
		command = "switch-client"
	}

	_, err := r.runner.Run(ctx, "", "tmux", command, "-t", exactSessionTarget(session))
	return err
}

func (r *Repository) SendKeys(ctx context.Context, session string, command []string) error {
	quoted := make([]string, 0, len(command))
	for _, part := range command {
		if strings.ContainsRune(part, ' ') {
			quoted = append(quoted, "'"+strings.ReplaceAll(part, "'", "'\\''")+"'")
			continue
		}

		quoted = append(quoted, part)
	}

	_, err := r.runner.Run(
		ctx,
		"",
		"tmux",
		"send-keys",
		"-t",
		firstPaneTarget(session),
		strings.Join(quoted, " "),
		"C-m",
	)
	return err
}

func exactSessionTarget(session string) string {
	return "=" + session
}

func firstPaneTarget(session string) string {
	return session + ":0.0"
}

func isMissingSession(result execx.Result, err error) bool {
	var commandErr execx.CommandError
	if !errors.As(err, &commandErr) {
		return false
	}

	output := strings.ToLower(result.Stderr + "\n" + result.Stdout + "\n" + commandErr.Stderr + "\n" + commandErr.Stdout)
	return strings.Contains(output, "can't find session")
}

func insideTmux() bool {
	return strings.TrimSpace(os.Getenv("TMUX")) != ""
}
