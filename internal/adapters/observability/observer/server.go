package observer

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	claudehookhttp "rig/internal/adapters/observability/claudehooks"
	hookhttp "rig/internal/adapters/observability/codexhooks"
	"rig/internal/core"
)

type ServerConfig struct {
	SocketPath      string
	HookListenAddr  string
	HookIngestor    core.HookEventIngestor
	Watcher         *TMuxWatcher
	Hub             *Hub
	Now             func() time.Time
	HookListener    net.Listener
	Fingerprint     string
	RefreshInterval time.Duration
}

func Serve(ctx context.Context, cfg ServerConfig) error {
	serveCtx, stop := context.WithCancel(ctx)
	defer stop()

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
	if cfg.RefreshInterval <= 0 {
		cfg.RefreshInterval = time.Second
	}
	if cfg.Watcher != nil {
		cfg.Watcher.hub = cfg.Hub
		defer cfg.Watcher.Close()
	}

	hookListener := cfg.HookListener
	var err error
	if hookListener == nil {
		hookListener, err = net.Listen("tcp", cfg.HookListenAddr)
		if err != nil {
			return fmt.Errorf("listen for hook ingestion: %w", err)
		}
	}
	defer hookListener.Close()

	hookMux := http.NewServeMux()
	publishingIngestor := newPublishingHookIngestor(cfg.HookIngestor, cfg.Hub, cfg.Watcher)
	hookMux.Handle("/hook", hookhttp.NewHTTPHandler(publishingIngestor, cfg.Now))
	hookMux.Handle("/claude-hook", claudehookhttp.NewHTTPHandler(publishingIngestor, cfg.Now))
	hookServer := &http.Server{Handler: hookMux}
	socketServer := NewSocketServer(SocketServerConfig{
		SocketPath:  cfg.SocketPath,
		Hub:         cfg.Hub,
		Fingerprint: cfg.Fingerprint,
		Stop:        stop,
	})

	errCh := make(chan error, 2)
	var wg sync.WaitGroup
	startHTTP := func(server *http.Server, listener net.Listener) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- server.Serve(listener)
		}()
	}
	startSocket := func() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- socketServer.Serve(serveCtx)
		}()
	}

	startSocket()
	startHTTP(hookServer, hookListener)
	if cfg.Watcher != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runRefreshLoop(serveCtx, cfg.Watcher, cfg.RefreshInterval)
		}()
	}

	select {
	case <-serveCtx.Done():
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = hookServer.Shutdown(shutdownCtx)
			wg.Wait()
			return err
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = hookServer.Shutdown(shutdownCtx)
	wg.Wait()

	return nil
}

func runRefreshLoop(ctx context.Context, watcher *TMuxWatcher, interval time.Duration) {
	if watcher == nil {
		return
	}

	_ = watcher.RefreshAll(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = watcher.RefreshAll(ctx)
		}
	}
}

type publishingHookIngestor struct {
	ingestor  core.HookEventIngestor
	observers core.ObserverRuntimeRepository
	hub       *Hub
	watcher   *TMuxWatcher
}

func newPublishingHookIngestor(
	ingestor core.HookEventIngestor,
	hub *Hub,
	watcher *TMuxWatcher,
) core.HookEventIngestor {
	var observers core.ObserverRuntimeRepository
	if repo, ok := ingestor.(core.ObserverRuntimeRepository); ok {
		observers = repo
	}

	return &publishingHookIngestor{
		ingestor:  ingestor,
		observers: observers,
		hub:       hub,
		watcher:   watcher,
	}
}

func (p *publishingHookIngestor) IngestHookEvent(
	ctx context.Context,
	input core.HookEventInput,
) (*core.HookSessionSummary, error) {
	summary, err := p.ingestor.IngestHookEvent(ctx, input)
	if err != nil {
		return summary, err
	}
	if summary != nil && p.hub != nil && p.observers != nil {
		if p.watcher != nil && p.watcher.RefreshTaskByID(ctx, summary.TaskID) == nil {
			return summary, nil
		}
		summaries, listErr := p.observers.ListObserverSummaries(ctx, []string{summary.TaskID})
		if listErr == nil {
			if observerSummary := summaries[summary.TaskID]; observerSummary != nil {
				p.hub.Publish(observerTaskUpdateFromSummary(observerSummary, summary, ""))
			}
		}
	}
	return summary, nil
}

func observerTaskUpdateFromSummary(
	summary *core.ObserverSummary,
	hookSummary *core.HookSessionSummary,
	provider string,
) core.ObserverTaskUpdate {
	if summary == nil {
		return core.ObserverTaskUpdate{}
	}

	var hookCopy *core.HookSessionSummary
	if hookSummary != nil {
		copied := *hookSummary
		hookCopy = &copied
	}

	return core.ObserverTaskUpdate{
		TaskID:          summary.TaskID,
		Provider:        firstNonEmptyProvider(provider, core.InferProviderFromHookSession(hookSummary)),
		DisplayStatus:   summary.DisplayStatus,
		DisplayActivity: summary.DisplayActivity,
		LastActivityAt:  summary.LastRuntimeObservedAt,
		HookSession:     hookCopy,
	}
}

func firstNonEmptyProvider(values ...string) string {
	for _, value := range values {
		if provider := core.NormalizeProvider(value); provider != "" {
			return provider
		}
	}
	return ""
}
