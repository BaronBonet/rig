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
}

func (s *store) GetProviderSetup(_ context.Context) (*core.ProviderSetup, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read user config %s: %w", s.path, err)
	}

	var file configFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("decode user config %s: %w", s.path, err)
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

	payload, err := json.MarshalIndent(configFile{
		Version:             currentConfigVersion,
		ConfiguredProviders: setup.Configured,
		DefaultProvider:     setup.Default,
	}, "", "  ")
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
