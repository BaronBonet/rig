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

func TestRepositoryReadSessionTokenUsage_ClaudeDeduplicatesAndSumsAcrossMessages(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "claude.jsonl")

	// Two messages: msg-1 appears 3 times (streaming), msg-2 appears twice.
	// Only the last occurrence per ID should count; then sum across IDs.
	raw := strings.Join([]string{
		// msg-1 first streaming chunk
		`{"type":"assistant","message":{"id":"msg-1","usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":100,"cache_creation_input_tokens":50}}}`,
		// msg-1 second streaming chunk (output grows)
		`{"type":"assistant","message":{"id":"msg-1","usage":{"input_tokens":10,"output_tokens":20,"cache_read_input_tokens":100,"cache_creation_input_tokens":50}}}`,
		// msg-1 final (output at final value)
		`{"type":"assistant","message":{"id":"msg-1","usage":{"input_tokens":10,"output_tokens":200,"cache_read_input_tokens":100,"cache_creation_input_tokens":50}}}`,
		// non-assistant line (should be ignored)
		`{"type":"permission-mode","permissionMode":"acceptEdits"}`,
		// msg-2 first chunk
		`{"type":"assistant","message":{"id":"msg-2","usage":{"input_tokens":5,"output_tokens":30,"cache_read_input_tokens":200,"cache_creation_input_tokens":0}}}`,
		// msg-2 final
		`{"type":"assistant","message":{"id":"msg-2","usage":{"input_tokens":5,"output_tokens":100,"cache_read_input_tokens":200,"cache_creation_input_tokens":0}}}`,
	}, "\n")
	require.NoError(t, os.WriteFile(path, []byte(raw), 0o644))

	repo := NewRepository()
	usage, err := repo.ReadSessionTokenUsage(t.Context(), "claude", path)
	require.NoError(t, err)
	require.NotNil(t, usage)

	// msg-1 final: input_tokens=10, cache_creation=50, cache_read=100, output=200
	// msg-2 final: input_tokens=5,  cache_creation=0,  cache_read=200, output=100
	// InputTokens = (10+50) + (5+0) = 65  (raw input + cache creation = full-rate input)
	// OutputTokens = 200 + 100 = 300
	// CacheCreationInputTokens = 50 + 0 = 50
	// CachedInputTokens = 100 + 200 = 300
	// TotalTokens = 65 + 300 + 300 = 665
	require.Equal(t, 65, usage.InputTokens)
	require.Equal(t, 300, usage.OutputTokens)
	require.Equal(t, 50, usage.CacheCreationInputTokens)
	require.Equal(t, 300, usage.CachedInputTokens)
	require.Equal(t, 665, usage.TotalTokens)
}

func TestRepositoryReadSessionTokenUsage_ClaudeReturnsNilWhenNoUsage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "claude.jsonl")
	raw := strings.Join([]string{
		`{"type":"permission-mode","permissionMode":"acceptEdits"}`,
		`{"type":"file-history-snapshot","messageId":"abc"}`,
	}, "\n")
	require.NoError(t, os.WriteFile(path, []byte(raw), 0o644))

	repo := NewRepository()
	usage, err := repo.ReadSessionTokenUsage(t.Context(), "claude", path)
	require.NoError(t, err)
	require.Nil(t, usage)
}

func TestRepositoryReadSessionTokenUsage_ClaudeIgnoresEntriesWithoutMessageID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "claude.jsonl")
	raw := strings.Join([]string{
		// Entry with usage but no message ID — should be skipped
		`{"type":"assistant","message":{"id":"","usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}`,
		// Valid entry
		`{"type":"assistant","message":{"id":"msg-1","usage":{"input_tokens":10,"output_tokens":20,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}`,
	}, "\n")
	require.NoError(t, os.WriteFile(path, []byte(raw), 0o644))

	repo := NewRepository()
	usage, err := repo.ReadSessionTokenUsage(t.Context(), "claude", path)
	require.NoError(t, err)
	require.NotNil(t, usage)
	require.Equal(t, 10, usage.InputTokens)
	require.Equal(t, 20, usage.OutputTokens)
	require.Equal(t, 30, usage.TotalTokens)
}
