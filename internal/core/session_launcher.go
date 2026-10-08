package core

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// defaultProviderSessionWait bounds how long a launch blocks waiting for the
// provider to report its session start before the prompt is typed; the launch
// then completes and the prompt is typed in the background once the session
// starts, within defaultProviderSessionLateWait. A folder trust dialog, for
// example, holds the provider up until the user answers it.
const (
	defaultProviderSessionWait     = 20 * time.Second
	defaultProviderSessionLateWait = 10 * time.Minute
)

// sessionLauncher resolves a task's configured provider, prepares its
// workspace, and starts or bootstraps its interactive session. It is the one
// home for behaviour shared by task creation, session reconnect, and provider
// switching: the configured-provider gate and the seed-then-bootstrap
// workspace ordering live here and nowhere else.
type sessionLauncher struct {
	tasks                TaskRepository
	providers            map[Provider]ProviderClient
	providerConfig       ProviderConfigStore
	workspace            TaskWorkspaceManager
	tmuxSession          TmuxSessionClient
	enableWorkspaceSetup bool
	// providerSessionWait bounds the wait for the provider's session-start
	// hook before a prompt is typed; zero types it on the ready marker alone,
	// with no background wait.
	providerSessionWait time.Duration
	// providerSessionLateWait bounds the background wait after that.
	providerSessionLateWait time.Duration
	// sessionPollInterval paces both waits.
	sessionPollInterval time.Duration
}

func newSessionLauncher(
	tasks TaskRepository,
	providers map[Provider]ProviderClient,
	providerConfig ProviderConfigStore,
	workspace TaskWorkspaceManager,
	tmuxSession TmuxSessionClient,
	enableWorkspaceSetup bool,
) *sessionLauncher {
	return &sessionLauncher{
		tasks:                   tasks,
		providers:               providers,
		providerConfig:          providerConfig,
		workspace:               workspace,
		tmuxSession:             tmuxSession,
		enableWorkspaceSetup:    enableWorkspaceSetup,
		providerSessionWait:     defaultProviderSessionWait,
		providerSessionLateWait: defaultProviderSessionLateWait,
		sessionPollInterval:     250 * time.Millisecond,
	}
}

// resolveProvider resolves a provider name to a provider the user has
// configured through provider setup and returns its adapter client. An empty
// provider resolves to the user's default provider. Provider-dependent task
// actions must use this gate so that unconfigured providers fail with a clear
// error instead of misbehaving.
func (l *sessionLauncher) resolveProvider(
	ctx context.Context,
	provider Provider,
) (Provider, ProviderClient, error) {
	if l.providerConfig == nil {
		return "", nil, fmt.Errorf("provider config store not configured")
	}
	setup, err := l.providerConfig.GetProviderSetup(ctx)
	if err != nil {
		return "", nil, err
	}
	if setup == nil {
		return "", nil, ErrProviderSetupRequired
	}

	if provider == "" {
		provider = setup.Default
	}
	if !setup.IsConfigured(provider) {
		return "", nil, fmt.Errorf("provider %q is not configured: run rig setup to enable it", provider)
	}

	providerClient, ok := l.providers[provider]
	if !ok {
		return "", nil, fmt.Errorf("provider %q unavailable", provider)
	}

	return provider, providerClient, nil
}

// repoSettings reads the repository's own settings for worktree tasks; without
// a workspace manager there are none.
func (l *sessionLauncher) repoSettings(repoRoot string) (RepoSettings, error) {
	if l.workspace == nil {
		return RepoSettings{}, nil
	}
	settings, err := l.workspace.LoadRepoSettings(repoRoot)
	if err != nil {
		return RepoSettings{}, fmt.Errorf("load repo settings: %w", err)
	}
	return settings, nil
}

