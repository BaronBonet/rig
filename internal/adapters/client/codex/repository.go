package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/BaronBonet/rig/internal/adapters/client/providerkit"
	"github.com/BaronBonet/rig/internal/core"
	"github.com/BaronBonet/rig/internal/pkg/prompts"
	"github.com/BaronBonet/rig/internal/pkg/subprocess"
)

const (
	readyMarker          = "›"
	codexHookPath        = "/codex-hook"
	legacyCodexHookPath  = "/hook"
	defaultCodexHooksURL = "http://127.0.0.1:4124" + codexHookPath
	// codexHomeEnvVar points Codex at a home other than ~/.codex, with its own
	// login, sessions and hooks.
	codexHomeEnvVar = "CODEX_HOME"
)

// ConfigEnvVars are the variables that choose which Codex home, and so which
// account, sessions and hooks, a session uses.
var ConfigEnvVars = []string{codexHomeEnvVar}

// Codex can show its composer before reporting a session-start hook. Recognize
// its input with either the banner or footer, since startup output can scroll
// the banner away. Numbered startup dialog selections are not input lines.
const composerLinePattern = `^›(?:[ \t]*| [[:alpha:]][^\n]*)\n[ \t]*(?:\n|$)`

var readyPattern = regexp.MustCompile(`(?m)(?:OpenAI Codex[^\n]*\n(?s:.*?)` + composerLinePattern +
	`|` + composerLinePattern + `[ \t]*(?:\? for shortcuts|[^\n]+ · [^\n]+|[0-9]+% context left))`)

// hookCatalog is Codex's hook event catalog: the one declaration of which
// hook events Rig observes from Codex, how each is matched, and which
// runtime phase it drives. Registration rules, the required-events health
// check, and the hook-to-status mapping are derived from it.
//
// Tool hooks match Bash only: Codex reports shell commands through Bash tool
// events, and those are the tool signals Rig ingests for activity and status.
//
// SubagentStart drives no phase. It records a thread-spawned subagent's
// transcript in provider session history as soon as the subagent is spawned,
// so status recovery can count subagents still running after the root agent's
// turn ends.
var hookCatalog = providerkit.Catalog{
	{Event: core.HookEventSessionStart, Matcher: "startup|resume", Phase: core.TaskStatusPhaseStarting},
	{Event: core.HookEventUserPromptSubmit, Phase: core.TaskStatusPhaseWorking},
	{Event: core.HookEventPreToolUse, Matcher: "Bash", Phase: core.TaskStatusPhaseWorking},
	{Event: core.HookEventPostToolUse, Matcher: "Bash", Phase: core.TaskStatusPhaseWorking},
	{Event: core.HookEventPermissionRequest, Phase: core.TaskStatusPhaseWaitingForInput},
	{Event: core.HookEventStop, Phase: core.TaskStatusPhaseWaitingForInput},
	{Event: core.HookEventSubagentStart},
}

// titleSkipPrefixes rejects Codex-specific CLI noise when parsing task title
// suggestions (common CLI noise is rejected by providerkit).
var titleSkipPrefixes = []string{"tokens used", "openai codex"}

type repository struct {
	runner              subprocess.Runner
	codexHomeDir        func() (string, error)
	rigDataDir          func() (string, error)
	binary              string
	collectorURL        string
	hookSecret          string
	transcriptIndexOnce sync.Once
	transcripts         *codexTranscriptIndex
	transcriptKindMu    sync.Mutex
	transcriptKinds     map[string]codexTranscriptKind
	transcriptKindOrder []string
	// usage keeps each account's latest plan usage reading; usageURL
	// overrides where it is read from.
	usage    *providerkit.UsageCache
	usageURL string
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
		hookSecret:   strings.TrimSpace(hooks.HookSecret),
		codexHomeDir: defaultCodexHomeDir,
		rigDataDir:   providerkit.DefaultRigDataDir,
		transcripts:  newCodexTranscriptIndex(defaultCodexTranscriptIndexBudget),
		usage:        providerkit.NewUsageCache(),
	}
}

