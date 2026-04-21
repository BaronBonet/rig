package codexprovider

type Config struct {
	Binary string `env:"RIG_CODEX_BINARY" envDefault:"codex"`
}

type HookForwardingConfig struct {
	CollectorURL  string
	RigBinaryPath string
	SourceRoot    string
}