// prepareWorkspace applies repo-local workspace setup (when enabled) and the
// active provider's bootstrap files, in that order. Seeding must precede
// bootstrap so provider files can rely on repo-local configuration.
//
// After the active provider's bootstrap, every other configured provider's
// bootstrap files are written best-effort so that a manually launched
// configured provider in this workspace stays observable (provider adoption).
// Their failures degrade observability but never fail the workspace.
func (l *sessionLauncher) prepareWorkspace(ctx context.Context, task *Task, repoRoot string) error {
	_, providerClient, err := l.resolveProvider(ctx, task.Provider)
	if err != nil {
		return fmt.Errorf("build workspace bootstrap spec: %w", err)
	}
	bootstrapSpec, err := providerClient.BuildWorkspaceBootstrapSpec(task)
	if err != nil {
		return fmt.Errorf("build workspace bootstrap spec: %w", err)
	}

	if l.workspace == nil {
		return nil
	}

	// Seeding copies repo files into a new worktree; a folder Task's workspace
	// is the original folder, so there is nothing to seed.
	if l.enableWorkspaceSetup && !task.UsesFolderWorkspace() {
		if err := l.workspace.SetupTaskWorkspace(ctx, task, repoRoot); err != nil {
			return fmt.Errorf("setup workspace: %w", err)
		}
	}

	if err := l.workspace.BootstrapTaskWorkspace(ctx, task, bootstrapSpec); err != nil {
		return fmt.Errorf("bootstrap workspace: %w", err)
	}

	_ = l.bootstrapConfiguredProviders(ctx, task, task.Provider)

	return nil
}

// bootstrapConfiguredProviders writes the workspace bootstrap files of every
// configured provider except skip into the task workspace, so any configured
// provider manually launched there reports hook events and can be adopted.
// Each provider is attempted independently; errors are collected, not fatal.
func (l *sessionLauncher) bootstrapConfiguredProviders(
	ctx context.Context,
	task *Task,
	skip Provider,
) []error {
	if l.workspace == nil || l.providerConfig == nil {
		return nil
	}
	setup, err := l.providerConfig.GetProviderSetup(ctx)
	if err != nil || setup == nil {
		return nil
	}

	var errs []error
	for _, provider := range setup.Configured {
		if provider == skip {
			continue
		}
		providerClient, ok := l.providers[provider]
		if !ok {
			continue
		}
		bootstrapSpec, err := providerClient.BuildWorkspaceBootstrapSpec(task)
		if err != nil {
			errs = append(errs, fmt.Errorf("build %s workspace bootstrap spec: %w", provider, err))
			continue
		}
		if len(bootstrapSpec.Files) == 0 {
			continue
		}
		if err := l.workspace.BootstrapTaskWorkspace(ctx, task, bootstrapSpec); err != nil {
			errs = append(errs, fmt.Errorf("bootstrap %s workspace files: %w", provider, err))
		}
	}
	return errs
}

// bootstrapWorkspace writes the given provider's bootstrap files into an
// existing task workspace without rerunning repo seeding or setup scripts.
// The client is explicit because provider switching bootstraps for the new
// provider before the task record's active provider changes.
func (l *sessionLauncher) bootstrapWorkspace(
	ctx context.Context,
	providerClient ProviderClient,
	task *Task,
) error {
	bootstrapSpec, err := providerClient.BuildWorkspaceBootstrapSpec(task)
	if err != nil {
		return fmt.Errorf("build workspace bootstrap spec: %w", err)
	}
	if l.workspace == nil {
		return nil
	}
	if err := l.workspace.BootstrapTaskWorkspace(ctx, task, bootstrapSpec); err != nil {
		return fmt.Errorf("bootstrap workspace: %w", err)
	}

	return nil
}

// startSession launches the task's active provider fresh in the task tmux
// session, then types the task prompt once the provider is ready.
//
// Readiness comes from the provider itself: its session-start hook, which
// fires once it has loaded the workspace and is past any startup dialog. The
// pane's ready marker alone is not enough, since a shell prompt may show the
// same character before the provider has even started and would swallow the
// prompt.
func (l *sessionLauncher) startSession(ctx context.Context, task *Task) (*Task, error) {
	return l.launchSession(ctx, task, false)
}

// resumeStartSession is startSession for a retried creation: a provider the
// failed attempt left running in the pane is not launched again, only given
// its prompt.
func (l *sessionLauncher) resumeStartSession(ctx context.Context, task *Task) (*Task, error) {
	return l.launchSession(ctx, task, true)
}

