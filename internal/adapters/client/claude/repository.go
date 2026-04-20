package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"rig/internal/core"
	"rig/internal/pkg/prompts"
	"rig/internal/pkg/subprocess"
)

type Repository struct {
	runner         subprocess.Runner
	binary         string
	hookListenAddr string
}

type Config struct {
	Binary         string `env:"AGENT_CLAUDE_BINARY" envDefault:"claude"`
	HookListenAddr string `env:"AGENT_HOOK_LISTEN_ADDR" envDefault:"127.0.0.1:4123"`
}

func NewRepository(runner subprocess.Runner, cfg Config) *Repository {
	if cfg.Binary == "" {
		cfg.Binary = "claude"
	}

	return &Repository{
		runner:         runner,
		binary:         cfg.Binary,
		hookListenAddr: cfg.HookListenAddr,
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

func (r *Repository) ProposeTaskName(ctx context.Context, prompt string) (core.TaskSuggestion, error) {
	result, err := r.runner.RunWithStdin(ctx, subprocess.RunWithStdinOptions{
		Name:  r.binary,
		Stdin: prompt,
		Args: []string{
			"-p",
			"--output-format", "json",
			"--tools", "",
			"--system-prompt", prompts.SuggestTaskPrompt,
		},
	})
	if err != nil {
		return core.TaskSuggestion{}, fmt.Errorf("claude exec failed: %w", err)
	}

	var parsed claudeResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(result.Stdout)), &parsed); err != nil {
		return core.TaskSuggestion{}, fmt.Errorf("claude: failed to parse JSON output: %w", err)
	}

	if parsed.IsError {
		return core.TaskSuggestion{}, fmt.Errorf("claude returned error: %s", parsed.Result)
	}

	var suggestion core.TaskSuggestion
	if err := json.Unmarshal([]byte(parsed.Result), &suggestion); err != nil {
		title := normalizeTitle(parsed.Result)
		if title == "" {
			return core.TaskSuggestion{}, fmt.Errorf("claude did not return a usable task title")
		}
		return core.TaskSuggestion{Name: title, BranchType: "feat"}, nil
	}

	suggestion.Name = normalizeTitle(suggestion.Name)
	if suggestion.Name == "" {
		return core.TaskSuggestion{}, fmt.Errorf("claude did not return a usable task title")
	}
	if suggestion.BranchType == "" {
		suggestion.BranchType = "feat"
	}

	return suggestion, nil
}

func (r *Repository) SuggestTaskName(ctx context.Context, prompt string) (core.TaskSuggestion, error) {
	return r.ProposeTaskName(ctx, prompt)
}

func (r *Repository) BuildWorkspaceBootstrapSpec(_ *core.Task) (core.WorkspaceBootstrapSpec, error) {
	listenAddr := strings.TrimSpace(r.hookListenAddr)
	if listenAddr == "" {
		listenAddr = strings.TrimSpace(os.Getenv("AGENT_HOOK_LISTEN_ADDR"))
	}
	if listenAddr == "" {
		listenAddr = "127.0.0.1:4123"
	}

	settings, err := BuildHookSettings(listenAddr)
	if err != nil {
		return core.WorkspaceBootstrapSpec{}, err
	}

	return core.WorkspaceBootstrapSpec{
		Files: []core.WorkspaceBootstrapFile{
			{
				Path:     filepath.Join(".claude", "settings.local.json"),
				Content:  settings,
				FileMode: 0o644,
			},
		},
	}, nil
}

func (r *Repository) BuildLaunchCommand(task *core.Task) ([]string, error) {
	return []string{r.binary, task.Prompt}, nil
}

func (r *Repository) BuildTaskSessionLaunchSpec(task *core.Task) (core.TaskSessionLaunchSpec, error) {
	var initialInput []string
	if strings.TrimSpace(task.Prompt) != "" {
		initialInput = []string{task.Prompt}
	}

	return core.TaskSessionLaunchSpec{
		Command:      []string{r.binary},
		ReadyMarker:  "❯",
		PrefillInput: initialInput,
	}, nil
}

func (r *Repository) RestoreTaskSessionLaunchSpec(
	_ *core.Task,
	hookSession *core.HookSessionSummary,
) (core.TaskSessionLaunchSpec, error) {
	command := []string{r.binary, "--resume"}
	if hookSession != nil && strings.TrimSpace(hookSession.SessionID) != "" {
		command = append(command, strings.TrimSpace(hookSession.SessionID))
	}

	return core.TaskSessionLaunchSpec{
		Command:     command,
		ReadyMarker: "❯",
	}, nil
}

func (r *Repository) DetectRuntimeState(snapshot core.RuntimeSnapshot) core.RuntimeState {
	return detectRuntimeState(snapshot)
}

func (r *Repository) PromptMarker() string {
	return "❯"
}

func BuildHookSettings(listenAddr string) ([]byte, error) {
	hookURL := "http://" + listenAddr + "/claude-hook"

	hook := map[string]any{
		"type":    "http",
		"url":     hookURL,
		"timeout": 5,
	}
	matchAll := []map[string]any{
		{"matcher": "", "hooks": []any{hook}},
	}

	settings := map[string]any{
		"hooks": map[string]any{
			"SessionStart":      matchAll,
			"UserPromptSubmit":  matchAll,
			"PreToolUse":        matchAll,
			"PostToolUse":       matchAll,
			"PermissionRequest": matchAll,
			"Stop":              matchAll,
		},
	}

	return json.MarshalIndent(settings, "", "  ")
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
