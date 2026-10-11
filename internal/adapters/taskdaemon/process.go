package taskdaemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/BaronBonet/rig/internal/core"
)

const (
	// healthyTimeout bounds how long a client waits for a freshly spawned
	// daemon to become healthy. It must absorb the slowest legitimate startup,
	// not the typical one: replacing a daemon after a rig upgrade stacks the
	// old daemon's shutdown, first execution of the new binary, and task
	// database opening. Giving up early strands a daemon that becomes healthy
	// moments later; the retry loop still connects within one retryInterval
	// once the socket is up, so a generous bound costs nothing on success.
	healthyTimeout         = 30 * time.Second
	socketOperationTimeout = 2 * time.Second
	retryInterval          = 25 * time.Millisecond
)

func ensureRunning(ctx context.Context, cfg Config) error {
	if cfg.SocketPath == "" {
		return fmt.Errorf("task daemon socket path not configured")
	}
	if cfg.ExecPath == "" {
		execPath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve task daemon executable: %w", err)
		}
		cfg.ExecPath = execPath
	}

	if err := probeSocketHealth(ctx, cfg.SocketPath); err == nil {
		if err := probeFrontendProtocol(ctx, cfg.SocketPath); err != nil {
			return restartDaemon(ctx, cfg)
		}
		if err := probeFrontendBuildVersion(ctx, cfg.SocketPath); err == nil {
			return nil
		}
		return restartDaemon(ctx, cfg)
	}

	if err := spawnTaskDaemonProcess(ctx, cfg.ExecPath, cfg.Env, cfg.SocketPath); err != nil {
		return err
	}

	return waitForHealthyTaskDaemon(ctx, cfg.SocketPath)
}

func restartDaemon(ctx context.Context, cfg Config) error {
	if cfg.SocketPath == "" {
		return fmt.Errorf("task daemon socket path not configured")
	}

	exists, err := taskDaemonSocketPathExists(cfg.SocketPath)
	if err != nil {
		return err
	}
	if exists {
		if err := stopTaskDaemon(ctx, cfg.SocketPath); err != nil {
			if !isRecoverableStaleSocketError(err) {
				return err
			}
		}
		if err := os.Remove(cfg.SocketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove stale task daemon socket: %w", err)
		}
	}

	return ensureRunning(ctx, cfg)
}

func stopDaemonIfRunning(ctx context.Context, cfg Config) error {
	if cfg.SocketPath == "" {
		return fmt.Errorf("task daemon socket path not configured")
	}

	exists, err := taskDaemonSocketPathExists(cfg.SocketPath)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}

	if err := stopTaskDaemon(ctx, cfg.SocketPath); err != nil {
		if !isRecoverableStaleSocketError(err) {
			return err
		}
	}

	if err := os.Remove(cfg.SocketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale task daemon socket: %w", err)
	}

	return nil
}

func daemonStatus(ctx context.Context, cfg Config) (*core.TaskDaemonStatus, error) {
	if cfg.SocketPath == "" {
		return nil, fmt.Errorf("task daemon socket path not configured")
	}

	status := &core.TaskDaemonStatus{SocketPath: cfg.SocketPath}
	exists, err := taskDaemonSocketPathExists(cfg.SocketPath)
	if err != nil {
		return nil, err
	}
	if !exists {
		return status, nil
	}

	status.Running = true
	if healthErr := probeSocketHealth(ctx, cfg.SocketPath); healthErr != nil {
		status.Error = healthErr.Error()
		if isRecoverableStaleSocketError(healthErr) {
			status.Running = false
		}
		return status, nil
	}
	status.Healthy = true

	if recordDaemonStatusError(status, probeFrontendProtocol(ctx, cfg.SocketPath)) {
		return status, nil
	}
	if recordDaemonStatusError(status, probeFrontendBuildVersion(ctx, cfg.SocketPath)) {
		return status, nil
	}

	status.Compatible = true
	return status, nil
}

func recordDaemonStatusError(status *core.TaskDaemonStatus, err error) bool {
	if err == nil {
		return false
	}

	status.Error = err.Error()
	return true
}

func stopTaskDaemon(ctx context.Context, socketPath string) error {
	if socketPath == "" {
		return fmt.Errorf("task daemon socket path not configured")
	}

	operationCtx, cancel := context.WithTimeout(ctx, socketOperationTimeout)
	defer cancel()

	conn, err := dialDaemonSocket(operationCtx, socketPath)
	if err != nil {
		return err
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(socketRequest{Command: socketCommandStop}); err != nil {
		return err
	}

	var resp handshakeEnvelope
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&resp); err != nil {
		return err
	}
	if resp.Type != handshakeEnvelopeStopping || !resp.OK {
		if resp.Error != "" {
			return errors.New(resp.Error)
		}
		return fmt.Errorf("unexpected task daemon stop response %q", resp.Type)
	}

	return nil
}

