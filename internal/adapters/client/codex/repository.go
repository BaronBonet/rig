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
	"rig/internal/pkg/prompts"
	"rig/internal/pkg/subprocess"
)

//go:embed forward-to-rig.sh.tmpl
var forwarderScriptTemplateText string

var forwarderScriptTemplate = template.Must(template.New("forward-to-rig.sh").Parse(forwarderScriptTemplateText))

const (
	readyMarker          = "›"
	defaultCodexHooksURL = "http://127.0.0.1:4124/codex-hook"
)

type repository struct {
	runner       subprocess.Runner
	codexHomeDir func() (string, error)
	binary       string
	collectorURL string
}

func New(runner subprocess.Runner, cfg Config, hooks HookForwardingConfig) core.ProviderClient {
	collectorURL := strings.TrimSpace(hooks.CollectorURL)
	if collectorURL == "" {
		collectorURL = defaultCodexHooksURL
	}

	return &repository{
		runner:       runner,
		binary:       cfg.Binary,
		collectorURL: collectorURL,
		codexHomeDir: defaultCodexHomeDir,
	}
}

func (r *repository) SuggestTaskName(ctx context.Context, prompt string) (core.TaskSuggestion, error) {
	tmpFile, err := os.CreateTemp("", "rig-codex-name-*.txt")
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

	fileOutput := readOutputFile(tmpPath)
	if suggestion, ok := parsePreferredSuggestion(fileOutput, result.Stdout); ok {
		return suggestion, nil
	}
	if err != nil {
		return core.TaskSuggestion{}, fmt.Errorf("codex exec failed: %w", err)
	}

	return core.TaskSuggestion{}, fmt.Errorf("codex did not return a usable task title")
}

func (r *repository) EnsureTaskSessionEnvironment(context.Context) error {
	codexHome, err := r.resolveCodexHomeDir()
	if err != nil {
		return err
	}

	scriptPath := filepath.Join(codexHome, "hooks", "forward-to-rig.sh")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o755); err != nil {
		return fmt.Errorf("create codex hooks dir: %w", err)
	}

	scriptBytes, err := r.renderForwarderScript()
	if err != nil {
		return err
	}
	if err := os.WriteFile(scriptPath, scriptBytes, 0o755); err != nil {
		return fmt.Errorf("write codex forwarder script: %w", err)
	}

	hooksPath := filepath.Join(codexHome, "hooks.json")
	cfg, err := r.loadHookConfig(hooksPath)
	if err != nil {
		return err
	}

	if err := r.ensureRigHookRules(&cfg, scriptPath); err != nil {
		return err
	}

	hooksJSON, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal codex hooks config: %w", err)
	}
	hooksJSON = append(hooksJSON, '\n')
	if err := os.WriteFile(hooksPath, hooksJSON, 0o644); err != nil {
		return fmt.Errorf("write codex hooks config: %w", err)
	}

	return nil
}

func (r *repository) BuildWorkspaceBootstrapSpec(_ *core.Task) (core.WorkspaceBootstrapSpec, error) {
	return core.WorkspaceBootstrapSpec{}, nil
}

func (r *repository) BuildTaskSessionLaunchSpec(task *core.Task) (core.TaskSessionLaunchSpec, error) {
	var prefillInput []string
	if strings.TrimSpace(task.Prompt) != "" {
		prefillInput = []string{task.Prompt}
	}

	return core.TaskSessionLaunchSpec{
		Command:      []string{r.binary},
		ReadyMarker:  readyMarker,
		PrefillInput: prefillInput,
	}, nil
}

func (r *repository) BuildReconnectTaskSessionLaunchSpec(
	_ *core.Task,
	sessionID string,
) (core.TaskSessionLaunchSpec, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return core.TaskSessionLaunchSpec{}, fmt.Errorf("session ID is required")
	}

	return core.TaskSessionLaunchSpec{
		Command:     []string{r.binary, "resume", sessionID},
		ReadyMarker: readyMarker,
	}, nil
}

func (r *repository) TaskSessionCommandName() string {
	commandName := filepath.Base(strings.TrimSpace(r.binary))
	if commandName == "." {
		return "codex"
	}
	return commandName
}

func readOutputFile(path string) string {
	fileBytes, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	return string(fileBytes)
}

