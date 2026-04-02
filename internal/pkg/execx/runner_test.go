package execx

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFakeRunnerRun_RecordsCallAndReturnsConfiguredResult(t *testing.T) {
	runner := NewFakeRunner([]Result{
		{Stdout: "hello\n"},
	})

	result, err := runner.Run(context.Background(), "/tmp/repo", "git", "status")
	require.NoError(t, err)
	require.Equal(t, "hello\n", result.Stdout)
	require.Len(t, runner.Calls, 1)
	require.Equal(t, "git", runner.Calls[0].Name)
	require.Equal(t, []string{"status"}, runner.Calls[0].Args)
}
