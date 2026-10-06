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
	"time"

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
	// configDirEnvVar points Claude Code at a configuration directory other
	// than ~/.claude, with its own login and sessions.
	configDirEnvVar = "CLAUDE_CONFIG_DIR"
)

// ConfigEnvVars are the variables that choose which Claude Code
// configuration, and so which account and sessions, a session uses.
var ConfigEnvVars = []string{configDirEnvVar}

// hookCatalog is Claude's hook event catalog: the one declaration of which
// hook events Rig observes from Claude, how each is matched, and which
// runtime phase it drives. Registration rules and the hook-to-status mapping
// are derived from it.
//
// Tool hooks are unmatched on purpose: most of Claude's work uses non-Bash
// tools (Read, Edit, Grep, ...), and every tool event must drive the task's
// working status.
//
// Stop drives needs-input unless its payload reports background work still in
// flight; HookEventToTaskStatus then reports the task as working in the
// background instead.
var hookCatalog = providerkit.Catalog{
	{Event: core.HookEventSessionStart, Matcher: "startup|resume", Phase: core.TaskStatusPhaseStarting},
	{Event: core.HookEventUserPromptSubmit, Phase: core.TaskStatusPhaseWorking},
	{Event: core.HookEventPreToolUse, Phase: core.TaskStatusPhaseWorking},
	{Event: core.HookEventPostToolUse, Phase: core.TaskStatusPhaseWorking},
	{
		Event:   core.HookEventNotification,
		Matcher: strings.Join(needsInputNotificationTypes, "|"),
		Phase:   core.TaskStatusPhaseWaitingForInput,
	},
	{Event: core.HookEventStop, Phase: core.TaskStatusPhaseWaitingForInput},
}

// needsInputNotificationTypes are the Claude notification types that ask the
// user to act. Other notifications say nothing new about the task: notably
// idle_prompt, which fires a minute after every turn end even while
// background work is still running.
var needsInputNotificationTypes = []string{
	"permission_prompt",
	"worker_permission_prompt",
	"elicitation_dialog",
	"elicitation_url_dialog",
	"agent_needs_input",
}

// titleSkipPrefixes rejects Claude-specific CLI noise when parsing task
// title suggestions (common CLI noise is rejected by providerkit).
var titleSkipPrefixes = []string(nil)

// launchOptions are the model aliases and effort levels `claude --model` and
// `claude --effort` accept, in display order. An alias always means the
// latest model of that family, so the list does not age with releases.
var launchOptions = core.ProviderLaunchOptions{
	Models:  []string{"fable", "opus", "sonnet", "haiku"},
	Efforts: []string{"low", "medium", "high", "xhigh", "max"},
	Handoff: true,
}