func parsePreferredSuggestion(fileOutput, stdout string) (core.TaskSuggestion, bool) {
	for _, candidate := range []string{fileOutput, stdout} {
		if suggestion, ok := parseCodexSuggestion(candidate); ok {
			return suggestion, true
		}
		if title := extractCodexTitle(candidate); title != "" {
			return core.TaskSuggestion{Name: title, BranchType: "feat"}, true
		}
	}

	return core.TaskSuggestion{}, false
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

// renderHooksJSON writes the Codex hook config file that tells Codex which
// events should call back into rig's observer ingestion flow.
func (r *repository) renderHooksJSON(scriptPath string) ([]byte, error) {
	config := hookConfig{
		Hooks: map[string][]hookRule{
			"SessionStart": {
				{
					Matcher: "startup|resume",
					Hooks:   []hookCommand{{Type: "command", Command: r.commandForEvent(scriptPath, "SessionStart")}},
				},
			},
			"PreToolUse": {
				{
					Matcher: "Bash",
					Hooks:   []hookCommand{{Type: "command", Command: r.commandForEvent(scriptPath, "PreToolUse")}},
				},
			},
			"PermissionRequest": {
				{
					Matcher: "Bash",
					Hooks: []hookCommand{
						{Type: "command", Command: r.commandForEvent(scriptPath, "PermissionRequest")},
					},
				},
			},
			"PostToolUse": {
				{
					Matcher: "Bash",
					Hooks:   []hookCommand{{Type: "command", Command: r.commandForEvent(scriptPath, "PostToolUse")}},
				},
			},
			"UserPromptSubmit": {
				{Hooks: []hookCommand{{Type: "command", Command: r.commandForEvent(scriptPath, "UserPromptSubmit")}}},
			},
			"Stop": {
				{Hooks: []hookCommand{{Type: "command", Command: r.commandForEvent(scriptPath, "Stop")}}},
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

func (r *repository) commandForEvent(scriptPath string, eventName string) string {
	return "/bin/sh " + shellQuote(scriptPath) + " " + shellQuote(strings.TrimSpace(eventName))
}

func (r *repository) loadHookConfig(path string) (hookConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return hookConfig{Hooks: map[string][]hookRule{}}, nil
		}
		return hookConfig{}, fmt.Errorf("read codex hooks config: %w", err)
	}

	var cfg hookConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return hookConfig{}, fmt.Errorf("decode codex hooks config: %w", err)
	}
	if cfg.Hooks == nil {
		cfg.Hooks = map[string][]hookRule{}
	}

	return cfg, nil
}

func (r *repository) ensureRigHookRules(cfg *hookConfig, scriptPath string) error {
	if cfg == nil {
		return nil
	}

	rigJSON, err := r.renderHooksJSON(scriptPath)
	if err != nil {
		return fmt.Errorf("render rig codex hooks config: %w", err)
	}

	var rigCfg hookConfig
	if err := json.Unmarshal(rigJSON, &rigCfg); err != nil {
		return fmt.Errorf("decode rig codex hooks config: %w", err)
	}

	for eventName, rules := range rigCfg.Hooks {
		for _, rule := range rules {
			if !containsHookRule(cfg.Hooks[eventName], rule) {
				cfg.Hooks[eventName] = append(cfg.Hooks[eventName], rule)
			}
		}
	}

	return nil
}

func containsHookRule(existing []hookRule, candidate hookRule) bool {
	for _, rule := range existing {
		if rule.Matcher != candidate.Matcher || len(rule.Hooks) != len(candidate.Hooks) {
			continue
		}

		matches := true
		for idx := range rule.Hooks {
			if rule.Hooks[idx] != candidate.Hooks[idx] {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}

	return false
}

func (r *repository) resolveCodexHomeDir() (string, error) {
	if r.codexHomeDir == nil {
		return defaultCodexHomeDir()
	}
	return r.codexHomeDir()
}

func defaultCodexHomeDir() (string, error) {
	if custom := strings.TrimSpace(os.Getenv("CODEX_HOME")); custom != "" {
		return custom, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve codex home: %w", err)
	}

	return filepath.Join(home, ".codex"), nil
}

func (r *repository) renderForwarderScript() ([]byte, error) {
	var buf bytes.Buffer
	if err := forwarderScriptTemplate.Execute(&buf, struct {
		CollectorURLQuoted string
	}{
		CollectorURLQuoted: shellQuote(r.collectorURL),
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
