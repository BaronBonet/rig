package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"time"
)

type taskDaemonProcessConfig struct {
	SocketPath string
	ExecPath   string
	Env        []string
}

type taskDaemonProcessManager struct{ cfg taskDaemonProcessConfig }

const (
	taskDaemonHealthyTimeout = 2 * time.Second
	taskDaemonRetryInterval  = 25 * time.Millisecond
)

func newTaskDaemonProcessManager(cfg taskDaemonProcessConfig) *taskDaemonProcessManager {
	return &taskDaemonProcessManager{cfg: cfg}
}

func (m *taskDaemonProcessManager) EnsureRunning(ctx context.Context) error {
	if m == nil {
		return fmt.Errorf("task daemon process manager not configured")
	}

	cfg := m.cfg
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

	if err := dialTaskDaemonHealth(ctx, cfg.SocketPath); err == nil {
		return nil
	}

	if err := spawnTaskDaemonProcess(ctx, cfg.ExecPath, cfg.Env); err != nil {
		return err
	}

	return waitForHealthyTaskDaemon(ctx, cfg.SocketPath)
}

func (m *taskDaemonProcessManager) Restart(ctx context.Context) error {
	if m == nil {
		return fmt.Errorf("task daemon process manager not configured")
	}

	cfg := m.cfg
	if cfg.SocketPath == "" {
		return fmt.Errorf("task daemon socket path not configured")
	}

	exists, err := taskDaemonSocketPathExists(cfg.SocketPath)
	if err != nil {
		return err
	}
	if exists {
		_ = stopTaskDaemon(ctx, cfg.SocketPath)
		if err := os.Remove(cfg.SocketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove stale task daemon socket: %w", err)
		}
	}

	return m.EnsureRunning(ctx)
}

func spawnTaskDaemonProcess(ctx context.Context, execPath string, env []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	cmd := exec.Command(execPath)
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
	waitCtx, cancel := context.WithTimeout(ctx, taskDaemonHealthyTimeout)
	defer cancel()

	ticker := time.NewTicker(taskDaemonRetryInterval)
	defer ticker.Stop()

	for {
		if err := dialTaskDaemonHealth(waitCtx, socketPath); err == nil {
			return nil
		}

		select {
		case <-waitCtx.Done():
			return fmt.Errorf("task daemon did not become healthy: %w", waitCtx.Err())
		case <-ticker.C:
		}
	}
}

func stopTaskDaemon(ctx context.Context, socketPath string) error {
	if socketPath == "" {
		return fmt.Errorf("task daemon socket path not configured")
	}

	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return err
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(daemonSocketRequest{Command: "stop"}); err != nil {
		return err
	}

	var resp daemonSocketEnvelope
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

func dialTaskDaemonHealth(ctx context.Context, socketPath string) error {
	conn, err := dialDaemonSocket(ctx, socketPath)
	if err != nil {
		return err
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(daemonSocketRequest{Command: "health"}); err != nil {
		return err
	}

	var resp daemonSocketEnvelope
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&resp); err != nil {
		return err
	}
	if resp.Type != "health" || !resp.OK {
		return fmt.Errorf("task daemon unhealthy")
	}

	return nil
}
