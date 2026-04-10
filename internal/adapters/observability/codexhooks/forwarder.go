package codexhooks

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agent/internal/core"
)

type Forwarder struct {
	CollectorURL string
	Ingestor     core.HookEventIngestor
	Client       *http.Client
	Now          func() time.Time
	ErrorLogPath string
}

func (f Forwarder) Forward(ctx context.Context, eventName string, body []byte) error {
	eventName = strings.TrimSpace(eventName)
	body = bytes.TrimSpace(body)

	var failures []string

	if collectorURL := strings.TrimSpace(f.CollectorURL); collectorURL != "" {
		if err := f.postToCollector(ctx, collectorURL, eventName, body); err == nil {
			return nil
		} else {
			failures = append(failures, "collector="+err.Error())
		}
	}

	if f.Ingestor != nil {
		input := DecodeHookEventInput(f.now(), eventName, body)
		if _, err := f.Ingestor.IngestHookEvent(ctx, input); err == nil || errors.Is(err, core.ErrUnmanagedHookEvent) {
			return nil
		} else {
			failures = append(failures, "ingest="+err.Error())
		}
	}

	if len(failures) == 0 {
		failures = append(failures, "no_forwarding_path_configured")
	}

	f.logFailure(eventName, strings.Join(failures, " "))
	return nil
}

func (f Forwarder) postToCollector(ctx context.Context, collectorURL string, eventName string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, collectorURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Codex-Hook-Event", eventName)

	resp, err := f.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	return nil
}

func (f Forwarder) client() *http.Client {
	if f.Client != nil {
		return f.Client
	}

	return &http.Client{Timeout: 2 * time.Second}
}

func (f Forwarder) now() func() time.Time {
	if f.Now != nil {
		return f.Now
	}

	return time.Now
}

func (f Forwarder) errorLogPath() string {
	if strings.TrimSpace(f.ErrorLogPath) != "" {
		return f.ErrorLogPath
	}

	return filepath.Join(".agent", "observability", "hook-forwarder-errors.log")
}

func (f Forwarder) logFailure(eventName string, detail string) {
	path := f.errorLogPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}

	entry := fmt.Sprintf(
		"%s event=%s detail=%s\n",
		f.now()().UTC().Format(time.RFC3339),
		strings.TrimSpace(eventName),
		strings.TrimSpace(detail),
	)

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer file.Close()

	_, _ = io.WriteString(file, entry)
}
