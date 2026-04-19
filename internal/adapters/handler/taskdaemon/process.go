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
	"time"
)

type ProcessConfig struct {
	SocketPath     string
	ExecPath       string
	CommandArgs    []string
	Env            []string
	Spawn          func(context.Context, string, []string, []string) error
	Dial           func(context.Context, string) error
	Stop           func(context.Context, string) error
	Remove         func(string) error
	HealthyTimeout time.Duration
	RetryInterval  time.Duration
}

type ProcessManager struct{ cfg ProcessConfig }

func NewProcessManager(cfg ProcessConfig) *ProcessManager {
	return &ProcessManager{cfg: cfg}
}

func (m *ProcessManager) EnsureRunning(ctx context.Context) error {
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
	if len(cfg.CommandArgs) == 0 {
		cfg.CommandArgs = []string{}
	}
	if cfg.Spawn == nil {
		cfg.Spawn = defaultProcessSpawn
	}
	if cfg.Dial == nil {
		cfg.Dial = defaultProcessDial
	}
	if cfg.HealthyTimeout <= 0 {
		cfg.HealthyTimeout = 2 * time.Second
	}
	if cfg.RetryInterval <= 0 {
		cfg.RetryInterval = 25 * time.Millisecond
	}

	if err := processHealthy(ctx, cfg); err == nil {
		return nil
	}

	if err := cfg.Spawn(ctx, cfg.ExecPath, cfg.CommandArgs, cfg.Env); err != nil {
		return err
	}

	return waitForHealthyProcess(ctx, cfg)
}

func (m *ProcessManager) Restart(ctx context.Context) error {
	if m == nil {
		return fmt.Errorf("task daemon process manager not configured")
	}

	cfg := m.cfg
	if cfg.SocketPath == "" {
		return fmt.Errorf("task daemon socket path not configured")
	}
	if cfg.Stop == nil {
		cfg.Stop = Stop
	}
	if cfg.Remove == nil {
		cfg.Remove = os.Remove
	}

	exists, err := socketPathExists(cfg.SocketPath)
	if err != nil {
		return err
	}
	if exists {
		_ = cfg.Stop(ctx, cfg.SocketPath)
		if err := cfg.Remove(cfg.SocketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove stale task daemon socket: %w", err)
		}
	}

	return m.EnsureRunning(ctx)
}

func defaultProcessSpawn(ctx context.Context, execPath string, args []string, env []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	cmd := exec.Command(execPath, args...)
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

func defaultProcessDial(ctx context.Context, socketPath string) error {
	return dialSocketHealth(ctx, socketPath)
}

func waitForHealthyProcess(ctx context.Context, cfg ProcessConfig) error {
	waitCtx, cancel := context.WithTimeout(ctx, cfg.HealthyTimeout)
	defer cancel()

	ticker := time.NewTicker(cfg.RetryInterval)
	defer ticker.Stop()

	for {
		if err := processHealthy(waitCtx, cfg); err == nil {
			return nil
		}

		select {
		case <-waitCtx.Done():
			return fmt.Errorf("task daemon did not become healthy: %w", waitCtx.Err())
		case <-ticker.C:
		}
	}
}

func processHealthy(ctx context.Context, cfg ProcessConfig) error {
	return cfg.Dial(ctx, cfg.SocketPath)
}

func Stop(ctx context.Context, socketPath string) error {
	if socketPath == "" {
		return fmt.Errorf("task daemon socket path not configured")
	}

	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", socketPath)
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

func socketPathExists(socketPath string) (bool, error) {
	_, err := os.Stat(socketPath)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}

	return false, fmt.Errorf("check task daemon socket path: %w", err)
}
