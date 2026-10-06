// Package userconfig persists the user's provider setup in a user-level
// config file. Provider setup intentionally lives here rather than in the
// task SQLite database so task state and user preferences stay separate.
package userconfig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BaronBonet/rig/internal/core"
)

const currentConfigVersion = 1

type Config struct {
	// Path is the user config file location. Empty resolves to
	// ~/.config/rig/config.json.
	Path string `env:"RIG_USER_CONFIG_PATH"`
}

// DefaultConfigPath returns the default user-level config file location.
func DefaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".rig", "config.json")
	}

	return filepath.Join(home, ".config", "rig", "config.json")
}

type store struct {
	path string
	// defaultProviderOverride is the runtime default-provider override
	// (RIG_PROVIDER). It can only select a provider that is already
	// configured; it can never bypass provider setup.
	defaultProviderOverride core.Provider
}

func New(cfg Config, defaultProviderOverride core.Provider) core.ProviderConfigStore {
	path := strings.TrimSpace(cfg.Path)
	if path == "" {
		path = DefaultConfigPath()
	}

	return &store{
		path:                    path,
		defaultProviderOverride: defaultProviderOverride,
	}
}

// configFile is the on-disk shape of the user-level rig config.
type configFile struct {
	Version             int             `json:"version"`
	ConfiguredProviders []core.Provider `json:"configured_providers"`
	DefaultProvider     core.Provider   `json:"default_provider"`
	// LaunchDefaults are the options each provider was last launched with,
	// preselected for its next launch.
	LaunchDefaults map[core.Provider]core.LaunchOptions `json:"launch_defaults,omitempty"`
}

// readConfigFile returns the persisted config, or false when none exists yet.
func (s *store) readConfigFile() (configFile, bool, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return configFile{}, false, nil
		}
		return configFile{}, false, fmt.Errorf("read user config %s: %w", s.path, err)
	}

	var file configFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return configFile{}, false, fmt.Errorf("decode user config %s: %w", s.path, err)
	}
	return file, true, nil
}

func (s *store) GetProviderSetup(_ context.Context) (*core.ProviderSetup, error) {
	file, exists, err := s.readConfigFile()
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}

	setup := core.ProviderSetup{
		Configured: file.ConfiguredProviders,
		Default:    file.DefaultProvider,
	}
	if err := setup.Validate(); err != nil {
		return nil, fmt.Errorf("invalid user config %s: %w", s.path, err)
	}

	if s.defaultProviderOverride != "" {
		if !setup.IsConfigured(s.defaultProviderOverride) {
			return nil, fmt.Errorf(
				"RIG_PROVIDER %q is not a configured provider: run rig setup to enable it",
				s.defaultProviderOverride,
			)
		}
		setup.Default = s.defaultProviderOverride
	}

	return &setup, nil
}

func (s *store) SaveProviderSetup(_ context.Context, setup core.ProviderSetup) error {
	if err := setup.Validate(); err != nil {
		return err
	}

	// Launch defaults survive a rerun of provider setup.
	current, _, err := s.readConfigFile()
	if err != nil {
		return err
	}
	return s.writeConfigFile(configFile{
		Version:             currentConfigVersion,
		ConfiguredProviders: setup.Configured,
		DefaultProvider:     setup.Default,
		LaunchDefaults:      current.LaunchDefaults,
	})
}

func (s *store) GetLaunchDefaults(_ context.Context) (map[core.Provider]core.LaunchOptions, error) {
	file, _, err := s.readConfigFile()
	if err != nil {
		return nil, err
	}
	return file.LaunchDefaults, nil
}

// SaveLaunchDefaults records one provider's last launch options. Provider
// setup must exist first: launch defaults never create a config on their own.
func (s *store) SaveLaunchDefaults(
	_ context.Context,
	provider core.Provider,
	options core.LaunchOptions,
) error {
	file, exists, err := s.readConfigFile()
	if err != nil {
		return err
	}
	if !exists {
		return core.ErrProviderSetupRequired
	}
	if file.LaunchDefaults == nil {
		file.LaunchDefaults = make(map[core.Provider]core.LaunchOptions)
	}
	options.Model = strings.TrimSpace(options.Model)
	options.Effort = strings.TrimSpace(options.Effort)
	if options == (core.LaunchOptions{}) {
		delete(file.LaunchDefaults, provider)
	} else {
		file.LaunchDefaults[provider] = options
	}
	if len(file.LaunchDefaults) == 0 {
		file.LaunchDefaults = nil
	}
	return s.writeConfigFile(file)
}

// writeConfigFile atomically replaces the config file.
func (s *store) writeConfigFile(file configFile) error {
	payload, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("encode user config: %w", err)
	}
	payload = append(payload, '\n')

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create user config directory %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".config-*.json")
	if err != nil {
		return fmt.Errorf("create user config temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := tmp.Write(payload); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write user config: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("secure user config permissions: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close user config temp file: %w", err)
	}

	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("replace user config %s: %w", s.path, err)
	}

	return nil
}
