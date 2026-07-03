package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BaronBonet/rig/internal/core"
	"github.com/BaronBonet/rig/internal/pkg/subprocess"

	"github.com/stretchr/testify/require"
)

func writeTranscript(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600))
	return path
}

func TestReadSessionTokenUsage_SumsUsageAcrossAssistantMessages(t *testing.T) {
	repo, _ := newTestRepository(t, subprocess.NewMockRunner(t))
	transcript := writeTranscript(t,
		`{"type":"user","message":{"role":"user","content":"fix the bug"}}`,
		`{"type":"assistant","requestId":"req_1","message":{"id":"msg_1",`+
			`"usage":{"input_tokens":100,"output_tokens":40,`+
			`"cache_creation_input_tokens":10,"cache_read_input_tokens":500}}}`,
		`{"type":"assistant","requestId":"req_2","message":{"id":"msg_2",`+
			`"usage":{"input_tokens":30,"output_tokens":60,`+
			`"cache_creation_input_tokens":5,"cache_read_input_tokens":700}}}`,
	)

	usage, err := repo.ReadSessionTokenUsage(t.Context(), transcript)

	require.NoError(t, err)
	require.Equal(t, &core.SessionTokenUsage{
		InputTokens:              130,
		OutputTokens:             100,
		CachedInputTokens:        1200,
		CacheCreationInputTokens: 15,
		TotalTokens:              1445,
	}, usage)
}

func TestReadSessionTokenUsage_CountsRepeatedMessageIDsOnce(t *testing.T) {
	repo, _ := newTestRepository(t, subprocess.NewMockRunner(t))
	// One logical assistant message spans multiple transcript lines that
	// repeat the same message ID and identical usage.
	line := `{"type":"assistant","requestId":"req_1","message":{"id":"msg_1",` +
		`"usage":{"input_tokens":100,"output_tokens":40,` +
		`"cache_creation_input_tokens":10,"cache_read_input_tokens":500}}}`
	transcript := writeTranscript(t, line, line, line)

	usage, err := repo.ReadSessionTokenUsage(t.Context(), transcript)

	require.NoError(t, err)
	require.Equal(t, 100, usage.InputTokens)
	require.Equal(t, 40, usage.OutputTokens)
	require.Equal(t, 650, usage.TotalTokens)
}

func TestReadSessionTokenUsage_IgnoresMalformedAndNonAssistantLines(t *testing.T) {
	repo, _ := newTestRepository(t, subprocess.NewMockRunner(t))
	transcript := writeTranscript(t,
		`not json at all`,
		`{"type":"system","message":{"id":"msg_sys","usage":{"input_tokens":999}}}`,
		`{"type":"assistant","message":{"id":"msg_1","usage":{"input_tokens":10,"output_tokens":5}}}`,
	)

	usage, err := repo.ReadSessionTokenUsage(t.Context(), transcript)

	require.NoError(t, err)
	require.Equal(t, 10, usage.InputTokens)
	require.Equal(t, 5, usage.OutputTokens)
	require.Equal(t, 15, usage.TotalTokens)
}

func TestReadSessionTokenUsage_ReturnsNilForMissingOrUsageFreeTranscripts(t *testing.T) {
	repo, _ := newTestRepository(t, subprocess.NewMockRunner(t))

	usage, err := repo.ReadSessionTokenUsage(t.Context(), "/tmp/does-not-exist.jsonl")
	require.NoError(t, err)
	require.Nil(t, usage)

	empty := writeTranscript(t, `{"type":"user","message":{"role":"user","content":"hello"}}`)
	usage, err = repo.ReadSessionTokenUsage(t.Context(), empty)
	require.NoError(t, err)
	require.Nil(t, usage)
}
