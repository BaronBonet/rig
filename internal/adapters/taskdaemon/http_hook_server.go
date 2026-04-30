package taskdaemon

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/BaronBonet/rig/internal/core"
)

func listenForHTTPHooks(ctx context.Context, addr string) (net.Listener, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil, fmt.Errorf("task daemon hook listen addr not configured")
	}
	if err := validateSafeHTTPHookListenAddr(addr); err != nil {
		return nil, err
	}

	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen for task daemon hook ingestion: %w", err)
	}

	return listener, nil
}

func newHTTPHookServer(routes []core.TaskDaemonHookRoute) http.Handler {
	mux := http.NewServeMux()
	for _, route := range routes {
		path := strings.TrimSpace(route.Path)
		if path == "" || route.Handler == nil {
			continue
		}
		mux.Handle(path, route.Handler)
	}

	return mux
}

func validateSafeHTTPHookListenAddr(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("validate task daemon hook listen addr: %w", err)
	}

	if strings.EqualFold(host, "localhost") {
		return nil
	}

	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("unsafe task daemon hook listen addr %q: host must be loopback", addr)
	}

	return nil
}
