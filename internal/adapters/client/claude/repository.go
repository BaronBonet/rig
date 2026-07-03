// Package claude implements Rig's provider boundary for the Claude Code CLI.
//
// Hook registration is workspace-scoped: BuildWorkspaceBootstrapSpec writes an
// untracked .claude/settings.local.json into the task worktree, so Rig hooks
// fire only inside Rig task workspaces. Rig never modifies user-level Claude
// settings (~/.claude/settings.json); only the shared forward-to-rig script,
// which does not trigger by itself, is installed at user level.
//
// Running/idle detection was verified against a real Claude CLI (v2.1.200) in
// tmux: pane_current_command reports the Claude process title, which is the
// version string (for example "2.1.200"), not "claude" or "node". The process
// comm name is "claude", so the tmux adapter also reports pane child process
// names and TaskSessionCommandName matches those.
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
	"unicode"

	"github.com/BaronBonet/rig/internal/core"
	"github.com/BaronBonet/rig/internal/pkg/prompts"
	"github.com/BaronBonet/rig/internal/pkg/subprocess"
)

//go:embed forward-to-rig.sh.tmpl
var forwarderScriptTemplateText string

var forwarderScriptTemplate = template.Must(template.New("forward-to-rig.sh").Parse(forwarderScriptTemplateText))

const (
	readyMarker           = "❯"
	claudeHookPath        = "/claude-hook"
	defaultClaudeHooksURL = "http://127.0.0.1:4124" + claudeHookPath
	workspaceSettingsPath = ".claude/settings.local.json"
)

type repository struct {
	runner       subprocess.Runner
	rigDataDir   func() (string, error)
	binary       string
	collectorURL string
	hookSecret   string
}

func New(runner subprocess.Runner, cfg Config, hooks HookForwardingConfig) core.ProviderClient {
	collectorURL := strings.TrimSpace(hooks.CollectorURL)
	if collectorURL == "" {
		collectorURL = defaultClaudeHooksURL
	}

	return &repository{
		runner:       runner,
		binary:       cfg.Binary,
		collectorURL: collectorURL,
		hookSecret:   strings.TrimSpace(hooks.HookSecret),
		rigDataDir:   defaultRigDataDir,
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

	result, err := r.runner.Run(
		ctx,
		"",
		r.binary,
		"-p",
		"--output-format",
		"text",
		fullPrompt,
	)
	if suggestion, ok := parseClaudeSuggestion(result.Stdout); ok {
		return suggestion, nil
	}
	if err != nil {
		return core.TaskSuggestion{}, fmt.Errorf("claude print mode failed: %w", err)
	}
	if title := extractClaudeTitle(result.Stdout); title != "" {
		return core.TaskSuggestion{Name: title, BranchType: "feat"}, nil
	}

	return core.TaskSuggestion{}, fmt.Errorf("claude did not return a usable task title")
}

// EnsureTaskSessionEnvironment installs or repairs the shared forward-to-rig
// script. The script does not trigger by itself: hook registration is written
// per task workspace by BuildWorkspaceBootstrapSpec, so Claude sessions
// outside Rig workspaces never report to Rig.
func (r *repository) EnsureTaskSessionEnvironment(context.Context) error {
	scriptPath, err := r.forwarderScriptPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o700); err != nil {
		return fmt.Errorf("create rig claude hooks dir: %w", err)
	}
	if err := os.Chmod(filepath.Dir(scriptPath), 0o700); err != nil {
		return fmt.Errorf("secure rig claude hooks dir: %w", err)
	}

	scriptBytes, err := r.renderForwarderScript()
	if err != nil {
		return err
	}
	if err := os.WriteFile(scriptPath, scriptBytes, 0o700); err != nil {
		return fmt.Errorf("write claude forwarder script: %w", err)
	}

	return nil
}

