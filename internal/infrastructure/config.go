package infrastructure

import (
	"fmt"
	"os"
	"path/filepath"
	tasksqlite "rig/internal/adapters/repository/tasksqlite"
	"rig/internal/core"

	claudeclient "rig/internal/adapters/client/claude"
	codexagent "rig/internal/adapters/client/codexagent"
	sqliterepo "rig/internal/adapters/repository/sqlite"

	"github.com/caarlos0/env/v11"
)

type ApplicationConfig struct {
	Provider   core.AgentProvider `env:"AGENT_PROVIDER" envDefault:"codex"`
	SQLite     sqliterepo.Config
	TaskSQLite tasksqlite.Config
	Codex      codexagent.Config
	Claude     claudeclient.Config
	Observer   ObserverConfig
}

type ObserverConfig struct {
	SocketPath string `env:"AGENT_OBSERVER_SOCKET_PATH"`
}

// LoadConfig loads the application configuration from environment variables.
func LoadConfig() (*ApplicationConfig, error) {
	config := ApplicationConfig{
		SQLite: sqliterepo.Config{
			Path: sqliterepo.DefaultSQLitePath(),
		},
		TaskSQLite: tasksqlite.Config{
			Path: tasksqlite.DefaultSQLitePath(),
		},
		Observer: ObserverConfig{
			SocketPath: defaultObserverSocketPath(),
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

func validateProvider(provider core.AgentProvider) error {
	switch provider {
	case core.AgentProviderCodex, core.AgentProviderClaude:
		return nil
	default:
		return fmt.Errorf("invalid AGENT_PROVIDER %q: expected codex or claude", provider)
	}
}

func defaultObserverSocketPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".agent/observer.sock"
	}

	return filepath.Join(home, ".local", "share", "agent", "observer.sock")
}
