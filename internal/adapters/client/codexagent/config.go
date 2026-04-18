package codexagent

type Config struct {
	Binary string `env:"AGENT_CODEX_BINARY" envDefault:"codex"`
}

type HookForwardingConfig struct {
	RigBinaryPath string
	SourceRoot    string
}
