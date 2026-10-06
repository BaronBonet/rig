package claude

import (
	"testing"

	"github.com/BaronBonet/rig/internal/core"
	"github.com/BaronBonet/rig/internal/pkg/subprocess"

	"github.com/stretchr/testify/require"
)

func TestRepositoryLaunchSpecs_PassTheTasksModelAndEffort(t *testing.T) {
	repo, _ := newTestRepository(t, subprocess.NewMockRunner(t))
	task := &core.Task{Prompt: "add billing retry flow", Model: "opus", Effort: "xhigh"}

	start, err := repo.BuildTaskSessionLaunchSpec(task)
	require.NoError(t, err)
	require.Equal(t, []string{"claude", "--model", "opus", "--effort", "xhigh"}, start.Command)
	require.Equal(t, []string{"add billing retry flow"}, start.PrefillInput)

	resume, err := repo.BuildReconnectTaskSessionLaunchSpec(task, "sess-1")
	require.NoError(t, err)
	require.Equal(t, []string{"claude", "--resume", "sess-1", "--model", "opus", "--effort", "xhigh"}, resume.Command,
		"a resumed session keeps the task's launch options")

	plain, err := repo.BuildTaskSessionLaunchSpec(&core.Task{Model: "  "})
	require.NoError(t, err)
	require.Equal(t, []string{"claude"}, plain.Command, "blank options mean the provider's defaults")
}

func TestRepositoryLaunchOptions_ListsModelAliasesAndEfforts(t *testing.T) {
	repo, _ := newTestRepository(t, subprocess.NewMockRunner(t))

	options := repo.LaunchOptions()

	require.Equal(t, []string{"fable", "opus", "sonnet", "haiku"}, options.Models)
	require.Equal(t, []string{"low", "medium", "high", "xhigh", "max"}, options.Efforts)
}
