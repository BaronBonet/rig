package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHeadAndTail_ReturnOnlyWholeLinesFromLargeTranscripts(t *testing.T) {
	line := strings.Repeat("x", 1000)
	lines := make([]string, 0, 600)
	for range 600 {
		lines = append(lines, line)
	}
	lines[0] = "first"
	lines[len(lines)-1] = "last"
	path := filepath.Join(t.TempDir(), "session.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600))

	head, err := Head(path)
	require.NoError(t, err)
	require.Equal(t, "first", string(head[0]))
	for _, got := range head[1:] {
		require.Len(t, got, 1000, "a line cut by the window must be dropped")
	}

	tail, err := Tail(path)
	require.NoError(t, err)
	require.Equal(t, "last", string(tail[len(tail)-2]))
	for _, got := range tail[:len(tail)-2] {
		require.Len(t, got, 1000, "a line cut by the window must be dropped")
	}
}

func TestTitle_FlattensAndShortensText(t *testing.T) {
	require.Equal(t, "fix the date format", Title("  fix the\n date   format "))
	long := Title(strings.Repeat("word ", 40))
	require.LessOrEqual(t, len([]rune(long)), TitleMaxRunes)
	require.True(t, strings.HasSuffix(long, "…"))
}
