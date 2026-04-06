package core

import (
	"os"
	"path/filepath"
)

type Config struct {
	BaseBranch     string
	DatabasePath   string
	WorktreeMode   string
	Provider       string
	AttachOnNew    bool
	NonInteractive bool
}

func DefaultConfig() Config {
	return Config{
		BaseBranch:   "main",
		DatabasePath: defaultDatabasePath(),
		WorktreeMode: "sibling",
		Provider:     "codex",
		AttachOnNew:  true,
	}
}

func defaultDatabasePath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".agent/state.db"
	}

	return filepath.Join(home, ".local", "share", "agent", "state.db")
}
