package infrastructure

import (
	"fmt"
	"os"
	"path/filepath"
	"rig/internal/adapters/taskdaemon"
	"rig/internal/core"

	sqlite "rig/internal/adapters/repository/sqlite"

	codexprovider "rig/internal/adapters/client/codexprovider"

	"github.com/caarlos0/env/v11"
)

type ApplicationConfig struct {
	Provider core.Provider `env:"RIG_PROVIDER" envDefault:"codex"`
	SQLite   sqlite.Config
	Codex    codexprovider.Config
	Daemon   taskdaemon.Config
}

// LoadConfig loads the application configuration from environment variables.
func LoadConfig() (*ApplicationConfig, error) {
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
	if err := validateProvider(config.Provider); err != nil {
		return nil, err
	}

	return &config, nil
}

func validateProvider(provider core.Provider) error {
	switch provider {
	case core.ProviderCodex:
		return nil
	default:
		return fmt.Errorf("invalid RIG_PROVIDER %q: expected codex", provider)
	}
}

func defaultDaemonSocketPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".rig/daemon.sock"
	}

	return filepath.Join(home, ".local", "share", "rig", "daemon.sock")
}
