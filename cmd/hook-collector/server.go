package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"agent/internal/experimental/hooklog"
)

type server struct {
	logPath string
	now     func() time.Time
	mu      sync.Mutex
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
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	eventName := r.Header.Get("X-Codex-Hook-Event")
	record := hooklog.NewRecord(s.now(), eventName, r.RemoteAddr, r.URL.Path, body)
	if record.EventName == "" {
		record.EventName = eventNameFromBody(record.RawPayload)
	}
	if record.EventName == "" {
		record.EventName = "unknown"
	}

	s.mu.Lock()
	err = appendRecord(s.logPath, record)
	s.mu.Unlock()
	if err != nil {
		http.Error(w, "append log: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

func appendRecord(path string, record hooklog.Record) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}

	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := file.Chmod(0o600); err != nil {
		return err
	}

	return json.NewEncoder(file).Encode(record)
}

func eventNameFromBody(rawPayload []byte) string {
	if len(rawPayload) == 0 {
		return ""
	}

	var payload struct {
		HookEventName string `json:"hook_event_name"`
	}
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		return ""
	}

	return payload.HookEventName
}
