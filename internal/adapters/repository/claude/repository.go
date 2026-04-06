package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"agent/internal/core"
	"agent/internal/pkg/execx"
)

type Repository struct {
	runner execx.Runner
	binary string
}

func NewRepository(runner execx.Runner, binary string) *Repository {
	if binary == "" {
		binary = "claude"
	}

	return &Repository{
		runner: runner,
		binary: binary,
	}
}

func (r *Repository) IsAvailable(ctx context.Context) error {
	_, err := r.runner.Run(ctx, "", r.binary, "--version")
	return err
}

type claudeResult struct {
	Result  string `json:"result"`
	IsError bool   `json:"is_error"`
}

func (r *Repository) ProposeTaskName(ctx context.Context, prompt string) (string, error) {
	result, err := r.runner.RunWithStdin(ctx, execx.RunWithStdinOptions{
		Name:  r.binary,
		Stdin: prompt,
		Args: []string{
			"-p",
			"--output-format", "json",
			"--tools", "",
			"--system-prompt", "You are a task naming assistant. Reply with ONLY a short title (3-5 words, no quotes). No explanations, no other text.",
		},
	})
	if err != nil {
		return "", fmt.Errorf("claude exec failed: %w", err)
	}

	var parsed claudeResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(result.Stdout)), &parsed); err != nil {
		return "", fmt.Errorf("claude: failed to parse JSON output: %w", err)
	}

	if parsed.IsError {
		return "", fmt.Errorf("claude returned error: %s", parsed.Result)
	}

	title := normalizeTitle(parsed.Result)
	if title == "" {
		return "", fmt.Errorf("claude did not return a usable task title")
	}

	return title, nil
}

func (r *Repository) BuildLaunchCommand(task *core.Task) ([]string, error) {
	return []string{r.binary, task.Prompt}, nil
}

func normalizeTitle(raw string) string {
	line := strings.TrimSpace(raw)
	line = strings.ReplaceAll(line, "`", "")
	line = strings.Trim(line, "[]")
	line = strings.Trim(line, ":")
	line = strings.Trim(line, `"'`)
	line = strings.TrimSpace(line)

	if line == "" {
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
