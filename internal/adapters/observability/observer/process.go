package observer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

type ProcessConfig struct {
	SocketPath     string
	ExecPath       string
	CommandArgs    []string
	Spawn          func(context.Context, string, []string) error
	Dial           func(context.Context, string) error
	Remove         func(string) error
	HealthyTimeout time.Duration
	RetryInterval  time.Duration
}

type ProcessManager struct {
	cfg ProcessConfig
}

var observerStartupGuards = struct {
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
		return fmt.Errorf("observer process manager not configured")
	}

	cfg := m.cfg
	if cfg.SocketPath == "" {
		return fmt.Errorf("observer socket path not configured")
	}
	if cfg.ExecPath == "" {
		execPath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve observer executable: %w", err)
		}
		cfg.ExecPath = execPath
	}
	if len(cfg.CommandArgs) == 0 {
		cfg.CommandArgs = []string{"observer", "serve"}
	}
	if cfg.Spawn == nil {
		cfg.Spawn = defaultProcessSpawn
	}
	if cfg.Dial == nil {
		cfg.Dial = defaultProcessDial
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

	if err := cfg.Dial(ctx, cfg.SocketPath); err == nil {
		return nil
	}

	release, err := acquireObserverStartupLock(ctx, cfg.SocketPath, cfg.RetryInterval)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, release())
	}()

	return ensureObserverRunningLocked(ctx, cfg)
}

func ensureObserverRunningLocked(ctx context.Context, cfg ProcessConfig) error {
	if err := cfg.Dial(ctx, cfg.SocketPath); err == nil {
		return nil
	}

	socketExists, err := socketPathExists(cfg.SocketPath)
	if err != nil {
		return err
	}
	if socketExists {
		if err := waitForHealthyObserver(ctx, cfg); err == nil {
			return nil
		}

		socketExists, err = socketPathExists(cfg.SocketPath)
		if err != nil {
			return err
		}
		if socketExists {
			if err := cfg.Remove(cfg.SocketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("remove stale observer socket: %w", err)
			}
		}
	}

	if err := cfg.Spawn(ctx, cfg.ExecPath, cfg.CommandArgs); err != nil {
		return err
	}

	return waitForHealthyObserver(ctx, cfg)
}

func acquireObserverStartupLock(ctx context.Context, socketPath string, retryInterval time.Duration) (func() error, error) {
	lockPath := observerStartupLockPath(socketPath)
	semaphore := observerStartupSemaphore(lockPath)

	select {
	case semaphore <- struct{}{}:
	case <-ctx.Done():
		return nil, fmt.Errorf("acquire observer startup guard: %w", ctx.Err())
	}

	lockFile, err := openObserverStartupLockFile(lockPath)
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
			return nil, fmt.Errorf("acquire observer startup lock: %w", flockErr)
		}
		if locked {
			return func() error {
				return releaseObserverStartupLock(lockFile, semaphore)
			}, nil
		}

		select {
		case <-ctx.Done():
			_ = lockFile.Close()
			<-semaphore
			return nil, fmt.Errorf("acquire observer startup lock: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func releaseObserverStartupLock(lockFile *os.File, semaphore chan struct{}) error {
	var errs []error

	if err := unlockFile(lockFile); err != nil {
		errs = append(errs, fmt.Errorf("unlock observer startup lock: %w", err))
	}
	if err := lockFile.Close(); err != nil {
		errs = append(errs, fmt.Errorf("close observer startup lock: %w", err))
	}

	<-semaphore

	return errors.Join(errs...)
}

func openObserverStartupLockFile(lockPath string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, fmt.Errorf("prepare observer startup lock directory: %w", err)
	}

	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open observer startup lock: %w", err)
	}

	return lockFile, nil
}

func observerStartupLockPath(socketPath string) string {
	return socketPath + ".startup.lock"
}

func observerStartupSemaphore(lockPath string) chan struct{} {
	observerStartupGuards.mu.Lock()
	defer observerStartupGuards.mu.Unlock()

	semaphore, ok := observerStartupGuards.semaphores[lockPath]
	if ok {
		return semaphore
	}

	semaphore = make(chan struct{}, 1)
	observerStartupGuards.semaphores[lockPath] = semaphore

	return semaphore
}

func defaultProcessSpawn(ctx context.Context, execPath string, args []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	cmd := exec.Command(execPath, args...)
	if err := cmd.Start(); err != nil {
		return err
	}

	go func() {
		_ = cmd.Wait()
	}()

	return nil
}

func defaultProcessDial(ctx context.Context, socketPath string) error {
	transport := &http.Transport{
		Proxy: func(*http.Request) (*url.URL, error) {
			return nil, nil
		},
	}
	transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", socketPath)
	}
	defer transport.CloseIdleConnections()

	client := &http.Client{Transport: transport}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix/healthz", nil)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("observer unhealthy: %s", resp.Status)
	}

	return nil
}

func waitForHealthyObserver(ctx context.Context, cfg ProcessConfig) error {
	waitCtx, cancel := context.WithTimeout(ctx, cfg.HealthyTimeout)
	defer cancel()

	ticker := time.NewTicker(cfg.RetryInterval)
	defer ticker.Stop()

	for {
		if err := cfg.Dial(waitCtx, cfg.SocketPath); err == nil {
			return nil
		}

		select {
		case <-waitCtx.Done():
			return fmt.Errorf("observer did not become healthy: %w", waitCtx.Err())
		case <-ticker.C:
		}
	}
}

func socketPathExists(socketPath string) (bool, error) {
	_, err := os.Stat(socketPath)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}

	return false, fmt.Errorf("check observer socket path: %w", err)
}