type repository struct {
	runner       subprocess.Runner
	rigDataDir   func() (string, error)
	binary       string
	collectorURL string
	hookSecret   string
	fileChanges  transcriptEditCache
	// claudeConfigDir overrides where Claude Code keeps its sessions; nil
	// means the task's CLAUDE_CONFIG_DIR, the daemon's, or ~/.claude.
	claudeConfigDir func() (string, error)
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

func (r *repository) SuggestTaskName(
	ctx context.Context,
	prompt string,
	env core.ProviderEnv,
) (core.TaskSuggestion, error) {
	fullPrompt := prompts.SuggestTaskPrompt + "\n\nTask description: " + prompt

	result, err := r.runner.RunWithStdin(ctx, subprocess.RunWithStdinOptions{
		Env:  providerkit.EnvOverrides(env, ConfigEnvVars),
		Name: r.binary,
		Args: []string{"-p", "--output-format", "text", fullPrompt},
	})
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
func (r *repository) EnsureTaskSessionEnvironment(context.Context, core.ProviderEnv) error {
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

func (r *repository) LaunchOptions() core.ProviderLaunchOptions {
	return core.ProviderLaunchOptions{
		Models:  append([]string(nil), launchOptions.Models...),
		Efforts: append([]string(nil), launchOptions.Efforts...),
		Handoff: launchOptions.Handoff,
	}
}

// launchArgs are the flags a task's launch options add to every claude
// launch, fresh or resumed.
func launchArgs(task *core.Task) []string {
	var args []string
	options := task.Launch()
	if options.Model != "" {
		args = append(args, "--model", options.Model)
	}
	if options.Effort != "" {
		args = append(args, "--effort", options.Effort)
	}
	return args
}

func (r *repository) BuildTaskSessionLaunchSpec(task *core.Task) (core.TaskSessionLaunchSpec, error) {
	var prefillInput []string
	if strings.TrimSpace(task.Prompt) != "" {
		prefillInput = []string{task.Prompt}
	}

	command := append([]string{r.binary}, launchArgs(task)...)
	return core.TaskSessionLaunchSpec{
		Command:      providerkit.EnvCommand(task.ProviderEnv, ConfigEnvVars, command),
		ReadyMarker:  readyMarker,
		PrefillInput: prefillInput,
	}, nil
}

// WriteSessionHandoff runs the previous session once more in print mode,
// forked so the session itself is left as it was, even while it is still open
// interactively, with hooks and tools off so nothing but the note comes out,
// and saves the note under rig's data dir. The session's own model is used: a
// smaller one might not fit its context. The request goes in on stdin: as an
// argument it would be swallowed by the variadic --tools flag.
func (r *repository) WriteSessionHandoff(
	ctx context.Context,
	task *core.Task,
	session core.TaskProviderSession,
	focus string,
) (string, error) {
	sessionID := strings.TrimSpace(session.ProviderSessionID)
	if sessionID == "" {
		return "", fmt.Errorf("session ID is required")
	}

	args := []string{
		"-p", "--output-format", "text",
		"--resume", sessionID, "--fork-session", "--no-session-persistence",
		"--setting-sources", "user", "--tools", "",
	}
	if model := strings.TrimSpace(session.Model); model != "" {
		args = append(args, "--model", model)
	} else if model := task.Launch().Model; model != "" {
		args = append(args, "--model", model)
	}

	cwd := strings.TrimSpace(session.Cwd)
	if cwd == "" {
		cwd = strings.TrimSpace(task.WorktreePath)
	}
	result, err := r.runner.RunWithStdin(ctx, subprocess.RunWithStdinOptions{
		Env:   providerkit.EnvOverrides(task.ProviderEnv, ConfigEnvVars),
		Cwd:   cwd,
		Name:  r.binary,
		Args:  args,
		Stdin: handoffPrompt(focus),
	})
	if err != nil {
		return "", fmt.Errorf("claude print mode failed: %w", err)
	}
	note := strings.TrimSpace(result.Stdout)
	if note == "" {
		return "", fmt.Errorf("claude returned an empty handoff note")
	}

	path, err := r.handoffPath(task, sessionID)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("create handoff directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(note+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("write handoff note: %w", err)
	}
	return path, nil
}

// handoffPrompt is the handoff request, told what the next session will be
// asked to do so the note covers what it needs for that.
func handoffPrompt(focus string) string {
	focus = strings.TrimSpace(focus)
	if focus == "" {
		return prompts.HandoffPrompt
	}
	return prompts.HandoffPrompt +
		"\n\nThe next session will start with this instruction from the user:\n" + focus +
		"\n\nMake sure the note covers everything it needs for that: " +
		"the context, files, decisions and open questions that bear on it."
}

// handoffPath names a task's handoff notes under rig's data dir, one file per
// session they were written from, so earlier notes stay readable.
func (r *repository) handoffPath(task *core.Task, sessionID string) (string, error) {
	dataDir, err := r.resolveRigDataDir()
	if err != nil {
		return "", err
	}
	stamp := time.Now().UTC().Format("20060102-150405")
	short := sessionID
	if len(short) > 8 {
		short = short[:8]
	}
	return filepath.Join(dataDir, "handoffs", strings.TrimSpace(task.ID), stamp+"-"+short+".md"), nil
}

func (r *repository) BuildReconnectTaskSessionLaunchSpec(
	task *core.Task,
	sessionID string,
) (core.TaskSessionLaunchSpec, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return core.TaskSessionLaunchSpec{}, fmt.Errorf("session ID is required")
	}

	// Claude Code finds a session in the configuration it was recorded in.
	command := append([]string{r.binary, "--resume", sessionID}, launchArgs(task)...)
	return core.TaskSessionLaunchSpec{
		Command:     providerkit.EnvCommand(task.ProviderEnv, ConfigEnvVars, command),
		ReadyMarker: readyMarker,
	}, nil
}

// ExitCommand ends an idle Claude Code session; Enter on the suggestion runs it.
func (r *repository) ExitCommand() string {
	return "/exit"
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
