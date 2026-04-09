package observer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEnsureObserverRunning_ReusesHealthyObserver(t *testing.T) {
	var spawnCalls int
	manager := NewProcessManager(ProcessConfig{
		SocketPath:          "/tmp/agent-observer-test.sock",
		ExecPath:            "/bin/agent",
		ExpectedFingerprint: "build-123",
		Dial: func(context.Context, string) error {
			return nil
		},
		Probe: func(context.Context, string) (HealthStatus, error) {
			return HealthStatus{Fingerprint: "build-123"}, nil
		},
		Spawn: func(context.Context, string, []string) error {
			spawnCalls++
			return nil
		},
	})

	err := manager.EnsureRunning(context.Background())
	require.NoError(t, err)
	require.Zero(t, spawnCalls)
}

func TestEnsureObserverRunning_RestartsStaleHealthyObserver(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "observer.sock")
	require.NoError(t, os.WriteFile(socketPath, []byte("x"), 0o600))

	var (
		spawnCalls  int
		removeCalls int
		probeCalls  int
		stopCalls   int
	)

	manager := NewProcessManager(ProcessConfig{
		SocketPath:          socketPath,
		ExecPath:            "/bin/agent",
		ExpectedFingerprint: "build-new",
		Dial: func(context.Context, string) error {
			return nil
		},
		Probe: func(context.Context, string) (HealthStatus, error) {
			probeCalls++
			if spawnCalls == 0 {
				return HealthStatus{Fingerprint: "build-old"}, nil
			}
			return HealthStatus{Fingerprint: "build-new"}, nil
		},
		Remove: func(string) error {
			removeCalls++
			return nil
		},
		Spawn: func(context.Context, string, []string) error {
			spawnCalls++
			return nil
		},
	})

	originalStopSocket := stopSocket
	stopSocket = func(_ context.Context, path string) error {
		require.Equal(t, socketPath, path)
		stopCalls++
		return nil
	}
	defer func() {
		stopSocket = originalStopSocket
	}()

	err := manager.EnsureRunning(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, spawnCalls)
	require.Equal(t, 1, stopCalls)
	require.Equal(t, 1, removeCalls)
	require.GreaterOrEqual(t, probeCalls, 2)
}

func TestEnsureObserverRunning_SpawnsObserverWhenUnavailable(t *testing.T) {
	var (
		mu        sync.Mutex
		dialCalls int
		spawned   bool
	)

	manager := NewProcessManager(ProcessConfig{
		SocketPath: "/tmp/agent-observer-test.sock",
		ExecPath:   "/bin/agent",
		Dial: func(context.Context, string) error {
			mu.Lock()
			defer mu.Unlock()
			dialCalls++
			if spawned {
				return nil
			}
			return errors.New("observer unavailable")
		},
		Spawn: func(context.Context, string, []string) error {
			mu.Lock()
			defer mu.Unlock()
			spawned = true
			return nil
		},
	})

	err := manager.EnsureRunning(context.Background())
	require.NoError(t, err)
	require.True(t, spawned)
	require.GreaterOrEqual(t, dialCalls, 2)
}

func TestEnsureObserverRunning_ReusesSlowStartingObserverWithExistingSocket(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "observer.sock")
	require.NoError(t, os.WriteFile(socketPath, []byte("x"), 0o600))

	var (
		mu          sync.Mutex
		dialCalls   int
		spawnCalls  int
		removeCalls int
	)

	manager := NewProcessManager(ProcessConfig{
		SocketPath:     socketPath,
		ExecPath:       "/bin/agent",
		HealthyTimeout: 50 * time.Millisecond,
		RetryInterval:  5 * time.Millisecond,
		Dial: func(context.Context, string) error {
			mu.Lock()
			defer mu.Unlock()
			dialCalls++
			if dialCalls >= 3 {
				return nil
			}
			return errors.New("observer starting")
		},
		Remove: func(string) error {
			removeCalls++
			return nil
		},
		Spawn: func(context.Context, string, []string) error {
			spawnCalls++
			return nil
		},
	})

	err := manager.EnsureRunning(context.Background())
	require.NoError(t, err)
	require.Zero(t, spawnCalls)
	require.Zero(t, removeCalls)
}

