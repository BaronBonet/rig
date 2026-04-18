package sqlite

import (
	"os"
	"path/filepath"
)

type Config struct {
	Path string `env:"AGENT_SQLITE_PATH"`
}

func DefaultSQLitePath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".agent/state.db"
	}

	return filepath.Join(home, ".local", "share", "agent", "state.db")
}
