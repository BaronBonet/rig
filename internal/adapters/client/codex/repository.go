package codex

import (
	"context"
	"fmt"
	"os"
	"strings"
	"unicode"

	"agent/internal/core"
	"agent/internal/pkg/execx"
)

type Repository struct {
	runner execx.Runner
	binary string
}

type Config struct {
	Binary string
}

func NewRepository(runner execx.Runner, cfg Config) *Repository {
	if cfg.Binary == "" {
		cfg.Binary = "codex"
	}

	return &Repository{
		runner: runner,
		binary: cfg.Binary,
	}
}

func (r *Repository) IsAvailable(ctx context.Context) error {
	_, err := r.runner.Run(ctx, "", r.binary, "--help")
	return err
}

func (r *Repository) ProposeTaskName(ctx context.Context, prompt string) (string, error) {
	tmpFile, err := os.CreateTemp("", "agent-codex-name-*.txt")
	if err != nil {
		return "", err
	}
	tmpPath := tmpFile.Name()
	_ = tmpFile.Close()
	defer func() { _ = os.Remove(tmpPath) }()

	result, err := r.runner.Run(
		ctx,
		"",
		r.binary,
		"exec",
		"--skip-git-repo-check",
		"--output-last-message",
		tmpPath,
		"Reply with only a short task title (3-5 words, no quotes): "+prompt,
	)
	if fileBytes, readErr := os.ReadFile(tmpPath); readErr == nil {
		if title := extractCodexTitle(string(fileBytes)); title != "" {
			return title, nil
		}
	}

	if title := extractCodexTitle(result.Stdout); title != "" {
		return title, nil
	}

	if err != nil {
		return "", fmt.Errorf("codex exec failed: %w", err)
	}

	return "", fmt.Errorf("codex did not return a usable task title")
}

func (r *Repository) SuggestTaskName(ctx context.Context, prompt string) (string, error) {
	return r.ProposeTaskName(ctx, prompt)
}

func (r *Repository) BuildLaunchCommand(task *core.Task) ([]string, error) {
	return []string{r.binary, task.Prompt}, nil
}

func (r *Repository) LaunchRequest(task *core.Task) (core.LaunchRequest, error) {
	return core.LaunchRequest{
		Command:      []string{r.binary, "--enable", "codex_hooks"},
		Prompt:       "›",
		InitialInput: []string{task.Prompt},
	}, nil
}

func (r *Repository) DetectRuntimeState(snapshot core.RuntimeSnapshot) core.RuntimeState {
	return detectRuntimeState(snapshot)
}

func (r *Repository) PromptMarker() string {
	return "›"
}

func extractCodexTitle(raw string) string {
	lines := strings.Split(raw, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if title := normalizeCodexTitle(lines[i]); title != "" {
			return title
		}
	}

	return ""
}

func normalizeCodexTitle(raw string) string {
	line := strings.TrimSpace(raw)
	line = strings.ReplaceAll(line, "`", "")
	line = strings.Trim(line, "[]")
	line = strings.Trim(line, ":")
	line = strings.Trim(line, `"'`)
	line = strings.TrimSpace(line)

	if line == "" {
		return ""
	}

	lower := strings.ToLower(line)
	if strings.HasPrefix(lower, "tokens used") {
		return ""
	}
	if strings.HasPrefix(lower, "openai codex") {
		return ""
	}
	if strings.HasPrefix(lower, "usage:") {
		return ""
	}
	if strings.HasPrefix(lower, "error:") {
		return ""
	}
	if strings.HasPrefix(lower, "exit status") {
		return ""
	}
	if !containsLetter(line) {
		return ""
	}

	return line
}

func containsLetter(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}

	return false
}
