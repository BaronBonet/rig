package infrastructure

import (
	"fmt"
	"os"
	"path/filepath"

	"rig/internal/adapters/repository/sqlite"
	"rig/internal/core"

	"github.com/caarlos0/env/v11"

	claudeclient "rig/internal/adapters/client/claude"
	codexclient "rig/internal/adapters/client/codex"
)

// TODO: this is way to complicated, move the adapter specific stuff to their own repos
type Config struct {
	Service  core.Config
	SQLite   sqlite.Config
	Codex    codexclient.Config
	Claude   claudeclient.Config
	Hooks    HookConfig
	Observer ObserverConfig
}

type HookConfig struct {
	ListenAddr string
}

type ObserverConfig struct {
	SocketPath string
}

type envConfig struct {
	Provider     string `env:"AGENT_PROVIDER"             envDefault:"codex"`
	SQLitePath   string `env:"AGENT_SQLITE_PATH"`
	CodexBinary  string `env:"AGENT_CODEX_BINARY"         envDefault:"codex"`
	ClaudeBinary string `env:"AGENT_CLAUDE_BINARY"        envDefault:"claude"`
	HookListen   string `env:"AGENT_HOOK_LISTEN_ADDR"     envDefault:"127.0.0.1:4123"`
	ObserverSock string `env:"AGENT_OBSERVER_SOCKET_PATH"`
}

func LoadConfig() (*Config, error) {
	raw := envConfig{}
	if err := env.Parse(&raw); err != nil {
		return nil, err
	}

	if raw.SQLitePath == "" {
		raw.SQLitePath = defaultSQLitePath()
	}
	if raw.ObserverSock == "" {
		raw.ObserverSock = defaultObserverSocketPath()
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
			Binary:         raw.ClaudeBinary,
			HookListenAddr: raw.HookListen,
		},
		Hooks: HookConfig{
			ListenAddr: raw.HookListen,
		},
		Observer: ObserverConfig{
			SocketPath: raw.ObserverSock,
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

func defaultObserverSocketPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".agent/observer.sock"
	}

	return filepath.Join(home, ".local", "share", "agent", "observer.sock")
}
