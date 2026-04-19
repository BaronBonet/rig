package tasksqlite

import (
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	Path string `env:"AGENT_TASK_SQLITE_PATH"`
}

func DefaultSQLitePath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".agent/tasks.db"
	}

	return filepath.Join(home, ".local", "share", "agent", "tasks.db")
}

func ValidateConfig(cfg Config) error {
	if filepath.Dir(cfg.Path) == "." {
		return fmt.Errorf("tasksqlite path %q must include a parent directory", cfg.Path)
	}

	return os.MkdirAll(filepath.Dir(cfg.Path), 0o755)
}
