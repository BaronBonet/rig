package infrastructure

import (
	"fmt"
	"os"
	"path/filepath"

	tasksqlite "rig/internal/adapters/repository/tasksqlite"
	"rig/internal/adapters/taskdaemon"
	"rig/internal/core"

	codexagent "rig/internal/adapters/client/codexagent"

	"github.com/caarlos0/env/v11"
)

type SQLiteConfig struct {
	Path string `env:"AGENT_SQLITE_PATH"`
}

type ApplicationConfig struct {
	Provider   core.AgentProvider `env:"AGENT_PROVIDER" envDefault:"codex"`
	SQLite     SQLiteConfig
	TaskSQLite tasksqlite.Config
	Codex      codexagent.Config
	TaskDaemon taskdaemon.Config
}

// LoadConfig loads the application configuration from environment variables.
func LoadConfig() (*ApplicationConfig, error) {
	config := ApplicationConfig{
		SQLite: SQLiteConfig{
			Path: defaultSQLitePath(),
		},
		TaskSQLite: tasksqlite.Config{
			Path: tasksqlite.DefaultSQLitePath(),
		},
		TaskDaemon: taskdaemon.Config{
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
	case core.AgentProviderCodex:
		return nil
	default:
		return fmt.Errorf("invalid AGENT_PROVIDER %q: expected codex", provider)
	}
}

func defaultObserverSocketPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".agent/observer.sock"
	}

	return filepath.Join(home, ".local", "share", "agent", "observer.sock")
}

func defaultSQLitePath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".agent/state.db"
	}

	return filepath.Join(home, ".local", "share", "agent", "state.db")
}
