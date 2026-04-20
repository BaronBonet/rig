package taskdaemon

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestTaskDaemonConfig_OnlyExposesEnvConfigFields(t *testing.T) {
	typ := reflect.TypeOf(Config{})
	var got []string
	for i := range typ.NumField() {
		got = append(got, typ.Field(i).Name)
	}

	want := []string{"SocketPath", "HookListenAddr"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("unexpected taskdaemon config fields: got %v want %v", got, want)
	}
}

func TestTaskDaemonPackage_SourceDoesNotUseDeprecatedNetErrorTemporary(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read taskdaemon package dir: %v", err)
	}

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}

		content, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}

		if strings.Contains(string(content), ".Temporary()") {
			t.Fatalf("taskdaemon package should not use deprecated net.Error.Temporary in %s", name)
		}
	}
}

func TestTaskDaemonPackage_NewReturnsCoreFrontendServerInterface(t *testing.T) {
	content, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("read server.go: %v", err)
	}

	if !strings.Contains(string(content), "func New(cfg Config, deps Dependencies) core.TaskFrontendServer") {
		t.Fatal("taskdaemon.New should return core.TaskFrontendServer")
	}
}
