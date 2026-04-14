package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"rig/internal/core"
	"rig/internal/pkg/execx"
	"rig/internal/pkg/prompts"
)

type Repository struct {
	runner         execx.Runner
	binary         string
	hookListenAddr string
}

type Config struct {
	Binary         string
	HookListenAddr string
}

func NewRepository(runner execx.Runner, cfg Config) *Repository {
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
	result, err := r.runner.RunWithStdin(ctx, execx.RunWithStdinOptions{
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

	return suggestion, nil
}

func (r *Repository) SuggestTaskName(ctx context.Context, prompt string) (core.TaskSuggestion, error) {
	return r.ProposeTaskName(ctx, prompt)
}

func (r *Repository) BuildLaunchCommand(task *core.Task) ([]string, error) {
	return []string{r.binary, task.Prompt}, nil
}

func (r *Repository) LaunchRequest(task *core.Task) (core.LaunchRequest, error) {
	var initialInput []string
	if strings.TrimSpace(task.Prompt) != "" {
		initialInput = []string{task.Prompt}
	}

	req := core.LaunchRequest{
		Command:      []string{r.binary},
		Prompt:       "❯",
		InitialInput: initialInput,
	}

	return r.withHookSettings(req)
}

func (r *Repository) RestoreLaunchRequest(
	_ *core.Task,
	hookSession *core.HookSessionSummary,
) (core.LaunchRequest, error) {
	command := []string{r.binary, "--resume"}
	if hookSession != nil && strings.TrimSpace(hookSession.SessionID) != "" {
		command = append(command, strings.TrimSpace(hookSession.SessionID))
	}

	req := core.LaunchRequest{
		Command: command,
		Prompt:  "❯",
	}

	return r.withHookSettings(req)
}

func (r *Repository) withHookSettings(req core.LaunchRequest) (core.LaunchRequest, error) {
	if r.hookListenAddr != "" {
		settings, err := buildHookSettings(r.hookListenAddr)
		if err != nil {
			return req, fmt.Errorf("build hook settings: %w", err)
		}
		req.SetupFiles = map[string][]byte{
			".claude/settings.local.json": settings,
		}
	}

	return req, nil
}

func (r *Repository) DetectRuntimeState(snapshot core.RuntimeSnapshot) core.RuntimeState {
	return detectRuntimeState(snapshot)
}

func (r *Repository) PromptMarker() string {
	return "❯"
}

func buildHookSettings(listenAddr string) ([]byte, error) {
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
