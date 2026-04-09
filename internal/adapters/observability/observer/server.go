package observer

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	hookhttp "agent/internal/adapters/observability/codexhooks"
	"agent/internal/core"
)

type ServerConfig struct {
	SocketPath     string
	HookListenAddr string
	HookIngestor   core.HookEventIngestor
	Hub            *Hub
	Now            func() time.Time
	HookListener   net.Listener
}

func Serve(ctx context.Context, cfg ServerConfig) error {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.SocketPath == "" {
		return fmt.Errorf("observer socket path not configured")
	}
	if cfg.HookListenAddr == "" {
		return fmt.Errorf("hook listen addr not configured")
	}
	if cfg.HookIngestor == nil {
		return fmt.Errorf("hook ingestor not configured")
	}
	if cfg.Hub == nil {
		return fmt.Errorf("hook hub not configured")
	}

	if err := os.MkdirAll(filepath.Dir(cfg.SocketPath), 0o755); err != nil {
		return fmt.Errorf("prepare observer socket directory: %w", err)
	}
	if err := os.Remove(cfg.SocketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale observer socket: %w", err)
	}

	healthListener, err := net.Listen("unix", cfg.SocketPath)
	if err != nil {
		return fmt.Errorf("listen on observer socket: %w", err)
	}
	defer healthListener.Close()
	defer os.Remove(cfg.SocketPath)

	hookListener := cfg.HookListener
	if hookListener == nil {
		hookListener, err = net.Listen("tcp", cfg.HookListenAddr)
		if err != nil {
			return fmt.Errorf("listen for hook ingestion: %w", err)
		}
	}
	defer hookListener.Close()

	healthMux := http.NewServeMux()
	healthMux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	healthServer := &http.Server{Handler: healthMux}

	hookMux := http.NewServeMux()
	hookMux.Handle("/hook", hookhttp.NewHTTPHandler(newPublishingHookIngestor(cfg.HookIngestor, cfg.Hub), cfg.Now))
	hookServer := &http.Server{Handler: hookMux}

	errCh := make(chan error, 2)
	var wg sync.WaitGroup
	start := func(server *http.Server, listener net.Listener) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- server.Serve(listener)
		}()
	}

	start(healthServer, healthListener)
	start(hookServer, hookListener)

	select {
	case <-ctx.Done():
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = healthServer.Shutdown(shutdownCtx)
			_ = hookServer.Shutdown(shutdownCtx)
			wg.Wait()
			return err
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = healthServer.Shutdown(shutdownCtx)
	_ = hookServer.Shutdown(shutdownCtx)
	wg.Wait()

	return nil
}

type publishingHookIngestor struct {
	ingestor core.HookEventIngestor
	hub      *Hub
}

func newPublishingHookIngestor(ingestor core.HookEventIngestor, hub *Hub) core.HookEventIngestor {
	return &publishingHookIngestor{
		ingestor: ingestor,
		hub:      hub,
	}
}

func (p *publishingHookIngestor) IngestHookEvent(ctx context.Context, input core.HookEventInput) (*core.HookSessionSummary, error) {
	summary, err := p.ingestor.IngestHookEvent(ctx, input)
	if err != nil {
		return summary, err
	}
	if summary != nil && p.hub != nil {
		p.hub.Publish(*summary)
	}
	return summary, nil
}
