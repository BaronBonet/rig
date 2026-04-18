package codex

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"unicode"

	"rig/internal/core"
	"rig/internal/pkg/execx"
	"rig/internal/pkg/prompts"
)

//go:embed forward-to-rig.sh.tmpl
var forwarderScriptTemplateText string

var forwarderScriptTemplate = template.Must(template.New("forward-to-rig.sh").Parse(forwarderScriptTemplateText))

type Repository struct {
	runner        execx.Runner
	binary        string
	rigBinaryPath string
	sourceRoot    string
}

type Config struct {
	Binary        string
	RigBinaryPath string
	SourceRoot    string
}

func NewRepository(runner execx.Runner, cfg Config) *Repository {
	if cfg.Binary == "" {
		cfg.Binary = "codex"
	}

	return &Repository{
		runner:        runner,
		binary:        cfg.Binary,
		rigBinaryPath: strings.TrimSpace(cfg.RigBinaryPath),
		sourceRoot:    strings.TrimSpace(cfg.SourceRoot),
	}
}

func (r *Repository) IsAvailable(ctx context.Context) error {
	_, err := r.runner.Run(ctx, "", r.binary, "--help")
	return err
}

func (r *Repository) ProposeTaskName(ctx context.Context, prompt string) (core.TaskSuggestion, error) {
	tmpFile, err := os.CreateTemp("", "agent-codex-name-*.txt")
	if err != nil {
		return core.TaskSuggestion{}, err
	}
	tmpPath := tmpFile.Name()
	_ = tmpFile.Close()
	defer func() { _ = os.Remove(tmpPath) }()

	fullPrompt := prompts.SuggestTaskPrompt + "\n\nTask description: " + prompt

	result, err := r.runner.Run(
		ctx,
		"",
		r.binary,
		"exec",
		"--skip-git-repo-check",
		"--output-last-message",
		tmpPath,
		fullPrompt,
	)

	// Try to parse structured JSON from the output file first
	if fileBytes, readErr := os.ReadFile(tmpPath); readErr == nil {
		if suggestion, ok := parseCodexSuggestion(string(fileBytes)); ok {
			return suggestion, nil
		}
	}

	// Fall back to stdout
	if suggestion, ok := parseCodexSuggestion(result.Stdout); ok {
		return suggestion, nil
	}

	// Fall back to extracting a plain title
	if fileBytes, readErr := os.ReadFile(tmpPath); readErr == nil {
		if title := extractCodexTitle(string(fileBytes)); title != "" {
			return core.TaskSuggestion{Name: title, BranchType: "feat"}, nil
		}
	}
	if title := extractCodexTitle(result.Stdout); title != "" {
		return core.TaskSuggestion{Name: title, BranchType: "feat"}, nil
	}

	if err != nil {
		return core.TaskSuggestion{}, fmt.Errorf("codex exec failed: %w", err)
	}

	return core.TaskSuggestion{}, fmt.Errorf("codex did not return a usable task title")
}

func (r *Repository) SuggestTaskName(ctx context.Context, prompt string) (core.TaskSuggestion, error) {
	return r.ProposeTaskName(ctx, prompt)
}

func (r *Repository) BuildWorkspaceBootstrapSpec(_ *core.Task) (core.WorkspaceBootstrapSpec, error) {
	hooksJSON, err := r.renderHooksJSON()
	if err != nil {
		return core.WorkspaceBootstrapSpec{}, err
	}

	forwarderScript, err := r.renderForwarderScript()
	if err != nil {
		return core.WorkspaceBootstrapSpec{}, err
	}

	return core.WorkspaceBootstrapSpec{
		Files: []core.WorkspaceBootstrapFile{
			{
				Path:     filepath.Join(".codex", "hooks", "hooks.json"),
				Content:  hooksJSON,
				FileMode: 0o644,
			},
			{
				Path:     filepath.Join(".codex", "hooks", "forward-to-rig.sh"),
				Content:  forwarderScript,
				FileMode: 0o755,
			},
		},
	}, nil
}

func parseCodexSuggestion(raw string) (core.TaskSuggestion, bool) {
	lines := strings.Split(raw, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var suggestion core.TaskSuggestion
		if err := json.Unmarshal([]byte(line), &suggestion); err == nil && suggestion.Name != "" {
			suggestion.Name = normalizeCodexTitle(suggestion.Name)
			if suggestion.Name != "" {
				if suggestion.BranchType == "" {
					suggestion.BranchType = "feat"
				}
				return suggestion, true
			}
		}
	}
	return core.TaskSuggestion{}, false
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
		ReadyMarker:  r.PromptMarker(),
		PrefillInput: initialInput,
	}, nil
}

func (r *Repository) RestoreTaskSessionLaunchSpec(
	_ *core.Task,
	hookSession *core.HookSessionSummary,
) (core.TaskSessionLaunchSpec, error) {
	command := []string{r.binary, "resume"}
	if hookSession != nil && strings.TrimSpace(hookSession.SessionID) != "" {
		command = append(command, strings.TrimSpace(hookSession.SessionID))
	}

	return core.TaskSessionLaunchSpec{
		Command:     command,
		ReadyMarker: r.PromptMarker(),
	}, nil
}

func (r *Repository) DetectRuntimeState(snapshot core.RuntimeSnapshot) core.RuntimeState {
	return detectRuntimeState(snapshot)
}

func (r *Repository) PromptMarker() string {
	return "›"
}

func (r *Repository) renderHooksJSON() ([]byte, error) {
	config := hookConfig{
		Hooks: map[string][]hookRule{
			"SessionStart": {
				{
					Matcher: "startup|resume",
					Hooks:   []hookCommand{{Type: "command", Command: r.commandForEvent("SessionStart")}},
				},
			},
			"PreToolUse": {
				{Matcher: "Bash", Hooks: []hookCommand{{Type: "command", Command: r.commandForEvent("PreToolUse")}}},
			},
			"PostToolUse": {
				{Matcher: "Bash", Hooks: []hookCommand{{Type: "command", Command: r.commandForEvent("PostToolUse")}}},
			},
			"UserPromptSubmit": {
				{Hooks: []hookCommand{{Type: "command", Command: r.commandForEvent("UserPromptSubmit")}}},
			},
			"Stop": {
				{Hooks: []hookCommand{{Type: "command", Command: r.commandForEvent("Stop")}}},
			},
		},
	}

	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(config); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (r *Repository) commandForEvent(eventName string) string {
	eventName = strings.TrimSpace(eventName)

	cmd := `repo_root=$(git rev-parse --show-toplevel 2>/dev/null) || exit 0;` +
		` exec /bin/sh "$repo_root/.codex/hooks/forward-to-rig.sh" ` + shellQuote(eventName)
	return "/bin/sh -c " + shellQuote(cmd)
}

func (r *Repository) renderForwarderScript() ([]byte, error) {
	var buf bytes.Buffer
	if err := forwarderScriptTemplate.Execute(&buf, struct {
		AgentExecQuoted  string
		SourceRootQuoted string
	}{
		AgentExecQuoted:  shellQuote(r.rigBinaryPath),
		SourceRootQuoted: shellQuote(r.sourceRoot),
	}); err != nil {
		return nil, fmt.Errorf("render codex forwarder script: %w", err)
	}

	return buf.Bytes(), nil
}

type hookConfig struct {
	Hooks map[string][]hookRule `json:"hooks"`
}

type hookRule struct {
	Matcher string        `json:"matcher,omitempty"`
	Hooks   []hookCommand `json:"hooks"`
}

type hookCommand struct {
	Type    string `json:"type"`
	Command string `json:"command"`
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

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}

	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
