// Package claude implements Rig's provider boundary for the Claude Code CLI.
//
// Hook registration is workspace-scoped: BuildWorkspaceBootstrapSpec writes an
// untracked .claude/settings.local.json into the task worktree, so Rig hooks
// fire only inside Rig task workspaces. The file is written into every Rig
// task workspace regardless of the task's launch provider, so a manually
// launched Claude session in any Rig task is observed as one of its agents;
// when the file already exists (Claude Code stores permission decisions
// there) Rig merges its hook rules in and preserves the rest. Rig never
// modifies user-level Claude settings (~/.claude/settings.json); only the
// shared forward-to-rig script, which does not trigger by itself, and the
// Rig skill, in Claude's user skills directory, are installed at user level.
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
// SessionStart matches every source that starts a Provider session in the
// agent: a new session, a resume, /clear and a fork. Not compact: it can fire
// mid-turn, and SessionStart drives starting, which would reset a working
// agent.
//
// SessionEnd drives no phase: task observation ends the agent session when
// the agent exits, and keeps it for /clear and /resume. Its timeout lifts
// Claude's 1.5s SessionEnd budget above the forwarder's POST.
//
// Tool hooks are unmatched on purpose: most of Claude's work uses non-Bash
// tools (Read, Edit, Grep, ...), and every tool event must drive the task's
// working status.
//
// Stop drives needs-input unless its payload reports background work still in
// flight; HookEventToTaskStatus then reports the task as working in the
// background instead.
//
// StopFailure replaces Stop when the turn ends on an API error, such as a rate
// limit, an overloaded or failed request, or an authentication problem. It is
// unmatched so that every error type ends the working status.
var hookCatalog = providerkit.Catalog{
	{Event: core.HookEventSessionStart, Matcher: "startup|resume|clear|fork", Phase: core.TaskStatusPhaseStarting},
	{Event: core.HookEventSessionEnd, Timeout: providerkit.SessionEndTimeout},
	{Event: core.HookEventUserPromptSubmit, Phase: core.TaskStatusPhaseWorking},
	{Event: core.HookEventPreToolUse, Phase: core.TaskStatusPhaseWorking},
	{Event: core.HookEventPostToolUse, Phase: core.TaskStatusPhaseWorking},
	{
		Event:   core.HookEventNotification,
		Matcher: strings.Join(needsInputNotificationTypes, "|"),
		Phase:   core.TaskStatusPhaseWaitingForInput,
	},
	{Event: core.HookEventStop, Phase: core.TaskStatusPhaseWaitingForInput},
	{Event: core.HookEventStopFailure, Phase: core.TaskStatusPhaseWaitingForInput},
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

type repository struct {
	runner     subprocess.Runner
	rigDataDir func() (string, error)
	// claudeConfigDir is Claude Code's user-level configuration directory,
	// which holds the user's skills.
	claudeConfigDir func() (string, error)
	binary          string
	collectorURL    string
	hookSecret      string
}

func New(runner subprocess.Runner, cfg Config, hooks HookForwardingConfig) core.ProviderClient {
	collectorURL := strings.TrimSpace(hooks.CollectorURL)
	if collectorURL == "" {
		collectorURL = defaultClaudeHooksURL
	}

	return &repository{
		runner:          runner,
		binary:          cfg.Binary,
		collectorURL:    collectorURL,
		hookSecret:      strings.TrimSpace(hooks.HookSecret),
		rigDataDir:      defaultRigDataDir,
		claudeConfigDir: defaultClaudeConfigDir,
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

// SuggestTaskName names a task with Claude print mode in the task's
// repository. The first attempt runs in auto permission mode, so Claude can
// read a ticket the prompt refers to with the user's own tools (gh, tracker
// MCP servers) while the auto mode classifier blocks anything risky. Auto mode
// approves file edits in the working directory without the classifier, so
// the edit tools are denied outright: a ticket body must never edit the
// checkout.
func (r *repository) SuggestTaskName(ctx context.Context, repoRoot string, prompt string) (core.TaskSuggestion, error) {
	return providerkit.SuggestWithTicketLookup(
		ctx,
		func(ctx context.Context, lookupTickets bool) (core.TaskSuggestion, error) {
			return r.suggestTaskName(ctx, repoRoot, prompt, lookupTickets)
		},
	)
}

func (r *repository) suggestTaskName(
	ctx context.Context,
	repoRoot string,
	prompt string,
	lookupTickets bool,
) (core.TaskSuggestion, error) {
	args := []string{"-p", "--output-format", "text", "--no-session-persistence"}
	if lookupTickets {
		args = append(args, "--disallowedTools", "Edit,Write,NotebookEdit", "--permission-mode", "auto")
	}
	args = append(args, prompts.SuggestTask(prompt, lookupTickets))

	result, err := r.runner.Run(ctx, repoRoot, r.binary, args...)
	if suggestion, ok := providerkit.ParseSuggestion(result.Stdout, titleSkipPrefixes); ok {
		return suggestion, nil
	}
	if err != nil {
		return core.TaskSuggestion{}, fmt.Errorf("claude print mode failed: %w", err)
	}
	// A ticket lookup narrates its tool use, so its last plain-text line is
	// commentary rather than a title.
	if title := providerkit.ExtractTitle(result.Stdout, titleSkipPrefixes); title != "" && !lookupTickets {
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

// InstallRigSkill writes the Rig skill to the user's Claude skills, replacing
// the one an earlier Rig wrote.
func (r *repository) InstallRigSkill(context.Context) error {
	path, err := r.rigSkillPath()
	if err != nil {
		return err
	}

	return providerkit.WriteRigSkill(path)
}

// CheckRigSkill fails when the user's Claude skills lack the Rig skill this
// Rig version installs.
func (r *repository) CheckRigSkill(context.Context) error {
	path, err := r.rigSkillPath()
	if err != nil {
		return err
	}

	return providerkit.CheckRigSkill(path)
}

func (r *repository) rigSkillPath() (string, error) {
	configDir, err := r.claudeConfigDir()
	if err != nil {
		return "", err
	}
	configDir = strings.TrimSpace(configDir)
	if configDir == "" {
		return "", fmt.Errorf("claude config dir is required")
	}

	return filepath.Join(configDir, "skills", "rig", "SKILL.md"), nil
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

// BuildTaskSessionLaunchSpec starts Claude on the task's launch model and
// reasoning level, when the task was launched on Claude and they are set.
func (r *repository) BuildTaskSessionLaunchSpec(task *core.Task) (core.TaskSessionLaunchSpec, error) {
	var prefillInput []string
	if strings.TrimSpace(task.Prompt) != "" {
		prefillInput = []string{task.Prompt}
	}

	model, reasoningLevel := task.LaunchChoiceFor(core.ProviderClaude)
	command := []string{r.binary}
	if model != "" {
		command = append(command, "--model", model)
	}
	command = appendEffortFlag(command, reasoningLevel)

	return core.TaskSessionLaunchSpec{
		Command:      command,
		ReadyMarker:  readyMarker,
		PrefillInput: prefillInput,
	}, nil
}

// BuildReconnectTaskSessionLaunchSpec resumes sessionID without a model flag:
// Claude resumes a session on the model it last used, including one picked
// with /model, which a flag would undo. Claude keeps no reasoning level with
// the session that Rig can read back, and would resume on the user's own, so
// a task launched on Claude passes its launch reasoning level again.
func (r *repository) BuildReconnectTaskSessionLaunchSpec(
	task *core.Task,
	sessionID string,
) (core.TaskSessionLaunchSpec, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return core.TaskSessionLaunchSpec{}, fmt.Errorf("session ID is required")
	}

	_, reasoningLevel := task.LaunchChoiceFor(core.ProviderClaude)
	return core.TaskSessionLaunchSpec{
		Command:     appendEffortFlag([]string{r.binary, "--resume", sessionID}, reasoningLevel),
		ReadyMarker: readyMarker,
	}, nil
}

// appendEffortFlag adds Claude's reasoning level flag to command, unless no
// level is set.
func appendEffortFlag(command []string, reasoningLevel string) []string {
	if reasoningLevel == "" {
		return command
	}
	return append(command, "--effort", reasoningLevel)
}

func (r *repository) TaskSessionCommandName() string {
	commandName := filepath.Base(strings.TrimSpace(r.binary))
	if commandName == "." {
		return "claude"
	}
	return commandName
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

// defaultClaudeConfigDir is the directory Claude Code keeps the user's
// configuration and skills in: $CLAUDE_CONFIG_DIR, or else ~/.claude. A
// CLAUDE_CONFIG_DIR that is not an absolute path, such as one starting with
// a ~ the shell did not expand, is an error rather than a path resolved
// against the daemon's working directory. Nor does it fall back to ~/.claude,
// which this Claude Code may not read: the error is what rig doctor shows.
func defaultClaudeConfigDir() (string, error) {
	if custom := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); custom != "" {
		if !filepath.IsAbs(custom) {
			return "", fmt.Errorf("CLAUDE_CONFIG_DIR must be an absolute path, not %q", custom)
		}
		return filepath.Clean(custom), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve claude config dir: %w", err)
	}

	return filepath.Join(home, ".claude"), nil
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
