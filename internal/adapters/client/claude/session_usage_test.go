package claude

import (
	"os"
	"path/filepath"
	"strconv"
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

func TestReadConversationUsage_CountsCompactBoundariesByTrigger(t *testing.T) {
	repo, _ := newTestRepository(t, subprocess.NewMockRunner(t))
	transcript := writeTranscript(t,
		`{"type":"user","message":{"role":"user","content":"why is \"compact_boundary\" in the logs?"}}`,
		`{"type":"system","subtype":"compact_boundary","content":"Conversation compacted",`+
			`"compactMetadata":{"trigger":"auto","preTokens":180000}}`,
		`{"type":"system","subtype":"microcompact_boundary","content":"Context microcompacted"}`,
		`{"type":"system","subtype":"compact_boundary","compactMetadata":{"trigger":"manual"}}`,
		`{"type":"system","subtype":"compact_boundary","compactMetadata":{"trigger":"auto"}}`,
		`{"type":"system","subtype":"compact_boundary"}`,
		`{"type":"system","subtype":"compact_boundary","isSidechain":true,"compactMetadata":{"trigger":"auto"}}`,
		`not json "compact_boundary"`,
	)

	usage, err := repo.ReadConversationUsage(t.Context(), []core.TaskProviderSession{{
		Provider:       core.ProviderClaude,
		TranscriptPath: transcript,
	}})

	require.NoError(t, err)
	require.Equal(t, core.ConversationUsage{Compactions: core.Compactions{Total: 4, Auto: 2, Manual: 1}}, usage)
}

// The context is what the root agent's latest request put in it, output
// tokens left out, even when that request's tool call mentions
// compact_boundary. A subagent's request on the sidechain and a synthetic
// message say nothing about it.
func TestReadConversationUsage_ReadsTheLatestRequestsContext(t *testing.T) {
	repo, _ := newTestRepository(t, subprocess.NewMockRunner(t))
	transcript := writeTranscript(t,
		claudeAssistantEntry(false, "claude-opus-5-5", 3, 12000, 8000),
		`{"isSidechain":false,"message":{"model":"claude-opus-5-5","role":"assistant","content":[`+
			`{"type":"tool_use","name":"Grep","input":{"pattern":"compact_boundary"}}],`+
			`"usage":{"input_tokens":2,"cache_creation_input_tokens":635,"cache_read_input_tokens":52000,`+
			`"output_tokens":200}},"type":"assistant"}`,
		claudeAssistantEntry(true, "claude-opus-5-5", 5, 30000, 90000),
		claudeAssistantEntry(false, "<synthetic>", 0, 0, 0),
		`{"type":"user","isSidechain":false,"message":{"role":"user","content":"thanks"}}`,
	)

	usage, err := repo.ReadConversationUsage(t.Context(), []core.TaskProviderSession{{
		Provider:       core.ProviderClaude,
		TranscriptPath: transcript,
	}})

	require.NoError(t, err)
	require.Equal(t, core.ConversationUsage{Context: &core.ContextUsage{Tokens: 52637}}, usage)
}

// A compaction after the root agent's latest request leaves its context
// unknown until the next request, even though it records the compacted
// size: that size leaves out the system prompt and tools.
func TestReadConversationUsage_LeavesTheContextUnknownAfterACompaction(t *testing.T) {
	repo, _ := newTestRepository(t, subprocess.NewMockRunner(t))
	fixture, err := os.ReadFile(filepath.Join("testdata", "claude_transcript_compacted.jsonl"))
	require.NoError(t, err)
	transcript := filepath.Join(t.TempDir(), "transcript.jsonl")
	require.NoError(t, os.WriteFile(transcript, fixture, 0o600))
	conversation := []core.TaskProviderSession{{Provider: core.ProviderClaude, TranscriptPath: transcript}}

	usage, err := repo.ReadConversationUsage(t.Context(), conversation)
	require.NoError(t, err)
	require.Equal(t, core.ConversationUsage{Compactions: core.Compactions{Total: 1, Manual: 1}}, usage)

	appendTranscript(t, transcript, claudeAssistantEntry(false, "claude-opus-5-5", 2, 23528, 29055)+"\n")
	usage, err = repo.ReadConversationUsage(t.Context(), conversation)
	require.NoError(t, err)
	require.Equal(t, core.ConversationUsage{
		Compactions: core.Compactions{Total: 1, Manual: 1},
		Context:     &core.ContextUsage{Tokens: 52585},
	}, usage)
}

func TestReadConversationUsage_ReturnsNoneWithoutATranscript(t *testing.T) {
	repo, _ := newTestRepository(t, subprocess.NewMockRunner(t))

	usage, err := repo.ReadConversationUsage(t.Context(), []core.TaskProviderSession{
		{Provider: core.ProviderClaude},
		{Provider: core.ProviderClaude, TranscriptPath: filepath.Join(t.TempDir(), "missing.jsonl")},
	})

	require.NoError(t, err)
	require.Equal(t, core.ConversationUsage{}, usage)
}

// claudeAssistantEntry is an assistant transcript entry for one API request
// whose usage reports the given input, cache creation and cache read tokens.
func claudeAssistantEntry(sidechain bool, model string, input int, cacheCreation int, cacheRead int) string {
	return `{"parentUuid":null,"isSidechain":` + strconv.FormatBool(sidechain) + `,"message":{"model":"` + model +
		`","type":"message","role":"assistant","content":[{"type":"text","text":"Done."}],"usage":{` +
		`"input_tokens":` + strconv.Itoa(input) +
		`,"cache_creation_input_tokens":` + strconv.Itoa(cacheCreation) +
		`,"cache_read_input_tokens":` + strconv.Itoa(cacheRead) +
		`,"output_tokens":455}},"type":"assistant"}`
}

func appendTranscript(t *testing.T, path string, content string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	require.NoError(t, err)
	_, err = file.WriteString(content)
	require.NoError(t, err)
	require.NoError(t, file.Close())
}
