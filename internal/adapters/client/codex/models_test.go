package codex

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BaronBonet/rig/internal/core"
	"github.com/BaronBonet/rig/internal/pkg/subprocess"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// fakeLineSession replays recorded app-server stdout lines, in order, and
// records the lines Rig sends and the directory the session started in. Once
// its lines run out, Receive reports the end of stdout.
type fakeLineSession struct {
	replies []string
	sent    []string
	cwd     string
	closed  bool
}

func (s *fakeLineSession) Send(line string) error {
	s.sent = append(s.sent, line)
	return nil
}

func (s *fakeLineSession) Receive() (string, error) {
	if len(s.replies) == 0 {
		return "", io.EOF
	}
	line := s.replies[0]
	s.replies = s.replies[1:]
	return line, nil
}

func (s *fakeLineSession) Close() error {
	s.closed = true
	return nil
}

func readFixtureLines(t *testing.T, name string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

// newListModelsRepository returns a repository whose app-server session
// replays replies. The runner expects the app-server command, under a
// deadline, in a private directory that exists while the session starts.
func newListModelsRepository(t *testing.T, replies ...string) (*repository, *fakeLineSession) {
	t.Helper()
	codexHome := t.TempDir()
	session := &fakeLineSession{replies: replies}
	runner := subprocess.NewMockRunner(t)
	runner.EXPECT().StartLineSession(mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, opts subprocess.StartLineSessionOptions) (subprocess.LineSession, error) {
			_, hasDeadline := ctx.Deadline()
			require.True(t, hasDeadline)
			require.Equal(t, "env", opts.Name)
			require.Equal(t, []string{"CODEX_HOME=" + codexHome, "codex", "app-server"}, opts.Args)
			require.True(t, strings.HasPrefix(opts.Cwd, filepath.Join(os.TempDir(), "rig-list-models-")), opts.Cwd)
			info, err := os.Stat(opts.Cwd)
			require.NoError(t, err)
			require.True(t, info.IsDir())
			require.Equal(t, os.FileMode(0o700), info.Mode().Perm())
			session.cwd = opts.Cwd
			return session, nil
		},
	)

	repo := New(runner, Config{Binary: "codex"}, HookForwardingConfig{}).(*repository)
	repo.codexHomeDir = func() (string, error) { return codexHome, nil }
	return repo, session
}

func TestRepositoryListModels_MapsTheRecordedModelList(t *testing.T) {
	repo, session := newListModelsRepository(t, readFixtureLines(t, "codex_app_server_model_list.jsonl")...)

	models, err := repo.ListModels(t.Context())

	require.NoError(t, err)
	ids := make([]string, 0, len(models))
	for _, model := range models {
		ids = append(ids, model.ID)
	}
	require.Equal(t, []string{
		"gpt-6-astra", "gpt-6-sol", "gpt-6-luna", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna",
	}, ids)
	require.Equal(t, core.ProviderModel{
		ID:              "gpt-6-astra",
		DisplayName:     "GPT-6-Astra",
		ReasoningLevels: []string{"low", "medium", "high", "xhigh", "max", "ultra"},
	}, models[0])
	require.Equal(t, []string{"low", "medium", "high", "xhigh", "max"}, models[2].ReasoningLevels)
	require.True(t, session.closed)
	require.NoDirExists(t, session.cwd)
}

func TestRepositoryListModels_InitializesBeforeListing(t *testing.T) {
	repo, session := newListModelsRepository(t, readFixtureLines(t, "codex_app_server_model_list.jsonl")...)

	_, err := repo.ListModels(t.Context())

	require.NoError(t, err)
	require.Equal(t, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"rig","version":"dev"}}}`,
		`{"jsonrpc":"2.0","method":"initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"model/list","params":{}}`,
	}, session.sent)
}

func TestRepositoryListModels_FollowsNextCursorAndSkipsHiddenModels(t *testing.T) {
	repo, session := newListModelsRepository(
		t,
		`{"id":1,"result":{}}`,
		`{"id":2,"result":{"data":[{"model":"gpt-a","displayName":"GPT-A","hidden":false,`+
			`"supportedReasoningEfforts":[{"reasoningEffort":"low"},{"reasoningEffort":"high"}]}],"nextCursor":"page-2"}}`,
		`{"id":3,"result":{"data":[{"model":"gpt-hidden","displayName":"Hidden","hidden":true},`+
			`{"model":"gpt-b","displayName":"GPT-B","supportedReasoningEfforts":[]}],"nextCursor":null}}`,
	)

	models, err := repo.ListModels(t.Context())

	require.NoError(t, err)
	require.Equal(t, []core.ProviderModel{
		{ID: "gpt-a", DisplayName: "GPT-A", ReasoningLevels: []string{"low", "high"}},
		{ID: "gpt-b", DisplayName: "GPT-B"},
	}, models)
	require.Equal(t, []string{
		`{"jsonrpc":"2.0","id":2,"method":"model/list","params":{}}`,
		`{"jsonrpc":"2.0","id":3,"method":"model/list","params":{"cursor":"page-2"}}`,
	}, session.sent[2:])
}

