package infrastructure

import (
	"os"
	"path/filepath"

	"github.com/caarlos0/env/v11"

	"agent/internal/adapters/repository/claude"
	"agent/internal/adapters/repository/codex"
	"agent/internal/adapters/repository/sqlite"
	"agent/internal/core"
)

type Config struct {
	Service core.Config
	SQLite  sqlite.Config
	Codex   codex.Config
	Claude  claude.Config
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

	return &Config{
		Service: core.Config{
			Provider: raw.Provider,
		},
		SQLite: sqlite.Config{
			Path: raw.SQLitePath,
		},
		Codex: codex.Config{
			Binary: raw.CodexBinary,
		},
		Claude: claude.Config{
			Binary: raw.ClaudeBinary,
		},
	}, nil
}

func defaultSQLitePath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".agent/state.db"
	}

	return filepath.Join(home, ".local", "share", "agent", "state.db")
}
