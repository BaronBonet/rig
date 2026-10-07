package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

// hookCatalog is Codex's hook event catalog: the one declaration of which
// hook events Rig observes from Codex, how each is matched, and which
// runtime phase it drives. Registration rules, the required-events health
// check, and the hook-to-status mapping are derived from it.
//
// Tool hooks match Bash only: Codex reports shell commands through Bash tool
// events, and those are the tool signals Rig ingests for activity and status.
var hookCatalog = providerkit.Catalog{
	{Event: core.HookEventSessionStart, Matcher: "startup|resume", Phase: core.TaskStatusPhaseStarting},
	{Event: core.HookEventUserPromptSubmit, Phase: core.TaskStatusPhaseWorking},
	{Event: core.HookEventPreToolUse, Matcher: "Bash", Phase: core.TaskStatusPhaseWorking},
	{Event: core.HookEventPostToolUse, Matcher: "Bash", Phase: core.TaskStatusPhaseWorking},
	{Event: core.HookEventPermissionRequest, Phase: core.TaskStatusPhaseWaitingForInput},
	{Event: core.HookEventStop, Phase: core.TaskStatusPhaseWaitingForInput},
}

// titleSkipPrefixes rejects Codex-specific CLI noise when parsing task title
// suggestions (common CLI noise is rejected by providerkit).
var titleSkipPrefixes = []string{"tokens used", "openai codex"}

// launchOptions are the models `codex -m` accepts and the reasoning efforts
// its model_reasoning_effort config takes, in display order.
var launchOptions = core.ProviderLaunchOptions{
	Models:  []string{"gpt-5-codex", "gpt-5"},
	Efforts: []string{"low", "medium", "high"},
}

type repository struct {
	runner              subprocess.Runner
	codexHomeDir        func() (string, error)
	binary              string
	collectorURL        string
	hookSecret          string
	transcriptIndexOnce sync.Once
	transcripts         *codexTranscriptIndex
	transcriptKindMu    sync.Mutex
	transcriptKinds     map[string]codexTranscriptKind
	transcriptKindOrder []string
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
		transcripts:  newCodexTranscriptIndex(defaultCodexTranscriptIndexBudget),
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

func (r *repository) LaunchOptions() core.ProviderLaunchOptions {
	return core.ProviderLaunchOptions{
		Models:  append([]string(nil), launchOptions.Models...),
		Efforts: append([]string(nil), launchOptions.Efforts...),
	}
}

// WriteSessionHandoff is not supported: Codex has no print-mode resume that
// leaves the session untouched.
func (r *repository) WriteSessionHandoff(
	context.Context,
	*core.Task,
	core.TaskProviderSession,
	string,
) (string, error) {
	return "", core.ErrHandoffUnsupported
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
