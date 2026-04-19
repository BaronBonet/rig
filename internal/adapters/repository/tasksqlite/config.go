package tasksqlite

import (
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	Path string `env:"AGENT_SQLITE_PATH"`
}

func ValidateConfig(cfg Config) error {
	if filepath.Dir(cfg.Path) == "." {
		return fmt.Errorf("tasksqlite path %q must include a parent directory", cfg.Path)
	}

	return os.MkdirAll(filepath.Dir(cfg.Path), 0o755)
}
