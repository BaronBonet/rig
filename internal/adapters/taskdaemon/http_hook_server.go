package taskdaemon

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

type HookRoute struct {
	Path    string
	Handler http.Handler
}

func listenForHTTPHooks(addr string) (net.Listener, error) {
	if strings.TrimSpace(addr) == "" {
		return nil, fmt.Errorf("task daemon hook listen addr not configured")
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen for task daemon hook ingestion: %w", err)
	}

	return listener, nil
}

func newHTTPHookServer(routes []HookRoute) http.Handler {
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
