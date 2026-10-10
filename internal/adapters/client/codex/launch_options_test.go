package codex

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/BaronBonet/rig/internal/core"
	"github.com/BaronBonet/rig/internal/pkg/subprocess"

	"github.com/stretchr/testify/require"
)

func TestRepositoryLaunchSpecs_PassTheTasksModelAndEffortBeforeAnySubcommand(t *testing.T) {
	codexHome := t.TempDir()
	repo := New(subprocess.NewMockRunner(t), Config{Binary: "codex"}, HookForwardingConfig{}).(*repository)
	repo.codexHomeDir = func() (string, error) { return codexHome, nil }
	task := &core.Task{Prompt: "add billing retry flow", Model: "gpt-5", Effort: "high"}

	start, err := repo.BuildTaskSessionLaunchSpec(task)
	require.NoError(t, err)
	require.Equal(t,
		[]string{"env", "CODEX_HOME=" + codexHome, "codex", "-m", "gpt-5", "-c", "model_reasoning_effort=high"},
		start.Command,
	)

	resume, err := repo.BuildReconnectTaskSessionLaunchSpec(task, "sess-1")
	require.NoError(t, err)
	require.Equal(t,
		[]string{
			"env", "CODEX_HOME=" + codexHome, "codex",
			"-m", "gpt-5", "-c", "model_reasoning_effort=high", "resume", "sess-1",
		},
		resume.Command,
	)
}

// clientModelsCache is a models cache as Codex writes it into a home: listed
// and hidden models, out of priority order, with uneven effort support.
const clientModelsCache = `{
  "fetched_at": "2026-10-10T19:14:52Z",
  "models": [
    {"slug": "gpt-5.6-luna", "visibility": "list", "priority": 9,
     "supported_reasoning_levels": [{"effort": "low"}, {"effort": "medium"}, {"effort": "high"}]},
    {"slug": "gpt-reserve", "visibility": "hide", "priority": 4,
     "supported_reasoning_levels": [{"effort": "internal"}]},
    {"slug": "gpt-6-sol", "visibility": "list", "priority": 3,
     "supported_reasoning_levels": [{"effort": "medium"}, {"effort": "high"}, {"effort": "ultra"}]},
    {"slug": "gpt-6-luna", "visibility": "list", "priority": 4,
     "supported_reasoning_levels": [{"effort": "low"}, {"effort": "max"}]}
  ]
}`

func newRepositoryWithHome(t *testing.T, codexHome string) *repository {
	t.Helper()
	repo := New(subprocess.NewMockRunner(t), Config{Binary: "codex"}, HookForwardingConfig{}).(*repository)
	repo.codexHomeDir = func() (string, error) { return codexHome, nil }
	return repo
}

func writeModelsCache(t *testing.T, codexHome string, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(codexHome, modelsCacheFile), []byte(content), 0o600))
}

func TestRepositoryLaunchOptions_ListsTheModelsOfTheWindowsCodexAccount(t *testing.T) {
	daemonHome, clientHome := t.TempDir(), t.TempDir()
	writeModelsCache(t, daemonHome, `{"models": [{"slug": "gpt-6.1-sol", "visibility": "list", "priority": 1,
		"supported_reasoning_levels": [{"effort": "xhigh"}]}]}`)
	writeModelsCache(t, clientHome, clientModelsCache)
	repo := newRepositoryWithHome(t, daemonHome)

	options := repo.LaunchOptions(core.ProviderEnv{"CODEX_HOME": clientHome})

	require.Equal(t, []string{"gpt-6-sol", "gpt-6-luna", "gpt-5.6-luna"}, options.Models,
		"the listed models, in Codex's priority order")
	require.Equal(t, []string{"medium", "high", "ultra", "low", "max"}, options.Efforts,
		"every effort a listed model supports, once")
	require.False(t, options.Handoff)

	daemonOptions := repo.LaunchOptions(nil)
	require.Equal(t, []string{"gpt-6.1-sol"}, daemonOptions.Models, "an undecided window gets the daemon's home")
	require.Equal(t, []string{"xhigh"}, daemonOptions.Efforts)
}

func TestRepositoryLaunchOptions_FallsBackWithoutAUsableModelsCache(t *testing.T) {
	for name, cache := range map[string]string{
		"missing":         "",
		"unreadable":      `{"models": [`,
		"changed shape":   `{"models": [{"slug": "gpt-6-sol", "visibility": "list", "priority": "first"}]}`,
		"no listed model": `{"models": [{"slug": "gpt-reserve", "visibility": "hide", "priority": 1}]}`,
		"blank slug":      `{"models": [{"slug": " ", "visibility": "list", "priority": 1}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			codexHome := t.TempDir()
			if cache != "" {
				writeModelsCache(t, codexHome, cache)
			}
			repo := newRepositoryWithHome(t, t.TempDir())

			options := repo.LaunchOptions(core.ProviderEnv{"CODEX_HOME": codexHome})

			require.Equal(t, fallbackLaunchOptions, options)
			options.Models[0] = "changed"
			require.NotEqual(t, "changed", fallbackLaunchOptions.Models[0], "callers get a copy")
		})
	}
}

func TestRepository_QuitsWithSlashQuitAndWritesNoHandoffs(t *testing.T) {
	repo := newRepositoryWithHome(t, t.TempDir())

	require.Equal(t, "/quit", repo.ExitCommand())

	_, err := repo.WriteSessionHandoff(context.Background(), &core.Task{}, core.TaskProviderSession{}, "")
	require.ErrorIs(t, err, core.ErrHandoffUnsupported)
}