func spawnTaskDaemonProcess(ctx context.Context, execPath string, env []string, socketPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	cmd := taskDaemonCommand(ctx, execPath, os.Environ(), taskDaemonWorkingDir(), env)

	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}

	startupLog, err := openTaskDaemonStartupLog(defaultTaskDaemonStartupLogPath(socketPath))
	if err != nil {
		_ = devNull.Close()
		return err
	}

	configureDetachedProcess(cmd, devNull, startupLog)
	if err := cmd.Start(); err != nil {
		_ = devNull.Close()
		_ = startupLog.Close()
		return err
	}

	go func() {
		defer devNull.Close()
		defer startupLog.Close()
		_ = cmd.Wait()
	}()

	return nil
}

// callerSessionEnvKeys are the environment variables an agent sets for the
// commands it runs. A daemon started by such a command, as `rig task create`
// run by an agent does, must not pass them on to the agents it launches: with
// CLAUDECODE set, claude refuses to start as a nested session. The user's own
// provider configuration, such as CODEX_HOME, is kept.
var callerSessionEnvKeys = map[string]bool{
	"CLAUDECODE":                     true,
	"CLAUDE_CODE_ENTRYPOINT":         true,
	"CLAUDE_CODE_SSE_PORT":           true,
	"CODEX_SANDBOX":                  true,
	"CODEX_SANDBOX_NETWORK_DISABLED": true,
	"CODEX_THREAD_ID":                true,
}

// taskDaemonCommand builds the command that starts the daemon: the caller's
// environment without its agent session variables, plus env, run from dir
// rather than the caller's working directory, which may be a task worktree
// deleted later.
func taskDaemonCommand(
	ctx context.Context,
	execPath string,
	environ []string,
	dir string,
	env []string,
) *exec.Cmd {
	cmd := exec.CommandContext(context.WithoutCancel(ctx), execPath)
	cmd.Dir = dir
	cmd.Env = make([]string, 0, len(environ)+len(env))
	for _, entry := range environ {
		key, _, _ := strings.Cut(entry, "=")
		if callerSessionEnvKeys[key] {
			continue
		}
		cmd.Env = append(cmd.Env, entry)
	}
	cmd.Env = append(cmd.Env, env...)

	return cmd
}

// taskDaemonWorkingDir is the directory the daemon runs from: the user's home
// directory, or the root directory when it is unknown.
func taskDaemonWorkingDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return string(filepath.Separator)
	}
	return home
}

func waitForHealthyTaskDaemon(ctx context.Context, socketPath string) error {
	waitCtx, cancel := context.WithTimeout(ctx, healthyTimeout)
	defer cancel()

	ticker := time.NewTicker(retryInterval)
	defer ticker.Stop()

	var lastErr error
	for {
		if err := probeSocketHealth(waitCtx, socketPath); err == nil {
			return nil
		} else if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			lastErr = err
		}

		select {
		case <-waitCtx.Done():
			startupLogErr := taskDaemonStartupLogError(defaultTaskDaemonStartupLogPath(socketPath))
			if lastErr != nil {
				return fmt.Errorf(
					"task daemon did not become healthy: %w",
					errors.Join(waitCtx.Err(), lastErr, startupLogErr),
				)
			}
			return fmt.Errorf("task daemon did not become healthy: %w", errors.Join(waitCtx.Err(), startupLogErr))
		case <-ticker.C:
		}
	}
}

func defaultTaskDaemonStartupLogPath(socketPath string) string {
	if socketPath == "" {
		return ""
	}

	return filepath.Join(filepath.Dir(socketPath), "daemon-startup.log")
}

func openTaskDaemonStartupLog(logPath string) (*os.File, error) {
	if logPath == "" {
		return os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	}

	if err := os.MkdirAll(filepath.Dir(logPath), socketDirMode); err != nil {
		return nil, fmt.Errorf("prepare task daemon startup log directory: %w", err)
	}

	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, socketFileMode)
	if err != nil {
		return nil, fmt.Errorf("open task daemon startup log: %w", err)
	}

	return file, nil
}

func taskDaemonStartupLogError(logPath string) error {
	if logPath == "" {
		return nil
	}

	content, err := os.ReadFile(logPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read task daemon startup log: %w", err)
	}

	message := strings.TrimSpace(string(content))
	if message == "" {
		return nil
	}
	const maxStartupLogBytes = 4096
	if len(message) > maxStartupLogBytes {
		message = message[len(message)-maxStartupLogBytes:]
	}

	return fmt.Errorf("daemon startup log:\n%s", message)
}

func taskDaemonSocketPathExists(socketPath string) (bool, error) {
	_, err := os.Stat(socketPath)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}

	return false, fmt.Errorf("check task daemon socket path: %w", err)
}

func isRecoverableStaleSocketError(err error) bool {
	var opErr *net.OpError
	if !errors.As(err, &opErr) {
		return false
	}

	return errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ENOENT)
}
