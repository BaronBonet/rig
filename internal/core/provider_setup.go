package core

import (
	"context"
	"fmt"
	"strings"
)

func (s *service) GetProviderSetup(ctx context.Context) (*ProviderSetup, error) {
	if s.providerConfig == nil {
		return nil, fmt.Errorf("provider config store not configured")
	}

	return s.providerConfig.GetProviderSetup(ctx)
}

func (s *service) SaveProviderSetup(ctx context.Context, setup ProviderSetup) error {
	if s.providerConfig == nil {
		return fmt.Errorf("provider config store not configured")
	}
	if err := setup.Validate(); err != nil {
		return err
	}

	// Setup uses the same provider checks task creation depends on: hook
	// prerequisites are installed or repaired first, then the provider doctor
	// must pass before the provider can be recorded as configured.
	for _, provider := range setup.Configured {
		providerClient, err := supportedProviderClient(s.providers, provider)
		if err != nil {
			return err
		}
		if err := providerClient.EnsureTaskSessionEnvironment(ctx); err != nil {
			return fmt.Errorf("install %s hooks: %w", provider, err)
		}
		if err := providerClient.Doctor(ctx); err != nil {
			return fmt.Errorf("provider %s failed setup checks: %w", provider, err)
		}
	}

	return s.providerConfig.SaveProviderSetup(ctx, setup)
}

func (s *service) DetectProviders(ctx context.Context) ([]ProviderDetection, error) {
	detections := make([]ProviderDetection, 0, len(SupportedProviders()))
	for _, provider := range SupportedProviders() {
		detections = append(detections, s.detectProvider(ctx, provider))
	}
	return detections, nil
}

func (s *service) detectProvider(ctx context.Context, provider Provider) ProviderDetection {
	detection := ProviderDetection{Provider: provider}

	providerClient, err := supportedProviderClient(s.providers, provider)
	if err != nil {
		detection.Detail = err.Error()
		return detection
	}
	// Detection runs the provider's full setup path, not just a version probe:
	// install or repair hook prerequisites, then run the provider doctor.
	if err := providerClient.EnsureTaskSessionEnvironment(ctx); err != nil {
		detection.Detail = err.Error()
		return detection
	}
	if err := providerClient.Doctor(ctx); err != nil {
		detection.Detail = err.Error()
		return detection
	}

	detection.Ready = true
	return detection
}

func (s *service) SwitchTaskProvider(ctx context.Context, taskID string, provider Provider) (*Task, error) {
	task, err := taskByID(ctx, s.tasks, taskID)
	if err != nil {
		return nil, err
	}

	provider = Provider(strings.TrimSpace(string(provider)))
	provider, providerClient, err := s.launcher.resolveProvider(ctx, provider)
	if err != nil {
		return nil, err
	}
	if provider == task.Provider {
		return task, nil
	}

	runtime, err := s.tmuxSession.InspectTaskSession(ctx, task)
	if err != nil {
		return nil, fmt.Errorf("inspect task session: %w", err)
	}
	if runtime.Exists {
		// Never kill or corrupt an interactive session: switching refuses unless
		// the pane is idle or already running the requested provider.
		if currentClient, currentErr := supportedProviderClient(s.providers, task.Provider); currentErr == nil &&
			taskSessionRunningProvider(runtime, currentClient.TaskSessionCommandName()) {
			return nil, fmt.Errorf(
				"%w: exit %s in the task session before switching",
				ErrProviderSessionActive,
				task.Provider,
			)
		}
		if taskSessionRunningProvider(runtime, providerClient.TaskSessionCommandName()) {
			return recordActiveProvider(ctx, s.tasks, task, provider)
		}
	}

	if err := providerClient.EnsureTaskSessionEnvironment(ctx); err != nil {
		return nil, fmt.Errorf("ensure task session environment: %w", err)
	}

	// Switching bootstraps the existing workspace for the new provider but
	// never reruns repo seeding or setup scripts. The client is passed
	// explicitly because the task record still names the old active provider.
	if err := s.launcher.bootstrapWorkspace(ctx, providerClient, task); err != nil {
		return nil, err
	}

	launch, err := promptlessTaskSessionLaunchSpec(providerClient, task)
	if err != nil {
		return nil, fmt.Errorf("build task session launch spec: %w", err)
	}
	if err := s.tmuxSession.StartTaskSession(ctx, task, launch); err != nil {
		return nil, fmt.Errorf("start task session: %w", err)
	}

	return recordActiveProvider(ctx, s.tasks, task, provider)
}
