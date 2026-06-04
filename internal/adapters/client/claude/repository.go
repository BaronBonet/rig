package claude

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

	"github.com/BaronBonet/rig/internal/core"
	"github.com/BaronBonet/rig/internal/pkg/prompts"
	"github.com/BaronBonet/rig/internal/pkg/subprocess"
)

//go:embed forward-to-rig.sh.tmpl
var forwarderScriptTemplateText string

var forwarderScriptTemplate = template.Must(template.New("forward-to-rig.sh").Parse(forwarderScriptTemplateText))

const (
	readyMarker            = ">"
	claudeHookPath         = "/claude-hook"
	defaultClaudeHooksURL  = "http://127.0.0.1:4124" + claudeHookPath
	settingsFilePermission = 0o600
)

var requiredRigHookEvents = []string{
	"SessionStart",
	"UserPromptSubmit",
	"Stop",
	"PreToolUse",
	"PostToolUse",
}

type repository struct {
	runner        subprocess.Runner
	claudeHomeDir func() (string, error)
	binary        string
	collectorURL  string
	hookSecret    string
}

func New(runner subprocess.Runner, cfg Config, hooks HookForwardingConfig) core.ProviderClient {
	collectorURL := strings.TrimSpace(hooks.CollectorURL)
	if collectorURL == "" {
		collectorURL = defaultClaudeHooksURL
	}

	return &repository{
		runner:        runner,
		binary:        cfg.Binary,
		collectorURL:  collectorURL,
		hookSecret:    strings.TrimSpace(hooks.HookSecret),
		claudeHomeDir: defaultClaudeHomeDir,
	}
}

func NewHookForwardingConfig(hookListenAddr string, hookSecret string) HookForwardingConfig {
	collectorURL := strings.TrimSpace(hookListenAddr)
	if collectorURL != "" &&
		!strings.HasPrefix(collectorURL, "http://") &&
		!strings.HasPrefix(collectorURL, "https://") {
		collectorURL = "http://" + collectorURL + claudeHookPath
	}

	return HookForwardingConfig{
		CollectorURL: collectorURL,
		HookSecret:   strings.TrimSpace(hookSecret),
	}
}

func (r *repository) Doctor(ctx context.Context) error {
	if _, err := r.runner.Run(ctx, "", r.binary, "--version"); err != nil {
		return err
	}

	if err := r.healthCheckHookForwarding(); err != nil {
		return fmt.Errorf("claude rig hook forwarding: %w", err)
	}

	return nil
}

func (r *repository) SuggestTaskName(ctx context.Context, prompt string) (core.TaskSuggestion, error) {
	fullPrompt := prompts.SuggestTaskPrompt + "\n\nTask description: " + prompt
	result, err := r.runner.Run(ctx, "", r.binary, "-p", fullPrompt)
	if err != nil {
		return core.TaskSuggestion{}, fmt.Errorf("claude prompt failed: %w", err)
	}

	if suggestion, ok := parseClaudeSuggestion(result.Stdout); ok {
		return suggestion, nil
	}

	return core.TaskSuggestion{}, fmt.Errorf("claude did not return a usable task title")
}

func (r *repository) EnsureTaskSessionEnvironment(context.Context) error {
	claudeHome, err := r.resolveClaudeHomeDir()
	if err != nil {
		return err
	}

	scriptPath := filepath.Join(claudeHome, "hooks", "forward-to-rig.sh")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o700); err != nil {
		return fmt.Errorf("create claude hooks dir: %w", err)
	}
	if err := os.Chmod(filepath.Dir(scriptPath), 0o700); err != nil {
		return fmt.Errorf("secure claude hooks dir: %w", err)
	}

	scriptBytes, err := r.renderForwarderScript()
	if err != nil {
		return err
	}
	if err := os.WriteFile(scriptPath, scriptBytes, 0o700); err != nil {
		return fmt.Errorf("write claude forwarder script: %w", err)
	}

	settingsPath := filepath.Join(claudeHome, "settings.json")
	cfg, err := r.loadHookConfig(settingsPath)
	if err != nil {
		return err
	}

	if err := r.ensureRigHookRules(&cfg, scriptPath); err != nil {
		return err
	}

	settingsJSON, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal claude settings: %w", err)
	}
	settingsJSON = append(settingsJSON, '\n')
	if err := os.WriteFile(settingsPath, settingsJSON, settingsFilePermission); err != nil {
		return fmt.Errorf("write claude settings: %w", err)
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
		Command:     []string{r.binary, "--resume", sessionID},
		ReadyMarker: readyMarker,
	}, nil
}

func (r *repository) TaskSessionCommandName() string {
	commandName := filepath.Base(strings.TrimSpace(r.binary))
	if commandName == "." {
		return "claude"
	}
	return commandName
}

