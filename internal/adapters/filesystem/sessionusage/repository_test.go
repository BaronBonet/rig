package sessionusage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRepositoryReadSessionTokenUsage_CodexReturnsLatestTotals(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "codex.jsonl")
	raw := strings.Join([]string{
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":20,"output_tokens":10,"reasoning_output_tokens":3,"total_tokens":110}}}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":240,"cached_input_tokens":80,"output_tokens":25,"reasoning_output_tokens":9,"total_tokens":265}}}}`,
	}, "\n")
	require.NoError(t, os.WriteFile(path, []byte(raw), 0o644))

	repo := NewRepository()
	usage, err := repo.ReadSessionTokenUsage(t.Context(), "codex", path)
	require.NoError(t, err)
	require.NotNil(t, usage)
	require.Equal(t, 240, usage.InputTokens)
	require.Equal(t, 80, usage.CachedInputTokens)
	require.Equal(t, 25, usage.OutputTokens)
	require.Equal(t, 9, usage.ReasoningOutputTokens)
	require.Equal(t, 265, usage.TotalTokens)
}

func TestRepositoryReadSessionTokenUsage_CodexReturnsNilWhenNoTokenCountEvent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "codex.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(`{"type":"session_meta"}`+"\n"), 0o644))

	repo := NewRepository()
	usage, err := repo.ReadSessionTokenUsage(t.Context(), "codex", path)
	require.NoError(t, err)
	require.Nil(t, usage)
}

func TestRepositoryReadSessionTokenUsage_UnsupportedProviderReturnsNil(t *testing.T) {
	repo := NewRepository()
	usage, err := repo.ReadSessionTokenUsage(t.Context(), "gemini", "/tmp/gemini.jsonl")
	require.NoError(t, err)
	require.Nil(t, usage)
}