func TestRepositoryListModels_StopsWhenACursorRepeats(t *testing.T) {
	repo, session := newListModelsRepository(
		t,
		`{"id":1,"result":{}}`,
		`{"id":2,"result":{"data":[{"model":"gpt-a"}],"nextCursor":"page-2"}}`,
		`{"id":3,"result":{"data":[{"model":"gpt-b"}],"nextCursor":"page-2"}}`,
	)

	_, err := repo.ListModels(t.Context())

	require.ErrorContains(t, err, `model/list returned cursor "page-2" again`)
	require.Len(t, session.sent, 4)
}

// A request from the server carries a method, even when its id matches one of
// Rig's requests, so it is never taken for a response. Rig turns it down, so a
// server waiting on its own request goes on; a notification gets no answer.
func TestRepositoryListModels_TurnsDownServerRequestsSharingARequestID(t *testing.T) {
	repo, session := newListModelsRepository(
		t,
		`{"id":1,"result":{}}`,
		`{"method":"account/updated","params":{}}`,
		`{"id":2,"method":"item/tool/requestUserInput","params":{}}`,
		`{"id":2,"result":{"data":[{"model":"gpt-a","displayName":"GPT-A"}],"nextCursor":null}}`,
	)

	models, err := repo.ListModels(t.Context())

	require.NoError(t, err)
	require.Equal(t, []core.ProviderModel{{ID: "gpt-a", DisplayName: "GPT-A"}}, models)
	require.Equal(t, []string{
		`{"jsonrpc":"2.0","id":2,"method":"model/list","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"error":{"code":-32601,"message":"method not found"}}`,
	}, session.sent[2:])
}

func TestRepositoryListModels_SkipsLinesThatAreNotItsResponse(t *testing.T) {
	repo, _ := newListModelsRepository(
		t,
		`{"id":1,"result":{}}`,
		`npm notice: a new version of codex is available`,
		`{"id":1,"result":{"data":[{"model":"stale"}],"nextCursor":null}}`,
		`{"id":7,"result":{"data":[{"model":"other"}],"nextCursor":null}}`,
		`{"id":2,"result":{"data":[{"model":"gpt-a","displayName":"GPT-A"}],"nextCursor":null}}`,
	)

	models, err := repo.ListModels(t.Context())

	require.NoError(t, err)
	require.Equal(t, []core.ProviderModel{{ID: "gpt-a", DisplayName: "GPT-A"}}, models)
}

func TestRepositoryListModels_MatchesAResponseIDWrittenAsAString(t *testing.T) {
	repo, _ := newListModelsRepository(
		t,
		`{"id":"1","result":{}}`,
		`{"id":"2","result":{"data":[{"model":"gpt-a","displayName":"GPT-A"}],"nextCursor":null}}`,
	)

	models, err := repo.ListModels(t.Context())

	require.NoError(t, err)
	require.Equal(t, []core.ProviderModel{{ID: "gpt-a", DisplayName: "GPT-A"}}, models)
}

func TestRepositoryListModels_ReportsAnErrorResponse(t *testing.T) {
	repo, session := newListModelsRepository(
		t,
		`{"id":1,"result":{}}`,
		`{"id":2,"error":{"code":-32603,"message":"failed to load models"}}`,
	)

	_, err := repo.ListModels(t.Context())

	require.ErrorContains(t, err, "model/list failed: failed to load models")
	require.True(t, session.closed)
}

func TestRepositoryListModels_ReportsStdoutEndingBeforeTheResponse(t *testing.T) {
	repo, session := newListModelsRepository(
		t,
		`{"id":1,"result":{}}`,
		`{"method":"account/updated","params":{}}`,
	)

	_, err := repo.ListModels(t.Context())

	require.ErrorIs(t, err, io.EOF)
	require.ErrorContains(t, err, "before answering model/list")
	require.True(t, session.closed)
}
