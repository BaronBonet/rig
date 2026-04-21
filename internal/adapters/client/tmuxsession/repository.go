package tmuxsession

import (
	"context"
	"errors"
	"strings"
	"time"

	"rig/internal/core"
	"rig/internal/pkg/subprocess"
)

const promptSubmitDelay = 500 * time.Millisecond

type repository struct {
	runner subprocess.Runner
	now    func() time.Time
	sleep  func(time.Duration)
}

func New(runner subprocess.Runner) core.TmuxSessionClient {
	return &repository{
		runner: runner,
		now:    time.Now,
		sleep:  time.Sleep,
	}
}

func (r *repository) StartTaskSession(ctx context.Context, task *core.Task, launch core.TaskSessionLaunchSpec) error {
	if err := r.createSession(ctx, task.TmuxSession, task.WorktreePath); err != nil {
		return err
	}

	if err := r.sendKeysToWindow(ctx, task.TmuxSession, "agent", launch.Command); err != nil {
		return err
	}

	if len(launch.PrefillInput) == 0 {
		return nil
	}

	if err := r.waitForPrompt(ctx, task.TmuxSession, "agent", launch.ReadyMarker); err != nil {
		return err
	}

	return r.typeInWindow(ctx, task.TmuxSession, "agent", launch.PrefillInput)
}

func (r *repository) OpenTaskSession(context.Context, *core.Task) error {
	panic("tmuxsession.Repository.OpenTaskSession not implemented")
}

func (r *repository) DeleteTaskSession(context.Context, *core.Task) error {
	panic("tmuxsession.Repository.DeleteTaskSession not implemented")
}

func (r *repository) InspectTaskSession(context.Context, *core.Task) (core.SessionResources, error) {
	panic("tmuxsession.Repository.InspectTaskSession not implemented")
}

func (r *repository) SnapshotTaskSession(context.Context, *core.Task) (core.RuntimeSnapshot, error) {
	panic("tmuxsession.Repository.SnapshotTaskSession not implemented")
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
		"agent",
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
	_, err := r.runner.Run(
		ctx,
		"",
		"tmux",
		"send-keys",
		"-t",
		exactWindowTarget(session, window),
		strings.Join(command, " "),
	)
	return err
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
