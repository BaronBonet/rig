package core

import (
	"context"
	"fmt"
)

func (s *service) GetProviderSetup(ctx context.Context) (*ProviderSetup, error) {
	if s.userConfig == nil {
		return nil, fmt.Errorf("user config store not configured")
	}

	return s.userConfig.GetProviderSetup(ctx)
}

func (s *service) SaveProviderSetup(ctx context.Context, setup ProviderSetup) error {
	if s.userConfig == nil {
		return fmt.Errorf("user config store not configured")
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
	if err := s.userConfig.SaveProviderSetup(ctx, setup); err != nil {
		return err
	}

	// Each configured provider then gets the Rig skill this Rig version
	// installs. Tasks work without it, so a skill that cannot be installed,
	// say into a read-only skills directory, is only a warning, which rig
	// doctor repeats. Skills of providers that are not configured are left
	// alone.
	for _, provider := range setup.Configured {
		if err := s.providers[provider].InstallRigSkill(ctx); err != nil {
			s.reportWarning(fmt.Errorf("install the rig skill for %s: %w", provider, err))
		}
	}

	return nil
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
