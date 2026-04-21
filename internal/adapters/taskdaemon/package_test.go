package taskdaemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

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
