package taskdaemon

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"rig/internal/core"

	"github.com/stretchr/testify/require"
)

type adapterSurface interface {
	Frontend() core.TaskFrontend
	EnsureRunning(context.Context) error
	Restart(context.Context) error
	Serve(context.Context, core.TaskService, []HookRoute, func()) error
}

func TestNew_ReturnsAdapterWithExpectedSurface(t *testing.T) {
	adapter := New(Config{})
	require.NotNil(t, adapter)

	var _ adapterSurface = adapter
	require.NotNil(t, adapter.Frontend())
}

func TestTaskDaemonConfig_ExposesRuntimeFieldsWithoutEnvTags(t *testing.T) {
	typ := reflect.TypeOf(Config{})

	fields := map[string]reflect.StructField{}
	for i := range typ.NumField() {
		field := typ.Field(i)
		fields[field.Name] = field
	}

	require.Contains(t, fields, "SocketPath")
	require.Contains(t, fields, "HookListenAddr")
	require.Contains(t, fields, "ExecPath")
	require.Contains(t, fields, "Env")

	require.NotEmpty(t, fields["SocketPath"].Tag.Get("env"))
	require.NotEmpty(t, fields["HookListenAddr"].Tag.Get("env"))
	require.Empty(t, fields["ExecPath"].Tag.Get("env"))
	require.Empty(t, fields["Env"].Tag.Get("env"))
}

func TestTaskDaemonPackage_SourceDoesNotUseDeprecatedNetErrorTemporary(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}

		content, err := os.ReadFile(name)
		require.NoError(t, err)
		require.NotContains(t, string(content), ".Temporary()")
	}
}

func TestTaskDaemonPackage_HTTPHookServerIsNotCodexSpecific(t *testing.T) {
	content, err := os.ReadFile("http_hook_server.go")
	require.NoError(t, err)

	source := string(content)
	require.NotContains(t, source, "codexhooks")
	require.NotContains(t, source, "codexHookToTaskStatus")
}
