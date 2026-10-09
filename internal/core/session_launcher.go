package core

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// sessionLauncher resolves a task's configured provider, prepares its
// workspace, and starts or bootstraps its interactive session and the agents
// in it. It is the one home for behaviour shared by task creation and session
// reconnect: the configured-provider gate, the seed-then-bootstrap workspace
// ordering, and recording the agent sessions it launches live here and nowhere
// else.
type sessionLauncher struct {
	providers      map[Provider]ProviderClient
	providerConfig ProviderConfigStore
	workspace      TaskWorkspaceManager
	tmuxSession    TmuxSessionClient
	// observation records the agent sessions the launcher starts.
	observation          *taskObservation
	enableWorkspaceSetup bool
}

func newSessionLauncher(
	providers map[Provider]ProviderClient,
	providerConfig ProviderConfigStore,
	workspace TaskWorkspaceManager,
	tmuxSession TmuxSessionClient,
	observation *taskObservation,
	enableWorkspaceSetup bool,
) *sessionLauncher {
	return &sessionLauncher{
		providers:            providers,
		providerConfig:       providerConfig,
		workspace:            workspace,
		tmuxSession:          tmuxSession,
		observation:          observation,
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
// launch provider's bootstrap files, in that order. Seeding must precede
// bootstrap so provider files can rely on repo-local configuration.
//
// After the launch provider's bootstrap, every other configured provider's
// bootstrap files are written best-effort so that a manually launched
// configured provider in this workspace is observed as an agent session.
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
// provider manually launched there reports hook events as an agent session.
// Each provider is attempted independently; errors are collected, not fatal.
func (l *sessionLauncher) bootstrapConfiguredProviders(
	ctx context.Context,
	task *Task,
	skip ...Provider,
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
		if slices.Contains(skip, provider) {
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
// The client is explicit because the provider need not be the task's launch
// provider, as for a restored agent session of another provider.
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

// startSession creates the task's tmux session and launches the task's launch
// provider fresh in its task window, prefilling the task prompt, then records
// the agent's agent session.
func (l *sessionLauncher) startSession(ctx context.Context, task *Task) (*Task, error) {
	provider, providerClient, err := l.resolveProvider(ctx, task.Provider)
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
	pane, err := l.tmuxSession.StartTaskSession(ctx, task, launch)
	if err != nil {
		return task, fmt.Errorf("start task session: %w", err)
	}
	l.recordLaunch(ctx, task, provider, pane)

	return task, nil
}

// recordLaunch records the agent session of an agent the launcher started in
// pane. Recording is best-effort, like placing hook events: the agent already
// runs, and when the record fails, or tmux reported no pane, its first hook
// event opens its agent session instead.
func (l *sessionLauncher) recordLaunch(ctx context.Context, task *Task, provider Provider, pane TmuxPaneRef) {
	if pane.ID == "" {
		return
	}
	_, _ = l.observation.recordLaunchedAgentSession(ctx, task, provider, pane)
}

// agentSessionRestore is one agent session restoreSession brings back, with
// the launch spec that resumes its current Provider session.
type agentSessionRestore struct {
	session AgentSession
	launch  TaskSessionLaunchSpec
}

// restoreSession recreates the task's missing Session and restores its open
// agent sessions there, oldest first: the first in the task window's pane and
// each of the rest in a pane split in beside it, left to right, each resuming
// its current Provider session with its own provider. An agent session whose
// provider is no longer configured is skipped, and one with no Provider
// session to resume ends. With nothing left to restore, restoreSession
// launches the task's launch provider fresh, without a prompt.
//
// It fails, creating nothing, when none of the agent sessions it would restore
// can launch, or when the Session cannot be created. Once the Session exists,
// an agent session that fails to restore doesn't stop the others; the error
// returned then names each agent session left behind, skipped ones included.
func (l *sessionLauncher) restoreSession(ctx context.Context, task *Task) error {
	open, err := l.observation.tasks.ListOpenAgentSessions(ctx, task.ID)
	if err != nil {
		return fmt.Errorf("list open agent sessions: %w", err)
	}

	var (
		restores []agentSessionRestore
		ended    []AgentSession
		problems []error
		failed   bool
	)
	prepared := make(map[Provider]error)
	for _, session := range open {
		if strings.TrimSpace(session.ProviderSessionID) == "" || duplicatesConversation(session, open) {
			ended = append(ended, session)
			continue
		}
		provider, providerClient, err := l.resolveProvider(ctx, session.Provider)
		if err != nil {
			problems = append(problems, agentSessionNotRestored(session, err))
			continue
		}
		launch, err := l.prepareRestore(ctx, task, provider, providerClient, session, prepared)
		if err != nil {
			problems = append(problems, agentSessionNotRestored(session, err))
			failed = true
			continue
		}
		restores = append(restores, agentSessionRestore{session: session, launch: launch})
	}
	if failed && len(restores) == 0 {
		return fmt.Errorf("reconnect: %s", joinErrors(problems))
	}

	var detached map[string]AgentSession
	if len(restores) > 0 || len(ended) > 0 {
		var changed []AgentSession
		detached, changed, err = l.observation.detachAgentSessionsForRestore(
			ctx,
			task.ID,
			restoreSessions(restores),
			ended,
		)
		if err != nil {
			return err
		}
		for _, session := range changed {
			problems = append(problems, agentSessionNotRestored(session, errAgentSessionChangedDuringRestore))
		}
	}
	restores = slices.DeleteFunc(restores, func(restore agentSessionRestore) bool {
		_, ok := detached[restore.session.ID]
		return !ok
	})
	if len(restores) == 0 {
		if err := l.launchFresh(ctx, task); err != nil {
			return withProblems(err, problems)
		}
		return agentSessionsNotRestored(problems)
	}

	restoredProviders := make([]Provider, 0, len(restores))
	for _, restore := range restores {
		restoredProviders = append(restoredProviders, restore.session.Provider)
	}
	_ = l.bootstrapConfiguredProviders(ctx, task, restoredProviders...)

	first := restores[0]
	taskPane, err := l.tmuxSession.StartTaskSession(ctx, task, first.launch)
	if err != nil {
		return withProblems(fmt.Errorf("reconnect task session: %w", err), problems)
	}
	l.recordRestore(ctx, task, detached[first.session.ID], taskPane)
	// Every other agent gets a pane of the task window, so the Session keeps
	// its layout: an editor window and a task window holding the agents.
	for _, restore := range restores[1:] {
		pane, err := l.tmuxSession.SplitAgentPane(ctx, task, taskPane.ID, restore.launch)
		if err != nil {
			err = fmt.Errorf("split agent pane: %w", err)
			problems = append(problems, agentSessionNotRestored(restore.session, err))
			continue
		}
		l.recordRestore(ctx, task, detached[restore.session.ID], pane)
	}
	return agentSessionsNotRestored(problems)
}

// prepareRestore readies the workspace for an agent session's provider, once
// per provider of a restore, and returns the launch spec that resumes the
// agent session's current Provider session.
func (l *sessionLauncher) prepareRestore(
	ctx context.Context,
	task *Task,
	provider Provider,
	providerClient ProviderClient,
	session AgentSession,
	prepared map[Provider]error,
) (TaskSessionLaunchSpec, error) {
	err, ok := prepared[provider]
	if !ok {
		err = l.prepareProvider(ctx, task, providerClient)
		prepared[provider] = err
	}
	if err != nil {
		return TaskSessionLaunchSpec{}, err
	}
	launch, err := providerClient.BuildReconnectTaskSessionLaunchSpec(task, session.ProviderSessionID)
	if err != nil {
		return TaskSessionLaunchSpec{}, fmt.Errorf("build reconnect task session launch spec: %w", err)
	}
	return launch, nil
}

// prepareProvider writes a provider's bootstrap files into the task's
// workspace, never rerunning repo seeding or setup scripts, and ensures its
// session environment.
func (l *sessionLauncher) prepareProvider(ctx context.Context, task *Task, providerClient ProviderClient) error {
	if err := l.bootstrapWorkspace(ctx, providerClient, task); err != nil {
		return err
	}
	if err := providerClient.EnsureTaskSessionEnvironment(ctx); err != nil {
		return fmt.Errorf("ensure task session environment: %w", err)
	}
	return nil
}

// launchFresh recreates the task's Session with its launch provider launched
// fresh, without a prompt, in its task window, and records its agent session.
func (l *sessionLauncher) launchFresh(ctx context.Context, task *Task) error {
	provider, providerClient, err := l.resolveProvider(ctx, task.Provider)
	if err != nil {
		return err
	}
	if err := l.prepareProvider(ctx, task, providerClient); err != nil {
		return err
	}
	_ = l.bootstrapConfiguredProviders(ctx, task, provider)

	launch, err := promptlessTaskSessionLaunchSpec(providerClient, task)
	if err != nil {
		return fmt.Errorf("build task session launch spec: %w", err)
	}
	pane, err := l.tmuxSession.StartTaskSession(ctx, task, launch)
	if err != nil {
		return fmt.Errorf("reconnect task session: %w", err)
	}
	l.recordLaunch(ctx, task, provider, pane)
	return nil
}

// recordRestore moves a restored agent session to the pane its agent resumed
// in. Like recordLaunch it is best-effort: when tmux reported no pane, or the
// move fails, the agent's first hook event opens its agent session instead,
// and the detached one ends once its launch grace is over.
func (l *sessionLauncher) recordRestore(ctx context.Context, task *Task, detached AgentSession, pane TmuxPaneRef) {
	if pane.ID == "" {
		return
	}
	_, _ = l.observation.recordRestoredAgentSession(ctx, task, detached, pane)
}

// duplicatesConversation reports whether session, without a pane, holds a
// conversation another open agent session in a pane holds too, as when an
// upgrade made the task's last conversation an agent session while that
// conversation's agent ran on and a hook opened its own. Only one of them may
// resume it.
func duplicatesConversation(session AgentSession, open []AgentSession) bool {
	if session.TmuxPane != "" {
		return false
	}
	return slices.ContainsFunc(open, func(other AgentSession) bool {
		return other.ID != session.ID && other.TmuxPane != "" && other.Provider == session.Provider &&
			other.ProviderSessionID == session.ProviderSessionID
	})
}

// errAgentSessionChangedDuringRestore is why Reconnect leaves behind an agent
// session a hook moved or ended while it was restoring it.
var errAgentSessionChangedDuringRestore = errors.New("its agent session changed during the reconnect")

// withProblems adds the agent sessions a Reconnect would have left behind to
// the error that stopped it.
func withProblems(err error, problems []error) error {
	if len(problems) == 0 {
		return err
	}
	return fmt.Errorf("%w; %s", err, joinErrors(problems))
}

func restoreSessions(restores []agentSessionRestore) []AgentSession {
	sessions := make([]AgentSession, 0, len(restores))
	for _, restore := range restores {
		sessions = append(sessions, restore.session)
	}
	return sessions
}

// agentSessionNotRestored is why Reconnect left an agent session behind,
// naming the conversation it would have resumed.
func agentSessionNotRestored(session AgentSession, err error) error {
	return fmt.Errorf("%s conversation %s not restored: %w", session.Provider, session.ProviderSessionID, err)
}

// agentSessionsNotRestoredError is the error of a Reconnect that recreated
// the Session but left agent sessions behind: the others run there. It names
// them all on one line, as the TUI shows an error.
type agentSessionsNotRestoredError struct {
	problems []error
}

// agentSessionsNotRestored returns the error naming the agent sessions a
// Reconnect left behind, or nil when it left none.
func agentSessionsNotRestored(problems []error) error {
	if len(problems) == 0 {
		return nil
	}
	return &agentSessionsNotRestoredError{problems: problems}
}

func (e *agentSessionsNotRestoredError) Error() string {
	return "reconnect: " + joinErrors(e.problems)
}

func (e *agentSessionsNotRestoredError) Unwrap() []error {
	return e.problems
}

// joinErrors joins error messages on one line, as the TUI shows an error.
func joinErrors(errs []error) string {
	messages := make([]string, 0, len(errs))
	for _, err := range errs {
		messages = append(messages, err.Error())
	}
	return strings.Join(messages, "; ")
}
