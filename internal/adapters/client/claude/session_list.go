package claude

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/BaronBonet/rig/internal/core"
	"github.com/BaronBonet/rig/internal/pkg/transcript"
)

// ListFolderSessions lists Claude Code sessions started in folder or below it.
// Claude Code keeps a folder's session transcripts in <config>/projects/<folder
// with every non-alphanumeric character replaced by "-">/<session id>.jsonl.
// That name is lossy ("/a/b-c" and "/a/b/c" share it, and so do siblings such
// as "/a/b-old"), so each session's folder is read from its transcript.
func (r *repository) ListFolderSessions(
	ctx context.Context,
	folder string,
	limit int,
) ([]core.ProviderSessionSummary, error) {
	configDir, err := r.resolveConfigDir()
	if err != nil {
		return nil, err
	}
	folder = filepath.Clean(strings.TrimSpace(folder))
	projectsDir := filepath.Join(configDir, "projects")
	projects, err := os.ReadDir(projectsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list claude projects in %q: %w", projectsDir, err)
	}

	folderDir := claudeProjectDirName(folder)
	belowDir := claudeProjectDirName(
		strings.TrimSuffix(folder, string(filepath.Separator)) + string(filepath.Separator),
	)
	type candidate struct {
		modTime    time.Time
		path       string
		id         string
		projectDir string
	}
	var candidates []candidate
	for _, project := range projects {
		name := project.Name()
		if !project.IsDir() || (name != folderDir && !strings.HasPrefix(name, belowDir)) {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(projectsDir, name))
		if err != nil {
			continue
		}
		for _, entry := range entries {
			id, ok := strings.CutSuffix(entry.Name(), ".jsonl")
			if entry.IsDir() || !ok {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			candidates = append(candidates, candidate{
				modTime:    info.ModTime(),
				path:       filepath.Join(projectsDir, name, entry.Name()),
				id:         id,
				projectDir: name,
			})
		}
	}
	slices.SortFunc(candidates, func(a, b candidate) int { return b.modTime.Compare(a.modTime) })

	var sessions []core.ProviderSessionSummary
	for _, candidate := range candidates {
		if limit > 0 && len(sessions) == limit {
			break
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		cwd, err := claudeSessionCwd(candidate.path, candidate.projectDir)
		if err != nil {
			return nil, err
		}
		// A transcript that never recorded its folder can only be placed by an
		// exact project match.
		if cwd == "" && candidate.projectDir == folderDir {
			cwd = folder
		}
		if cwd == "" || !core.FolderContains(folder, cwd) {
			continue
		}
		title, err := claudeSessionTitle(candidate.path)
		if err != nil {
			return nil, err
		}
		// A session without a prompt has nothing to resume.
		if title == "" {
			continue
		}
		sessions = append(sessions, core.ProviderSessionSummary{
			LastActiveAt:   candidate.modTime,
			Provider:       core.ProviderClaude,
			SessionID:      candidate.id,
			Title:          title,
			Cwd:            cwd,
			TranscriptPath: candidate.path,
		})
	}
	return sessions, nil
}

// claudeSessionCwd returns the folder a session was started in, which is where
// `claude --resume` finds it. Transcript lines record the working directory at
// the time, and a session can move into other folders, so the first one that
// names the session's project directory wins; failing that, the first one
// recorded. It returns "" when the transcript's start records no folder.
func claudeSessionCwd(path string, projectDir string) (string, error) {
	head, err := transcript.Head(path)
	if err != nil {
		return "", err
	}
	first := ""
	for _, line := range head {
		var record struct {
			Cwd string `json:"cwd"`
		}
		if json.Unmarshal(line, &record) != nil || !filepath.IsAbs(record.Cwd) {
			continue
		}
		cwd := filepath.Clean(record.Cwd)
		if claudeProjectDirName(cwd) == projectDir {
			return cwd, nil
		}
		first = cmp.Or(first, cwd)
	}
	return first, nil
}

var nonAlphanumeric = regexp.MustCompile(`[^a-zA-Z0-9]`)

func claudeProjectDirName(folder string) string {
	return nonAlphanumeric.ReplaceAllString(folder, "-")
}

// claudeSessionTitle names a session the way Claude Code does: by the title the
// user gave it, else the title Claude Code generated, else an older-style
// summary, else its first prompt. Title records are rewritten throughout a
// transcript, so the newest sit near the end; the first prompt sits near the
// start. Only those two windows are read.
func claudeSessionTitle(path string) (string, error) {
	tail, err := transcript.Tail(path)
	if err != nil {
		return "", err
	}
	var customTitle, aiTitle, summary string
	for index := len(tail) - 1; index >= 0; index-- {
		var record struct {
			Type        string `json:"type"`
			CustomTitle string `json:"customTitle"`
			AITitle     string `json:"aiTitle"`
			Summary     string `json:"summary"`
		}
		if json.Unmarshal(tail[index], &record) != nil {
			continue
		}
		switch record.Type {
		case "custom-title":
			customTitle = cmp.Or(customTitle, strings.TrimSpace(record.CustomTitle))
		case "ai-title":
			aiTitle = cmp.Or(aiTitle, strings.TrimSpace(record.AITitle))
		case "summary":
			summary = cmp.Or(summary, strings.TrimSpace(record.Summary))
		}
	}
	if title := cmp.Or(customTitle, aiTitle, summary); title != "" {
		return transcript.Title(title), nil
	}

	head, err := transcript.Head(path)
	if err != nil {
		return "", err
	}
	for _, line := range head {
		var entry claudeActivityLine
		if json.Unmarshal(line, &entry) != nil || entry.Type != "user" || entry.IsMeta || entry.IsSidechain {
			continue
		}
		prompt := strings.TrimSpace(claudeUserMessageText(entry.Message.Content))
		// Slash commands and injected context are wrapped in tags.
		if prompt != "" && !strings.HasPrefix(prompt, "<") {
			return transcript.Title(prompt), nil
		}
	}
	return "", nil
}

func (r *repository) resolveConfigDir() (string, error) {
	if r.claudeConfigDir != nil {
		return r.claudeConfigDir()
	}
	if custom := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); custom != "" {
		return custom, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve claude config dir: %w", err)
	}
	return filepath.Join(home, ".claude"), nil
}
