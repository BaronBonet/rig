// Package transcript holds helpers shared by the provider adapters for reading
// append-only JSONL session transcripts.
package transcript

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

// WindowBytes bounds how much of a transcript Head and Tail read. Session
// titles sit near either end, and transcripts of long sessions run to tens of
// megabytes.
const WindowBytes = 256 << 10

// Head returns the complete lines within the first WindowBytes of path.
func Head(path string) ([][]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open transcript %q: %w", path, err)
	}
	defer file.Close()

	buf := make([]byte, WindowBytes)
	count, err := io.ReadFull(file, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("read transcript %q: %w", path, err)
	}
	lines := bytes.Split(buf[:count], []byte("\n"))
	if count == WindowBytes && len(lines) > 1 {
		lines = lines[:len(lines)-1] // drop the line the window cut
	}
	return lines, nil
}

// Tail returns the complete lines within the last WindowBytes of path, oldest
// first.
func Tail(path string) ([][]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open transcript %q: %w", path, err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat transcript %q: %w", path, err)
	}
	start := max(info.Size()-WindowBytes, 0)
	buf := make([]byte, info.Size()-start)
	if _, err := file.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("read transcript %q: %w", path, err)
	}
	lines := bytes.Split(buf, []byte("\n"))
	if start > 0 && len(lines) > 1 {
		lines = lines[1:] // drop the line the window cut
	}
	return lines, nil
}

// TitleMaxRunes bounds a session title taken from a prompt.
const TitleMaxRunes = 80

// Title turns free text such as a first prompt into a one-line title.
func Title(text string) string {
	title := strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(title) <= TitleMaxRunes {
		return title
	}
	runes := []rune(title)
	return strings.TrimSpace(string(runes[:TitleMaxRunes-1])) + "…"
}
