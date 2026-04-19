package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDebugMode_DefaultIsSupported(t *testing.T) {
	switch debugMode {
	case debugModeCreate, debugModeSubscribe:
	default:
		t.Fatalf("unsupported debugMode default %q", debugMode)
	}
}

func TestDebugCreateStatusStreaming_DefaultsToNoTimeout(t *testing.T) {
	if debugStatusObserver.StatusWaitAfter != 0 {
		t.Fatalf("expected create-mode status streaming to stay open until cancelled, got %s", debugStatusObserver.StatusWaitAfter)
	}
}

func TestDebugMode_SourceDoesNotContainLegacyStatusIngestMode(t *testing.T) {
	content, err := os.ReadFile(filepath.Join(".", "main.go"))
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	if strings.Contains(string(content), "status-ingest") {
		t.Fatal("main.go should not contain legacy status-ingest mode")
	}
}

func TestDebugMode_SourceDoesNotConstructSQLiteRepositoryDirectly(t *testing.T) {
	content, err := os.ReadFile(filepath.Join(".", "main.go"))
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	if strings.Contains(string(content), "sqliterepo.NewRepository") {
		t.Fatal("main.go should not construct repository/sqlite directly")
	}
}
