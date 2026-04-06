package infrastructure

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/caarlos0/env/v11"

	claudeclient "agent/internal/adapters/client/claude"
	codexclient "agent/internal/adapters/client/codex"
	"agent/internal/adapters/repository/sqlite"
	"agent/internal/core"
)

type Config struct {
	Service core.Config
	SQLite  sqlite.Config
	Codex   codexclient.Config
	Claude  claudeclient.Config
}

type envConfig struct {
	Provider     string `env:"AGENT_PROVIDER" envDefault:"codex"`
	SQLitePath   string `env:"AGENT_SQLITE_PATH"`
	CodexBinary  string `env:"AGENT_CODEX_BINARY" envDefault:"codex"`
	ClaudeBinary string `env:"AGENT_CLAUDE_BINARY" envDefault:"claude"`
}

func LoadConfig() (*Config, error) {
	raw := envConfig{}
	if err := env.Parse(&raw); err != nil {
		return nil, err
	}

	if raw.SQLitePath == "" {
		raw.SQLitePath = defaultSQLitePath()
	}
	if err := validateProvider(raw.Provider); err != nil {
		return nil, err
	}

	return &Config{
		Service: core.Config{
			Provider: raw.Provider,
		},
		SQLite: sqlite.Config{
			Path: raw.SQLitePath,
		},
		Codex: codexclient.Config{
			Binary: raw.CodexBinary,
		},
		Claude: claudeclient.Config{
			Binary: raw.ClaudeBinary,
		},
	}, nil
}

func validateProvider(provider string) error {
	switch provider {
	case "codex", "claude":
		return nil
	default:
		return fmt.Errorf("invalid AGENT_PROVIDER %q: expected codex or claude", provider)
	}
}

func defaultSQLitePath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".agent/state.db"
	}

	return filepath.Join(home, ".local", "share", "agent", "state.db")
}
