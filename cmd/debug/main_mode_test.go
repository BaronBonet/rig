package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDebugCreateStatusStreaming_DefaultsToNoTimeout(t *testing.T) {
	if debugTaskDaemon.StatusWaitAfter != 0 {
		t.Fatalf(
			"expected create-mode status streaming to stay open until cancelled, got %s",
			debugTaskDaemon.StatusWaitAfter,
		)
	}
}

func TestDebugCreate_DoesNotPrepareWorkspaceByDefault(t *testing.T) {
	if debugCreate.PrepareWorkspace {
		t.Fatal("expected debug create flow to skip workspace preparation by default")
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

func TestDebugMode_SourceDoesNotDependOnLegacyStatusstreamPackage(t *testing.T) {
	content, err := os.ReadFile(filepath.Join(".", "main.go"))
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	if strings.Contains(string(content), "internal/adapters/observability/statusstream") {
		t.Fatal("main.go should not import the legacy statusstream package")
	}
	if strings.Contains(string(content), "statusstream.") {
		t.Fatal("main.go should not reference the legacy statusstream package")
	}
}

func TestDebugMode_SourceUsesTaskDaemonConfigInsteadOfObserverConfig(t *testing.T) {
	content, err := os.ReadFile(filepath.Join(".", "main.go"))
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	if strings.Contains(string(content), "cfg.Observer.") {
		t.Fatal("main.go should use cfg.Daemon instead of the removed observer config")
	}
}

func TestDebugMode_SourceUsesUnifiedTaskdaemonAdapterOnly(t *testing.T) {
	content, err := os.ReadFile(filepath.Join(".", "main.go"))
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}

	source := string(content)
	for _, legacyImport := range []string{
		"internal/adapters/client/taskdaemonfrontend",
		"internal/adapters/client/taskdaemonprocess",
		"internal/adapters/handler/taskdaemon",
	} {
		if strings.Contains(source, legacyImport) {
			t.Fatalf("main.go should not import legacy taskdaemon package %q", legacyImport)
		}
	}

	if !strings.Contains(source, "\"rig/internal/adapters/taskdaemon\"") {
		t.Fatal("main.go should import the unified internal/adapters/taskdaemon package")
	}

	if strings.Contains(source, "taskdaemon.New(cfg.Daemon).Serve(") {
		t.Fatal("main.go should serve daemon mode through an adapter variable, not an inline New(...).Serve(...) chain")
	}
	if !strings.Contains(source, "adapter := taskdaemon.New(cfg.Daemon)") {
		t.Fatal("main.go should construct a taskdaemon adapter variable for daemon-mode serving")
	}
	if !strings.Contains(source, "adapter.Serve(") {
		t.Fatal("main.go should call adapter.Serve for daemon-mode serving")
	}
}

func TestDebugDaemonHookRoutes_ExposeCodexHooksOnly(t *testing.T) {
	t.Parallel()

	routes := debugDaemonHookRoutes(nil)
	if len(routes) != 2 {
		t.Fatalf("expected 2 hook routes, got %d", len(routes))
	}

	got := []string{routes[0].Path, routes[1].Path}
	want := []string{"/hook", "/codex-hook"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("unexpected hook routes: got %v want %v", got, want)
	}
}
