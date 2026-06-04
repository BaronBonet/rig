package infrastructure

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BaronBonet/rig/internal/adapters/client/claude"
	"github.com/BaronBonet/rig/internal/adapters/client/codex"
	"github.com/BaronBonet/rig/internal/adapters/taskdaemon"
	"github.com/BaronBonet/rig/internal/core"

	sqlite "github.com/BaronBonet/rig/internal/adapters/repository/sqlite"

	"github.com/caarlos0/env/v11"
)

var ErrProviderSetupRequired = errors.New("provider setup required")

type ApplicationConfig struct {
	Provider      core.Provider `env:"RIG_PROVIDER"`
	ConfigPath    string        `env:"RIG_CONFIG_PATH"`
	ProviderSetup ProviderSetup
	SQLite        sqlite.Config
	Codex         codex.Config
	Claude        claude.Config
	Daemon        taskdaemon.Config
}

// LoadConfig loads the application configuration from environment variables.
func LoadConfig() (*ApplicationConfig, error) {
	config, err := loadBaseConfig()
	if err != nil {
		return nil, err
	}

	setup, err := LoadProviderSetup(config.ConfigPath)
	if err != nil {
		return nil, err
	}
	if err := setup.Validate(); err != nil {
		return nil, err
	}

	provider := config.Provider
	if provider == "" {
		provider = setup.DefaultProvider
	}
	if err := validateRuntimeProvider(provider, setup); err != nil {
		return nil, err
	}

	config.Provider = provider
	config.ProviderSetup = setup
	return config, nil
}

func LoadConfigForProviderSetup() (*ApplicationConfig, ProviderSetup, error) {
	config, err := loadBaseConfig()
	if err != nil {
		return nil, ProviderSetup{}, err
	}

	setup, err := LoadProviderSetup(config.ConfigPath)
	if err != nil {
		if errors.Is(err, ErrProviderSetupRequired) {
			return config, ProviderSetup{}, nil
		}
		return nil, ProviderSetup{}, err
	}

	return config, setup, nil
}

func loadBaseConfig() (*ApplicationConfig, error) {
	config := ApplicationConfig{
		SQLite: sqlite.Config{
			Path: sqlite.DefaultSQLitePath(),
		},
		Daemon: taskdaemon.Config{
			SocketPath: defaultDaemonSocketPath(),
		},
	}

	if err := env.Parse(&config); err != nil {
		return nil, err
	}
	if config.ConfigPath == "" {
		config.ConfigPath = DefaultProviderSetupPath()
	}

	return &config, nil
}

func validateRuntimeProvider(provider core.Provider, setup ProviderSetup) error {
	if !IsSupportedProvider(provider) {
		return fmt.Errorf("invalid RIG_PROVIDER %q: expected one of: %s", provider, supportedProviderList())
	}
	if !setup.HasProvider(provider) {
		return fmt.Errorf("invalid RIG_PROVIDER %q: provider is not configured; run rig setup", provider)
	}

	return nil
}

func defaultDaemonSocketPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".rig/daemon.sock"
	}

	return filepath.Join(home, ".local", "share", "rig", "daemon.sock")
}
