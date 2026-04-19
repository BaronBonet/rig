package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDebugCreateStatusStreaming_DefaultsToNoTimeout(t *testing.T) {
	if debugStatusDaemon.StatusWaitAfter != 0 {
		t.Fatalf("expected create-mode status streaming to stay open until cancelled, got %s", debugStatusDaemon.StatusWaitAfter)
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

func TestDebugMode_SourceDoesNotContainManualModeSwitching(t *testing.T) {
	content, err := os.ReadFile(filepath.Join(".", "main.go"))
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	for _, needle := range []string{"debugModeCreate", "debugModeSubscribe", "debugMode"} {
		if strings.Contains(string(content), needle) {
			t.Fatalf("main.go should not contain legacy manual mode switching token %q", needle)
		}
	}
}
