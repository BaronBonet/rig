package statusstream

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
	"sync"
	"time"
)

type ProcessConfig struct {
	SocketPath          string
	ExecPath            string
	CommandArgs         []string
	Env                 []string
	ExpectedFingerprint string
	Spawn               func(context.Context, string, []string, []string) error
	Dial                func(context.Context, string) error
	Probe               func(context.Context, string) (HealthStatus, error)
	Stop                func(context.Context, string) error
	Remove              func(string) error
	HealthyTimeout      time.Duration
	RetryInterval       time.Duration
}

type ProcessManager struct {
	cfg ProcessConfig
}

var processStartupGuards = struct {
	mu         sync.Mutex
	semaphores map[string]chan struct{}
}{
	semaphores: make(map[string]chan struct{}),
}

func NewProcessManager(cfg ProcessConfig) *ProcessManager {
	return &ProcessManager{cfg: cfg}
}

func (m *ProcessManager) EnsureRunning(ctx context.Context) (err error) {
	if m == nil {
		return fmt.Errorf("status observer process manager not configured")
	}

	cfg := m.cfg
	if cfg.SocketPath == "" {
		return fmt.Errorf("status observer socket path not configured")
	}
	if cfg.ExecPath == "" {
		execPath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve status observer executable: %w", err)
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
	if cfg.Probe == nil && cfg.ExpectedFingerprint != "" {
		cfg.Probe = defaultProcessProbe
	}
	if cfg.Stop == nil {
		cfg.Stop = Stop
	}
	if cfg.Remove == nil {
		cfg.Remove = os.Remove
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

	release, err := acquireStartupLock(ctx, cfg.SocketPath, cfg.RetryInterval)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, release())
	}()

	return ensureRunningLocked(ctx, cfg)
}

func (m *ProcessManager) Restart(ctx context.Context) error {
	if m == nil {
		return fmt.Errorf("status observer process manager not configured")
	}

	cfg := m.cfg
	if cfg.SocketPath == "" {
		return fmt.Errorf("status observer socket path not configured")
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
			return fmt.Errorf("remove stale status observer socket: %w", err)
		}
	}

	return m.EnsureRunning(ctx)
}

func ensureRunningLocked(ctx context.Context, cfg ProcessConfig) error {
	if err := processHealthy(ctx, cfg); err == nil {
		return nil
	}

	socketExists, err := socketPathExists(cfg.SocketPath)
	if err != nil {
		return err
	}
	if socketExists {
		_ = cfg.Stop(ctx, cfg.SocketPath)
		if err := cfg.Remove(cfg.SocketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove stale status observer socket: %w", err)
		}
	}

	if err := cfg.Spawn(ctx, cfg.ExecPath, cfg.CommandArgs, cfg.Env); err != nil {
		return err
	}

	return waitForHealthyProcess(ctx, cfg)
}

func acquireStartupLock(ctx context.Context, socketPath string, retryInterval time.Duration) (func() error, error) {
	lockPath := socketPath + ".startup.lock"
	semaphore := startupSemaphore(lockPath)

	select {
	case semaphore <- struct{}{}:
	case <-ctx.Done():
		return nil, fmt.Errorf("acquire status observer startup guard: %w", ctx.Err())
	}

	lockFile, err := openStartupLockFile(lockPath)
	if err != nil {
		<-semaphore
		return nil, err
	}

	if retryInterval <= 0 {
		retryInterval = 25 * time.Millisecond
	}

	ticker := time.NewTicker(retryInterval)
	defer ticker.Stop()

	for {
		locked, flockErr := tryLockFile(lockFile)
		if flockErr != nil {
			_ = lockFile.Close()
			<-semaphore
			return nil, fmt.Errorf("acquire status observer startup lock: %w", flockErr)
		}
		if locked {
			return func() error {
				return releaseStartupLock(lockFile, semaphore)
			}, nil
		}

		select {
		case <-ctx.Done():
			_ = lockFile.Close()
			<-semaphore
			return nil, fmt.Errorf("acquire status observer startup lock: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func releaseStartupLock(lockFile *os.File, semaphore chan struct{}) error {
	var errs []error
	if err := unlockFile(lockFile); err != nil {
		errs = append(errs, fmt.Errorf("unlock status observer startup lock: %w", err))
	}
	if err := lockFile.Close(); err != nil {
		errs = append(errs, fmt.Errorf("close status observer startup lock: %w", err))
	}

	<-semaphore
	return errors.Join(errs...)
}

func openStartupLockFile(lockPath string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, fmt.Errorf("prepare status observer startup lock directory: %w", err)
	}

	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open status observer startup lock: %w", err)
	}

	return lockFile, nil
}

func startupSemaphore(lockPath string) chan struct{} {
	processStartupGuards.mu.Lock()
	defer processStartupGuards.mu.Unlock()

	semaphore, ok := processStartupGuards.semaphores[lockPath]
	if ok {
		return semaphore
	}

	semaphore = make(chan struct{}, 1)
	processStartupGuards.semaphores[lockPath] = semaphore
	return semaphore
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

func defaultProcessProbe(ctx context.Context, socketPath string) (HealthStatus, error) {
	return probeSocketHealth(ctx, socketPath)
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
			return fmt.Errorf("status observer did not become healthy: %w", waitCtx.Err())
		case <-ticker.C:
		}
	}
}

func processHealthy(ctx context.Context, cfg ProcessConfig) error {
	if err := cfg.Dial(ctx, cfg.SocketPath); err != nil {
		return err
	}

	if cfg.Probe == nil {
		return nil
	}

	status, err := cfg.Probe(ctx, cfg.SocketPath)
	if err != nil {
		return err
	}
	if cfg.ExpectedFingerprint != "" && status.Fingerprint != cfg.ExpectedFingerprint {
		return fmt.Errorf("status observer fingerprint mismatch")
	}

	return nil
}

func Stop(ctx context.Context, socketPath string) error {
	if socketPath == "" {
		return fmt.Errorf("status observer socket path not configured")
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
		return fmt.Errorf("unexpected status observer stop response %q", resp.Type)
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

	return false, fmt.Errorf("check status observer socket path: %w", err)
}
