package claude

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

// fakeLineSession replays recorded Claude stdout lines, in order, and records
// the lines Rig sends and the directory the session started in. Once its lines
// run out, Receive reports the end of stdout.
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

// readInitializeFixture returns the recorded initialize exchange, answering
// Rig's request ID in place of the recording's.
func readInitializeFixture(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "claude_initialize_models.jsonl"))
	require.NoError(t, err)
	recorded := strings.ReplaceAll(string(raw), `"request_id":"init-1"`, `"request_id":"`+listModelsRequestID+`"`)
	return strings.Split(strings.TrimSpace(recorded), "\n")
}

// newListModelsRepository returns a repository whose Claude session replays
// replies. The runner expects Claude's stream-json probe, under a deadline,
// in Rig's private control directory, which exists while the session starts.
func newListModelsRepository(t *testing.T, replies ...string) (*repository, *fakeLineSession) {
	t.Helper()
	session := &fakeLineSession{replies: replies}
	var controlDir string
	runner := subprocess.NewMockRunner(t)
	runner.EXPECT().StartLineSession(mock.Anything, mock.Anything).RunAndReturn(
		func(ctx context.Context, opts subprocess.StartLineSessionOptions) (subprocess.LineSession, error) {
			_, hasDeadline := ctx.Deadline()
			require.True(t, hasDeadline)
			require.Equal(t, "claude", opts.Name)
			require.Equal(t, []string{
				"-p",
				"--input-format", "stream-json",
				"--output-format", "stream-json",
				"--verbose",
				"--no-session-persistence",
				"--setting-sources", "user",
				"--settings", `{"disableAllHooks":true}`,
			}, opts.Args)
			require.Equal(t, controlDir, opts.Cwd)
			info, err := os.Stat(opts.Cwd)
			require.NoError(t, err)
			require.True(t, info.IsDir())
			require.Equal(t, os.FileMode(0o700), info.Mode().Perm())
			session.cwd = opts.Cwd
			return session, nil
		},
	)

	repo, dataDir := newTestRepository(t, runner)
	controlDir = filepath.Join(dataDir, "claude", "control")
	return repo, session
}

func TestRepositoryListModels_MapsTheRecordedModelsWithoutTheDefaultEntry(t *testing.T) {
	repo, session := newListModelsRepository(t, readInitializeFixture(t)...)

	models, err := repo.ListModels(t.Context())

	require.NoError(t, err)
	ids := make([]string, 0, len(models))
	byID := make(map[string]core.ProviderModel, len(models))
	for _, model := range models {
		ids = append(ids, model.ID)
		byID[model.ID] = model
	}
	require.Equal(t, []string{
		"opus", "claude-opus-5", "sonnet", "claude-sonnet-5", "fable", "haiku", "claude-haiku-4-5-20251001",
		"claude-fable-5", "claude-opus-4-8", "claude-opus-4-7", "claude-opus-4-6", "claude-sonnet-4-6",
	}, ids)
	require.Equal(t, core.ProviderModel{
		ID:              "opus",
		ResolvedID:      "claude-opus-5-5",
		DisplayName:     "Opus 5.5",
		ReasoningLevels: []string{"low", "medium", "high", "xhigh", "max"},
	}, models[0])
	require.Equal(t, []string{"low", "medium", "high", "max"}, byID["claude-opus-4-6"].ReasoningLevels)
	require.Empty(t, byID["claude-haiku-4-5-20251001"].ReasoningLevels)
	require.Equal(t, []string{
		`{"type":"control_request","request_id":"` + listModelsRequestID + `","request":{"subtype":"initialize"}}`,
	}, session.sent)
	require.True(t, session.closed)
	require.DirExists(t, session.cwd)
}

func TestRepositoryListModels_TrimsLevelsAndDropsEmptyOnes(t *testing.T) {
	repo, _ := newListModelsRepository(
		t,
		`{"type":"control_response","response":{"subtype":"success","request_id":"`+listModelsRequestID+`",`+
			`"response":{"models":[{"value":"opus","displayName":"Opus","supportsEffort":true,`+
			`"supportedEffortLevels":[" low ","","high"]}]}}}`,
	)

	models, err := repo.ListModels(t.Context())

	require.NoError(t, err)
	require.Equal(
		t,
		[]core.ProviderModel{{ID: "opus", DisplayName: "Opus", ReasoningLevels: []string{"low", "high"}}},
		models,
	)
}

func TestRepositoryListModels_IgnoresResponsesToOtherRequests(t *testing.T) {
	repo, _ := newListModelsRepository(
		t,
		`{"type":"control_response","response":{"subtype":"error","request_id":"other","error":"not ours"}}`,
		`{"type":"control_response","response":{"subtype":"success","request_id":"`+listModelsRequestID+`",`+
			`"response":{"models":[{"value":"sonnet","displayName":"Sonnet","supportsEffort":false,`+
			`"supportedEffortLevels":["low"]}]}}}`,
	)

	models, err := repo.ListModels(t.Context())

	require.NoError(t, err)
	require.Equal(t, []core.ProviderModel{{ID: "sonnet", DisplayName: "Sonnet"}}, models)
}

func TestRepositoryListModels_ReportsAnErrorResponse(t *testing.T) {
	repo, session := newListModelsRepository(
		t,
		`{"type":"system","subtype":"hook_started"}`,
		`{"type":"control_response","response":{"subtype":"error","request_id":"`+listModelsRequestID+`",`+
			`"error":"not logged in"}}`,
	)

	_, err := repo.ListModels(t.Context())

	require.ErrorContains(t, err, "initialize failed: not logged in")
	require.True(t, session.closed)
}

func TestRepositoryListModels_ReportsStdoutEndingBeforeTheResponse(t *testing.T) {
	repo, session := newListModelsRepository(t, `{"type":"system","subtype":"hook_started"}`)

	_, err := repo.ListModels(t.Context())

	require.ErrorIs(t, err, io.EOF)
	require.ErrorContains(t, err, "before answering initialize")
	require.True(t, session.closed)
}
