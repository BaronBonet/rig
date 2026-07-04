// Package claude implements Rig's provider boundary for the Claude Code CLI.
//
// Hook registration is workspace-scoped: BuildWorkspaceBootstrapSpec writes an
// untracked .claude/settings.local.json into the task worktree, so Rig hooks
// fire only inside Rig task workspaces. The file is written into every Rig
// task workspace regardless of the task's active provider, so a manually
// launched Claude session in any Rig task is observable and adoptable; when
// the file already exists (Claude Code stores permission decisions there) Rig
// merges its hook rules in and preserves the rest. Rig never modifies
// user-level Claude settings (~/.claude/settings.json); only the shared
// forward-to-rig script, which does not trigger by itself, is installed at
// user level.
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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BaronBonet/rig/internal/adapters/client/providerkit"
	"github.com/BaronBonet/rig/internal/core"
	"github.com/BaronBonet/rig/internal/pkg/prompts"
	"github.com/BaronBonet/rig/internal/pkg/subprocess"
)

const (
	readyMarker           = "❯"
	claudeHookPath        = "/claude-hook"
	defaultClaudeHooksURL = "http://127.0.0.1:4124" + claudeHookPath
	workspaceSettingsPath = ".claude/settings.local.json"
)

// hookCatalog is Claude's hook event catalog: the one declaration of which
// hook events Rig observes from Claude, how each is matched, and which
// runtime phase it drives. Registration rules and the hook-to-status mapping
// are derived from it.
//
// Tool hooks are unmatched on purpose: most of Claude's work uses non-Bash
// tools (Read, Edit, Grep, ...), and every tool event must drive the task's
// working status.
var hookCatalog = providerkit.Catalog{
	{Event: core.HookEventSessionStart, Matcher: "startup|resume", Phase: core.TaskStatusPhaseStarting},
	{Event: core.HookEventUserPromptSubmit, Phase: core.TaskStatusPhaseWorking},
	{Event: core.HookEventPreToolUse, Phase: core.TaskStatusPhaseWorking},
	{Event: core.HookEventPostToolUse, Phase: core.TaskStatusPhaseWorking},
	{Event: core.HookEventNotification, Phase: core.TaskStatusPhaseWaitingForInput},
	{Event: core.HookEventStop, Phase: core.TaskStatusPhaseWaitingForInput},
}

// titleSkipPrefixes rejects Claude-specific CLI noise when parsing task
// title suggestions (common CLI noise is rejected by providerkit).
var titleSkipPrefixes = []string(nil)

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
	if suggestion, ok := providerkit.ParseSuggestion(result.Stdout, titleSkipPrefixes); ok {
		return suggestion, nil
	}
	if err != nil {
		return core.TaskSuggestion{}, fmt.Errorf("claude print mode failed: %w", err)
	}
	if title := providerkit.ExtractTitle(result.Stdout, titleSkipPrefixes); title != "" {
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

	return r.forwarder().WriteScript(scriptPath)
}

func (r *repository) forwarder() providerkit.Forwarder {
	return providerkit.Forwarder{
		ProviderLabel: "claude",
		EventHeader:   hookEventHeader,
		CollectorURL:  r.collectorURL,
		HookSecret:    r.hookSecret,
	}
}

// BuildWorkspaceBootstrapSpec emits the workspace-scoped Claude settings that
// register Rig's hooks inside the task worktree. The file is untracked, so it
// never dirties the task branch or appears in diffs and PRs. When the
// workspace already has settings — Claude Code stores permission decisions in
// the same file — Rig merges its hook rules in and preserves everything else.
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
				Merge: func(existing []byte) ([]byte, error) {
					return mergeWorkspaceHookSettings(existing, scriptPath)
				},
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

	return providerkit.HealthCheckScript(scriptPath, r.collectorURL)
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

// renderWorkspaceHookSettings renders the workspace-level Claude settings
// that register Rig's hook forwarding for the events in the hook catalog.
func renderWorkspaceHookSettings(scriptPath string) ([]byte, error) {
	settings, err := hookCatalog.RenderHookConfig(hookCommandRenderer(scriptPath))
	if err != nil {
		return nil, fmt.Errorf("encode claude workspace hook settings: %w", err)
	}

	return settings, nil
}

func hookCommandRenderer(scriptPath string) func(eventName string) string {
	return func(eventName string) string {
		return "/bin/sh " + providerkit.ShellQuote(scriptPath) + " " + providerkit.ShellQuote(eventName)
	}
}

// mergeWorkspaceHookSettings integrates Rig's hook rules into an existing
// workspace settings file, replacing only rules that invoke Rig's forwarder
// script and preserving every other key Claude Code stores there, such as
// permission decisions.
func mergeWorkspaceHookSettings(existing []byte, scriptPath string) ([]byte, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(existing, &doc); err != nil {
		return nil, fmt.Errorf("decode existing claude workspace settings: %w", err)
	}
	if doc == nil {
		doc = map[string]json.RawMessage{}
	}

	var hooks map[string][]providerkit.HookRule
	if rawHooks, ok := doc["hooks"]; ok {
		if err := json.Unmarshal(rawHooks, &hooks); err != nil {
			return nil, fmt.Errorf("decode existing claude workspace hooks: %w", err)
		}
	}

	merged := providerkit.MergeRigHookRules(hooks, hookCatalog.HookRules(hookCommandRenderer(scriptPath)), scriptPath)
	encodedHooks, err := json.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("encode merged claude workspace hooks: %w", err)
	}
	doc["hooks"] = encodedHooks

	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(doc); err != nil {
		return nil, fmt.Errorf("encode claude workspace settings: %w", err)
	}

	return buf.Bytes(), nil
}