func (l *sessionLauncher) launchSession(ctx context.Context, task *Task, reuseRunning bool) (*Task, error) {
	_, providerClient, err := l.resolveProvider(ctx, task.Provider)
	if err != nil {
		return task, err
	}
	if err := providerClient.EnsureTaskSessionEnvironment(ctx, task.ProviderEnv); err != nil {
		return task, fmt.Errorf("ensure task session environment: %w", err)
	}

	launch, err := providerClient.BuildTaskSessionLaunchSpec(task)
	if err != nil {
		return task, fmt.Errorf("build task session launch spec: %w", err)
	}
	prefill := launch.PrefillInput
	launch.PrefillInput = nil

	knownSessions := l.providerSessionIDs(ctx, task)
	reused := reuseRunning && l.providerRunning(ctx, task, providerClient)
	if !reused {
		if err := l.tmuxSession.StartTaskSession(ctx, task, launch); err != nil {
			return task, fmt.Errorf("start task session: %w", err)
		}
	}
	if len(prefill) == 0 {
		return task, nil
	}

	// A provider that was already running reported its session before; only
	// a fresh launch has a session start to wait for.
	launch.PrefillInput = prefill
	if reused || l.providerSessionWait <= 0 ||
		l.awaitProviderSession(ctx, task, knownSessions, l.providerSessionWait) {
		if err := l.tmuxSession.PrefillTaskSession(ctx, task, launch); err != nil {
			return task, fmt.Errorf("type task prompt into the session: %w", err)
		}
		return task, nil
	}

	// The provider has not reported its session yet, most likely a startup
	// dialog is waiting for the user. The launch is complete; the prompt is
	// typed once the session starts. The task record keeps it meanwhile.
	go l.prefillWhenSessionStarts(context.WithoutCancel(ctx), task, launch, knownSessions)
	return task, nil
}

func (l *sessionLauncher) prefillWhenSessionStarts(
	ctx context.Context,
	task *Task,
	launch TaskSessionLaunchSpec,
	known map[string]bool,
) {
	if !l.awaitProviderSession(ctx, task, known, l.providerSessionLateWait) {
		return
	}
	_ = l.tmuxSession.PrefillTaskSession(ctx, task, launch)
}

// providerRunning reports whether the task's pane is already running the
// provider's CLI.
func (l *sessionLauncher) providerRunning(ctx context.Context, task *Task, providerClient ProviderClient) bool {
	runtime, err := l.tmuxSession.InspectTaskSession(ctx, task)
	if err != nil || !runtime.Exists {
		return false
	}
	return taskSessionRunningProvider(runtime, providerClient.TaskSessionCommandName())
}

// providerSessionIDs snapshots the provider sessions already recorded for the
// task, so a launch can tell its own session-start from earlier ones.
func (l *sessionLauncher) providerSessionIDs(ctx context.Context, task *Task) map[string]bool {
	known := make(map[string]bool)
	if l.tasks == nil {
		return known
	}
	sessions, err := l.tasks.ListTaskProviderSessions(ctx, task.ID)
	if err != nil {
		return known
	}
	for _, session := range sessions {
		known[string(session.Provider)+"/"+strings.TrimSpace(session.ProviderSessionID)] = true
	}
	return known
}

// awaitProviderSession waits until the task records a provider session that
// was not known before the launch, or the wait runs out. It reports whether
// the session was observed.
func (l *sessionLauncher) awaitProviderSession(
	ctx context.Context,
	task *Task,
	known map[string]bool,
	wait time.Duration,
) bool {
	if l.tasks == nil || wait <= 0 {
		return false
	}
	interval := l.sessionPollInterval
	if interval <= 0 {
		interval = 250 * time.Millisecond
	}
	deadline := time.Now().Add(wait)
	for {
		sessions, err := l.tasks.ListTaskProviderSessions(ctx, task.ID)
		if err == nil {
			for _, session := range sessions {
				if session.Provider != task.Provider {
					continue
				}
				if !known[string(session.Provider)+"/"+strings.TrimSpace(session.ProviderSessionID)] {
					return true
				}
			}
		}
		if !time.Now().Before(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(interval):
		}
	}
}

// rememberLaunchDefaults records the options a provider was just launched
// with as the preselection for its next launch. Losing the preference is not
// worth failing the launch.
func (l *sessionLauncher) rememberLaunchDefaults(ctx context.Context, provider Provider, options LaunchOptions) {
	if l.providerConfig == nil {
		return
	}
	_ = l.providerConfig.SaveLaunchDefaults(ctx, provider, options)
}