func TestEnsureObserverRunning_CleansStaleSocketBeforeRespawn(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "observer.sock")
	require.NoError(t, os.WriteFile(socketPath, []byte("x"), 0o600))

	var events []string

	manager := NewProcessManager(ProcessConfig{
		SocketPath:     socketPath,
		ExecPath:       "/bin/agent",
		HealthyTimeout: 20 * time.Millisecond,
		RetryInterval:  5 * time.Millisecond,
		Dial: func(context.Context, string) error {
			if len(events) < 2 {
				return errors.New("observer unavailable")
			}
			return nil
		},
		Remove: func(string) error {
			events = append(events, "remove")
			return nil
		},
		Spawn: func(context.Context, string, []string) error {
			events = append(events, "spawn")
			return nil
		},
	})

	err := manager.EnsureRunning(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"remove", "spawn"}, events)
}

func TestEnsureObserverRunning_ConcurrentCallsSpawnObserverOnce(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "observer.sock")

	var (
		mu           sync.Mutex
		spawnCalls   int
		healthy      bool
		initialDials int
	)

	initialDialReady := make(chan struct{})
	releaseInitialDial := make(chan struct{})
	firstSpawnStarted := make(chan struct{})
	secondSpawnStarted := make(chan struct{}, 1)
	releaseFirstSpawn := make(chan struct{})
	start := make(chan struct{})

	manager := NewProcessManager(ProcessConfig{
		SocketPath:     socketPath,
		ExecPath:       "/bin/agent",
		HealthyTimeout: 200 * time.Millisecond,
		RetryInterval:  5 * time.Millisecond,
		Dial: func(ctx context.Context, _ string) error {
			mu.Lock()
			if !healthy && initialDials < 2 {
				initialDials++
				if initialDials == 2 {
					close(initialDialReady)
				}
				mu.Unlock()

				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-releaseInitialDial:
				}

				return errors.New("observer unavailable")
			}

			isHealthy := healthy
			mu.Unlock()

			if isHealthy {
				return nil
			}

			return errors.New("observer unavailable")
		},
		Spawn: func(ctx context.Context, _ string, _ []string) error {
			mu.Lock()
			spawnCalls++
			call := spawnCalls
			mu.Unlock()

			if call == 1 {
				close(firstSpawnStarted)

				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-releaseFirstSpawn:
				}

				mu.Lock()
				healthy = true
				mu.Unlock()

				return nil
			}

			select {
			case secondSpawnStarted <- struct{}{}:
			default:
			}

			return nil
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	errCh := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			errCh <- manager.EnsureRunning(ctx)
		}()
	}

	close(start)
	<-initialDialReady
	close(releaseInitialDial)
	<-firstSpawnStarted

	select {
	case <-secondSpawnStarted:
		t.Fatal("second EnsureRunning call attempted to spawn observer concurrently")
	case <-time.After(100 * time.Millisecond):
	}

	close(releaseFirstSpawn)

	for range 2 {
		require.NoError(t, <-errCh)
	}

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, 1, spawnCalls)
}

func TestDefaultProcessSpawn_ReturnsBeforeCommandExits(t *testing.T) {
	dir := t.TempDir()
	startedPath := filepath.Join(dir, "started")
	finishedPath := filepath.Join(dir, "finished")
	script := fmt.Sprintf("printf started > %q; sleep 1; printf done > %q", startedPath, finishedPath)

	start := time.Now()
	err := defaultProcessSpawn(context.Background(), "/bin/sh", []string{"-c", script})
	require.NoError(t, err)
	require.Less(t, time.Since(start), 200*time.Millisecond)

	require.Eventually(t, func() bool {
		_, err := os.Stat(startedPath)
		return err == nil
	}, 200*time.Millisecond, 10*time.Millisecond)
	_, err = os.Stat(finishedPath)
	require.Error(t, err)
}
