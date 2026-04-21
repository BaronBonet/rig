package taskdaemon

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"

	"rig/internal/core"
)

func listenForHTTPHooks(ctx context.Context, addr string) (net.Listener, error) {
	if strings.TrimSpace(addr) == "" {
		return nil, fmt.Errorf("task daemon hook listen addr not configured")
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