func parseClaudeSuggestion(raw string) (core.TaskSuggestion, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return core.TaskSuggestion{}, false
	}

	var suggestion core.TaskSuggestion
	if err := json.Unmarshal([]byte(raw), &suggestion); err == nil {
		suggestion.Name = normalizeClaudeTitle(suggestion.Name)
		return suggestion, suggestion.Name != ""
	}

	lines := strings.Split(raw, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if title := normalizeClaudeTitle(lines[i]); title != "" {
			return core.TaskSuggestion{Name: title, BranchType: "feat"}, true
		}
	}

	return core.TaskSuggestion{}, false
}

func normalizeClaudeTitle(raw string) string {
	title := strings.TrimSpace(raw)
	title = strings.Trim(title, "`\"' ")
	title = strings.TrimPrefix(title, "- ")
	return strings.TrimSpace(title)
}

func (r *repository) renderForwarderScript() ([]byte, error) {
	var rendered bytes.Buffer
	data := struct {
		CollectorURLQuoted string
		HookSecretQuoted   string
	}{
		CollectorURLQuoted: shellQuote(r.collectorURL),
		HookSecretQuoted:   shellQuote(r.hookSecret),
	}
	if err := forwarderScriptTemplate.Execute(&rendered, data); err != nil {
		return nil, fmt.Errorf("render claude forwarder script: %w", err)
	}

	return rendered.Bytes(), nil
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

func (r *repository) loadHookConfig(path string) (hookConfig, error) {
	fileBytes, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return hookConfig{Hooks: map[string][]hookRule{}}, nil
	}
	if err != nil {
		return hookConfig{}, fmt.Errorf("read claude settings: %w", err)
	}

	var cfg hookConfig
	if err := json.Unmarshal(fileBytes, &cfg); err != nil {
		return hookConfig{}, fmt.Errorf("decode claude settings: %w", err)
	}
	if cfg.Hooks == nil {
		cfg.Hooks = map[string][]hookRule{}
	}
	return cfg, nil
}

func (r *repository) healthCheckHookForwarding() error {
	claudeHome, err := r.resolveClaudeHomeDir()
	if err != nil {
		return err
	}
	claudeHome = strings.TrimSpace(claudeHome)
	if claudeHome == "" {
		return fmt.Errorf("claude home is required")
	}

	scriptPath := filepath.Join(claudeHome, "hooks", "forward-to-rig.sh")
	scriptBytes, err := os.ReadFile(scriptPath)
	if err != nil {
		return fmt.Errorf("%s: %w", scriptPath, err)
	}
	if !strings.Contains(string(scriptBytes), r.collectorURL) {
		return fmt.Errorf("forwarder script collector URL must include %s", r.collectorURL)
	}

	cfg, err := r.loadHookConfig(filepath.Join(claudeHome, "settings.json"))
	if err != nil {
		return err
	}

	for _, eventName := range requiredRigHookEvents {
		if !hasHookCommandForScript(cfg.Hooks[eventName], scriptPath, eventName) {
			return fmt.Errorf("missing %s hook for %s", eventName, scriptPath)
		}
	}

	return nil
}

func (r *repository) ensureRigHookRules(cfg *hookConfig, scriptPath string) error {
	if cfg.Hooks == nil {
		cfg.Hooks = map[string][]hookRule{}
	}

	for _, eventName := range requiredRigHookEvents {
		cfg.Hooks[eventName] = hookRulesWithoutScriptCommand(cfg.Hooks[eventName], scriptPath)
		cfg.Hooks[eventName] = append(cfg.Hooks[eventName], hookRule{
			Hooks: []hookCommand{{Type: "command", Command: r.commandForEvent(scriptPath, eventName)}},
		})
	}

	return nil
}

func (r *repository) commandForEvent(scriptPath string, eventName string) string {
	return shellQuote(scriptPath) + " " + shellQuote(eventName)
}

func hasHookCommandForScript(rules []hookRule, scriptPath string, eventName string) bool {
	for _, rule := range rules {
		for _, hook := range rule.Hooks {
			if hook.Type == "command" &&
				strings.Contains(hook.Command, scriptPath) &&
				strings.Contains(hook.Command, eventName) {
				return true
			}
		}
	}
	return false
}

func hookRulesWithoutScriptCommand(rules []hookRule, scriptPath string) []hookRule {
	filtered := make([]hookRule, 0, len(rules))
	for _, rule := range rules {
		keepHooks := make([]hookCommand, 0, len(rule.Hooks))
		for _, hook := range rule.Hooks {
			if hook.Type == "command" && strings.Contains(hook.Command, scriptPath) {
				continue
			}
			keepHooks = append(keepHooks, hook)
		}
		if len(keepHooks) == 0 {
			continue
		}
		rule.Hooks = keepHooks
		filtered = append(filtered, rule)
	}
	return filtered
}

func (r *repository) resolveClaudeHomeDir() (string, error) {
	if r.claudeHomeDir == nil {
		return defaultClaudeHomeDir()
	}
	return r.claudeHomeDir()
}

func defaultClaudeHomeDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		if err == nil {
			err = fmt.Errorf("home directory is empty")
		}
		return "", fmt.Errorf("resolve claude home: %w", err)
	}

	return filepath.Join(home, ".claude"), nil
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
