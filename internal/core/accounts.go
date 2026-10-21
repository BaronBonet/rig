package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// ListProviderAccounts returns, for each configured provider, the account env
// selects, the rig window's own, then the other accounts the user config
// offers in folder. The window's account takes the name the config gives its
// directory, wherever the config offers it.
func (s *service) ListProviderAccounts(ctx context.Context, folder string, env ProviderEnv) ([]ProviderAccount, error) {
	setup, err := s.GetProviderSetup(ctx)
	if err != nil {
		return nil, err
	}
	if setup == nil {
		return nil, ErrProviderSetupRequired
	}
	configs, err := s.providerConfig.GetAccounts(ctx)
	if err != nil {
		return nil, err
	}

	var accounts []ProviderAccount
	for _, provider := range SupportedProviders() {
		if !setup.IsConfigured(provider) {
			continue
		}
		client, err := supportedProviderClient(s.providers, provider)
		if err != nil {
			continue
		}
		windowDir, err := client.AccountDir(env)
		if err != nil {
			return nil, err
		}
		window := ProviderAccount{
			Provider: provider,
			Name:     homeRelative(windowDir),
			Dir:      windowDir,
			Env:      ProviderEnv{},
			Window:   true,
		}
		// The window keeps its variables as it set them.
		for name := range client.AccountEnv(windowDir) {
			window.Env[name], _ = env.Lookup(name)
		}
		window.Options = client.LaunchOptions(window.Env)

		offered := []ProviderAccount{window}
		named := false
		for _, config := range configs {
			if config.Provider != provider {
				continue
			}
			accountEnv := client.AccountEnv(config.Dir)
			dir, err := client.AccountDir(accountEnv)
			if err != nil {
				return nil, err
			}
			if dir == windowDir {
				if !named {
					offered[0].Name, named = config.Name, true
				}
				continue
			}
			if !config.OfferedIn(folder) || accountListed(offered, dir) {
				continue
			}
			offered = append(offered, ProviderAccount{
				Provider: provider,
				Name:     config.Name,
				Dir:      dir,
				Env:      accountEnv,
				Options:  client.LaunchOptions(accountEnv),
			})
		}
		accounts = append(accounts, offered...)
	}
	return accounts, nil
}

// GetAccountUsage reads the account's usage of its plan's rate limits from
// its provider.
func (s *service) GetAccountUsage(ctx context.Context, account ProviderAccount) (*AccountUsage, error) {
	client, err := supportedProviderClient(s.providers, account.Provider)
	if err != nil {
		return nil, err
	}
	return client.AccountUsage(ctx, account.Env)
}

func accountListed(accounts []ProviderAccount, dir string) bool {
	for _, account := range accounts {
		if account.Dir == dir {
			return true
		}
	}
	return false
}

// homeRelative shows path under the home directory as ~/..., the way the
// user writes it.
func homeRelative(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	rel, err := filepath.Rel(home, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return path
	}
	if rel == "." {
		return "~"
	}
	return filepath.Join("~", rel)
}
