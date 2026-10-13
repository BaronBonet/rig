package cli_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/BaronBonet/rig/internal/core"
)

func knownRepos() []core.KnownRepo {
	return []core.KnownRepo{
		{Name: "backend", Root: "/work/backend", TaskCount: 2},
		{Name: "frontend", Root: "/work/frontend", TaskCount: 12},
		{Name: "notes", Root: "/Users/me/code/notes"},
	}
}

func TestRepoList_PrintsATableOfTheKnownRepositories(t *testing.T) {
	h := newCLIHarness(t)
	h.frontend.EXPECT().ListRepos(mock.Anything).Return(knownRepos(), nil).Once()

	require.NoError(t, h.runRepo(t.Context(), "list"))

	require.Equal(t, ""+
		"NAME      TASKS  ROOT\n"+
		"backend   2      /work/backend\n"+
		"frontend  12     /work/frontend\n"+
		"notes     0      /Users/me/code/notes\n",
		h.stdout.String())
	require.Empty(t, h.stderr.String())
}

func TestRepoList_SaysSoWhenNoRepositoryIsKnown(t *testing.T) {
	h := newCLIHarness(t)
	h.frontend.EXPECT().ListRepos(mock.Anything).Return(nil, nil).Once()

	require.NoError(t, h.runRepo(t.Context(), "list"))

	require.Equal(t, "No known repositories.\n", h.stdout.String())
}

func TestRepoList_PrintsTheKnownRepositoriesAsJSON(t *testing.T) {
	h := newCLIHarness(t)
	h.frontend.EXPECT().ListRepos(mock.Anything).Return(knownRepos(), nil).Once()

	require.NoError(t, h.runRepo(t.Context(), "list", "--json"))

	var printed []map[string]any
	require.NoError(t, json.Unmarshal(h.stdout.Bytes(), &printed))
	require.Equal(t, []map[string]any{
		{"name": "backend", "root": "/work/backend", "task_count": float64(2)},
		{"name": "frontend", "root": "/work/frontend", "task_count": float64(12)},
		{"name": "notes", "root": "/Users/me/code/notes", "task_count": float64(0)},
	}, printed)
	require.Empty(t, h.stderr.String())
}

func TestRepoList_PrintsAnEmptyJSONArrayWhenNoRepositoryIsKnown(t *testing.T) {
	h := newCLIHarness(t)
	h.frontend.EXPECT().ListRepos(mock.Anything).Return(nil, nil).Once()

	require.NoError(t, h.runRepo(t.Context(), "list", "--json"))

	require.Equal(t, "[]\n", h.stdout.String())
}

func TestRepoList_FailsWhenTheRepositoriesCannotBeListed(t *testing.T) {
	h := newCLIHarness(t)
	h.frontend.EXPECT().ListRepos(mock.Anything).Return(nil, errors.New(
		"list the repositories in repo directory /work: open /work: permission denied",
	)).Once()

	err := h.runRepo(t.Context(), "list")

	require.EqualError(t, err, "list the repositories in repo directory /work: open /work: permission denied")
	require.Empty(t, h.stdout.String())
}

func TestRepoList_RejectsArguments(t *testing.T) {
	h := newCLIHarness(t)

	err := h.runRepo(t.Context(), "list", "frontend")

	require.EqualError(t, err, `unknown command "frontend" for "repo list"`)
	require.Zero(t, h.frontendCalls)
}

func TestRepoCommand_PrintsItsHelpWithoutASubcommand(t *testing.T) {
	h := newCLIHarness(t)

	require.NoError(t, h.runRepo(t.Context()))

	require.Contains(t, h.stdout.String(), "list")
	require.Zero(t, h.frontendCalls)
}

func TestRepoCommand_RejectsAnUnknownSubcommand(t *testing.T) {
	h := newCLIHarness(t)

	err := h.runRepo(t.Context(), "lsit")

	require.EqualError(t, err, `unknown command "lsit" for "repo"`)
	require.Zero(t, h.frontendCalls)
}

func TestRepoMain_AttachesToTheMainSessionOfTheWorkingDirectorysRepository(t *testing.T) {
	h := newCLIHarness(t)
	h.frontend.EXPECT().AttachRepoSession(mock.Anything, "/work/backend").Return(nil).Once()

	require.NoError(t, h.runRepo(t.Context(), "main"))
}
