package codexprovider

type Config struct {
	Binary string `env:"RIG_CODEX_BINARY" envDefault:"codex"`
}

type HookForwardingConfig struct {
	RigBinaryPath string
	SourceRoot    string
}