func NewHookForwardingConfig(hookListenAddr string, hookSecret string) HookForwardingConfig {
	collectorURL := strings.TrimSpace(hookListenAddr)
	if collectorURL != "" &&
		!strings.HasPrefix(collectorURL, "http://") &&
		!strings.HasPrefix(collectorURL, "https://") {
		collectorURL = "http://" + collectorURL + codexHookPath
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
		return fmt.Errorf("codex rig hook forwarding: %w", err)
	}

	return nil
}

func (r *repository) SuggestTaskName(
	ctx context.Context,
	prompt string,
	env core.ProviderEnv,
) (core.TaskSuggestion, error) {
	tmpFile, err := os.CreateTemp("", "rig-codex-name-*.txt")
	if err != nil {
		return core.TaskSuggestion{}, err
	}
	tmpPath := tmpFile.Name()
	_ = tmpFile.Close()
	defer func() { _ = os.Remove(tmpPath) }()

	fullPrompt := prompts.SuggestTaskPrompt + "\n\nTask description: " + prompt

	result, err := r.runner.RunWithStdin(ctx, subprocess.RunWithStdinOptions{
		Env:  providerkit.EnvOverrides(env, ConfigEnvVars),
		Name: r.binary,
		Args: []string{"exec", "--skip-git-repo-check", "--output-last-message", tmpPath, fullPrompt},
	})

	fileOutput := readOutputFile(tmpPath)
	if suggestion, ok := parsePreferredSuggestion(fileOutput, result.Stdout); ok {
		return suggestion, nil
	}
	if err != nil {
		return core.TaskSuggestion{}, fmt.Errorf("codex exec failed: %w", err)
	}

	return core.TaskSuggestion{}, fmt.Errorf("codex did not return a usable task title")
}

