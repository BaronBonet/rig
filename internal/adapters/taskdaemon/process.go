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
	"syscall"
	"time"

	"rig/internal/core"
)

const (
	healthyTimeout         = 2 * time.Second
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

	if err := spawnTaskDaemonProcess(ctx, cfg.ExecPath, cfg.Env); err != nil {
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

	if err := json.NewEncoder(conn).Encode(socketRequest{Command: "stop"}); err != nil {
		return err
	}

	var resp socketEnvelope
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&resp); err != nil {
		return err
	}
	if resp.Type != "stopping" || !resp.OK {
		if resp.Error != "" {
			return errors.New(resp.Error)
		}
		return fmt.Errorf("unexpected task daemon stop response %q", resp.Type)
	}

	return nil
}

func spawnTaskDaemonProcess(ctx context.Context, execPath string, env []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	cmd := exec.CommandContext(context.WithoutCancel(ctx), execPath)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}

	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}

	configureDetachedProcess(cmd, devNull)
	if err := cmd.Start(); err != nil {
		_ = devNull.Close()
		return err
	}

	go func() {
		defer devNull.Close()
		_ = cmd.Wait()
	}()

	return nil
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
			if lastErr != nil {
				return fmt.Errorf("task daemon did not become healthy: %w", errors.Join(waitCtx.Err(), lastErr))
			}
			return fmt.Errorf("task daemon did not become healthy: %w", waitCtx.Err())
		case <-ticker.C:
		}
	}
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
