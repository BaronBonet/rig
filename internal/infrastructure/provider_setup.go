package infrastructure

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BaronBonet/rig/internal/core"
)

type ProviderSetup struct {
	ConfiguredProviders []core.Provider `json:"configured_providers"`
	DefaultProvider     core.Provider   `json:"default_provider"`
}

type providerSetupFile struct {
	Providers ProviderSetup `json:"providers"`
}

func SupportedProviders() []core.Provider {
	return []core.Provider{core.ProviderCodex, core.ProviderClaude}
}

func IsSupportedProvider(provider core.Provider) bool {
	for _, supported := range SupportedProviders() {
		if provider == supported {
			return true
		}
	}
	return false
}

func LoadProviderSetup(path string) (ProviderSetup, error) {
	path = providerSetupPath(path)
	fileBytes, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ProviderSetup{}, fmt.Errorf("%w: %s", ErrProviderSetupRequired, path)
	}
	if err != nil {
		return ProviderSetup{}, fmt.Errorf("read provider setup: %w", err)
	}

	var file providerSetupFile
	if err := json.Unmarshal(fileBytes, &file); err != nil {
		return ProviderSetup{}, fmt.Errorf("invalid provider setup: %w", err)
	}

	return file.Providers, nil
}

func SaveProviderSetup(path string, setup ProviderSetup) error {
	path = providerSetupPath(path)
	if err := setup.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create rig config dir: %w", err)
	}

	fileBytes, err := json.MarshalIndent(providerSetupFile{Providers: setup.Normalized()}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal provider setup: %w", err)
	}
	fileBytes = append(fileBytes, '\n')

	if err := os.WriteFile(path, fileBytes, 0o600); err != nil {
		return fmt.Errorf("write provider setup: %w", err)
	}

	return nil
}

func (s ProviderSetup) Validate() error {
	s = s.Normalized()
	if len(s.ConfiguredProviders) == 0 {
		return fmt.Errorf("%w: configure at least one provider with rig setup", ErrProviderSetupRequired)
	}
	for _, provider := range s.ConfiguredProviders {
		if !IsSupportedProvider(provider) {
			return fmt.Errorf("invalid provider setup: unknown provider %q", provider)
		}
	}
	if !IsSupportedProvider(s.DefaultProvider) {
		return fmt.Errorf("invalid provider setup: unknown default provider %q", s.DefaultProvider)
	}
	if !s.HasProvider(s.DefaultProvider) {
		return fmt.Errorf("invalid provider setup: default provider %q is not configured", s.DefaultProvider)
	}

	return nil
}

func (s ProviderSetup) Normalized() ProviderSetup {
	seen := make(map[core.Provider]struct{}, len(s.ConfiguredProviders))
	providers := make([]core.Provider, 0, len(s.ConfiguredProviders))
	for _, provider := range s.ConfiguredProviders {
		provider = core.Provider(strings.TrimSpace(string(provider)))
		if provider == "" {
			continue
		}
		if _, ok := seen[provider]; ok {
			continue
		}
		seen[provider] = struct{}{}
		providers = append(providers, provider)
	}
	sort.Slice(providers, func(i, j int) bool {
		return providers[i] < providers[j]
	})

	return ProviderSetup{
		ConfiguredProviders: providers,
		DefaultProvider:     core.Provider(strings.TrimSpace(string(s.DefaultProvider))),
	}
}

func (s ProviderSetup) HasProvider(provider core.Provider) bool {
	provider = core.Provider(strings.TrimSpace(string(provider)))
	for _, configured := range s.ConfiguredProviders {
		if configured == provider {
			return true
		}
	}
	return false
}

func DefaultProviderSetupPath() string {
	configDir, err := os.UserConfigDir()
	if err != nil || strings.TrimSpace(configDir) == "" {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil || strings.TrimSpace(home) == "" {
			return filepath.Join(".config", "rig", "config.json")
		}
		configDir = filepath.Join(home, ".config")
	}

	return filepath.Join(configDir, "rig", "config.json")
}

func IsProviderSetupRequired(err error) bool {
	return errors.Is(err, ErrProviderSetupRequired)
}

func providerSetupPath(path string) string {
	path = strings.TrimSpace(path)
	if path != "" {
		return path
	}
	return DefaultProviderSetupPath()
}

func supportedProviderList() string {
	values := SupportedProviders()
	parts := make([]string, 0, len(values))
	for _, provider := range values {
		parts = append(parts, string(provider))
	}
	return strings.Join(parts, ", ")
}