// EnsureTaskSessionEnvironment registers Rig's hooks in the Codex home the
// session runs with, which is where Codex reads them.
func (r *repository) EnsureTaskSessionEnvironment(_ context.Context, env core.ProviderEnv) error {
	codexHome, err := r.resolveCodexHomeDir(env)
	if err != nil {
		return err
	}

	scriptPath := filepath.Join(codexHome, "hooks", providerkit.ForwarderScriptName)
	if err := r.forwarder().WriteScript(scriptPath); err != nil {
		return err
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

// WriteSessionHandoff forks the previous session without a terminal and
// without saving the fork (`codex exec fork --ephemeral`), so the session is
// left as it was, even while it is still open interactively: `exec resume`
// would add the request to it, ephemeral or not. Hooks are off and the
// sandbox read-only so nothing but the note comes out; the note is saved
// under rig's data dir. The session's own model is used: a smaller one might
// not fit its context. The request goes in on stdin.
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

	outputFile, err := os.CreateTemp("", "rig-codex-handoff-*.md")
	if err != nil {
		return "", err
	}
	outputPath := outputFile.Name()
	_ = outputFile.Close()
	defer func() { _ = os.Remove(outputPath) }()

	args := []string{
		"exec", "fork", sessionID, "--ephemeral", "--disable", "hooks", "--skip-git-repo-check",
		"-c", `sandbox_mode="read-only"`, "-o", outputPath,
	}
	model := strings.TrimSpace(session.Model)
	if model == "" {
		model = task.Launch().Model
	}
	if model != "" {
		args = append(args, "-m", model)
	}
	if effort := task.Launch().Effort; effort != "" {
		args = append(args, "-c", "model_reasoning_effort="+effort)
	}
	args = append(args, "-")

	cwd := strings.TrimSpace(session.Cwd)
	if cwd == "" {
		cwd = strings.TrimSpace(task.WorktreePath)
	}
	if _, err := r.runner.RunWithStdin(ctx, subprocess.RunWithStdinOptions{
		Env:   providerkit.EnvOverrides(r.sessionEnv(task, session), ConfigEnvVars),
		Cwd:   cwd,
		Name:  r.binary,
		Args:  args,
		Stdin: providerkit.HandoffPrompt(focus),
	}); err != nil {
		return "", fmt.Errorf("codex exec fork failed: %w", err)
	}
	note := strings.TrimSpace(readOutputFile(outputPath))
	if note == "" {
		return "", fmt.Errorf("codex returned an empty handoff note")
	}

	dataDir, err := r.resolveRigDataDir()
	if err != nil {
		return "", err
	}
	return providerkit.SaveHandoffNote(dataDir, task, sessionID, note)
}

// sessionEnv is the Task's env under the account the session's rollout is
// kept in, where codex exec fork finds it. A Task recorded before rig kept
// its account has no env, and its session, started under the folder's direnv
// account, is not in the daemon's.
func (r *repository) sessionEnv(task *core.Task, session core.TaskProviderSession) core.ProviderEnv {
	dir, ok := providerkit.TranscriptAccountDir(session.TranscriptPath, "sessions", 4)
	if !ok {
		return task.ProviderEnv
	}
	if current, err := r.AccountDir(task.ProviderEnv); err == nil && current == dir {
		return task.ProviderEnv
	}
	return task.ProviderEnv.With(r.AccountEnv(dir))
}

func (r *repository) resolveRigDataDir() (string, error) {
	if r.rigDataDir == nil {
		return providerkit.DefaultRigDataDir()
	}
	return r.rigDataDir()
}

// launchArgs are the global flags a task's launch options add before any
// codex subcommand.
func launchArgs(task *core.Task) []string {
	var args []string
	options := task.Launch()
	if options.Model != "" {
		args = append(args, "-m", options.Model)
	}
	if options.Effort != "" {
		args = append(args, "-c", "model_reasoning_effort="+options.Effort)
	}
	return args
}

func (r *repository) BuildTaskSessionLaunchSpec(task *core.Task) (core.TaskSessionLaunchSpec, error) {
	var prefillInput []string
	if strings.TrimSpace(task.Prompt) != "" {
		prefillInput = []string{task.Prompt}
	}

	command, err := r.codexTaskCommand(task.ProviderEnv, launchArgs(task)...)
	if err != nil {
		return core.TaskSessionLaunchSpec{}, err
	}

	return core.TaskSessionLaunchSpec{
		Command:      command,
		ReadyMarker:  readyMarker,
		ReadyPattern: readyPattern,
		PrefillInput: prefillInput,
	}, nil
}

func (r *repository) BuildReconnectTaskSessionLaunchSpec(
	task *core.Task,
	sessionID string,
) (core.TaskSessionLaunchSpec, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return core.TaskSessionLaunchSpec{}, fmt.Errorf("session ID is required")
	}

	command, err := r.codexTaskCommand(task.ProviderEnv, append(launchArgs(task), "resume", sessionID)...)
	if err != nil {
		return core.TaskSessionLaunchSpec{}, err
	}

	return core.TaskSessionLaunchSpec{
		Command:     command,
		ReadyMarker: readyMarker,
	}, nil
}

// ExitCommand ends an idle Codex session.
func (r *repository) ExitCommand() string {
	return "/quit"
}

// ClearCommand is empty: no Codex command for a fresh conversation has been
// verified, so a Codex session is ended and launched anew.
func (r *repository) ClearCommand() string {
	return ""
}

func (r *repository) TaskSessionCommandName() string {
	commandName := filepath.Base(strings.TrimSpace(r.binary))
	if commandName == "." {
		return "codex"
	}
	return commandName
}

func (r *repository) codexTaskCommand(env core.ProviderEnv, args ...string) ([]string, error) {
	codexHome, err := r.resolveCodexHomeDir(env)
	if err != nil {
		return nil, err
	}
	codexHome = strings.TrimSpace(codexHome)
	if codexHome == "" {
		return nil, fmt.Errorf("codex home is required")
	}

	command := []string{"env", "CODEX_HOME=" + codexHome, r.binary}
	return append(command, args...), nil
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
		if suggestion, ok := providerkit.ParseSuggestion(candidate, titleSkipPrefixes); ok {
			return suggestion, true
		}
		if title := providerkit.ExtractTitle(candidate, titleSkipPrefixes); title != "" {
			return core.TaskSuggestion{Name: title, BranchType: "feat"}, true
		}
	}

	return core.TaskSuggestion{}, false
}

