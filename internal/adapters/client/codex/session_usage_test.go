package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"rig/internal/core"

	"github.com/stretchr/testify/require"
)

func TestRepositoryReadSessionTokenUsage_ReturnsLatestTotals(t *testing.T) {
	repo := &repository{}
	path := writeJSONL(t, []string{
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":20,"output_tokens":10,"reasoning_output_tokens":3,"total_tokens":110}}}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":240,"cached_input_tokens":80,"cache_creation_input_tokens":15,"output_tokens":25,"reasoning_output_tokens":9,"total_tokens":265}}}}`,
	})

	usage, err := repo.ReadSessionTokenUsage(t.Context(), path)

	require.NoError(t, err)
	require.Equal(t, &core.SessionTokenUsage{
		InputTokens:              240,
		CachedInputTokens:        80,
		CacheCreationInputTokens: 15,
		OutputTokens:             25,
		ReasoningOutputTokens:    9,
		TotalTokens:              265,
	}, usage)
}

func TestRepositoryReadSessionTokenUsage_SkipsLargeNonTokenLines(t *testing.T) {
	repo := &repository{}
	path := writeJSONL(t, []string{
		`{"type":"event_msg","payload":{"type":"tool_output","text":"` + strings.Repeat("x", 2*1024*1024) + `"}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":240,"cached_input_tokens":80,"output_tokens":25,"reasoning_output_tokens":9,"total_tokens":265}}}}`,
	})

	usage, err := repo.ReadSessionTokenUsage(t.Context(), path)

	require.NoError(t, err)
	require.Equal(t, &core.SessionTokenUsage{
		InputTokens:           240,
		CachedInputTokens:     80,
		OutputTokens:          25,
		ReasoningOutputTokens: 9,
		TotalTokens:           265,
	}, usage)
}

func TestRepositoryReadSessionTokenUsage_MissingTranscriptReturnsNil(t *testing.T) {
	repo := &repository{}

	usage, err := repo.ReadSessionTokenUsage(t.Context(), "/tmp/does-not-exist.jsonl")

	require.NoError(t, err)
	require.Nil(t, usage)
}

func TestRepositoryReadSessionTokenUsage_OpenErrorReturnsError(t *testing.T) {
	repo := &repository{}

	usage, err := repo.ReadSessionTokenUsage(t.Context(), "bad\x00path.jsonl")

	require.Error(t, err)
	require.Nil(t, usage)
}

func writeJSONL(t *testing.T, lines []string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	content := strings.Join(lines, "\n") + "\n"
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}
