package core

import (
	"context"
	"fmt"
)

// sessionLauncher resolves a task's configured provider, prepares its
// workspace, and starts or bootstraps its interactive session. It is the one
// home for behaviour shared by task creation, session reconnect, and provider
// switching: the configured-provider gate and the seed-then-bootstrap
// workspace ordering live here and nowhere else.
type sessionLauncher struct {
	providers            map[Provider]ProviderClient
	providerConfig       ProviderConfigStore
	workspace            TaskWorkspaceManager
	tmuxSession          TmuxSessionClient
	enableWorkspaceSetup bool
}

func newSessionLauncher(
	providers map[Provider]ProviderClient,
	providerConfig ProviderConfigStore,
	workspace TaskWorkspaceManager,
	tmuxSession TmuxSessionClient,
	enableWorkspaceSetup bool,
) *sessionLauncher {
	return &sessionLauncher{
		providers:            providers,
		providerConfig:       providerConfig,
		workspace:            workspace,
		tmuxSession:          tmuxSession,
		enableWorkspaceSetup: enableWorkspaceSetup,
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

	if l.enableWorkspaceSetup {
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
// session, prefilling the task prompt.
func (l *sessionLauncher) startSession(ctx context.Context, task *Task) (*Task, error) {
	_, providerClient, err := l.resolveProvider(ctx, task.Provider)
	if err != nil {
		return task, err
	}
	if err := providerClient.EnsureTaskSessionEnvironment(ctx); err != nil {
		return task, fmt.Errorf("ensure task session environment: %w", err)
	}

	launch, err := providerClient.BuildTaskSessionLaunchSpec(task)
	if err != nil {
		return task, fmt.Errorf("build task session launch spec: %w", err)
	}
	if err := l.tmuxSession.StartTaskSession(ctx, task, launch); err != nil {
		return task, fmt.Errorf("start task session: %w", err)
	}

	return task, nil
}
