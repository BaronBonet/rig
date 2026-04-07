package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"agent/internal/experimental/hooklog"
)

type server struct {
	logPath string
	now     func() time.Time
}

func newServer(logPath string, now func() time.Time) *server {
	if now == nil {
		now = time.Now
	}

	return &server{
		logPath: logPath,
		now:     now,
	}
}

func (s *server) handleHook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	record := hooklog.NewRecord(s.now(), r.Header.Get("X-Codex-Hook-Event"), r.RemoteAddr, r.URL.Path, body)
	if record.EventName == "" {
		record.EventName = "unknown"
	}

	if err := appendRecord(s.logPath, record); err != nil {
		http.Error(w, "append log: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

func appendRecord(path string, record hooklog.Record) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()

	return json.NewEncoder(file).Encode(record)
}
