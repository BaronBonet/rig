package main

import (
	"net/http"
	"time"

	hookhttp "agent/internal/adapters/observability/codexhooks"
	"agent/internal/core"
)

type server struct {
	handler http.Handler
}

func newServer(repo core.HookEventIngestor, now func() time.Time) *server {
	return &server{
		handler: hookhttp.NewHTTPHandler(repo, now),
	}
}

func (s *server) handleHook(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}
