package core

import "context"

// GetLaunchSettings returns every configured provider's launch options under
// env, the window's provider configuration, and the options each was last
// launched with.
func (s *service) GetLaunchSettings(ctx context.Context, env ProviderEnv) (*LaunchSettings, error) {
	setup, err := s.GetProviderSetup(ctx)
	if err != nil {
		return nil, err
	}
	if setup == nil {
		return nil, ErrProviderSetupRequired
	}

	settings := &LaunchSettings{
		Options:  make(map[Provider]ProviderLaunchOptions, len(setup.Configured)),
		Defaults: make(map[Provider]LaunchOptions),
	}
	for _, provider := range setup.Configured {
		providerClient, err := supportedProviderClient(s.providers, provider)
		if err != nil {
			continue
		}
		settings.Options[provider] = providerClient.LaunchOptions(env)
	}

	defaults, err := s.providerConfig.GetLaunchDefaults(ctx)
	if err != nil {
		return nil, err
	}
	for provider, options := range defaults {
		if setup.IsConfigured(provider) {
			settings.Defaults[provider] = options
		}
	}
	return settings, nil
}
