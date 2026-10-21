//go:build integration

package tmux

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BaronBonet/rig/internal/core"
	"github.com/BaronBonet/rig/internal/pkg/subprocess"

	"github.com/stretchr/testify/require"
)

// isolatedTmuxRunner keeps every repository command on the test's socket.
type isolatedTmuxRunner struct {
	socket string
}

func (r isolatedTmuxRunner) Run(
	ctx context.Context, cwd, name string, args ...string,
) (subprocess.Result, error) {
	return r.RunWithStdin(ctx, subprocess.RunWithStdinOptions{Cwd: cwd, Name: name, Args: args})
}

func (r isolatedTmuxRunner) RunWithStdin(
	ctx context.Context, opts subprocess.RunWithStdinOptions,
) (subprocess.Result, error) {
	if opts.Name == "tmux" {
		opts.Args = append([]string{"-S", r.socket, "-f", "/dev/null"}, opts.Args...)
		opts.Env = map[string]string{"TMUX": "", "SHELL": "/bin/sh"}
	}
	return (subprocess.ExecRunner{}).RunWithStdin(ctx, opts)
}

func TestRepositoryIntegration_DetachKeepsTaskAliveUntilExplicitlyClosed(t *testing.T) {
	_, err := exec.LookPath("tmux")
	require.NoError(t, err)
	// A full test-name TempDir path exceeds macOS's Unix socket path limit.
	directory, err := os.MkdirTemp("", "rig-tmux-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	runner := isolatedTmuxRunner{socket: filepath.Join(directory, "sock")}
	run := func(args ...string) string {
		result, runErr := runner.Run(t.Context(), "", "tmux", args...)
		require.NoError(t, runErr)
		return strings.TrimSpace(result.Stdout)
	}
	run("new-session", "-d", "-s", "rig", "/bin/sleep", "120")
	t.Cleanup(func() { _, _ = runner.Run(context.Background(), "", "tmux", "kill-server") })
	run("set-option", "-g", "default-shell", "/bin/sh")
	run("set-option", "-g", "default-command", "sleep 120")
	rigPane := run("display-message", "-p", "-t", "=rig:", "#{pane_id}")

	attachClient := func(session string) io.WriteCloser {
		cmd := exec.Command("tmux", "-S", runner.socket, "-C", "attach-session", "-t", "="+session)
		stdin, pipeErr := cmd.StdinPipe()
		require.NoError(t, pipeErr)
		cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
		require.NoError(t, cmd.Start())
		t.Cleanup(func() {
			_ = stdin.Close()
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		})
		return stdin
	}
	send := func(client io.Writer, command string) {
		_, writeErr := io.WriteString(client, command+"\n")
		require.NoError(t, writeErr)
	}
	clientCount := func(session string) string {
		return run("display-message", "-p", "-t", "="+session+":", "#{session_attached}")
	}
	exists := func(session string) bool {
		_, checkErr := runner.Run(t.Context(), "", "tmux", "has-session", "-t", "="+session)
		return checkErr == nil
	}

	client := attachClient("rig")
	require.Eventually(t, func() bool { return clientCount("rig") == "1" }, 3*time.Second, 10*time.Millisecond)
	repo := New(runner).(*repository)
	repo.sleep = func(time.Duration) {}
	repo.getenv = func(key string) string {
		return map[string]string{"TMUX": "inside", "TMUX_PANE": rigPane}[key]
	}
	task := &core.Task{ID: "task-1", TmuxSession: "task", WorktreePath: t.TempDir()}
	require.NoError(t, repo.StartTaskSession(t.Context(), task, core.TaskSessionLaunchSpec{}))
	require.True(t, exists("task"), "a new task survives until first opened")
	require.NoError(t, repo.AttachTaskSession(t.Context(), task))
	panePID := run("display-message", "-p", "-t", "=task:task", "#{pane_pid}")
	otherClient := attachClient("task")
	require.Eventually(t, func() bool { return clientCount("task") == "2" }, 3*time.Second, 10*time.Millisecond)
	send(client, "switch-client -t =rig")
	require.Eventually(t, func() bool { return clientCount("task") == "1" }, 3*time.Second, 10*time.Millisecond)
	send(otherClient, "detach-client")
	require.Eventually(t, func() bool { return clientCount("task") == "0" }, 3*time.Second, 10*time.Millisecond)
	require.True(t, exists("task"), "detaching the last client keeps the task running")
	require.Equal(t, panePID, run("display-message", "-p", "-t", "=task:task", "#{pane_pid}"))
	require.True(t, exists("rig"))

	require.NoError(t, repo.AttachTaskSession(t.Context(), task))
	send(client, "switch-client -t =rig")
	require.Eventually(t, func() bool { return clientCount("task") == "0" }, 3*time.Second, 10*time.Millisecond)
	require.True(t, exists("task"), "returning to Rig keeps the task running")
	require.Equal(t, panePID, run("display-message", "-p", "-t", "=task:task", "#{pane_pid}"))
	require.True(t, exists("rig"), "returning to Rig keeps its dashboard alive")

	// Shelving uses this explicit teardown; normal detachment never does.
	require.NoError(t, repo.DeleteTaskSession(t.Context(), task))
	require.False(t, exists("task"))
	require.NoError(t, repo.StartTaskSession(t.Context(), task, core.TaskSessionLaunchSpec{}))
	require.True(t, exists("task"), "a closed task can be recreated on demand")
}