func (r *repository) commandForEvent(scriptPath string, eventName string) string {
	return "/bin/sh " + providerkit.ShellQuote(scriptPath) + " " + providerkit.ShellQuote(strings.TrimSpace(eventName))
}

func (r *repository) forwarder() providerkit.Forwarder {
	return providerkit.Forwarder{
		ProviderLabel: "codex",
		EventHeader:   "X-Codex-Hook-Event",
		CollectorURL:  r.collectorURL,
		HookSecret:    r.hookSecret,
	}
}

func (r *repository) loadHookConfig(path string) (providerkit.HookConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return providerkit.HookConfig{Hooks: map[string][]providerkit.HookRule{}}, nil
		}
		return providerkit.HookConfig{}, fmt.Errorf("read codex hooks config: %w", err)
	}

	var cfg providerkit.HookConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return providerkit.HookConfig{}, fmt.Errorf("decode codex hooks config: %w", err)
	}
	if cfg.Hooks == nil {
		cfg.Hooks = map[string][]providerkit.HookRule{}
	}

	return cfg, nil
}

func (r *repository) healthCheckHookForwarding() error {
	codexHome, err := r.resolveCodexHomeDir(nil)
	if err != nil {
		return err
	}
	codexHome = strings.TrimSpace(codexHome)
	if codexHome == "" {
		return fmt.Errorf("codex home is required")
	}

	scriptPath := filepath.Join(codexHome, "hooks", providerkit.ForwarderScriptName)
	if err := providerkit.HealthCheckScript(scriptPath, r.collectorURL); err != nil {
		return err
	}

	cfg, err := r.loadHookConfig(filepath.Join(codexHome, "hooks.json"))
	if err != nil {
		return err
	}
	for _, eventName := range hookCatalog.EventNames() {
		if !hookConfigHasScriptCommand(cfg, eventName, scriptPath) {
			return fmt.Errorf("missing %s hook for %s", eventName, scriptPath)
		}
	}

	return nil
}

func hookConfigHasScriptCommand(cfg providerkit.HookConfig, eventName string, scriptPath string) bool {
	for _, rule := range cfg.Hooks[eventName] {
		if providerkit.HookRuleHasScriptCommand(rule, scriptPath) {
			return true
		}
	}
	return false
}

func (r *repository) ensureRigHookRules(cfg *providerkit.HookConfig, scriptPath string) error {
	if cfg == nil {
		return nil
	}

	cfg.Hooks = providerkit.MergeRigHookRules(cfg.Hooks, hookCatalog.HookRules(func(eventName string) string {
		return r.commandForEvent(scriptPath, eventName)
	}))

	return nil
}

// resolveCodexHomeDir returns the Codex home env selects, falling back to the
// daemon's own when env does not decide it.
func (r *repository) resolveCodexHomeDir(env core.ProviderEnv) (string, error) {
	if home, decided := env.Lookup(codexHomeEnvVar); decided {
		if home != "" {
			return home, nil
		}
		return userCodexHomeDir()
	}
	if r.codexHomeDir == nil {
		return defaultCodexHomeDir()
	}
	return r.codexHomeDir()
}

func defaultCodexHomeDir() (string, error) {
	if custom := strings.TrimSpace(os.Getenv(codexHomeEnvVar)); custom != "" {
		return custom, nil
	}
	return userCodexHomeDir()
}

// userCodexHomeDir is where Codex keeps its home when CODEX_HOME is unset.
func userCodexHomeDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve codex home: %w", err)
	}

	return filepath.Join(home, ".codex"), nil
}
