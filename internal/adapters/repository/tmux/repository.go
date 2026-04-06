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

func (r *Repository) WindowExists(ctx context.Context, session, window string) (bool, error) {
	window = windowOrDefault(window, "agent")

	result, err := r.runner.Run(
		ctx,
		"",
		"tmux",
		"list-windows",
		"-t",
		exactSessionTarget(session),
		"-F",
		"#{window_name}",
	)
	if err != nil {
		if isMissingSession(result, err) {
			return false, nil
		}

		return false, err
	}

	for _, line := range strings.Split(result.Stdout, "\n") {
		if strings.TrimSpace(line) == window {
			return true, nil
		}
	}

	return false, nil
}

func (r *Repository) CreateSession(ctx context.Context, in core.CreateSessionInput) error {
	sessionName := normalizedSessionName(in.SessionName)
	agentWindowName := windowOrDefault(in.AgentWindowName, "agent")
	editorWindowName := windowOrDefault(in.EditorWindowName, "editor")

	_, err := r.runner.Run(
		ctx,
		"",
		"tmux",
		"new-session",
		"-d",
		"-s",
		sessionName,
		"-n",
		agentWindowName,
		"-c",
		in.WorkingDir,
	)
	if err != nil {
		return err
	}

	_, err = r.runner.Run(
		ctx,
		"",
		"tmux",
		"new-window",
		"-d",
		"-t",
		exactSessionTarget(sessionName),
		"-n",
		editorWindowName,
		"-c",
		in.WorkingDir,
	)
	if err == nil {
		return nil
	}

	_, cleanupErr := r.runner.Run(ctx, "", "tmux", "kill-session", "-t", exactSessionTarget(sessionName))
	if cleanupErr != nil {
		return errors.Join(err, cleanupErr)
	}

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

func (r *Repository) SendKeysToWindow(ctx context.Context, session, window string, command []string) error {
	window = windowOrDefault(window, "agent")

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
		exactWindowTarget(session, window),
		strings.Join(quoted, " "),
		"C-m",
	)
	return err
}

func (r *Repository) CapturePaneContent(ctx context.Context, session, window string) (string, error) {
	window = windowOrDefault(window, "agent")

	result, err := r.runner.Run(
		ctx,
		"",
		"tmux",
		"capture-pane",
		"-t",
		exactWindowTarget(session, window),
		"-p",
	)
	if err != nil {
		return "", err
	}

	return result.Stdout, nil
}

func (r *Repository) TypeInWindow(ctx context.Context, session, window string, command []string) error {
	window = windowOrDefault(window, "agent")

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
		exactWindowTarget(session, window),
		strings.Join(quoted, " "),
	)
	return err
}

func exactSessionTarget(session string) string {
	return "=" + normalizedSessionName(session)
}

func exactWindowTarget(session, window string) string {
	return "=" + normalizedSessionName(session) + ":" + window
}

func normalizedSessionName(session string) string {
	return strings.ReplaceAll(session, ":", "-")
}

func windowOrDefault(window, fallback string) string {
	if strings.TrimSpace(window) == "" {
		return fallback
	}

	return window
}

func isMissingSession(result execx.Result, err error) bool {
	var commandErr execx.CommandError
	if !errors.As(err, &commandErr) {
		return false
	}

	output := strings.ToLower(
		result.Stderr + "\n" + result.Stdout + "\n" + commandErr.Stderr + "\n" + commandErr.Stdout,
	)
	return strings.Contains(output, "can't find session")
}

func insideTmux() bool {
	return strings.TrimSpace(os.Getenv("TMUX")) != ""
}
