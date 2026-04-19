package statusstream

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	hookhttp "rig/internal/adapters/observability/codexhooks"
	"rig/internal/core"
)

type ServerConfig struct {
	SocketPath     string
	HookListenAddr string
	HookIngestor   core.HookEventIngestor
	Hub            *Hub
	Now            func() time.Time
	HookListener   net.Listener
	Fingerprint    string
	Stop           func()
}

func Serve(ctx context.Context, cfg ServerConfig) error {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.SocketPath == "" {
		return fmt.Errorf("status observer socket path not configured")
	}
	if cfg.HookListenAddr == "" {
		return fmt.Errorf("status hook listen addr not configured")
	}
	if cfg.HookIngestor == nil {
		return fmt.Errorf("status hook ingestor not configured")
	}
	if cfg.Hub == nil {
		cfg.Hub = NewHub()
	}

	hookListener := cfg.HookListener
	var err error
	if hookListener == nil {
		hookListener, err = net.Listen("tcp", cfg.HookListenAddr)
		if err != nil {
			return fmt.Errorf("listen for status hook ingestion: %w", err)
		}
	}
	defer hookListener.Close()

	socketServer := NewSocketServer(SocketServerConfig{
		SocketPath:  cfg.SocketPath,
		Hub:         cfg.Hub,
		Fingerprint: cfg.Fingerprint,
		Stop:        cfg.Stop,
	})

	hookMux := http.NewServeMux()
	hookMux.Handle("/hook", hookhttp.NewHTTPHandler(newPublishingIngestor(cfg.HookIngestor, cfg.Hub, cfg.Now), cfg.Now))
	hookServer := &http.Server{Handler: hookMux}

	errCh := make(chan error, 2)
	go func() {
		errCh <- socketServer.Serve(ctx)
	}()
	go func() {
		errCh <- hookServer.Serve(hookListener)
	}()

	select {
	case <-ctx.Done():
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			_ = hookServer.Shutdown(context.Background())
			return err
		}
	}

	_ = hookServer.Shutdown(context.Background())
	return nil
}

type publishingIngestor struct {
	ingestor core.HookEventIngestor
	hub      *Hub
	now      func() time.Time
}

func newPublishingIngestor(ingestor core.HookEventIngestor, hub *Hub, now func() time.Time) core.HookEventIngestor {
	return &publishingIngestor{
		ingestor: ingestor,
		hub:      hub,
		now:      now,
	}
}

func (p *publishingIngestor) IngestHookEvent(ctx context.Context, input core.HookEventInput) (*core.HookSessionSummary, error) {
	summary, err := p.ingestor.IngestHookEvent(ctx, input)
	if err != nil {
		return summary, err
	}

	update, ok := MapCodexHookToStatus(summary, input.OccurredAt)
	if ok && p.hub != nil {
		if update.ObservedAt.IsZero() && p.now != nil {
			update.ObservedAt = p.now().UTC()
		}
		p.hub.Publish(update)
	}

	return summary, nil
}
