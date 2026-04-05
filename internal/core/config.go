package core

import (
	"os"
	"path/filepath"
)

type Config struct {
	BaseBranch     string
	DatabasePath   string
	WorktreeMode   string
	CodexBinary    string
	ClaudeBinary   string
	Provider       string
	AttachOnNew    bool
	NonInteractive bool
}

func DefaultConfig() Config {
	return Config{
		BaseBranch:   "main",
		DatabasePath: defaultDatabasePath(),
		WorktreeMode: "sibling",
		CodexBinary:  "codex",
		ClaudeBinary: "claude",
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
