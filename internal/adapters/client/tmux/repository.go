package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/BaronBonet/rig/internal/core"
	"github.com/BaronBonet/rig/internal/pkg/subprocess"
)

const (
	promptSubmitDelay = 500 * time.Millisecond
	taskWindowName    = "task"
)

type repository struct {
	runner subprocess.Runner
	now    func() time.Time
	sleep  func(time.Duration)
	getenv func(string) string
}

func New(runner subprocess.Runner) core.TmuxSessionClient {
	return &repository{
		runner: runner,
		now:    time.Now,
		sleep:  time.Sleep,
		getenv: os.Getenv,
	}
}

func (r *repository) HealthCheck(ctx context.Context) error {
	_, err := r.runner.Run(ctx, "", "tmux", "-V")
	return err
}

func (r *repository) StartTaskSession(ctx context.Context, task *core.Task, launch core.TaskSessionLaunchSpec) error {
	if err := r.createSession(ctx, task.TmuxSession, task.WorktreePath); err != nil {
		return err
	}

	if len(launch.Command) == 0 {
		return nil
	}

	if err := r.sendKeysToWindow(ctx, task.TmuxSession, taskWindowName, launch.Command); err != nil {
		return r.cleanupStartedSession(ctx, task.TmuxSession, err)
	}

	if len(launch.PrefillInput) == 0 {
		return nil
	}

	if err := r.waitForPrompt(ctx, task.TmuxSession, taskWindowName, launch.ReadyMarker); err != nil {
		return r.cleanupStartedSession(ctx, task.TmuxSession, err)
	}

	if err := r.typeInWindow(ctx, task.TmuxSession, taskWindowName, launch.PrefillInput); err != nil {
		return r.cleanupStartedSession(ctx, task.TmuxSession, err)
	}

	return nil
}

func (r *repository) AttachTaskSession(ctx context.Context, task *core.Task) error {
	if task == nil || strings.TrimSpace(task.TmuxSession) == "" {
		return fmt.Errorf("task tmux session is required")
	}

	command := "attach-session"
	if r.getenv != nil && strings.TrimSpace(r.getenv("TMUX")) != "" {
		command = "switch-client"
	}

	result, err := r.runner.Run(
		ctx,
		"",
		"tmux",
		command,
		"-t",
		exactSessionTarget(task.TmuxSession),
	)
	if isMissingSessionError(err, result) {
		return core.ErrTaskSessionNotFound
	}
	return err
}

func (r *repository) InspectTaskSession(ctx context.Context, task *core.Task) (core.TaskSessionRuntimeState, error) {
	if task == nil || strings.TrimSpace(task.TmuxSession) == "" {
		return core.TaskSessionRuntimeState{}, nil
	}

	result, err := r.runner.Run(
		ctx,
		"",
		"tmux",
		"list-panes",
		"-t",
		exactWindowTarget(task.TmuxSession, taskWindowName),
		"-F",
		"#{pane_current_command}",
	)
	if isMissingSessionError(err, result) {
		return core.TaskSessionRuntimeState{}, nil
	}
	if err != nil {
		return core.TaskSessionRuntimeState{}, err
	}

	return core.TaskSessionRuntimeState{
		Exists:         true,
		ActiveCommands: paneCommandsFromTmuxOutput(result.Stdout),
	}, nil
}

func (r *repository) DeleteTaskSession(ctx context.Context, task *core.Task) error {
	if task == nil || strings.TrimSpace(task.TmuxSession) == "" {
		return nil
	}

	result, err := r.runner.Run(
		ctx,
		"",
		"tmux",
		"kill-session",
		"-t",
		exactSessionTarget(task.TmuxSession),
	)
	if isMissingSessionError(err, result) {
		return nil
	}

	return err
}

func (r *repository) createSession(ctx context.Context, sessionName, workingDir string) error {
	sessionName = normalizedSessionName(sessionName)

	_, err := r.runner.Run(
		ctx,
		"",
		"tmux",
		"new-session",
		"-d",
		"-s",
		sessionName,
		"-n",
		taskWindowName,
		"-c",
		workingDir,
	)
	if err != nil {
		return err
	}

	r.sleep(promptSubmitDelay)

	_, err = r.runner.Run(
		ctx,
		"",
		"tmux",
		"new-window",
		"-d",
		"-t",
		exactSessionTarget(sessionName),
		"-n",
		"editor",
		"-c",
		workingDir,
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

func (r *repository) cleanupStartedSession(ctx context.Context, sessionName string, cause error) error {
	_, cleanupErr := r.runner.Run(ctx, "", "tmux", "kill-session", "-t", exactSessionTarget(sessionName))
	if cleanupErr != nil {
		return errors.Join(cause, cleanupErr)
	}

	return cause
}

func (r *repository) sendKeysToWindow(ctx context.Context, session, window string, command []string) error {
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

func (r *repository) capturePaneContent(ctx context.Context, session, window string) (string, error) {
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

func (r *repository) typeInWindow(ctx context.Context, session, window string, command []string) error {
	text := strings.Join(command, " ")
	bufferName := "rig-prefill-" + normalizedSessionName(session) + "-" + window

	_, err := r.runner.RunWithStdin(ctx, subprocess.RunWithStdinOptions{
		Cwd:   "",
		Name:  "tmux",
		Args:  []string{"load-buffer", "-b", bufferName, "-"},
		Stdin: text,
	})
	if err != nil {
		return fmt.Errorf("load task input into tmux buffer: %w", err)
	}

	_, pasteErr := r.runner.Run(
		ctx,
		"",
		"tmux",
		"paste-buffer",
		"-t",
		exactWindowTarget(session, window),
		"-b",
		bufferName,
	)

	_, deleteErr := r.runner.Run(ctx, "", "tmux", "delete-buffer", "-b", bufferName)
	if pasteErr != nil {
		if deleteErr != nil {
			return errors.Join(pasteErr, deleteErr)
		}
		return pasteErr
	}

	return deleteErr
}

func (r *repository) waitForPrompt(ctx context.Context, session, window, marker string) error {
	const (
		pollInterval = 500 * time.Millisecond
		timeout      = 30 * time.Second
	)

	deadline := r.now().Add(timeout)
	for r.now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		content, err := r.capturePaneContent(ctx, session, window)
		if err == nil && strings.Contains(content, marker) {
			return nil
		}

		r.sleep(pollInterval)
	}

	return errors.New("timed out waiting for " + marker + " prompt")
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

func paneCommandsFromTmuxOutput(output string) []string {
	lines := strings.Split(output, "\n")
	commands := make([]string, 0, len(lines))
	for _, line := range lines {
		command := strings.TrimSpace(line)
		if command == "" {
			continue
		}
		commands = append(commands, command)
	}
	return commands
}

func isMissingSessionError(err error, result subprocess.Result) bool {
	if err == nil {
		return false
	}

	stderr := result.Stderr
	if strings.TrimSpace(stderr) == "" {
		var commandErr subprocess.CommandError
		if errors.As(err, &commandErr) {
			stderr = commandErr.Stderr
		}
	}

	lower := strings.ToLower(stderr)
	return strings.Contains(lower, "can't find session") ||
		strings.Contains(lower, "can't find window") ||
		strings.Contains(lower, "can't find pane")
}