// BuildWorkspaceBootstrapSpec emits the workspace-scoped Claude settings that
// register Rig's hooks inside the task worktree. The file is untracked, so it
// never dirties the task branch or appears in diffs and PRs.
func (r *repository) BuildWorkspaceBootstrapSpec(_ *core.Task) (core.WorkspaceBootstrapSpec, error) {
	scriptPath, err := r.forwarderScriptPath()
	if err != nil {
		return core.WorkspaceBootstrapSpec{}, err
	}

	settings, err := renderWorkspaceHookSettings(scriptPath)
	if err != nil {
		return core.WorkspaceBootstrapSpec{}, err
	}

	return core.WorkspaceBootstrapSpec{
		Files: []core.WorkspaceBootstrapFile{
			{
				Path:     workspaceSettingsPath,
				Content:  settings,
				FileMode: 0o600,
			},
		},
	}, nil
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

// RecoverLatestTaskStatus returns no recovery: Claude status is driven by
// hook events only in this first version.
func (r *repository) RecoverLatestTaskStatus(
	context.Context,
	core.TaskStatusUpdate,
	[]core.TaskProviderSession,
) (*core.TaskStatusUpdate, error) {
	return nil, nil
}

func (r *repository) healthCheckHookForwarding() error {
	scriptPath, err := r.forwarderScriptPath()
	if err != nil {
		return err
	}

	scriptInfo, err := os.Stat(scriptPath)
	if err != nil {
		return fmt.Errorf("stat %s: %w", scriptPath, err)
	}
	if scriptInfo.IsDir() {
		return fmt.Errorf("%s must be a file", scriptPath)
	}
	if scriptInfo.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("%s must be executable", scriptPath)
	}
	scriptBytes, err := os.ReadFile(scriptPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", scriptPath, err)
	}
	if !strings.Contains(string(scriptBytes), r.collectorURL) {
		return fmt.Errorf("%s collector URL must include %s", scriptPath, r.collectorURL)
	}

	return nil
}

func (r *repository) forwarderScriptPath() (string, error) {
	dataDir, err := r.resolveRigDataDir()
	if err != nil {
		return "", err
	}
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return "", fmt.Errorf("rig data dir is required")
	}

	return filepath.Join(dataDir, "claude", "hooks", "forward-to-rig.sh"), nil
}

func (r *repository) resolveRigDataDir() (string, error) {
	if r.rigDataDir == nil {
		return defaultRigDataDir()
	}
	return r.rigDataDir()
}

func defaultRigDataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve rig data dir: %w", err)
	}

	return filepath.Join(home, ".local", "share", "rig"), nil
}

func (r *repository) renderForwarderScript() ([]byte, error) {
	var buf bytes.Buffer
	if err := forwarderScriptTemplate.Execute(&buf, struct {
		CollectorURLQuoted string
		HookSecretQuoted   string
	}{
		CollectorURLQuoted: shellQuote(r.collectorURL),
		HookSecretQuoted:   shellQuote(r.hookSecret),
	}); err != nil {
		return nil, fmt.Errorf("render claude forwarder script: %w", err)
	}

	return buf.Bytes(), nil
}

// renderWorkspaceHookSettings renders the workspace-level Claude settings
// that register Rig's hook forwarding for the events Rig observes.
func renderWorkspaceHookSettings(scriptPath string) ([]byte, error) {
	command := func(eventName string) string {
		return "/bin/sh " + shellQuote(scriptPath) + " " + shellQuote(eventName)
	}

	settings := workspaceSettings{
		Hooks: map[string][]hookRule{
			"SessionStart": {
				{
					Matcher: "startup|resume",
					Hooks:   []hookCommand{{Type: "command", Command: command("SessionStart")}},
				},
			},
			"UserPromptSubmit": {
				{Hooks: []hookCommand{{Type: "command", Command: command("UserPromptSubmit")}}},
			},
			// Tool hooks are unmatched on purpose: most of Claude's work uses
			// non-Bash tools (Read, Edit, Grep, ...), and every tool event must
			// drive the task's working status.
			"PreToolUse": {
				{Hooks: []hookCommand{{Type: "command", Command: command("PreToolUse")}}},
			},
			"PostToolUse": {
				{Hooks: []hookCommand{{Type: "command", Command: command("PostToolUse")}}},
			},
			"Notification": {
				{Hooks: []hookCommand{{Type: "command", Command: command("Notification")}}},
			},
			"Stop": {
				{Hooks: []hookCommand{{Type: "command", Command: command("Stop")}}},
			},
		},
	}

	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(settings); err != nil {
		return nil, fmt.Errorf("encode claude workspace hook settings: %w", err)
	}

	return buf.Bytes(), nil
}

type workspaceSettings struct {
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

func parseClaudeSuggestion(raw string) (core.TaskSuggestion, bool) {
	lines := strings.Split(raw, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var suggestion core.TaskSuggestion
		if err := json.Unmarshal([]byte(line), &suggestion); err == nil && suggestion.Name != "" {
			suggestion.Name = normalizeClaudeTitle(suggestion.Name)
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

func extractClaudeTitle(raw string) string {
	lines := strings.Split(raw, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if title := normalizeClaudeTitle(lines[i]); title != "" {
			return title
		}
	}

	return ""
}

func normalizeClaudeTitle(raw string) string {
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
